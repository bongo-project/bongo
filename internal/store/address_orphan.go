package store

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// OrphanedAddress is an entry in the global address index whose owning user no
// longer exists.
type OrphanedAddress struct {
	Address string // the address as stored, e.g. "admin@example.com"; the domain for KindCatchAll
	UserID  string // the owner recorded on the record; has no profile
	Kind    string // "primary", "alias", "identity", KindLegacyAlias or KindCatchAll
	Target  string `json:",omitempty"` // KindLegacyAlias only: the target email that has no account
}

// Report-only kinds. They describe keys outside the address index, so repair
// never deletes them; an operator removes them by hand.
const (
	KindLegacyAlias = "legacy-alias" // /system/alias/{addr} whose target email has no account
	KindCatchAll    = "catchall"     // /system/catchall/{domain} whose user ID has no profile
)

// FindOrphanedAddresses returns every /system/address/ record whose UserId has
// no corresponding /user/{id}/profile key.
//
// Why these are harmful rather than merely untidy: RecipientResolver consults
// the address index FIRST, ahead of the legacy /system/alias key, the users
// table and the per-domain catch-all. A record pointing at a deleted user
// therefore wins over every other tier and resolves mail to an account that
// does not exist. Delivery then "succeeds" -- the message is written under
// /user/{deleted-id}/ and into that user's per-user database -- but no profile
// or users row exists, so the mailbox is unreachable from the web UI, IMAP and
// POP3 alike. The mail is neither bounced nor visible.
//
// This is reachable through ordinary admin use: deleting a user removes
// /user/{id}/* and the users row but historically left the address index
// untouched, so an alias later created for the same address was silently
// shadowed by the dead record.
//
// It also lists leftover legacy /system/alias/ keys whose target email has no
// account (KindLegacyAlias) and /system/catchall/ keys whose user ID has no
// profile (KindCatchAll). Those are report-only; repair never removes them.
//
// Read-only. PopulateAddressIndex cannot repair these because it is create-only
// and never revisits a record that already exists.
func FindOrphanedAddresses(ps *PebbleStore) ([]OrphanedAddress, error) {
	if ps == nil {
		return nil, nil
	}

	iter, err := ps.PrefixScan([]byte("/system/address/"))
	if err != nil {
		return nil, fmt.Errorf("scanning address index: %w", err)
	}
	defer func() { _ = iter.Close() }()

	// Cache profile existence: several addresses commonly share one owner.
	seen := make(map[string]bool)

	var orphans []OrphanedAddress
	for iter.Next() {
		address := strings.TrimPrefix(string(iter.Key()), "/system/address/")
		if address == "" {
			continue
		}
		val, vErr := iter.Value()
		if vErr != nil {
			continue
		}
		var rec bongov1.AddressRecord
		if err := proto.Unmarshal(val, &rec); err != nil {
			continue
		}
		if rec.UserId == "" {
			continue
		}

		exists, cached := seen[rec.UserId]
		if !cached {
			exists = userProfileExists(ps, rec.UserId)
			seen[rec.UserId] = exists
		}
		if exists {
			continue
		}

		orphans = append(orphans, OrphanedAddress{
			Address: address,
			UserID:  rec.UserId,
			Kind:    rec.Kind,
		})
	}

	aliasOrphans, err := findOrphanedLegacyAliases(ps)
	if err != nil {
		return nil, err
	}
	orphans = append(orphans, aliasOrphans...)

	catchAllOrphans, err := findOrphanedCatchAlls(ps)
	if err != nil {
		return nil, err
	}
	return append(orphans, catchAllOrphans...), nil
}

// findOrphanedLegacyAliases reports /system/alias/{addr} keys whose value (the
// target's email) names no account. The target is live when its address record
// is a primary or alias whose owner has a profile, or, failing that, when some
// profile carries that email: a crash between the profile and address writes in
// CreateUser leaves a live account with no record. A disabled account is live.
// A failed record read aborts rather than producing a false orphan.
func findOrphanedLegacyAliases(ps *PebbleStore) ([]OrphanedAddress, error) {
	iter, err := ps.PrefixScan([]byte("/system/alias/"))
	if err != nil {
		return nil, fmt.Errorf("scanning legacy aliases: %w", err)
	}
	defer func() { _ = iter.Close() }()

	var (
		orphans       []OrphanedAddress
		profileEmails map[string]bool // built lazily, only when a record check misses
	)
	for iter.Next() {
		aliasAddr := strings.TrimPrefix(string(iter.Key()), "/system/alias/")
		val, vErr := iter.Value()
		if aliasAddr == "" || vErr != nil {
			continue
		}
		target := strings.ToLower(strings.TrimSpace(string(val)))
		if target == "" {
			continue
		}

		live, err := aliasTargetHasLiveRecord(ps, target)
		if err != nil {
			return nil, err
		}
		if !live {
			if profileEmails == nil {
				if profileEmails, err = profileEmailSet(ps); err != nil {
					return nil, err
				}
			}
			live = profileEmails[target]
		}
		if live {
			continue
		}
		orphans = append(orphans, OrphanedAddress{Address: aliasAddr, Target: target, Kind: KindLegacyAlias})
	}
	return orphans, nil
}

