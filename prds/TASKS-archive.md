# Tasks Archive

Append-only archive of fully-completed phases collapsed out of TASKS.md.

## Phase 1: Security follow-ups

| # | Task | PRD | Status | Notes |
|---|------|-----|--------|-------|
| 1.1 | M3: per-IP inbound SMTP rate limiting | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | messages_per_minute + max_connections_per_ip |
| 1.2 | M1: CRLF header-injection hardening (compose, bounce) | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | internal/mail sanitize helpers |
| 1.3 | H3: fail loudly on self-signed TLS fallback | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | LoadCert + mailListenerTLS |
| 1.4 | M2: validate blob hash | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | store.IsValidHash + handler 400 |
| 1.5 | H4: non-root container | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | non-root uid 10001, su-exec + setcap |
| 1.6 | M5: relay AUTH requires STARTTLS | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | shouldSendAuth + allow_insecure_auth |
| 1.7 | Low: honor max_message_size + parse MIME once | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | ParseSize wired; MIME-once deferred |
| 1.8 | Low: per-purpose key derivation | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | DeriveKey at choke points; verified live |
| 1.9 | CI: build + publish image to GHCR; compose pulls it | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | ghcr.io/lhaig/bongo-go |
| 1.10 | Discovered: raft tolerates absent cluster TLS cert on first boot | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | warn + plaintext fallback |

## Phase 3: Multi-Domain — Foundation & Quick Wins (PRD Phase A) — 3 tasks completed 2026-07-18

Source PRD: [prd-multidomain-hosting.md](done/prd-multidomain-hosting.md). Sender hygiene + per-domain DKIM.

| # | Task | PRD | Status | Notes |
|---|------|-----|--------|-------|
| 3.1 | FR-11 sender hygiene: bounces use `postmaster@<relevant-domain>` (not `@localhost`) with an FQDN Message-ID; welcome email uses an FQDN Message-ID (not `@bongo`) | [prd-multidomain-hosting.md](done/prd-multidomain-hosting.md) | DONE (2026-07-18) | `mail.ResolveServerDomain` chain; fixed all 4 bounce localhost sites + both welcome Message-ID sites; review PASS |
| 3.2 | FR-1 per-domain DKIM signer registry: `DomainRecord` gains `dkim_selector`+`dkim_private_key`+`dkim_enabled`; refactor single global signer (`agent.go:987`, `queue/worker.go:139`) to a registry keyed by From domain; unkeyed domains send unsigned + logged (never wrong `d=`) | [prd-multidomain-hosting.md](done/prd-multidomain-hosting.md) | DONE (2026-07-18) | `mail.DKIMRegistry` keyed by From-header domain; new `DomainRecord`/domain.proto; worker uses registry, unkeyed→unsigned+log; single-domain seeded from config (byte-compat); review MINOR |
| 3.3 | FR-2 admin DKIM key management: "Generate DKIM key" action per domain → store private key (never logged), display `selector._domainkey.<domain> TXT` record; rotation regenerates + updates display | [prd-multidomain-hosting.md](done/prd-multidomain-hosting.md) | DONE (2026-07-18) | GenerateDKIMKey (RSA-2048, PKCS8) stores DomainRecord@/system/domain/{d}/dkim via Raft, Adds to shared registry (immediate signing), LoadStoredDKIMKeys at startup; TXT = public key only; key never logged/rendered; review PASS |

## Phase 4: Multi-Domain — Receiving (PRD Phase B) — 5 tasks completed 2026-07-18

Source PRD: [prd-multidomain-hosting.md](done/prd-multidomain-hosting.md). Address index + alias-delivery fix + catch-all + address management.

| # | Task | Status | Notes |
|---|------|--------|-------|
| 4.1 | FR-3 global address index (AddressRecord proto + AddressStore + system.db `addresses`) | DONE (2026-07-18) | Put/Get/Delete/ListByUser; Raft-first + SQLite mirror (errors not swallowed); case-insensitive; proto clean. Commit aa8fc2c |
| 4.2 | FR-12 migration (usernames→primary, aliases→alias) | DONE (2026-07-18) | store.PopulateAddressIndex leader-only/idempotent/additive; alias username→user_id resolved. Commit ad6d1a9 |
| 4.3 | FR-4 unify delivery (fix alias inbound, US-RECV-1) | DONE (2026-07-18) | store.RecipientResolver 3-tier; ONE shared instance across compose+pipeline+SMTP RCPT; no O(n) scan; integration test external→alias→INBOX. Commit f58ce50 |
| 4.4 | FR-6 per-domain catch-all | DONE (2026-07-18) | CatchAllStore /system/catchall/{domain}; resolver tier-3 (explicit wins, no-key→reject open-relay guard); admin Set/ClearCatchAll. Commit 4ca9867 |
| 4.5 | FR-5 admin address management + migration create-only | DONE (2026-07-18) | admin Add/RemoveAddress; primary-delete + unknown-user guards; PopulateAddressIndex create-only; CSP-clean; i18n parity. Commit 902ce70 |

## Phase 5: Multi-Domain — Sending Identity (PRD Phase C) — 2 tasks completed 2026-07-18

Source PRD: [prd-multidomain-hosting.md](done/prd-multidomain-hosting.md). Send-as owned/verified addresses + both verification paths.

| # | Task | Status | Notes |
|---|------|--------|-------|
| 5.1 | FR-7 send-as: From-picker (owned can_send&&verified), server-side enforcement, From/envelope/Message-ID/DKIM d= alignment | DONE (2026-07-18) | server-rendered From `<select>`; tampered/unowned/unverified→403, default path bypasses (back-compat); fromAddr drives all identity sites. Commit 1f3fc74 |
| 5.2 | FR-7b verification: admin grant + self-serve emailed one-time code | DONE (2026-07-18) | AddressVerifyStore (crypto/rand code → sha256 hash stored not plaintext, never logged, single-use, expiry, max-5-attempts, constant-time); cross-user claim rejected; emailed via queue. Commits f89ac1c |

