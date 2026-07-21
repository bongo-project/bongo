# Tasks

Source PRD: security review follow-ups.

## Phase 1: Security follow-ups — 10 tasks completed 2026-07-03 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 2: Rich Signatures & HTML Composer — 12 tasks completed 2026-07-20 (see [TASKS-archive.md](TASKS-archive.md))

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

## Phase 8: Documentation

Documentation sprint (user-requested, previously parked). Goal: an operator can
stand up and run multi-domain hosting from the docs, and admins get in-context
help in the UI. In-app help must be CSP-safe (no inline scripts/styles) and i18n
across all 4 locales.

| # | Task | Status | Notes |
|---|------|--------|-------|
| 8.1 | In-app help for admin domain & tenancy settings: concise help text / tooltips on the domain Manage page (DKIM generate/rotate, DNS status states ok/fail/neutral/error, DNS setup guide, catch-all, owner, domain-admins, quotas) and other admin forms where the action is non-obvious. CSP-safe (CSS classes + data-attrs, no inline JS/style); i18n ×4 locales. | DONE (2026-07-21) | Domain page already had per-section help (7.1/7.3); added the genuine gaps: a DNS status-state legend (Valid/Missing/Not generated/Could not check) below the checks table, and a DKIM re-publish reminder near generate/rotate. CSP-safe (`<small>`/utility classes, no inline JS/style); 5 new i18n keys ×4 locales (i18n coverage test enforces parity); templ render test asserts the legend. |
| 8.2 | Operator guide `docs/multi-domain-hosting.md`: end-to-end how-to — add a domain, point DNS (use the `/admin/domains/{domain}` DNS setup guide / BIND zone), generate + publish DKIM, verify SPF/DMARC, set catch-all, delegate domain-admins, set per-domain quotas (max-users + storage), client autoconfig (autoconfig/autodiscover/SRV). Link from README. | DONE (2026-07-21) | Written from a verified feature map (routes/records/DKIM/quotas/autoconfig grounded in the real code). Shared-host model up front; per-step DNS record table, status-check reading, DKIM rotation caveat, tenancy isolation, quota enforcement points, autoconfig endpoints; checklist. Linked from the new top-level README. |
| 8.3 | Getting-started / deployment doc refresh: consolidate `deploy/README.md` + the generic example config into a top-level quickstart (single-node Docker + Traefik) so a new operator can go from clone to running server. | DONE (2026-07-21) | New top-level `README.md`: what bongo is, dev quickstart (`make dev`), single-server production quickstart distilled from deploy/README.md (names → DNS → DKIM → TLS → run → first admin), multi-domain pointer, server roles, and a docs index. Links out to deploy/README.md and the docs/ guides. |

## Phase 9: Attachment deduplication (de-MIME)

Completes the original design (project PRD §5.2–5.3): store a message skeleton +
deduplicated content-addressed attachment blobs, reconstruct byte-exact on read.
PRD: [prd-attachment-dedup.md](active/prd-attachment-dedup.md). Decisions:
encoded-bytes dedup (DKIM-safe), new mail only, default on with an ingest
self-check + fallback, threshold-gated (32 KB default). **Start with 9.1 (spike)
— it de-risks the whole feature before committing to the build.**

