# PRD: Multi-Domain, Multi-Tenant Mail Hosting

Status: finalized (2026-07-18) — ready for `norman import`. Large multi-phase epic — see the Phased Plan.

## 1. Introduction / Overview

Bongo-go can technically *receive* for multiple domains (recipient resolution is by full
email address, and domains live in `/system/domain/{domain}` + a `domains` table), but it is
**not** a multi-tenant host today. Concretely, investigation found:

- **DKIM is a single global signer** (`internal/mail/dkim.go`, one signer built at
  `agent.go:987`, applied to every outbound message in `queue/worker.go:139`). Mail from a
  non-primary domain is signed with the primary domain's `d=`, so DMARC alignment fails for
  every hosted domain that isn't the primary.
- **Aliases don't work for inbound delivery.** `/system/alias/{addr}` exists and is honored in
  the compose path (`compose.go resolveLocalUser`), but inbound `ResolveRecipient`
  (`smtp/delivery.go:124`) only does `SELECT id FROM users WHERE username = ?` — the alias
  store is never consulted, so external mail to an alias is rejected at RCPT ("550 Relaying
  denied"). One account therefore cannot receive at multiple addresses/domains today.
- **One address per account.** `UserProfile.email` and `users.username` are single-valued; no
  additional-addresses / identities model; no catch-all.
- **No tenancy.** The `domains` table has only `name, enabled, created_at` — no owner/tenant;
  `is_admin` is global-only; users are not scoped to a domain; no per-domain quotas/policies.
- **Hardcoded sender hygiene bugs:** bounces are `postmaster@localhost`
  (`mail/bounce.go:68`), welcome Message-ID uses `@bongo` (`admin.go:849`).

This PRD turns bongo-go into a multi-tenant mail host: per-domain DKIM, working
alias/multi-address receiving with catch-all, first-class multiple addresses per account with
send-as, and full tenant isolation with delegated domain administrators.

## 2. Goals

- Every hosted domain sends DMARC-aligned mail (per-domain DKIM keys, signed by the From
  domain).
- A single account can receive at, and send as, multiple addresses across multiple domains.
- Inbound delivery correctly resolves aliases, additional addresses, and per-domain catch-all.
- Domains can be owned by a tenant and administered by a scoped domain-admin who cannot see or
  touch other tenants' users, mail, aliases, or settings.
- Per-domain quotas and policies are enforceable.
- No regression for the existing single-domain deployment (example.com stays working
  throughout).

## 3. Design Decisions (from scoping)

- **Tenancy:** Full tenant isolation — domains have an owner; a domain-admin role administers
  only its own domain(s); global admin retains full control.
- **Receiving:** Both — fix alias-based inbound delivery AND add a first-class
  addresses-per-account model; add per-domain catch-all.
- **Sending:** Send-as — a user may send from any address they own/are verified for; DKIM
  signs with that address's domain key.
- **Send-as verification:** Both paths — an admin/domain-admin may grant an address as
  send-capable, AND a user may self-verify ownership via an emailed code. Either sets
  `verified=true` + `can_send=true`.
- **Quota-exceed behaviour:** Reject at RCPT with a 5xx (permanent) SMTP error — no accept-then-
  bounce, no silent loss. (A 4xx temp-defer variant is a possible future refinement, not now.)
- **Address model:** Collapse aliases and identities into ONE address type. A single `addresses`
  entry has `kind` (descriptive), `can_send`, and `verified` flags; an "alias" is simply an
  address with `can_send=false`. One delivery + ownership path, no split concepts.
- **Domain-admin scope:** A delegated domain-admin manages, for their granted domain(s) only:
  users & addresses, the catch-all, per-domain quotas/policies (within global caps), and DKIM
  key generation/rotation + DNS record view.

## 4. Architecture / Data Model (proposed)

- **Address as a first-class entity.** Introduce a global address index that maps an address to
  an owning account and its capabilities, replacing the ad-hoc alias store as the single source
  of truth for "who receives this address":
  - Pebble: `/system/address/{lowercased-addr}` → `AddressRecord{ user_id, domain, kind
    (primary|alias|identity), can_send bool, verified bool }`.
  - SQLite index: `addresses(address PK, user_id, domain, kind, can_send, verified, created_at)`
    for fast delivery + send-as checks.
  - Migration: every existing `users.username` becomes a `primary` address; every
    `/system/alias/{a}` becomes an `alias` address for its target user.
- **Delivery** (`ResolveRecipient`): look up `addresses` (primary/alias/identity) → account;
  then per-domain catch-all; then reject. One code path serves all receiving addresses.
- **Per-domain DKIM:** `DomainRecord` gains `dkim_selector` + `dkim_private_key` (or key file
  ref) + `dkim_enabled`. A domain-keyed signer registry selects the key by From domain;
  messages from a domain with no key are sent unsigned (logged), never signed with the wrong d=.
- **Tenancy:** `DomainRecord` gains `owner_user_id` (tenant). A `domain_admins(domain,
  user_id)` grant table plus `is_admin` (global) drives authorization. All admin queries for a
  domain-admin are filtered to their granted domains.

(Exact proto/field choices to be finalized in the DKIM/address foundation tasks.)

## 5. User Stories

### US-DKIM-1: Per-domain DKIM signing
As an operator, outbound mail from each hosted domain is signed with that domain's own key so
it passes DMARC alignment.
**Acceptance Criteria:**
- [ ] Given domains A and B each with a generated DKIM key, a message with `From: x@A` is
      DKIM-signed with `d=A` and a message with `From: y@B` with `d=B` (assert the
      `DKIM-Signature` `d=` tag equals the From domain).
- [ ] A message from a domain that has no DKIM key configured is sent with **no**
      `DKIM-Signature` header (never signed with another domain's `d=`), and the event is logged.
- [ ] Existing single-domain behaviour is preserved: with only the primary domain keyed, output
      matches today's signing.

### US-DKIM-2: DKIM key management in admin
As an operator, I can generate a DKIM keypair for a domain and see the exact DNS TXT record to
publish.
**Acceptance Criteria:**
- [ ] The admin domain view has a "Generate DKIM key" action; after generating, it displays the
      selector and the full `selector._domainkey.<domain> TXT "v=DKIM1; k=rsa; p=…"` value.
- [ ] The generated private key is stored (not logged) and used to sign that domain's mail.
- [ ] Re-generating rotates the key and updates the displayed record.

### US-RECV-1: Alias delivery actually works
As a user, mail sent to an alias of my account is delivered to my mailbox.
**Acceptance Criteria:**
- [ ] Given account `lance@example.com` and an alias `lance@example.net`, an inbound SMTP
      message (external sender) to `lance@example.net` is accepted at RCPT and delivered to
      lance's INBOX (integration test through the real SMTP path).
- [ ] An address that is neither a user, alias, additional address, nor catch-all is still
      rejected at RCPT.

### US-RECV-2: Multiple first-class addresses per account
As a user, my account can own several addresses across domains and receive mail to all of them
in one mailbox.
**Acceptance Criteria:**
- [ ] An account can have addresses `a@d1` (primary) and `b@d2` (identity); mail to either is
      delivered to that account's INBOX.
- [ ] Removing an address stops delivery to it (subsequent mail is rejected).
- [ ] The existing single-address accounts are migrated so their current email is their
      `primary` address with no behaviour change.

### US-RECV-3: Per-domain catch-all
As a domain admin, I can set a catch-all so unmatched addresses at my domain go to a chosen
account.
**Acceptance Criteria:**
- [ ] With catch-all `*@d -> acct`, mail to a non-existent `random@d` is delivered to `acct`;
      with no catch-all, it is rejected.
- [ ] An explicit address always wins over the catch-all.

### US-SEND-1: Send-as an owned address
As a user, I can choose which of my addresses to send from, and it authenticates correctly.
**Acceptance Criteria:**
- [ ] Compose shows a From selector listing only addresses the account owns with `can_send=true`
      AND `verified=true`; selecting `b@d2` produces a message with `From: b@d2`, envelope MAIL
      FROM `b@d2`, and a `d=d2` DKIM signature.
- [ ] Attempting to send as an address the account does NOT own or that is not verified/send-
      capable (e.g. by tampering the form) is rejected server-side (403/400), not sent.
- [ ] Message-ID host and any bounce/return-path align with the chosen sending domain.

### US-SEND-2: Becoming send-capable (both verification paths)
As a user or admin, an address becomes send-capable either by admin grant or by the user
verifying ownership via an emailed code.
**Acceptance Criteria:**
- [ ] Admin path: an admin/domain-admin marking an address send-capable sets `can_send=true` +
      `verified=true`; it then appears in that account's From selector.
- [ ] Self-serve path: a user adds an address and requests verification; the system sends a
      one-time code to that address; submitting the correct code sets `verified=true` +
      `can_send=true`; an unverified address never appears in the From selector and cannot be
      sent as.
- [ ] Verification codes are single-use, time-limited, and never logged.

### US-TENANT-1: Domain ownership
As a global admin, I can assign a domain to an owner (tenant).
**Acceptance Criteria:**
- [ ] A domain record carries an `owner_user_id`; the admin UI shows and can change it.
- [ ] Creating users/aliases/addresses under a domain associates them with that domain/tenant.

### US-TENANT-2: Delegated domain admin, isolated
As a domain admin, I manage only my own domain and cannot see or affect other tenants.
**Acceptance Criteria:**
- [ ] A user granted domain-admin for `d1` can, for `d1` only: create/disable users and manage
      addresses, set the catch-all, set per-domain quotas/policies (bounded by global caps), and
      generate/rotate the `d1` DKIM key + view its DNS record.
- [ ] Requests targeting `d2`'s users/addresses/catch-all/quota/DKIM/domain settings return 403
      and reveal no `d2` data (list endpoints never include `d2` rows).
- [ ] A domain admin cannot read another tenant's mailbox, blobs, or settings.
- [ ] A global admin retains full cross-domain access.

### US-TENANT-3: Per-domain quotas & policies
As an admin, I can set per-domain limits, and over-quota inbound mail is rejected cleanly.
**Acceptance Criteria:**
- [ ] A domain can have a mailbox/storage quota and a max-users limit; creating a user beyond
      the domain's max-users is rejected.
- [ ] When a mailbox/domain is over its storage quota, an inbound message is rejected at RCPT
      with a 5xx permanent error (the sending server bounces it) — the message is not accepted,
      not silently dropped, and no local copy is stored.
- [ ] Under-quota delivery is unaffected.

### US-HYGIENE-1: Correct sender identities
As an operator, system-generated mail uses proper domains.
**Acceptance Criteria:**
- [ ] Bounces use `postmaster@<relevant-domain>` (not `@localhost`) and an FQDN Message-ID.
- [ ] Welcome emails use an FQDN Message-ID (not `@bongo`).

## 6. Functional Requirements

- FR-1: Per-domain DKIM keyset keyed by domain; signer selects by From domain; unkeyed domains
  send unsigned (logged). (US-DKIM-1)
- FR-2: Admin DKIM key generation + DNS TXT display + rotation; private keys never logged.
  (US-DKIM-2)
- FR-3: Global address index (`/system/address/{addr}` + `addresses` table) as the single
  delivery/ownership source; migrate existing usernames + aliases into it. (US-RECV-1/2)
- FR-4: `ResolveRecipient` (SMTP RCPT + pipeline deliver) resolves via the address index, then
  per-domain catch-all, then reject — used identically on the inbound path. (US-RECV-1/3)
- FR-5: Address management (add/remove/verify addresses per account) in user + admin UIs.
  (US-RECV-2)
- FR-6: Per-domain catch-all configuration. (US-RECV-3)
- FR-7: Compose From-picker limited to owned `can_send && verified` addresses; server-side
  ownership+verified enforcement; DKIM/envelope/Message-ID follow the chosen From. (US-SEND-1)
- FR-7b: Send-as verification — admin/domain-admin grant path AND a self-serve emailed one-time-
  code flow; codes single-use, time-limited, never logged. (US-SEND-2)
- FR-8: Domain ownership (`owner_user_id`) and a `domain_admins` grant model. (US-TENANT-1/2)
- FR-9: Authorization layer that scopes every admin read/write for a domain-admin to their
  granted domains (list endpoints filtered, mutations checked, mailbox/blob access denied
  cross-tenant). (US-TENANT-2)
- FR-10: Per-domain quotas/policies (storage, max-users) enforced at the relevant choke points;
  over-quota inbound rejected at RCPT with a 5xx (no accept-then-bounce). (US-TENANT-3)
- FR-11: Fix bounce sender + welcome/bounce Message-ID to proper FQDNs. (US-HYGIENE-1)
- FR-12: Data migration for existing deployment (usernames→primary addresses, aliases→alias
  addresses, primary domain→owned by the global admin) with a reversible/idempotent path.

## 7. Non-Goals (for this epic; revisit later)

- Self-service public signup / billing / a customer portal (operator or domain-admin creates
  accounts).
- Automated DNS publishing (we display records; the operator publishes them).
- Per-domain separate TLS certificates beyond Traefik/Let's Encrypt SAN handling.
- Cross-server tenant sharding / dedicated per-tenant storage backends.
- Migrating a user's *primary* address between domains (add a new address instead).

## 8. Technical Considerations

- Inbound consistency: compose already resolves aliases but delivery does not — unify both on
  the new address index to remove the split-brain (`compose.go:581` vs `delivery.go:124`).
- DKIM: current signer is built once (`agent.go:987`) and shared by the queue worker
  (`queue/worker.go:32,139`); refactor to a registry looked up by From domain; keep RSA
  (`rsa-sha256`) + selector-per-domain.
- Security is the hard part: tenant isolation must be enforced server-side on **every** admin
  endpoint and any mailbox/blob access — default-deny, filter lists, and adversarial tests
  (a domain admin probing another tenant must get 403 and zero data leakage).
- Writes go through Raft `ApplyCommand`; new stores follow the pebble-read + applier-write
  pattern; SQLite indexes are rebuildable/idempotent.
- Migration must be safe on the live example.com node (idempotent, reversible, run once on
  leader).

## 9. Phased Plan (recommended sequencing)

Independent, high-value, low-risk first; the big security surface last.

- **Phase A — Foundation & quick wins** (mostly independent):
  - FR-11 sender hygiene (bounce/Message-ID).
  - FR-1/FR-2 per-domain DKIM (registry + admin key gen/DNS). *This alone unblocks correct
    hosting of additional domains for sending.*
- **Phase B — Receiving:**
  - FR-3/FR-4 address index + alias-delivery fix + migration; FR-6 catch-all.
  - FR-5 address management UI; FR-12 migration.
- **Phase C — Sending identity:**
  - FR-7 send-as (From-picker + ownership+verified enforcement + DKIM/envelope alignment) and
    FR-7b verification (admin-grant + self-serve emailed code).
- **Phase D — Tenancy (largest, security-critical):**
  - FR-8 ownership + domain-admin grants; FR-9 isolation/authorization layer; FR-10 per-domain
    quotas/policies; adversarial isolation tests.

## 10. Success Metrics

- Mail from each hosted domain passes DKIM+DMARC at a mainstream receiver (verified via headers
  / an integration assertion on `d=`).
- One account demonstrably receives at ≥2 domains and sends as ≥2 domains in an integration test.
- A domain admin cannot access another tenant's data in adversarial tests (0 leaks).
- example.com single-domain deployment shows no regression across the existing suite.

## 11. Resolved Decisions & Remaining Questions

**Resolved (2026-07-18):**
- Send-as verification → **both** admin-grant and self-serve emailed one-time-code (FR-7b).
- Quota-exceed → **reject at RCPT with 5xx** (FR-10); no accept-then-bounce.
- Address model → **collapse** aliases + identities into one `addresses` type with
  `can_send`/`verified` flags (FR-3).
- Domain-admin delegated scope → **users & addresses, catch-all, per-domain quotas/policies,
  and DKIM key/DNS** for granted domains only (US-TENANT-2).

**Still open (can be settled during the relevant phase, not blocking):**
- Filter rules / antispam thresholds: keep global for now, or also delegate per domain later?
  (Default: not delegated in this epic.)
- Storage-quota accounting granularity: per-mailbox vs per-domain aggregate vs both — finalize
  in Phase D against the blob/store model.
- Verification-code transport for a brand-new domain with no working MX yet (bootstrap): admin-
  grant covers this; self-serve email verify assumes the address can already receive.
- Backup/restore + blob GC implications of tenant data — out of scope to build here; revisit so
  a tenant export/delete doesn't strand shared/dedup'd blobs.
