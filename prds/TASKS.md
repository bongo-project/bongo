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

## Phase 7: Admin UX follow-ups — 3 tasks completed 2026-07-19 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 8: Documentation — 3 tasks completed 2026-07-21 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 9: Attachment deduplication (de-MIME) — 8 tasks completed 2026-07-22 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 10: Outbound deliverability & View Original — 10 tasks completed 2026-08-17 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 11: Verification gaps from Phase 10 — 3 tasks completed 2026-08-17 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 12: Trusted submission & scanner wiring — 8 tasks completed 2026-08-20 (see [TASKS-archive.md](TASKS-archive.md))

## Backlog (unscheduled)

| # | Task | Status | Notes |
|---|------|--------|-------|
| B1 | De-brand the repository: replace all deployment-specific domain references (167 across ~25 files — deploy/ configs incl. bongo.prod.hcl + node name, .env.example, autoconfig comment, docs/, prds/ logs, tests) with generic placeholders (`example.com`, `mail.example.com`, node `bongo-1`) driven by config/env, so the repo mentions only `bongo`/`bongo-go`. | DONE (2026-07-20) | Working-tree scrub done: only 1 runtime match (a comment), rest configs/docs/tests; edge cases handled (reversed-domain assertion, case-insensitivity variant, ssh-key path); build + full `-race` suites green. Live `/mnt/data/bongo` config keeps real values (not in git). History also purged: git filter-branch rewrote all commits + tags (backup bundle kept), force-pushed main + tags to origin — zero legacy-domain references across every ref. |
| B2 | Migrate SMTP auth throttling to the shared `authlimit` package: `internal/smtp/auth_throttle.go` duplicates `internal/authlimit` (per-key sliding-window limiter). Replace the bespoke authFail map/window/sweep with `authlimit.New/Record/Blocked/Reset`, preserving the 5-fail / 15m lockout behaviour; delete the duplicate impl. | DONE (2026-07-21) | `Backend.authFail` is now `*authlimit.Limiter`; deleted the bespoke map/sweep + 3 methods; kept `maxAuthFailures`/`authFailWindow` consts. Existing throttle tests (the behaviour spec) + authlimit's own suite green. No behaviour change. |
| B3 | Queue worker: honour config `poll_interval`. `internal/queue/worker.go:18` `pollInterval` is a hardcoded 5s const (ticker at :66); wire it to the config like the pipeline runner (`agent.go` uses `Config.Pipeline.PipelinePollInterval()`) so outbound-delivery latency is tunable. | DONE (2026-07-21) | Added optional `queue { poll_interval }` block (`QueueConfig` pointer, nil-safe `QueuePollInterval()` default 5s) + `queue.WithPollInterval` functional option (mirrors runner); wired in agent.go. Config + worker tests. |
| B4 | MIME parse-once: the message view and related paths call `enmime.ReadEnvelope` multiple times per message (`internal/web/handlers/mail.go` has several call sites). Parse once and reuse the envelope to cut CPU on large messages. | DONE (2026-07-21) | Extracted `applyEnvelopeBody` — the single MIME parse+sanitize+attachment-merge path shared by `ShowMessage` and `messageDetailFromMeta` (removed ~110 lines of byte-identical duplication and one of the redundant `ReadEnvelope` call sites). Unit test asserts body+attachment population + proto-hash merge. |
| B5 | Unread badge INBOX-only: `internal/web/handlers/sse.go` pushes `TotalUnreadCount` (sum across ALL folders incl. Sent/Trash/Junk) to `#unread-badge`; change to INBOX-only unread so the badge reflects actionable mail. | DONE (2026-07-21) | Added `MailboxStore.UnreadCount(userID, mailbox)`; SSE badge now uses INBOX-only. Badge is SSE-populated only (no other server render site). Test proves Junk unread does not inflate the INBOX badge. |
| B6 | Observability: add app-specific Grafana dashboards beyond the generic per-app Hostbox Logs dashboard — e.g. SMTP throughput, outbound queue depth, delivery latency, spam-scoring distribution, per-domain volume. | DONE (2026-07-21) | Instrumented new Prometheus metrics (queue depth/oldest-pending gauges, delivery-latency histogram, queue-result counter, antispam-score histogram) with tests. Added `latency_seconds` to the delivered log line + an "Outbound delivery latency" panel to the log-based `bongo-mail.json` (works today). New `bongo-metrics.json` Prometheus dashboard for when Prometheus is enabled — NOTE: the Prometheus datasource is still commented out in `deploy/nomad/loki-stack.nomad.hcl`, so those panels are inert until an operator wires it. |
| B7 | Security: multi-node transport hardening items documented in `docs/security-review-2026-07-06.md` (deferred). Only relevant when running a real multi-node Raft cluster — the current deployment is single-node. Revisit before any multi-node rollout. | DEFERRED + gated (2026-07-21) | Per user decision: implementation deferred (changing the live single-node consensus path is riskier than the non-exploitable flaw). Added a hard startup gate — a `server` with non-empty `cluster.join` refuses to start without `cluster.gossip.encryption_key` + `cluster.tls.{cert,key,ca}` (`Config.Validate`, 4 test cases; single-node unaffected) — plus `docs/multi-node-cluster-checklist.md`. The H4/M8/L6 code fixes remain TODO, now blocked-open by the gate so they can't be skipped before a cluster forms. |
| B8 | **`Config.Validate()` is dead code in production.** Nothing outside tests calls it — not `LoadWithDiscovery`, not `runServe`. So `server.role` validity, the agent-role requirements, and the multi-node cluster transport gate (`config.go:379`) are all test-only guards today. Wiring it in is worthwhile but is a DEPLOYMENT-AFFECTING change: any existing deployment with `cluster.join` non-empty and no `cluster.gossip.encryption_key` + `cluster.tls.{cert,key,ca}` boots today and would refuse to boot afterwards. Needs its own change, a release note, and a check of the live prod config before enabling. Surfaced during 12.2, where a worker wired it in as a side effect and it was reverted. | TODO | Do not fold into an unrelated task |