| # | Task | Status | Notes |
|---|------|--------|-------|
| 9.1 | (spike) Byte-exact encoded-span extraction + skeleton representation. Prototype: given a raw RFC822 message, locate the exact encoded body span of each attachment part ≥ threshold, build a skeleton, and reconstruct byte-for-byte. Prove round-trip on base64/QP, nested multipart, and a DKIM-signed message (re-verify passes). Decide the on-disk skeleton representation. | DONE (2026-07-21) | FEASIBLE. internal/attsplit spike: manual MIME boundary scan locates each attachment part's exact ENCODED body byte-range (no enmime decode); Split partitions the raw into an ordered sequence of literal spans + attachment refs, Reconstruct concatenates -> byte-exact by construction. 8 round-trip tests green: base64, quoted-printable, nested multipart, multiple attachments, DKIM-signed (byte-equal => sig preserved), plain passthrough, below-threshold skip, and dedup (same encoded attachment in two different messages -> same SHA-256). Recommended 9.2 on-disk form: skeleton blob = concatenated literal spans + an ordered splice index in MailMeta (segment = literal-len | attachment-hash+len). Caveats for prod: assumes CRLF; add delimiter-suffix check for boundary-prefix ambiguity; decide handling of large INLINE (non-attachment) parts and top-level single-part attachments. |
| 9.2 | Skeleton storage + `FullRawMessage(meta)` retrieval: reconstruct for split records, passthrough for legacy whole-raw. Mark split records via splice metadata. | DONE (2026-07-21) | proto: MailMeta.layout (repeated MessageSegment LITERAL|ATTACHMENT) marks split records; empty = legacy whole-raw. attsplit.Layout() exposes skeleton+segments proto-agnostically. store.PrepareSplit(raw,minSize) -> SplitMessage (skeleton bytes + layout + attachment BlobRefs + encoded bytes), nil below threshold. store.FullRawMessage(meta,get) reconstructs split records (skeleton interleaved with attachment blobs), passthrough for legacy. Tests: byte-exact round-trip, legacy passthrough, below-threshold nil, shared-attachment dedup (2nd msg adds 1 blob), corrupt-layout error. Full store -race green. |
| 9.3 | Threshold config knob `dedup_min_size` (default 32 KB). | TODO | US-006, FR-9. Small; needed by 9.4/9.6. |
| 9.4 | Inbound ingest split in `pipeline/deliver`: extract qualifying attachment blobs, store skeleton, populate `MailMeta.Attachments` hashes, with the ingest self-check + whole-raw fallback (FR-7). | TODO | US-001/005/007, FR-1/2/7. needs: 9.2, 9.3 |
| 9.5 | Route all full-message readers through `FullRawMessage`: IMAP FETCH/BODY, web message view (`applyEnvelopeBody`), `ServeAttachment`/`ServeBlob` whole-message reads, `ExportEML`, DKIM/antispam re-parse, backup/export. | TODO | FR-5. needs: 9.2 |
| 9.6 | Outbound unification in compose/send: store skeleton + deduped attachment blobs instead of the whole raw plus separate attachment blobs; reconstruct byte-exact (incl. DKIM on send). | TODO | US-008. needs: 9.2, 9.3 |
| 9.7 | Blob GC / reference-counting counts `MailMeta.Attachments[].Hash` (and skeleton hashes) as live refs so shared attachments survive until the last referencing message is deleted. Verify what GC actually exists first. | TODO | US-009, FR-8. needs: 9.4 or 9.6 |
| 9.8 | (optional) Observability: `bongo_attachment_dedup_*` counters — blobs written vs deduped, bytes saved. | TODO | Open question in the PRD; nice-to-have. |

## Backlog (unscheduled)

