package store

import (
	"fmt"
	"strings"
)

// RecipientResolver resolves an email address to a local user ID.
//
// Three-tier lookup:
//
//  1. AddressStore.Get(addr) reads Pebble (source of truth, correct on all nodes).
//     Covers migrated primary addresses and aliases written by tasks 4.1/4.2.
//     Skipped when addresses is nil (edge/agent/degraded nodes fall through).
//
//  2. Legacy Pebble /system/alias/{addr} key, then direct users-table lookup.
//     Covers accounts and aliases created after the last address index migration.
//     Note: the users-table lookup reads per-node SQLite, which may be stale on
//     non-leader followers and edge nodes. This is documented and acceptable
//     within task 4.3 scope; Pebble reads (tiers 1 and 2a) are always current.
//
//  TODO(4.4): per-domain catch-all lookup — insert between tier 2 and the reject.
//  Query the catch-all configuration for the recipient's domain here. Return the
//  catch-all owner's user ID if configured, otherwise fall through to the reject.
//
//  Reject: returns a non-nil error. Callers should map this to a 550 (no relay).
//
// Address normalization (lowercase, strip angle brackets, trim whitespace) is
// applied before every lookup so that casing differences never cause a silent miss.
type RecipientResolver struct {
	addresses *AddressStore // nil on edge/agent: tier 1 skipped when nil
	pebble    *PebbleStore  // for tier 2 legacy alias Pebble key
	system    *SystemIndex  // nil on edge/agent: tier 2 users-table skipped when nil
}

// NewRecipientResolver constructs a RecipientResolver.
//
// addresses may be nil (tier 1 skipped). pebble and system may be nil
// (tier 2 skipped). When all tiers fail the resolver returns an error so that
// the caller (e.g. SMTP RCPT) can reject with a 550 and prevent open relay.
func NewRecipientResolver(addresses *AddressStore, pebble *PebbleStore, system *SystemIndex) *RecipientResolver {
	return &RecipientResolver{
		addresses: addresses,
		pebble:    pebble,
		system:    system,
	}
}

// ResolveRecipient maps an email address to the owning local user ID.
// Returns ("", error) when no tier resolves the address (caller should reject).
//
// Satisfies all consumer-defined RecipientResolver interfaces in the codebase:
// smtp.RecipientResolver, pipeline/deliver.RecipientResolver,
// pipeline/rules.RecipientResolver, and pipeline/antispam.RecipientResolver.
func (r *RecipientResolver) ResolveRecipient(email string) (string, error) {
	// Normalize: strip angle brackets, trim whitespace, lowercase.
	// AddressStore keys are lowercased on write; a mismatch silently misses.
	email = strings.TrimPrefix(email, "<")
	email = strings.TrimSuffix(email, ">")
	email = strings.TrimSpace(email)
	email = strings.ToLower(email)

	if email == "" {
		return "", fmt.Errorf("resolve recipient: empty address")
	}

	// Tier 1: AddressStore.Get reads Pebble (source of truth, all nodes).
	// Covers primary addresses and aliases in the global address index.
	if r.addresses != nil {
		rec, ok, err := r.addresses.Get(email)
		if err == nil && ok && rec.UserId != "" {
			return rec.UserId, nil
		}
		// On Pebble read error or miss, fall through to the legacy tier.
		// A transient Pebble error does not mean the user is absent.
	}

	// Tier 2a: Legacy Pebble /system/alias/{addr} key.
	// Value is the target username (primary email of the target user).
	// Covers aliases created after the last address index migration (task 4.2).
	if r.pebble != nil {
		aliasData, aliasErr := r.pebble.Get([]byte("/system/alias/" + email))
		if aliasErr == nil && len(aliasData) > 0 {
			targetUsername := strings.TrimSpace(string(aliasData))
			if r.system != nil {
				var userID string
				qErr := r.system.DB().QueryRow(
					"SELECT id FROM users WHERE username = ? AND enabled = 1",
					targetUsername,
				).Scan(&userID)
				if qErr == nil && userID != "" {
					return userID, nil
				}
			}
		}
	}

	// Tier 2b: Direct users-table lookup by email (= username column).
	// users.username is set to profile.Email by the FSM on user creation.
	// Covers primary accounts created after the last address index migration.
	// Note: reads per-node SQLite which may lag behind on followers/edge nodes.
	// Guarded independently by system != nil (does not require pebble).
	if r.system != nil {
		var userID string
		qErr := r.system.DB().QueryRow(
			"SELECT id FROM users WHERE username = ? AND enabled = 1",
			email,
		).Scan(&userID)
		if qErr == nil && userID != "" {
			return userID, nil
		}
	}

	// TODO(4.4): per-domain catch-all lookup slot.
	// Before rejecting, query the catch-all configuration for the domain part of
	// 'email'. If a catch-all owner is configured for that domain, return their
	// user ID here. This avoids open relay: only domains explicitly configured
	// as local (with a catch-all owner) should be accepted.

	return "", fmt.Errorf("resolve recipient: %s: not a local address", email)
}
