package store

import (
	"fmt"
	"strings"
)

// RecipientResolver resolves an email address to a local user ID.
//
// Lookup tiers (explicit tiers run first; catch-all is always last):
//
//  1. AddressStore.Get(addr) reads Pebble (source of truth, correct on all nodes).
//     Only records of Kind "primary" or "alias" route here: those are written
//     by the server (migration, CreateUser, CreateAlias, admin AddAddress).
//     Identity (send-as) records never route at this tier, because anyone can
//     claim one through the self-serve flow. Skipped when addresses is nil.
//
//  2. Legacy Pebble /system/alias/{addr} key (2a), then direct users-table
//     lookup (2b). Covers accounts and aliases not yet in the address index.
//     Note: the users-table lookup reads per-node SQLite, which may be stale on
//     non-leader followers and edge nodes; Pebble reads (tiers 1 and 2a) are
//     always current.
//
//     2c. A Verified identity record routes after accounts and aliases, so a
//     verified claim can never outrank a real account or alias. An unverified
//     identity never routes. Verified identities keep routing (today's
//     behaviour): admin-added extra addresses that GrantSend marked verified
//     depend on it.
//
//  3. Per-domain catch-all: reads /system/catchall/{domain} from Pebble and
//     returns the owner's user ID when configured. Only domains with an explicit
//     catch-all entry accept unmatched mail; others are still rejected.
//     Skipped when pebble is nil (same nil-guard as tier 2a).
//
//     Reject: returns a non-nil error. Callers should map this to a 550 (no relay).
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
	var identityOwner string
	if r.addresses != nil {
		rec, ok, err := r.addresses.Get(email)
		if err == nil && ok && rec.UserId != "" {
			switch {
			case rec.Kind == "primary" || rec.Kind == "alias":
				return rec.UserId, nil
			case rec.Kind == "identity" && rec.Verified:
				identityOwner = rec.UserId // tier 2c, after accounts and aliases
			}
		}
		// On Pebble read error or miss, fall through to the legacy tier.
		// A transient Pebble error does not mean the user is absent.
	}

	// Tier 2a: Legacy Pebble /system/alias/{addr} key.
	// Value is the target username (primary email of the target user).
	// Covers aliases created after the last address index migration.
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

	// Tier 2c: verified identity (see type comment).
	if identityOwner != "" {
		return identityOwner, nil
	}

	// Tier 3: per-domain catch-all.
	// Reads /system/catchall/{domain} directly from Pebble — correct on all nodes.
	// Open-relay guard: no key == no acceptance; domains without an explicit
	// catch-all entry continue to reject below. pebble nil-guard mirrors tier 2a.
	if r.pebble != nil {
		parts := strings.SplitN(email, "@", 2)
		if len(parts) == 2 && parts[1] != "" {
			domain := parts[1] // already lowercased by normalisation above
			catchAllData, catchAllErr := r.pebble.Get(CatchAllKey(domain))
			if catchAllErr == nil && len(catchAllData) > 0 {
				ownerID := strings.TrimSpace(string(catchAllData))
				if ownerID != "" {
					return ownerID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("resolve recipient: %s: not a local address", email)
}
