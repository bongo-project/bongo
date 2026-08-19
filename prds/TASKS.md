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

## Phase 12: Trusted submission & scanner wiring

PRD: [prd-trusted-submission-and-scanner-wiring.md](backlog/prd-trusted-submission-and-scanner-wiring.md).
Closes the two gaps that every tester meets in their first hour: mail between accounts on a hosted
domain being junked by DMARC escalation, and antivirus/external-antispam backends that are fully
implemented but never constructed.

**Scope guard carried from Phase 10:** this is NOT a general antispam bypass. 12.3 skips origin
*authentication* interpretation only (SPF/DKIM-alignment/DMARC); phrase, Bayes and antivirus keep
running for trusted mail, and that distinction must be enforced by a test, not by comment.

| # | Task | Status | Notes |
|---|------|--------|-------|
| 12.1 | US-001 / FR-1, FR-2: carry a trust indicator on `QueueEnvelope`. Add the field to `proto/bongo/v1/pipeline.proto` (next free tag is 14) and regenerate. Set it in `internal/smtp/server.go:402/:438` from session state (`s.authed`) — nothing else may set it. **Do not add a 6th positional param:** `EnqueueLocal(from, recipients, blobHash, spfResult, sourceIP)` and `DeliverWithSPF(...)` are already 5 wide and are an interface (`internal/smtp/server.go:31,39`) with 3 implementations; a trailing `bool` is exactly how a careless caller sets trust by accident, which is the failure mode FR-2 exists to prevent. Introduce an options/metadata struct instead. Note `mail.BounceGenerator` uses the 3-arg `Deliver` (`bounce.go:24,140`) — it must keep compiling and stay untrusted (bounces are DKIM-signed as of 10.2). | TODO | |
| 12.2 | US-002 / FR-5, FR-6: `trusted_networks` CIDR list under `smtp` in config (`SMTPConfig`, `internal/config/config.go:124`), default empty. Parse to `[]*net.IPNet` at startup; an unparseable entry is a `Config.Validate` error naming the offending value, not a silent skip. Feed the match into the trust decision from 12.1 — no address is trusted implicitly, including loopback and RFC 1918. Needs: 12.1. | TODO | |
| 12.3 | US-001 / FR-3, FR-4: honour the flag in `internal/pipeline/antispam/builtin.go`. When trusted, skip `VerifyDKIM`-derived alignment input, the SPF result interpretation, and the `applyDMARC` call at `builtin.go:223` — but keep `scorer.Score` and the Bayes blend running, and leave the antivirus stage untouched. Tests must cover BOTH halves: the production reproduction (authenticated `registrations@` → `lance@` on a `p=quarantine` domain lands INBOX, `action=pass`, `TargetMailbox` empty, no `dmarc_*`/`SPF softfail`/`DKIM missing` reason) AND that a trusted message scoring above threshold on content alone still routes to Junk. Also assert an untrusted MX-port message scores byte-identically to today. Needs: 12.1. | TODO | |
| 12.4 | **Bulk rescan from quarantine — prerequisite for 12.5, not optional.** Quarantine is only a safe failure policy if it can be drained, and today it cannot: `BulkAction`'s `release` (`internal/web/handlers/quarantine.go:127`) moves `quarantine/ → deliver/`, which SKIPS every remaining pipeline stage — bulk-releasing unscanned mail would deliver it unscanned. Add a third bulk action (`rescan`) that moves `quarantine/ → incoming/` so the runner reprocesses it. **The move alone is not enough:** `runner.go:135` skips any stage whose checked-flag is set, and the quarantine path sets `MsgFlagVirusChecked` at `runner.go:169` BEFORE acting on the verdict — so rescan must also `ClearFlag` `MsgFlagVirusChecked` + `MsgFlagQuarantined`, drop the stale `antivirus` entry from `env.Stages`, and `WriteEnvelope` before the move. Test: a quarantined envelope with the virus flag set is rescanned and the antivirus stage actually re-runs (assert it is not skipped). | TODO | |
| 12.5 | US-003 / FR-7, FR-12: construct the ClamAV processor. Replace the `"antivirus stage enabled but not yet implemented, skipping"` branch at `internal/agent/agent.go:1053-1054` with `antivirus.NewClamAVProcessor` built from `AntivirusConfig.ClamAV`. **DECIDED (operator, 2026-08-19): an unreachable scanner quarantines the message — it is neither delivered unscanned nor 4xx-deferred, and it is drained via the 12.4 bulk rescan once clamd is back.** Make it a config option (`on_failure`) with quarantine as the documented default, so the choice is explicit rather than emergent — note the default-if-unset path today is `spool/error` (`runner.go:158-163`), which strands mail with no UI at all. EICAR test proves detection; an unreachable-socket test proves the message lands in quarantine, not `error/` and not the inbox. Remove the "not yet implemented" string. Needs: 12.4. | TODO | |
| 12.6 | US-004 / FR-8..FR-11: select the antispam processor from the stage's `Backend` instead of hardcoding builtin at `agent.go:1042`. Wire `NewRspamdProcessor`, `NewSpamAssassinProcessor` and `NewCompositeProcessor` (builtin + one external, stronger verdict wins — it already carries `DmarcResult` across). Add `spamassassin` to `knownBackends` (`config.go:374`) and a `SpamAssassinConfig` block mirroring `RspamdConfig` (`config.go:284`). An unreachable external must degrade to builtin and log — never fail the message. Note this is deliberately the OPPOSITE of 12.5's policy: a dead spam scorer is a scoring-quality problem, a dead virus scanner is a safety one. Tests use injected transports; none may require a running rspamd/spamd. | TODO | |
| 12.7 | (docs) Document what 12.1–12.6 changed for an operator: `trusted_networks` and the recommended pattern for co-located apps (PRD open question 3 — AUTH on :587 vs a trusted CIDR), the antivirus failure policy + the quarantine-then-rescan drain flow, and how to select an external antispam backend — all in `deploy/bongo.example.hcl` in place. Then correct the two places that currently tell testers antivirus is a no-op: `deploy/release-README.md` ("The antivirus pipeline stage is a no-op") and `prds/research/assessment-share-for-testing.md`. Needs: 12.5, 12.6. | TODO | |

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
