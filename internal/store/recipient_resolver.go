package store

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bongo-project/bongo/internal/srs"
)

// ErrRecipientUnavailable is returned (wrapped) by ResolveRecipient when an
// address matches a routing entry whose target account no longer exists or is
// disabled. Callers map it to a permanent rejection and must not treat it as
// "not local", or an authenticated session would relay the mail out.
var ErrRecipientUnavailable = errors.New("recipient unavailable")

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
//     2d. postmaster@ and abuse@ on a hosted domain (RFC 5321 section 4.5.1).
//     Matches the exact local parts only, after the explicit tiers and before the
//     catch-all. The target is the domain owner, else the account named by
//     SetPostmasterAccount. The tier applies only when it has a live target: a
//     disabled or deleted owner falls through to the configured account, and with
//     no live target resolution continues to the catch-all, and with none of
//     those the address is ErrRecipientUnavailable (550 5.1.1, and never
//     relayed out on an authenticated session). Never used by ResolveAccount, or sharing a calendar
//     with postmaster@d would grant the domain owner. A bare "postmaster" (no
//     domain) maps to the configured account.
//
//     Domain alias rewrite: when email is on an alias domain (hosted, with
//     /system/domain/{alias}/alias_of naming a hosted target) and tiers 1 to 2d
//     found nothing, the lookup is repeated once for local@target with every tier,
//     so the target's catch-all or refusal applies. Explicit addresses on the
//     alias win. The alias's own catch-all is never consulted. For postmaster@
//     and abuse@ the alias's own live owner comes first, then the rewrite.
//     The rewrite is also applied in strict mode (ResolveAccount).
//
//  3. Per-domain catch-all: reads /system/catchall/{domain} from Pebble and
//     returns the owner's user ID when configured. Only domains with an explicit
//     catch-all entry accept unmatched mail; others are still rejected.
//     Skipped when pebble is nil (same nil-guard as tier 2a). SRS0/SRS1 local
//     parts are never matched by the catch-all.
//
//     Before any tier, an address on a domain an administrator has disabled
//     returns ErrRecipientUnavailable (ResolveRecipient only; ResolveAccount
//     is unaffected, because disabling a domain stops its mail, not its
//     accounts). A domain read error fails open.
//
//     Every tier checks that the target's profile exists and is not disabled.
//     The first tier that matches decides; a dead or disabled target returns
//     ErrRecipientUnavailable and never falls through to a later tier. This is
//     the single place that decides inbound mail for disabled or deleted users.
//     A profile read error fails open.
//
//     Reject: returns a non-nil error. Callers should map this to a 550 (no relay).
//
// Address normalization (lowercase, strip angle brackets, trim whitespace) is
// applied before every lookup so that casing differences never cause a silent miss.
type RecipientResolver struct {
	addresses *AddressStore // nil on edge/agent: tier 1 skipped when nil
	pebble    *PebbleStore  // for tier 2 legacy alias Pebble key
	system    *SystemIndex  // nil on edge/agent: tier 2 users-table skipped when nil

	// postmasterAccount is the lowercased address of the account that receives
	// postmaster@ and abuse@ when a domain has no live owner. Set once at
	// startup before the resolver is shared; not safe to change afterwards.
	postmasterAccount string
}

// SetPostmasterAccount sets the fallback account (a full local address) for
// postmaster@ and abuse@. Call before the resolver is shared between
// goroutines.
func (r *RecipientResolver) SetPostmasterAccount(addr string) {
	r.postmasterAccount = strings.ToLower(strings.TrimSpace(addr))
}

// PostmasterAccount returns the configured fallback address, or "".
func (r *RecipientResolver) PostmasterAccount() string { return r.postmasterAccount }

// isRoleLocalPart reports whether local is one of the role mailboxes the
// postmaster tier serves. Exact match only: no plus-tags, no prefixes.
func isRoleLocalPart(local string) bool {
	return local == "postmaster" || local == "abuse"
}

