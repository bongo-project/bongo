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
	// Revoke is set when the conflict is one the resolver resolves ahead of a
	// verified identity (account, alias-table or alias), the owner is a
	// different user, and Pebble backs it (a profile, or a /system/alias/ key).
	// Catch-all and hosted-domain conflicts never set it: a verified identity
	// is resolved before both, so the identity is not shadowed by them.
	Revoke bool
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
		conflict, owner, revoke := identityConflict(ps, system, address, rec.UserId)
		if rec.Verified && conflict == "" {
			continue
		}
		out = append(out, ShadowedIdentity{
			Address:       address,
			UserID:        rec.UserId,
			Verified:      rec.Verified,
			Conflict:      conflict,
			ConflictOwner: owner,
			Revoke:        revoke,
		})
	}
	return out, nil
}

// identityConflict reports the first other way address resolves. revoke is
// true only for a conflict held by a different user that Pebble, the source of
// truth, confirms: SQLite rows are derived and can be stale.
func identityConflict(ps *PebbleStore, system *SystemIndex, address, ownerID string) (kind, owner string, revoke bool) {
	if system != nil {
		var id string
		if err := system.DB().QueryRow(
			`SELECT id FROM users WHERE lower(username) = ? LIMIT 1`, address).Scan(&id); err == nil && id != ownerID {
			return "account", id, userProfileExists(ps, id)
		}
		if err := system.DB().QueryRow(
			`SELECT target_user_id FROM aliases WHERE lower(address) = ? LIMIT 1`, address).Scan(&id); err == nil && id != ownerID {
			_, aErr := ps.Get([]byte("/system/alias/" + address))
			return "alias-table", id, aErr == nil
		}
	}
	if v, err := ps.Get([]byte("/system/alias/" + address)); err == nil && len(v) > 0 {
		target := strings.TrimSpace(string(v))
		// The value is a username. An alias to the identity's own owner is no
		// conflict; an unresolvable target is reported but never revoked.
		var targetID string
		resolved := false
		if system != nil {
			if err := system.DB().QueryRow(
				`SELECT id FROM users WHERE lower(username) = lower(?) LIMIT 1`, target).Scan(&targetID); err == nil {
				resolved = true
			}
		}
		if !resolved || targetID != ownerID {
			return "alias", target, resolved && userProfileExists(ps, targetID)
		}
	}
	if at := strings.LastIndex(address, "@"); at >= 0 {
		domain := address[at+1:]
		if v, err := ps.Get(CatchAllKey(domain)); err == nil && len(v) > 0 {
			return "catch-all", strings.TrimSpace(string(v)), false
		}
		if _, err := ps.Get([]byte("/system/domain/" + domain)); err == nil {
			return "hosted-domain", "", false
		}
	}
	return "", "", false
}