// aliasTargetHasLiveRecord reports whether /system/address/{target} is a
// primary or alias record owned by an account with a profile. A record that
// does not decode counts as live, as the index pass skips it: the report must
// not list a false orphan, and one corrupt record must not block repair.
func aliasTargetHasLiveRecord(ps *PebbleStore, target string) (bool, error) {
	data, err := ps.Get(addressKey(target))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading address record %q: %w", target, err)
	}
	var rec bongov1.AddressRecord
	if err := proto.Unmarshal(data, &rec); err != nil {
		return true, nil
	}
	if rec.Kind != "primary" && rec.Kind != "alias" {
		return false, nil
	}
	return userProfileExists(ps, rec.UserId), nil
}

// profileEmailSet returns the lower-cased email of every stored profile.
func profileEmailSet(ps *PebbleStore) (map[string]bool, error) {
	iter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, fmt.Errorf("scanning profiles: %w", err)
	}
	defer func() { _ = iter.Close() }()

	emails := make(map[string]bool)
	for iter.Next() {
		parts := strings.Split(string(iter.Key()), "/")
		if len(parts) != 4 || parts[3] != "profile" {
			continue
		}
		val, vErr := iter.Value()
		if vErr != nil {
			continue
		}
		var p bongov1.UserProfile
		if err := proto.Unmarshal(val, &p); err != nil {
			continue
		}
		if e := strings.ToLower(strings.TrimSpace(p.Email)); e != "" {
			emails[e] = true
		}
	}
	return emails, nil
}

// findOrphanedCatchAlls reports /system/catchall/{domain} keys whose user ID
// has no profile.
func findOrphanedCatchAlls(ps *PebbleStore) ([]OrphanedAddress, error) {
	iter, err := ps.PrefixScan([]byte("/system/catchall/"))
	if err != nil {
		return nil, fmt.Errorf("scanning catch-alls: %w", err)
	}
	defer func() { _ = iter.Close() }()

	var orphans []OrphanedAddress
	for iter.Next() {
		domain := strings.TrimPrefix(string(iter.Key()), "/system/catchall/")
		val, vErr := iter.Value()
		if domain == "" || vErr != nil {
			continue
		}
		id := strings.TrimSpace(string(val))
		if id == "" || userProfileExists(ps, id) {
			continue
		}
		orphans = append(orphans, OrphanedAddress{Address: domain, UserID: id, Kind: KindCatchAll})
	}
	return orphans, nil
}

// DeleteOrphanedAddresses removes the given records from the address index via
// Raft, returning the addresses actually deleted.
//
// Each address is re-checked immediately before deletion so a record whose user
// was recreated between the scan and the repair is left alone: the caller may
// have been holding a stale list, and deleting a live routing entry is the one
// outcome worse than leaving a dead one.
func DeleteOrphanedAddresses(ps *PebbleStore, as *AddressStore, orphans []OrphanedAddress) ([]string, error) {
	if as == nil {
		return nil, fmt.Errorf("address store not available")
	}

	var deleted []string
	for _, o := range orphans {
		if o.Kind == KindLegacyAlias || o.Kind == KindCatchAll {
			continue // report-only: the key is not an address-index record
		}
		if ps != nil && userProfileExists(ps, o.UserID) {
			continue // owner exists again; not an orphan
		}
		if err := as.Delete(o.Address); err != nil {
			return deleted, fmt.Errorf("deleting address %s: %w", o.Address, err)
		}
		deleted = append(deleted, o.Address)
	}
	return deleted, nil
}

// userProfileExists reports whether a user account is still live, which is the
// only durable proof of it: deleting a user removes /user/{id}/profile. Shared
// by the orphaned-address scan and the dangling-mail report so the two cannot
// drift on what "the owner is gone" means.
func userProfileExists(ps *PebbleStore, userID string) bool {
	if ps == nil || userID == "" {
		return false
	}
	data, err := ps.Get([]byte("/user/" + userID + "/profile"))
	return err == nil && len(data) > 0
}

// profileState reports whether a user's profile exists and whether it is
// disabled. A missing key or empty value is (false, false, nil); any other read
// or decode error is returned so the caller can choose to fail open.
func profileState(ps *PebbleStore, userID string) (exists, disabled bool, err error) {
	if userID == "" {
		return false, false, nil
	}
	data, err := ps.Get([]byte("/user/" + userID + "/profile"))
	if errors.Is(err, ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read profile %s: %w", userID, err)
	}
	if len(data) == 0 {
		return false, false, nil
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		return false, false, fmt.Errorf("decode profile %s: %w", userID, err)
	}
	return true, p.Disabled, nil
}
