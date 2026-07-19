# Tasks

Source PRD: security review follow-ups.

## Phase 1: Security follow-ups — 10 tasks completed 2026-07-03 (see [TASKS-archive.md](TASKS-archive.md))

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
| 2.11 | Rich draft round-trip: SaveDraft records body_mode so a rich draft reopens into the rich editor as HTML, not raw tags | [prd-rich-signatures.md](active/prd-rich-signatures.md) | TODO | split from 2.10(c); lower priority |
| 2.12 | Close verify test-coverage gaps: (US-002) plain send with EMPTY signature_html + plain Signature -> single text/plain ending with plain sig; (US-005) inline logo embedded in the HTML SIGNATURE round-trips to a cid: inline part on the sent message | [prd-rich-signatures.md](active/prd-rich-signatures.md) | DONE (2026-07-17) | added US-002 empty-sig plain + US-005 signature-logo cid tests |

## Phase 3: Multi-Domain — Foundation & Quick Wins (PRD Phase A) — 3 tasks completed 2026-07-18 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 4: Multi-Domain — Receiving (PRD Phase B) — 5 tasks completed 2026-07-18 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 5: Multi-Domain — Sending Identity (PRD Phase C) — 2 tasks completed 2026-07-18 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 6: Multi-Domain — Tenancy & Isolation (PRD Phase D)

Source PRD: multi-domain multi-tenant hosting. Largest, security-critical. Domain ownership,
delegated domain-admins, per-domain quotas, adversarial isolation tests. Default-deny.

| # | Task | PRD | Status | Notes |
|---|------|-----|--------|-------|
| 6.1 | FR-8 domain ownership + grants: `DomainRecord.owner_user_id` (tenant) + `domain_admins(domain,user_id)` grant table; admin UI shows/changes owner; new users/addresses associate with their domain | [prd-multidomain-hosting.md](active/prd-multidomain-hosting.md) | DONE (2026-07-19) | DomainAdminStore: owner at /system/domain/{d}/owner (raw bytes, clobber-safe), grants at /system/domain_admin/{d}/{uid}; AuthorizedDomains helper (global→all/owner→owned/granted/none); global-admin mgmt UI; NO enforcement yet (6.2); review MINOR |
| 6.2 | FR-9 authorization/isolation layer: scope every admin read/write for a domain-admin to granted domains — list endpoints filtered, mutations checked, cross-tenant mailbox/blob access denied; default-deny; global admin retains full access | [prd-multidomain-hosting.md](active/prd-multidomain-hosting.md) | DONE (2026-07-19) | AdminScope middleware + requireGlobalAdmin/DomainAccess/UserAccess helpers across all endpoints; lists filtered; global-only set locked; DeleteUser cascade; review PASS; I tightened AddAddress+GrantSend to also scope the ADDRESS domain (cross-tenant allocation/spoof fix) + tests |
| 6.3 | FR-10 per-domain quotas/policies: storage + max-users limits (within global caps); creating a user beyond max-users rejected; over-quota inbound rejected at RCPT with 5xx (no accept-then-bounce, no silent drop, no local copy) | [prd-multidomain-hosting.md](active/prd-multidomain-hosting.md) | TODO | US-TENANT-3; needs: 6.1 |
| 6.4 | Adversarial tenant-isolation tests: a domain admin probing another tenant's users/addresses/catch-all/quota/DKIM/mailbox/blobs gets 403 and zero data leakage across all admin endpoints | [prd-multidomain-hosting.md](active/prd-multidomain-hosting.md) | TODO | verifies US-TENANT-2; needs: 6.2, 6.3 |
