package store

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// OrphanedAddress is an entry in the global address index whose owning user no
// longer exists.
type OrphanedAddress struct {
	Address string // the address as stored, e.g. "admin@example.com"
	UserID  string // the owner recorded on the record; has no profile
	Kind    string // "primary" or "identity"
}

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