// postmasterTarget returns the live recipient for postmaster@/abuse@ on a
// hosted domain: the domain owner first, then the configured account. For an
// alias domain the order is the alias's own owner, the target's owner, then the
// configured account, so an alias never falls straight to the configured
// account. ok is false when the domain is not hosted, a read fails, or no
// candidate is live.
func (r *RecipientResolver) postmasterTarget(domain string) (userID string, ok bool) {
	// A "/" would address a sub-key such as /system/domain/{d}/owner.
	if r.pebble == nil || domain == "" || strings.Contains(domain, "/") {
		return "", false
	}
	if hosted, err := DomainHosted(r.pebble, domain); err != nil || !hosted {
		return "", false
	}
	if id, ok := r.postmasterOwner(domain); ok {
		return id, true
	}
	if target, isAlias, err := DomainAliasOf(r.pebble, domain); err == nil && isAlias {
		// Resolution rewrites to the target, and a disabled target refuses
		// everything, so report no target rather than the owner or account.
		if off, err := DomainDisabled(r.pebble, target); err == nil && off {
			return "", false
		}
		if id, ok := r.postmasterOwner(target); ok {
			return id, true
		}
	}
	return r.configuredPostmaster()
}

// postmasterOwner returns the live owner of a hosted domain.
func (r *RecipientResolver) postmasterOwner(domain string) (userID string, ok bool) {
	// A "/" would address a sub-key such as /system/domain/{d}/owner.
	if r.pebble == nil || domain == "" || strings.Contains(domain, "/") {
		return "", false
	}
	if hosted, err := DomainHosted(r.pebble, domain); err != nil || !hosted {
		return "", false
	}
	if data, err := r.pebble.Get(domainOwnerKey(domain)); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			if exists, disabled, perr := profileState(r.pebble, id); perr == nil && exists && !disabled {
				return id, true
			}
		}
	}
	return "", false
}

// configuredPostmaster resolves smtp.postmaster_account. Strict mode skips the
// identity, catch-all and postmaster tiers, so it cannot recurse.
func (r *RecipientResolver) configuredPostmaster() (string, bool) {
	if r.postmasterAccount == "" {
		return "", false
	}
	id, err := r.resolve(r.postmasterAccount, true)
	return id, err == nil
}

// HasPostmasterTarget reports whether postmaster@ and abuse@ on domain have a
// live target. Used by the startup report.
func (r *RecipientResolver) HasPostmasterTarget(domain string) bool {
	_, ok := r.postmasterTarget(strings.ToLower(domain))
	return ok
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
	return r.resolve(email, false)
}

// ResolveAccount maps an address to the user ID of an existing, enabled
// account for authorisation decisions such as sharing. It uses the same
// normalisation and the primary/alias, legacy alias and users-table tiers as
// ResolveRecipient, but never routes through a verified send-as identity or the
// domain catch-all (an arbitrary unknown@domain must not name the catch-all
// owner), and fails closed when a profile cannot be read.
func (r *RecipientResolver) ResolveAccount(email string) (string, error) {
	return r.resolve(email, true)
}

// resolve implements both lookups. strict selects the ResolveAccount rules.
func (r *RecipientResolver) resolve(email string, strict bool) (string, error) {
	return r.resolveOnce(email, strict, true)
}