| # | Task | Status | Notes |
|---|------|--------|-------|
| B1 | De-brand the repository: replace all deployment-specific domain references (167 across ~25 files — deploy/ configs incl. bongo.prod.hcl + node name, .env.example, autoconfig comment, docs/, prds/ logs, tests) with generic placeholders (`example.com`, `mail.example.com`, node `bongo-1`) driven by config/env, so the repo mentions only `bongo`/`bongo-go`. | DONE (2026-07-20) | Working-tree scrub done: only 1 runtime match (a comment), rest configs/docs/tests; edge cases handled (reversed-domain assertion, case-insensitivity variant, ssh-key path); build + full `-race` suites green. Live `/mnt/data/bongo` config keeps real values (not in git). History also purged: git filter-branch rewrote all commits + tags (backup bundle kept), force-pushed main + tags to origin — zero legacy-domain references across every ref. |
| 7.2 | DNS config status checks (MX/SPF/DKIM/DMARC) on the domain detail page, comparing ACTUAL DNS vs EXPECTED-for-this-server. FIX MX bug: mark ✓ only if an MX host matches the server mail hostname (SMTP.Hostname), not just any-MX-exists (example.net points to OVH but shows valid). | DONE (2026-07-19) | buildDNSChecks pure fn (4-state ok/fail/neutral/error), concurrent timed lookups; MX now points-to-us; SPF authorises-server; DKIM published p== ours; DMARC v=DMARC1; list MX col uses fixed check; I fixed 2 MINORs (hostname fallback, gofmt) |
| B2 | Migrate SMTP auth throttling to the shared `authlimit` package: `internal/smtp/auth_throttle.go` duplicates `internal/authlimit` (per-key sliding-window limiter). Replace the bespoke authFail map/window/sweep with `authlimit.New/Record/Blocked/Reset`, preserving the 5-fail / 15m lockout behaviour; delete the duplicate impl. | DONE (2026-07-21) | `Backend.authFail` is now `*authlimit.Limiter`; deleted the bespoke map/sweep + 3 methods; kept `maxAuthFailures`/`authFailWindow` consts. Existing throttle tests (the behaviour spec) + authlimit's own suite green. No behaviour change. |
| B3 | Queue worker: honour config `poll_interval`. `internal/queue/worker.go:18` `pollInterval` is a hardcoded 5s const (ticker at :66); wire it to the config like the pipeline runner (`agent.go` uses `Config.Pipeline.PipelinePollInterval()`) so outbound-delivery latency is tunable. | DONE (2026-07-21) | Added optional `queue { poll_interval }` block (`QueueConfig` pointer, nil-safe `QueuePollInterval()` default 5s) + `queue.WithPollInterval` functional option (mirrors runner); wired in agent.go. Config + worker tests. |
| B4 | MIME parse-once: the message view and related paths call `enmime.ReadEnvelope` multiple times per message (`internal/web/handlers/mail.go` has several call sites). Parse once and reuse the envelope to cut CPU on large messages. | DONE (2026-07-21) | Extracted `applyEnvelopeBody` — the single MIME parse+sanitize+attachment-merge path shared by `ShowMessage` and `messageDetailFromMeta` (removed ~110 lines of byte-identical duplication and one of the redundant `ReadEnvelope` call sites). Unit test asserts body+attachment population + proto-hash merge. |
| B5 | Unread badge INBOX-only: `internal/web/handlers/sse.go` pushes `TotalUnreadCount` (sum across ALL folders incl. Sent/Trash/Junk) to `#unread-badge`; change to INBOX-only unread so the badge reflects actionable mail. | DONE (2026-07-21) | Added `MailboxStore.UnreadCount(userID, mailbox)`; SSE badge now uses INBOX-only. Badge is SSE-populated only (no other server render site). Test proves Junk unread does not inflate the INBOX badge. |
| B6 | Observability: add app-specific Grafana dashboards beyond the generic per-app Hostbox Logs dashboard — e.g. SMTP throughput, outbound queue depth, delivery latency, spam-scoring distribution, per-domain volume. | DONE (2026-07-21) | Instrumented new Prometheus metrics (queue depth/oldest-pending gauges, delivery-latency histogram, queue-result counter, antispam-score histogram) with tests. Added `latency_seconds` to the delivered log line + an "Outbound delivery latency" panel to the log-based `bongo-mail.json` (works today). New `bongo-metrics.json` Prometheus dashboard for when Prometheus is enabled — NOTE: the Prometheus datasource is still commented out in `deploy/nomad/loki-stack.nomad.hcl`, so those panels are inert until an operator wires it. |
| B7 | Security: multi-node transport hardening items documented in `docs/security-review-2026-07-06.md` (deferred). Only relevant when running a real multi-node Raft cluster — the current deployment is single-node. Revisit before any multi-node rollout. | DEFERRED + gated (2026-07-21) | Per user decision: implementation deferred (changing the live single-node consensus path is riskier than the non-exploitable flaw). Added a hard startup gate — a `server` with non-empty `cluster.join` refuses to start without `cluster.gossip.encryption_key` + `cluster.tls.{cert,key,ca}` (`Config.Validate`, 4 test cases; single-node unaffected) — plus `docs/multi-node-cluster-checklist.md`. The H4/M8/L6 code fixes remain TODO, now blocked-open by the gate so they can't be skipped before a cluster forms. |