## Phase 6: Multi-Domain — Tenancy & Isolation (PRD Phase D) — 4 tasks completed 2026-07-19

Source PRD: [prd-multidomain-hosting.md](done/prd-multidomain-hosting.md). Domain ownership, delegated domain-admins, per-domain quotas, adversarial isolation. Default-deny.

| # | Task | Status | Notes |
|---|------|--------|-------|
| 6.1 | FR-8 domain ownership + domain-admin grant model | DONE (2026-07-19) | DomainAdminStore (owner @/system/domain/{d}/owner clobber-safe, grants @/system/domain_admin/{d}/{uid}); AuthorizedDomains helper; global-admin mgmt UI; no enforcement. Commit fd27844 |
| 6.2 | FR-9 default-deny isolation layer | DONE (2026-07-19) | AdminScope middleware + requireGlobalAdmin/DomainAccess/UserAccess across all endpoints; lists filtered (zero leak); DeleteUser cascade; AddAddress+GrantSend also scope address-domain. Commit 38a15b6 |
| 6.3 | FR-10 per-domain max-users + storage quotas | DONE (2026-07-19) | DomainQuota proto + store; max-users at CreateUser; leader recompute job; RCPT 5xx before store; global-admin sets/domain-admin views. Commit ac9ca66 |
| 6.4 | Adversarial isolation sweep | DONE (2026-07-19) | 35-case master sweep, NO real gaps (guards held); RemoveAddress address-domain guard added for symmetry; gosec clean. Commit f14fb3e |

## Phase 2: Rich Signatures & HTML Composer

Source PRD: rich signatures & HTML composer. Full rich composer + HTML signatures;
contenteditable WYSIWYG with HTML-source toggle; inline images via cid:; plain/rich
compose toggle.

| # | Task | PRD | Status | Notes |
|---|------|-----|--------|-------|
| 2.1 | Proto + store: add `signature_html` to UserPreferences (keep `signature` as plain fallback), regen pb.go, PreferenceStore r/w + tests | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-12) | signature_html field 14; buf regen; store round-trips |
| 2.2 | Server-side HTML sanitizer for signature/compose (allow-list tags, links, img data:/cid: only, colour styles); reuse display sanitizer; tests | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-12) | internal/web/sanitize; data:/cid: only, no remote img |
| 2.3 | MIME builder: refactor compose.go into alternative/related/mixed tree builder + data:->cid: extraction helper; table tests + plain-only backward compat | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-12) | compose_mime.go buildMIME + extractDataURIImages; sha256 cid, media whitelist |
| 2.4 | Reusable contenteditable WYSIWYG Alpine component in bongo.js: toolbar (bold/italic/link/colour/insert-image), HTML-source toggle, live preview, hidden-field post, image->data: with size cap | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-12) | richeditor.js + Alpine component; compose wiring deferred to 2.6, i18n to 2.8 |
| 2.5 | Settings: wire editor into HTML signature; sanitize on save (2.2) + derive editable plain text | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-13) | settings HTML signature; richEditor field-name generalised; MINOR: empty sig cannot be cleared (follow-up) |
| 2.6 | Rich compose body: reuse editor for body, plain/rich toggle, hidden HTML input + plain derivation, prefill HTML signature, send via MIME builder | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-13) | x-if plain/rich toggle; rich->buildMIME multipart/alternative; MINOR edge-cases -> 2.10 |
| 2.7 | Inline image / logo plumbing: cid: parts get Content-ID + Content-Disposition: inline; blob-store images; per-image + total-size caps | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-13) | per-image 1MB + total 10MB caps; 400 before writes; lint fixed |
| 2.8 | i18n + a11y: translation strings for toolbar/source-toggle; aria-labels + keyboard operability | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-13) | shared RichEditorField component; 11 i18n keys x4 locales; role=textbox |
| 2.9 | Integration test: HTML signature + logo -> rich compose -> send -> assert MIME (alternative + both parts + inline cid + sanitized); plain-only still text/plain | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-13) | TestRichComposeEndToEnd: rich MIME + plain; -tags integration |
| 2.10 | Signature fixes: (a) SaveSettings clears an emptied signature; (b) new-message plain mode prefills the plain signature (composeInitialBody returns body for new) without double-sig | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-13) | r.Form.Has clears sig; composeInitialBody returns body for new |
| 2.11 | Rich draft round-trip: SaveDraft records body_mode so a rich draft reopens into the rich editor as HTML, not raw tags | [prd-rich-signatures.md](done/prd-rich-signatures.md) | DONE (2026-07-20) | SaveDraft honours body_mode='rich' → multipart/alternative HTML (buildRichMessage gained includeSignature bool; drafts skip sig to avoid dup); new EditDraft handler + GET /compose/draft/{mailbox}/{uid} parses the draft (enmime) and reopens rich→editor / plain→textarea; Drafts rows link to the editor via msgRowLink; ComposePage(+htmlBody), composeDefaultBodyMode(mode,htmlBody); compose.editDraft i18n ×4; 3 round-trip tests; handlers -race + integration green |
| 2.12 | Close verify test-coverage gaps: (US-002) plain send with EMPTY signature_html + plain Signature -> single text/plain ending with plain sig; (US-005) inline logo embedded in the HTML SIGNATURE round-trips to a cid: inline part on the sent message | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-17) | added US-002 empty-sig plain + US-005 signature-logo cid tests |
