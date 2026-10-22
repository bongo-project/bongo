package store

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ShadowedIdentity is an identity (send-as) address record that either is not
// Verified or collides with another way of resolving the same address.
type ShadowedIdentity struct {
	Address  string // address as stored
	UserID   string // owner recorded on the identity record
	Verified bool
	// Conflict names what else resolves the address ("account", "alias",
	// "alias-table", "catch-all", "hosted-domain"); empty when nothing does.
	Conflict string
	// ConflictOwner is the account ID or catch-all owner, when known.
	ConflictOwner string
}

// FindShadowedIdentities lists identity records worth an operator's attention
// after the self-serve send-as hijack fix (H1):
//
//   - Unverified identities. The resolver no longer routes mail on them, so
//     any that used to deliver (admin-added extra addresses never granted
//     send) have stopped receiving mail. Fix: GrantSend, or re-add them.
//   - Identities, verified or not, whose address is also an account, an alias,
//     a catch-all domain or a hosted domain. A verified identity on a local
//     address may itself be the result of the old exploit, because the
//     verification code used to resolve back to the claimant.
//
// Read-only: nothing is modified. system may be nil, in which case the
// users and aliases tables are not consulted.
func FindShadowedIdentities(ps *PebbleStore, system *SystemIndex) ([]ShadowedIdentity, error) {
	if ps == nil {
		return nil, nil
	}
	iter, err := ps.PrefixScan([]byte("/system/address/"))
	if err != nil {
		return nil, fmt.Errorf("scanning address index: %w", err)
	}
	defer func() { _ = iter.Close() }()

	var out []ShadowedIdentity
	for iter.Next() {
		address := strings.TrimPrefix(string(iter.Key()), "/system/address/")
		val, vErr := iter.Value()
		if address == "" || vErr != nil {
			continue
		}
		var rec bongov1.AddressRecord
		if proto.Unmarshal(val, &rec) != nil || rec.Kind != "identity" {
			continue
		}
		conflict, owner := identityConflict(ps, system, address, rec.UserId)
		if rec.Verified && conflict == "" {
			continue
		}
		out = append(out, ShadowedIdentity{
			Address:       address,
			UserID:        rec.UserId,
			Verified:      rec.Verified,
			Conflict:      conflict,
			ConflictOwner: owner,
		})
	}
	return out, nil
}

// identityConflict reports the first other way address resolves.
func identityConflict(ps *PebbleStore, system *SystemIndex, address, ownerID string) (kind, owner string) {
	if system != nil {
		var id string
		if err := system.DB().QueryRow(
			`SELECT id FROM users WHERE lower(username) = ? LIMIT 1`, address).Scan(&id); err == nil && id != ownerID {
			return "account", id
		}
		if err := system.DB().QueryRow(
			`SELECT target_user_id FROM aliases WHERE lower(address) = ? LIMIT 1`, address).Scan(&id); err == nil && id != ownerID {
			return "alias-table", id
		}
	}
	if v, err := ps.Get([]byte("/system/alias/" + address)); err == nil && len(v) > 0 {
		return "alias", strings.TrimSpace(string(v))
	}
	if at := strings.LastIndex(address, "@"); at >= 0 {
		domain := address[at+1:]
		if v, err := ps.Get(CatchAllKey(domain)); err == nil && len(v) > 0 {
			return "catch-all", strings.TrimSpace(string(v))
		}
		if _, err := ps.Get([]byte("/system/domain/" + domain)); err == nil {
			return "hosted-domain", ""
		}
	}
	return "", ""
}
