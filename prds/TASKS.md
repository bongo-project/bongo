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

## Phase 6: Multi-Domain — Tenancy & Isolation (PRD Phase D) — 4 tasks completed 2026-07-19 (see [TASKS-archive.md](TASKS-archive.md))

---

**Multi-domain multi-tenant hosting epic (PRD Phases A–D / norman Phases 3–6): COMPLETE + DEPLOYED as v0.6.0 (2026-07-19).**
PRD in [done/prd-multidomain-hosting.md](done/prd-multidomain-hosting.md).

## Phase 7: Admin UX follow-ups

Post-epic UI improvements (user-requested). Docs get a dedicated sprint later.

| # | Task | Status | Notes |
|---|------|--------|-------|
| 7.1 | Per-domain detail sub-screen + DNS status: move all per-domain settings off the overflowing 9-col table onto a `/admin/domains/{domain}` page; list = Domain·Status·MX·Manage; MX-not-configured indicator (extensible to SPF/DKIM/DMARC); respect 6.2 isolation on the new route | DONE (2026-07-19) | ShowDomain (requireDomainAccess + 404-on-unknown); AdminDomainDetail stacked sections, DKIM prominent; list slimmed to 4 cols + Manage; MX ✓/✗; adversarial 403 test; I fixed 2 MINORs (u-mb-025 class, DeleteDomain→hxRedirect for hx-post delete) |
| 7.3 | Domain-owner DNS setup guide: server-generated per-domain DNS records (A/AAAA, MX→shared SMTP.Hostname, SPF, DKIM real key, DMARC, autodiscover/autoconfig/imaps/submission/caldavs/carddavs SRV) on the Manage page as a copyable table + downloadable BIND zone at `/admin/domains/{domain}/dns.zone`. Reuse the same expected values as the 7.2 status checks so guide and checks never drift. Domain-admins see it for their domains (respect 6.2 isolation). Empty-p= DKIM shows a "generate key first" notice, never a broken record. | DONE (2026-07-19) | dns_guide.go pure buildDNSGuide + dnsGuideZone (14 unit tests); resolveServerIPs from mailHostname; DownloadDNSZone route w/ requireDomainAccess + adversarial 403 + attachment test; DNSGuideRow type + Manage-page table w/ per-row copy (reuses data-copy-previous) + zone download; DKIM-pending notice; 10 i18n keys x4 locales; -race green |

## Backlog (unscheduled)

| # | Task | Status | Notes |
|---|------|--------|-------|
| B1 | De-brand the repository: remove ALL `deployment` references (157 across ~23 files — deploy/ configs incl. bongo.prod.hcl + node name `bongo-1`, .env.example, autoconfig comment, docs/, prds/ logs, tests). Repo should mention only `bongo`/`bongo-go`; deployment specifics become generic placeholders (e.g. `example.com`, `mail.example.com`) driven by config/env. USER-AUTHORISED: rewrite git history + FORCE-PUSH if needed to purge it from history. | TODO | Requested 2026-07-19. CAVEAT: the LIVE server config at `/mnt/data/bongo` is NOT in git (manual scp) and keeps its real deployment values — only the REPO goes generic; verify no runtime code depends on the literal string "deployment" before scrubbing. Do as its own focused pass (grep -rniI deployment), then history-scrub (git filter-repo/BFG) + force-push. |
| 7.2 | DNS config status checks (MX/SPF/DKIM/DMARC) on the domain detail page, comparing ACTUAL DNS vs EXPECTED-for-this-server. FIX MX bug: mark ✓ only if an MX host matches the server mail hostname (SMTP.Hostname), not just any-MX-exists (example.net points to OVH but shows valid). | DONE (2026-07-19) | buildDNSChecks pure fn (4-state ok/fail/neutral/error), concurrent timed lookups; MX now points-to-us; SPF authorises-server; DKIM published p== ours; DMARC v=DMARC1; list MX col uses fixed check; I fixed 2 MINORs (hostname fallback, gofmt) |