// resolveOnce is resolve with the domain-alias rewrite switchable. The
// rewritten lookup passes allowRewrite=false, so the rewrite is applied at
// most once: a chain planted by a race (A->B while B->C) follows one hop only.
func (r *RecipientResolver) resolveOnce(email string, strict, allowRewrite bool) (string, error) {
	// Normalize: strip angle brackets, trim whitespace, lowercase.
	// AddressStore keys are lowercased on write; a mismatch silently misses.
	email = strings.TrimPrefix(email, "<")
	email = strings.TrimSuffix(email, ">")
	email = strings.TrimSpace(email)
	email = strings.ToLower(email)

	if email == "" {
		return "", fmt.Errorf("resolve recipient: empty address")
	}

	// Bare "postmaster" (RFC 5321 allows it) maps to the configured account.
	// go-smtp refuses this form before the session sees it; see SECURITY.md.
	if email == "postmaster" && !strict {
		if id, ok := r.configuredPostmaster(); ok {
			return id, nil
		}
		return "", fmt.Errorf("resolve recipient: %s: not a local address", email)
	}

	// The reserved no-relay sender is never a mailbox: not a user, alias or
	// identity, and not the catch-all owner's. Refusing it up front stops the
	// rules and antispam stages running that owner's forwards on a bounce.
	if at := strings.LastIndex(email, "@"); at > 0 && srs.IsNoRelay(email[:at]) {
		return "", fmt.Errorf("resolve recipient: %s: reserved address", email)
	}

	if !strict && r.pebble != nil {
		if at := strings.LastIndex(email, "@"); at > 0 {
			// Fail open, like the profile check: a transient read error must
			// not become a permanent 5xx.
			if off, err := DomainDisabled(r.pebble, email[at+1:]); err == nil && off {
				return "", unavailable(email)
			}
		}
	}

	// Tier 1: AddressStore.Get reads Pebble (source of truth, all nodes).
	// Covers primary addresses and aliases in the global address index.
	var identityOwner string
	if r.addresses != nil {
		rec, ok, err := r.addresses.Get(email)
		if err == nil && ok && rec.UserId != "" {
			switch {
			case rec.Kind == "primary" || rec.Kind == "alias":
				return r.live(email, rec.UserId, strict)
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
			// The alias key matched, so this tier decides. The target is
			// found through the users table or, when that row is missing
			// or lagging (the FSM drops it with the profile), through the
			// address index. A target found by neither is dead: falling
			// through would hand the mail to the domain catch-all owner.
			if r.system != nil {
				if userID, enabled, ok := r.usersRow(targetUsername); ok {
					return r.liveEnabled(email, userID, enabled, strict)
				}
			}
			if r.addresses != nil {
				rec, ok, err := r.addresses.Get(strings.ToLower(targetUsername))
				if err == nil && ok && rec.UserId != "" && (rec.Kind == "primary" || rec.Kind == "alias") {
					return r.live(email, rec.UserId, strict)
				}
				if err != nil {
					return "", fmt.Errorf("resolve recipient: %s: alias target lookup: %w", email, err)
				}
			}
			if r.system != nil || r.addresses != nil {
				return "", unavailable(email)
			}
		}
	}

	// Tier 2b: Direct users-table lookup by email (= username column).
	// users.username is set to profile.Email by the FSM on user creation.
	// Covers primary accounts created after the last address index migration.
	// Note: reads per-node SQLite which may lag behind on followers/edge nodes.
	// Guarded independently by system != nil (does not require pebble).
	if r.system != nil {
		if userID, enabled, ok := r.usersRow(email); ok {
			return r.liveEnabled(email, userID, enabled, strict)
		}
	}

	// Tier 2c: verified identity (see type comment).
	if identityOwner != "" && !strict {
		return r.live(email, identityOwner, strict)
	}

	// Tier 2d: postmaster@ and abuse@ (see type comment). On an alias domain
	// only the alias's own owner counts here; the rewrite below then reaches
	// the target's explicit address, owner and configured account.
	aliasTarget, isAlias := r.aliasTarget(email, allowRewrite)
	if !strict {
		if local, domain, found := strings.Cut(email, "@"); found && isRoleLocalPart(local) {
			if isAlias {
				if id, ok := r.postmasterOwner(domain); ok {
					return id, nil
				}
			} else if id, ok := r.postmasterTarget(domain); ok {
				return id, nil
			}
		}
	}

	// Domain alias rewrite (see type comment): nothing resolved local@alias,
	// so retry once as local@target. The result, including the target's
	// catch-all or its refusal, is returned as is.
	if isAlias {
		local, _, _ := strings.Cut(email, "@")
		return r.resolveOnce(local+"@"+aliasTarget, strict, false)
	}

	// Tier 3: per-domain catch-all.
	// Reads /system/catchall/{domain} directly from Pebble — correct on all nodes.
	// Open-relay guard: no key == no acceptance; domains without an explicit
	// catch-all entry continue to reject below. pebble nil-guard mirrors tier 2a.
	//
	// SRS addresses never reach the catch-all: a bounce to one is reversed by
	// the SMTP and deliver stages before any resolution, and one that was not
	// (a failed verification) must not be handed to the catch-all owner, nor
	// have that owner's rules, forwards and antispam preferences run on it.
	if r.pebble != nil && !strict {
		parts := strings.SplitN(email, "@", 2)
		if len(parts) == 2 && parts[1] != "" && !srs.IsSRS(parts[0]) {
			domain := parts[1] // already lowercased by normalisation above
			catchAllData, catchAllErr := r.pebble.Get(CatchAllKey(domain))
			if catchAllErr == nil && len(catchAllData) > 0 {
				ownerID := strings.TrimSpace(string(catchAllData))
				if ownerID != "" {
					return r.live(email, ownerID, strict)
				}
			}
		}
	}

	// postmaster@ and abuse@ on a hosted domain with no target (and no
	// catch-all) are ours, so they are refused as an unavailable mailbox
	// (550 5.1.1). A plain miss would read as "not local": an unauthenticated
	// session would get 5.7.1 and an authenticated one would relay the mail
	// out to our own MX.
	if !strict && r.pebble != nil {
		if local, domain, found := strings.Cut(email, "@"); found && isRoleLocalPart(local) && !strings.Contains(domain, "/") {
			if hosted, err := DomainHosted(r.pebble, domain); err == nil && hosted {
				return "", unavailable(email)
			}
		}
	}

	return "", fmt.Errorf("resolve recipient: %s: not a local address", email)
}

// aliasTarget returns the target domain when email is on a hosted alias
// domain and the rewrite is allowed. The alias must itself be hosted (an
// alias_of left by a half-finished create is inert) and so must the target (a
// target deleted under a live alias is a miss). Read errors count as no alias.
func (r *RecipientResolver) aliasTarget(email string, allowRewrite bool) (string, bool) {
	if !allowRewrite || r.pebble == nil {
		return "", false
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "", false
	}
	domain := email[at+1:]
	if !validAliasName(domain) {
		return "", false
	}
	target, ok, err := DomainAliasOf(r.pebble, domain)
	if err != nil || !ok || target == domain {
		return "", false
	}
	if hosted, err := DomainHosted(r.pebble, domain); err != nil || !hosted {
		return "", false
	}
	if hosted, err := DomainHosted(r.pebble, target); err != nil || !hosted {
		return "", false
	}
	return target, true
}

// usersRow looks up a users-table row by username. ok is false when no row
// exists or the query fails (the caller falls through, as a stale or missing
// local index says nothing about the account).
func (r *RecipientResolver) usersRow(username string) (id string, enabled, ok bool) {
	var en int
	if err := r.system.DB().QueryRow(
		"SELECT id, enabled FROM users WHERE username = ?", username,
	).Scan(&id, &en); err != nil || id == "" {
		return "", false, false
	}
	return id, en != 0, true
}

// liveEnabled is live for tiers that matched through the users table: when
// Pebble is available the profile decides (the SQLite enabled flag may be
// stale on a follower); without Pebble the flag is all there is.
func (r *RecipientResolver) liveEnabled(email, userID string, enabled, strict bool) (string, error) {
	if r.pebble == nil && !enabled {
		return "", unavailable(email)
	}
	return r.live(email, userID, strict)
}

// live returns userID when its account exists and is not disabled, and
// ErrRecipientUnavailable otherwise. The first tier that matches an address
// decides: an unavailable target is never passed down to a later tier, or a
// disabled user's mail would fall into the domain catch-all.
func (r *RecipientResolver) live(email, userID string, strict bool) (string, error) {
	if r.pebble == nil {
		if strict {
			return "", fmt.Errorf("resolve account: %s: no profile store", email)
		}
		return userID, nil // edge/agent: the server this forwards to checks
	}
	exists, disabled, err := profileState(r.pebble, userID)
	if err != nil {
		if strict {
			return "", fmt.Errorf("resolve account: %s: %w", email, err)
		}
		// Fail open: a transient read error must not become a permanent 5xx.
		return userID, nil
	}
	if !exists || disabled {
		return "", unavailable(email)
	}
	return userID, nil
}

// DomainsWithoutPostmaster lists the hosted, enabled domains on which
// postmaster@ and abuse@ have no live target, sorted. Read-only; used by the
// startup report. Sub-keys of a domain (/owner, /dkim, ...) are skipped.
func (r *RecipientResolver) DomainsWithoutPostmaster() (out []string, rErr error) {
	if r.pebble == nil {
		return nil, nil
	}
	it, err := r.pebble.PrefixScan([]byte("/system/domain/"))
	if err != nil {
		return nil, fmt.Errorf("scan domains: %w", err)
	}
	defer closeIter(it, &rErr)
	for it.Next() {
		name := strings.TrimPrefix(string(it.Key()), "/system/domain/")
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		val, err := it.Value()
		if err != nil {
			return nil, fmt.Errorf("read domain %s: %w", name, err)
		}
		if string(val) == "disabled" {
			continue
		}
		if !r.HasPostmasterTarget(name) {
			out = append(out, strings.ToLower(name))
		}
	}
	slices.Sort(out)
	return out, nil
}

func unavailable(email string) error {
	return fmt.Errorf("resolve recipient: %s: %w", email, ErrRecipientUnavailable)
}
