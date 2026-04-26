# Continuous Improvement Loop — Design Exploration

**Status:** Thought experiment / design sketch. Not yet scoped into a sprint.
**Last discussed:** 2026-04-11

## Purpose

Explore how to rig bongo-go so that Claude (or any AI assistant) can continuously
observe the running system, triage problems, and propose — or eventually apply —
improvements to the code and configuration. Two distinct but related directions:

1. **Generic code observability + improvement loop** — bugs, performance, regressions
2. **Spam scoring analysis + optimisation loop** — tuning detection based on user behaviour

Both share the same underlying shape: emit structured data, let the assistant
analyse it, feed suggestions back in under staged autonomy. They differ in
blast radius, reversibility, and how quickly ground truth arrives.

---

## Part 1 — Generic code improvement loop

### The core problem

Putting bongo-go on the internet means it starts handling real mail with real
users. Bugs, regressions, and performance cliffs need to be noticed and fixed
quickly. The question is how much of that loop can be handed to an AI assistant
without creating new failure modes worse than the ones being fixed.

Mail is unforgiving: lost messages are unrecoverable, delivery bugs damage
reputation for weeks, and Raft means a bad write propagates before anyone can
react. That shapes every decision below.

### Getting data to the assistant

The existing agent already has Raft, admin HTTP routes, and protocol servers.
The missing layer is telemetry egress.

- **Structured logs + metrics + traces** via a standard stack:
  Prometheus/Grafana, Loki (or Grafana Cloud), OpenTelemetry, Sentry for panics.
  All have MCP servers or HTTP APIs an assistant can query directly.
- **A `/admin/diag` "diagnostic bundle" endpoint** — a single call returning
  a situational snapshot: queue depth, Raft state (leader, lag, last index),
  Pebble stats, blob GC state, antispam verdict counts, SMTP reason codes,
  goroutine profile. One call = full picture.
- **Alert-driven invocation** instead of polling. Prometheus Alertmanager →
  webhook → `RemoteTrigger` to wake a Claude session with the alert payload.
  Cheaper than polling, and only engages when something is actually off.
- **Scheduled sanity passes** via cron triggers for lower-urgency checks:
  daily queue age scan, weekly blob store growth, spam score drift.

### Staged autonomy ladder

Mail has three properties that make autonomous code changes dangerous:

1. **Lost mail is unrecoverable.** A silent drop in `deliver.go` is worse than downtime.
2. **Reputation is stateful.** One bad outbound bug = weeks on Spamhaus.
3. **Raft propagates.** A bad FSM apply runs on every node in the cluster.

So autonomy has to be earned in stages. Each stage should have a track record
before the next is unlocked:

| Stage | What the assistant does | What a human does |
|---|---|---|
| **1. Observer** | Query telemetry, triage, post findings to Slack/Linear | Everything else |
| **2. PR author** | Open PRs with fixes + repro + test, run `make test lint sec` | Review, merge, deploy |
| **3. Canary-only** | Deploy to a single **edge** node behind a feature flag, monitor | Promote to servers |
| **4. Full auto** | Only clearly-scoped non-critical paths (UI strings, help text, non-delivery code) | Set policy |

Stage 1 is where almost all the value lives. "Claude noticed queue latency
climbed at 03:14, correlated with an rspamd timeout spike, here's the suspect
commit and a draft fix" is enormously useful on its own.

### Areas that should *never* be fully autonomous

Even long-term:

- `internal/pipeline/deliver/` and `internal/smtp/relay.go` — delivery correctness
- The Raft FSM — a bad Apply() corrupts state on every node
- Blob GC — false positives delete user data
- Antispam thresholds at the global level — flooding inboxes or quarantining legit mail

### Preconditions to put in place first

Before any of this is worth wiring up:

1. **A canary deploy target.** A real `edge` node that's acceptable to break.
   The role already exists in the code; just need a physical box.
2. **Instant rollback.** Keep N-1 binary. Watchdog auto-rollback on metric regression.
   Non-negotiable for any automated deploy.
3. **Feature flags for anything an assistant can touch.** So "roll back" doesn't
   require a redeploy.
4. **Immutable audit log** of every change made and why, separate from Raft,
   ideally append-only to an external system. Essential for 2am forensics.
5. **Dry-run / shadow mode** for pipeline changes. Run new code alongside old,
   compare outputs, only use the new result once parity is proven.

### Smallest useful first step

The minimum viable version is observer-only:

1. Add `/admin/diag` returning the bundle above as JSON.
2. Ship Prometheus metrics for the obvious things: queue depth, delivery
   latency p50/p95/p99, SMTP reject reasons, antispam verdicts, Raft leader changes.
3. Wire Alertmanager → `RemoteTrigger` for three alerts to start:
   queue backing up, Raft leader flapping, delivery error rate > baseline.
4. Give the assistant read access to Grafana/Loki via MCP.
5. **Stop there for a month.** Observer only. Verify the triage is actually
   useful before giving a commit bit.

The temptation is to jump straight to "assistant sees bug, fixes bug, ships fix."
The boring version — assistant sees bug, writes a good PR, human merges — is
where almost all the value is, with none of the downside.

---

## Part 2 — Spam scoring analysis + optimisation loop

### Why spam is a better fit for autonomy than delivery code

Two reasons worth being explicit about:

1. **Spam mistakes are recoverable.** A false positive sits in Junk until someone
   fishes it out. A lost delivery is gone forever. The reversibility budget is
   much larger.
2. **It's naturally a well-posed ML problem.** Every message has features, a
   score, a verdict, and — eventually — a ground-truth label from user behaviour.
   That's the loop.

### What's missing from the current code

`internal/pipeline/antispam/` has `builtin`, `rspamd`, and `spamassassin`
backends, but none of them are systematically logging the outcome side. Before
any analysis is possible, the system needs to record, per message:

- **Features at scoring time** — which backend, component sub-scores (SPF/DKIM/DMARC,
  rspamd symbols, SA rules fired, sender reputation, new-sender flag, etc.)
- **The decision** — final score, verdict, threshold in effect at the time
- **Ground truth signal (arrives later)** — did the user read it? Move it out of
  Junk? Move it *into* Junk? Reply to it? Delete without reading?

Replies and "moved out of Junk" are the strongest signals — much stronger than
explicit "Not Spam" button clicks, because users rarely click those.

### Proposed data model additions

```
/user/{user_id}/spam_report/{message_guid}   → SpamReport (user verdict + timestamp)
/user/{user_id}/spam_outcome/{message_guid}  → derived behaviour (read/replied/moved)
/system/spam_stats/{day}                     → daily aggregates per score bucket
/system/spam_ruleset/{version}               → versioned rulesets, rollbackable
```

The `MailMeta` protobuf needs a `SpamScoringRecord` sub-message with the
feature snapshot at scoring time. Without this, later retraining is blind —
you can't learn from verdicts if you don't remember *why* the old system made
its call.

### Four things the assistant can do, ordered by safety

**1. Report generation (pure observer).** Weekly analysis:
"FP rate in score band 5–7 has climbed from 2% to 6% over the last 14 days.
Common pattern in misclassified messages: legitimate ESP domains with new
`List-Unsubscribe-Post` headers. Current rules penalise new headers.
Suggested rule adjustment attached." Zero risk, 90% of the value.

**2. Per-user personalisation (narrow blast radius).** If a user repeatedly
fishes the same newsletter out of Junk, the assistant proposes a per-user
weight adjustment stored at `/user/.../config/spam_*`. Worst case: one user's
inbox is slightly wrong and they can undo it. Never touches anyone else.
Good sandbox for building trust.

**3. Global rule / threshold tuning (wide blast radius).** Proposed as PRs or
versioned `/system/spam_ruleset/N+1` entries, deployed in **shadow mode first**:
both rulesets score every message, only the old one's verdict is acted on,
differences are logged. After N messages with a clearly better confusion matrix,
flip a flag to promote. Rollback is the same flag in reverse.

**4. Novel rule synthesis.** The assistant clusters misclassified messages,
proposes new rspamd symbols or SA rules with a test corpus. Always human-reviewed,
always shadow-mode before live.

Stages 1 and 2 are where the ROI lives. Stages 3 and 4 are tempting but
high-effort relative to the gain.

### Adversarial considerations

User reports are the ground truth, which means a motivated attacker can
*become* ground truth. Two attacks to design against from day one:

- **Poisoning via reports.** Spammer registers accounts, reports legitimate
  bulk senders (Amazon, banks) as spam to get them quarantined globally.
  Defences: don't let a single user move global weights; require agreement
  from K independent, aged accounts; weight reports by account age and
  activity; cap the daily delta any user can apply to global state.
- **Probe-and-adapt.** Spammer sends variations to detect where the threshold
  is, then tunes payloads to sit just below. Defences: add random jitter to
  thresholds, don't expose scores in headers or UI, rotate rulesets on a
  schedule so cached probes expire.

These matter less for a self-hosted server than for Gmail, but become relevant
the moment there's open signup on the public internet.

### What the existing bongo-go architecture makes easy

- **Raft makes shadow mode clean.** A `ShadowScoringCommand` in the log makes
  every node score both ways consistently, FSM records the divergence,
  cluster-wide view is free.
- **Blob store makes training-set export trivial.** Every misclassified
  message's raw content is already content-addressed. "Give me the last 1000
  FP messages in score band X" is a prefix scan + blob fetch.
- **Per-user SQLite makes personalisation efficient.** The per-user database
  pattern already exists for folders/tags; per-user spam weights slot in next
  to filter rules.
- **Pipeline stages are pluggable.** Adding a `shadow_antispam` stage alongside
  `antispam` is structurally easy given `internal/pipeline/flags.go`.

### What's genuinely missing

The outcome-tracking side. Nothing currently watches whether a user read,
replied to, or moved a message after delivery. That feedback loop is a
meaningful chunk of work on its own — and everything downstream depends on it.

### Smallest useful first step

1. Add the feature snapshot + verdict to `MailMeta`. Start recording *why*
   every message was scored the way it was. Cheap insurance even if nothing
   else happens.
2. Add a lightweight outcome tracker: "did this message move mailboxes in
   the first 24 hours, and to where?" Store as a single enum on `MailMeta`.
3. Write one weekly report generator runnable via scheduled trigger —
   confusion matrix, score distribution, top FPs, top FNs.
4. Read the reports for a few weeks. If the insights are actually useful,
   *then* wire up shadow-mode rulesets.

Ordering matters. Most teams skip straight to "build the ML loop" and discover
three months in that their labels are garbage. The unsexy feature-snapshot +
outcome-tracking work is what makes everything downstream possible.

---

---

## Part 3 — Rollout and rollback via Nomad + CI/CD

(Added 2026-04-11. Originally open question #3 in the first draft.)

Nomad is a good fit here, and better than Kubernetes for a project that was
deliberately designed around a single static binary.

### Why Nomad works well for bongo-go

- **Single Go binary + exec driver.** No Docker layer needed. Nomad's `exec`
  or `raw_exec` driver runs the bongo-go binary directly with embedded assets.
- **HashiCorp stack coherence.** The code already uses `hashicorp/raft` and
  `hashicorp/memberlist`; Nomad's operational model will feel familiar.
  Vault is the natural pairing for DKIM keys and TLS certs.
- **Native canary semantics.** The `update` stanza gives `canary = N`,
  `min_healthy_time`, `healthy_deadline`, `auto_revert = true`,
  `auto_promote = false` as first-class concepts. `nomad job revert` is a
  one-liner. The rollout machinery is already built — only the health check
  that decides "promote or revert" has to be written.
- **Lightweight.** Respects bongo-go's single-binary minimal-ops ethos.
  Kubernetes would fight that ethos the whole way.

### Two things that will bite you

**1. "Canary" means something different for mail than for HTTP.**

For web services, canary means "route 5% of traffic to the new version." For
SMTP you cannot do this — inbound mail goes where MX records point, and one
TCP connection cannot be sharded. For IMAP, users are bound to whichever
server their client connected to.

So for bongo-go, "canary" really means: *one edge node runs the candidate
build, the rest run production, and the deployment watcher stares at that
specific node's metrics.* Nomad supports this with `canary = 1` on a job
with `count = N`, but it is not what the Nomad docs assume you mean. Be
explicit about this or the health checks will measure the wrong thing.

**2. Stateful `server` role vs stateless `edge` role.**

- **Edge nodes**: stateless. No Pebble, no blob store, no SQLite. Ideal
  Nomad workload — kill and reschedule freely, canary freely. Almost all
  early experiments should target edge.
- **Server nodes**: stateful. Local Pebble, local blob store, local SQLite.
  A server task cannot be moved to a new Nomad client without moving the
  data or pre-seeding from a peer. `host_volume` or CSI handles this, but
  it changes the deploy model — in-place binary upgrades with preserved
  state, not cattle-style reschedule.

Practical implication: the autonomy ladder maps cleanly onto role targets.
**Stage 3 canary deploys should only hit edge** until there's high confidence.
Server-role rollouts stay human-gated for much longer and use in-place
rolling upgrades rather than reschedules.

### Pipeline shape

```
PR opened (human or assistant)
  ↓ CI: make test-race / lint / sec / build
  ↓ PR merged (human-gated at Stage 2; pre-approved classes auto at Stage 4)
  ↓ CI builds tagged artifact (bongo-go binary + static assets, SHA-tagged)
  ↓ Artifact uploaded (S3 / Nomad HTTP artifact / GH Releases)
  ↓ CI triggers: nomad job run -var="version=<sha>" bongo-edge-canary.nomad
  ↓ Nomad canary = 1, auto_revert = true, auto_promote = false
  ↓ External deployment watcher polls Prometheus:
      - delivery error rate on canary vs baseline
      - queue depth trajectory
      - Raft leader stability (for server-role deploys)
      - panic count
      - p99 SMTP/IMAP latency
  ↓ If clean for min_healthy_time: nomad deployment promote
  ↓ If regression:               nomad deployment fail → auto_revert
  ↓ Webhook back to assistant / Slack with outcome
```

The deployment watcher is the load-bearing piece — and it's where the two
earlier parts converge. The same `/admin/diag` + Prometheus telemetry that
powers observer-mode Stage 1 is what gates promotion in Stage 3. **Build
the observability layer once; both loops and the rollout watcher all use it.**

### Autonomy ladder as ACL

The CI/CD pipeline is the enforcement point for the staged autonomy ladder,
which means "trust level" stops being a vague policy and becomes an actual
access control list:

| Stage | Access granted |
|---|---|
| **1. Observer** | Read on Prometheus, Loki, Nomad API (`deployment status`, `job status`) |
| **2. PR author** | GitHub write on feature branches, no merge, no deploy |
| **3. Canary gated** | Can trigger `nomad job run` on pre-approved job files; cannot `deployment promote` |
| **4. Full loop** | Can promote after green watcher result, for a whitelisted set of change classes |

Much easier to audit than "the assistant is trusted with X but not Y." It's
literally a Nomad ACL policy and a GitHub permissions config.

### Gotchas to design around

- **Don't run the Nomad control plane on the same hosts as bongo-go servers.**
  Circular dependency: if bongo-go breaks and the fix deploys via Nomad, and
  Nomad shares fate with bongo-go, you're stuck. Separate clusters — or at
  minimum separate physical hosts.
- **Out-of-band alerting.** If the deploy pipeline notifies via email through
  the broken mail server, it cannot tell you the mail server is broken.
  Slack / PagerDuty / ntfy — anything except the thing being deployed.
- **Two Raft clusters in the critical path now.** Nomad uses Raft internally,
  bongo-go uses Raft internally. Not a problem technically, but it's another
  consensus system to operate and reason about.
- **Secrets via Vault.** DKIM private keys, TLS certs, SMTP relay credentials
  should all move to Vault before the pipeline goes live. The assistant must
  never have access to these directly — only to Nomad jobs that reference them.
- **Artifact provenance.** Sign release artifacts (cosign / minisign) and have
  Nomad verify before deploy. Closes the "assistant uploads a malicious binary"
  attack surface if Stage 4 is ever reached.

### What this unlocks for the two loops

- **Generic code loop Stage 3** becomes concretely implementable: assistant
  merges a PR → CI builds → Nomad canary → watcher promotes or reverts →
  assistant sees result. No new glue needed once the pipeline exists.
- **Spam loop shadow mode** can be deployed the same way. A new ruleset
  version is just a job variable; promoting a shadow ruleset to live is the
  same `deployment promote` call.
- **Rollback becomes boring.** `nomad job revert` is one command. The
  assistant can be trusted to *trigger rollback* long before it's trusted to
  trigger forward deploys, because rollback's failure mode is almost always
  "you end up where you were five minutes ago."

### Still open after this part

- Which CI? (GitHub Actions, Drone, Woodpecker, Concourse?) Drives where the
  assistant's write permissions live.
- Where do canary edge nodes physically run? Same data centre as servers, or
  deliberately different to catch network-dependent regressions?
- Does the Nomad cluster get its own Raft quorum separate from bongo-go, or
  share infrastructure with something else (e.g. the existing dev/staging
  environment)?
- What's the "pre-approved change classes" whitelist for Stage 4? Probably
  starts as: static asset updates, i18n string changes, UI copy, help text.
  Nothing that touches Go code paths. Where does the whitelist live — in
  CI config, in a separate policy file, in code owners?
- Budget for regression windows: how long does the watcher hold before
  promoting? Five minutes catches most acute regressions; an hour catches
  slow-burn issues like goroutine leaks; a day catches antispam drift. The
  answer probably differs per change class.

---

## Part 4 — Phased implementation roadmap

(Added 2026-04-11. Full discussion captured in
`docs/claude-chat-self-improvement.md`. This section is the distilled plan.)

The roadmap that turns Parts 1–3 into executable work. Security and PII
hygiene are threaded through every phase rather than bolted on at the end.
Each phase is independently valuable — stopping after any one of them
leaves the project better off than it started.

### Phase 0 — Decisions (confirmed 2026-04-11)

Locked choices that downstream phases build on. Changing any of these later
touches every instrumentation call site — don't revisit without a reason.

| Decision | Choice | Notes |
|---|---|---|
| Telemetry hosting | Self-hosted Prometheus + Grafana + Loki + Tempo | Full stack under own control |
| Metrics library | `prometheus/client_golang` | Canonical reference implementation |
| Log shipping | `slog` (stdlib, JSON handler) → stdout → promtail → Loki | No new dependency |
| Tracing | OpenTelemetry SDK + OTLP exporter → Tempo | Vendor-neutral |
| Panic capture | **Skipped for now.** Panics visible via `bongo_panics_total` metric and Loki stack traces | **Future:** GlitchTip (Sentry-compatible, lightweight). Revisit when log-based triage becomes painful |
| Ticket system | GitHub Issues, with GitHub MCP server configured for assistant access | Co-located with code, single mental model |
| Secret management | Env vars via systemd `EnvironmentFile=` (restricted perms, never in repo) | Repo is public — secrets stay out of git entirely |
| Secret management (future) | **OpenBao** (LF fork of Vault, open-source licence) when Nomad pipeline from Part 3 is live | Preserves open-source alignment, avoids BSL |
| Alert routing | Alertmanager → `cmd/bongo-alert-bridge` webhook receiver → {GitHub Issues, Slack, RemoteTrigger} | Bridge is a new binary in this repo, runs on a different host from bongo-go (circular-dependency avoidance per Part 3). Built in Phase 3 |
| Chat / notifications | Slack (provisional — see "Communication channel" note at end of Part 4) | |

### Phase 1 — In-process instrumentation (no external deps)

bongo-go emits everything, nothing external has to exist yet. Verifiable by
curling `/metrics` and tailing JSON logs. **Independently valuable** even if
the project stops here — you get local observability.

**Deliverables:**

1. **`internal/observability/` package** — single home for metric
   definitions, tracer setup, logger construction. All `promauto.New*` calls
   live here, named and documented. No scattered instrumentation.
2. **Metric naming convention:** `bongo_<subsystem>_<metric>_<unit>`.
   Initial set (deliberately small):
   - `bongo_smtp_messages_total{verdict}`
   - `bongo_smtp_delivery_duration_seconds` (histogram)
   - `bongo_queue_depth{state}`
   - `bongo_queue_oldest_age_seconds`
   - `bongo_raft_state{role}`
   - `bongo_raft_last_index`
   - `bongo_raft_leader_changes_total`
   - `bongo_pebble_disk_bytes`
   - `bongo_blob_store_bytes`
   - `bongo_blob_gc_last_run_timestamp`
   - `bongo_antispam_verdicts_total{backend, verdict}`
   - `bongo_panics_total{subsystem}`
   - `bongo_imap_connections_active`
   - `bongo_pop3_connections_active`
3. **Structured logging migration.** Switch log calls to `slog` with a
   consistent field schema: `user_id` (hashed), `message_guid`, `subsystem`,
   `stage`, `latency_ms`. **Never log** mail body, subject, or recipient
   addresses in cleartext — this is the PII line.
4. **OpenTelemetry spans on the pipeline.** Each pipeline stage
   (`antispam`, `antivirus`, `rules`, `deliver`) becomes a span. SMTP
   session is the root. End-to-end traces per message.
5. **`/admin/diag` endpoint.** Single admin-auth-gated, rate-limited route
   returning a JSON situational snapshot:
   ```
   {
     "timestamp", "version", "uptime_seconds",
     "raft": { "role", "leader", "last_index", "last_applied", "peers", "lag" },
     "queue": { "pending", "retrying", "dead", "oldest_age_seconds" },
     "stores": { "pebble_bytes", "blob_bytes", "blob_gc_last_run" },
     "protocols": { "smtp_connections", "imap_connections", "pop3_connections" },
     "antispam": { "verdicts_1h", "backend_health" },
     "goroutines": { "count", "profile_url" },
     "recent_panics": [...]
   }
   ```
   No PII, no mail content, no user identifiers beyond counts. Safe to share
   with the assistant. Concrete shape needs a design pass against the real
   `internal/agent/agent.go` before coding.
6. **Panic recovery middleware** — bumps `bongo_panics_total` and ships to
   Sentry when it exists. Never crashes silently.

**Security gates (Phase 1):**
- PII audit of every new log statement before merge. Commit hook or linter
  rule flagging `slog.String("body", ...)` and similar.
- `/admin/diag` behind existing admin auth, rate-limited.
- Metric cardinality audit — no user IDs, message GUIDs, or IP addresses
  as labels. Those belong in logs or traces, not metrics.

### Phase 2 — Observability stack stand-up

External systems come online. bongo-go itself doesn't change — pure ops.

**Deliverables:**
- Prometheus scraping bongo-go `/metrics` (mTLS preferred)
- Grafana with dashboards: Overview, SMTP, IMAP, Queue, Raft, Antispam, Storage
- Loki + promtail (or OTel collector) ingesting JSON logs
- Tempo ingesting OTLP traces
- Sentry / GlitchTip ingesting panics
- Retention: metrics 90d, logs 30d, traces 7d, panics 90d (tune later)
- Grafana OIDC auth
- TLS everywhere — not just public edge

**Security gates (Phase 2):**
- Observability stack on separate hosts from bongo-go. Compromise of Grafana
  must not compromise mail.
- Read-only assistant credentials provisioned here, not shared with operator
  credentials.
- Network segmentation — bongo-go pushes out; stack has no write path back.

### Phase 3 — Alerting and ticket glue

Where the loop closes. Alerts become actionable tickets, not notification
fatigue.

**Deliverables:**

1. **Alertmanager rules.** Five, not fifty:
   - `BongoDown` — scrape failure 2m
   - `QueueBackingUp` — `bongo_queue_oldest_age_seconds > 300` for 5m
   - `RaftLeaderFlapping` — `rate(bongo_raft_leader_changes_total[10m]) > 0.1`
   - `DeliveryErrorSpike` — reject rate > baseline + 3σ for 10m
   - `PanicObserved` — `increase(bongo_panics_total[5m]) > 0`
2. **Webhook receiver service** (small Go binary, possibly
   `cmd/bongo-alert-bridge`, possibly separate repo). Responsibilities:
   - Receive Alertmanager and Sentry webhooks
   - Deduplicate — one incident, one ticket
   - Enrich with `/admin/diag` snapshot at alert time
   - Create ticket with structured payload
   - Fire RemoteTrigger to wake assistant session with ticket ID
3. **Ticket template** — every auto-opened ticket has the same shape:
   ```
   ## Alert
   <name, severity, fired_at>

   ## Diag snapshot
   <JSON from /admin/diag>

   ## Relevant dashboards
   - Grafana: <link>
   - Loki query: <pre-filled link>
   - Tempo traces: <link>

   ## Recent deploys
   <last 3 deploys, SHA + timestamp>

   ## Suspected area
   <alert name → subsystem mapping>
   ```
4. **Out-of-band alert channel** — Slack / ntfy / PagerDuty. Never email.

**Security gates (Phase 3):**
- Webhook receiver validates signed payloads (Alertmanager signing, Sentry HMAC).
- Ticket creation via scoped API token, not personal.
- Receiver has read-only access to `/admin/diag`, no write path to bongo-go.
- RemoteTrigger auth is a shared secret, rotated.

### Phase 4 — Assistant integration

Only now does the assistant get access. Everything upstream validated
without AI involvement.

**Deliverables:**
- Read-only credentials: Grafana, Loki, Prometheus, Sentry, ticket system,
  `/admin/diag`
- MCP servers configured: Grafana MCP, Linear/GitHub MCP
- `CLAUDE.md` updated with: telemetry locations, ticket template format,
  metric semantics, which dashboard to check first per alert type
- `docs/runbooks/` with one runbook per initial alert — guidance for the
  assistant *and* human documentation
- RemoteTrigger endpoint wired to the webhook receiver
- **Stage 1 only.** No PR permissions yet. Assistant can read, triage,
  comment on tickets, propose fixes in comments. No code changes.

**Security gates (Phase 4):**
- Credentials scoped, rotatable, revocable in one place
- Every assistant action logged — which ticket, what was read, what was written
- Visible kill switch — one command disables assistant access entirely

### Phase 5 — Scheduled passes and trust-building

Alert-driven triage is working. Now add lower-urgency periodic scans.

- **Daily:** queue age scan, blob GC health, error budget review
- **Weekly:** blob store growth, performance trends, deploy correlation analysis
- **Monthly:** architectural drift review

Each scheduled pass produces a ticket or Slack post, **not a PR**. Building
trust gradually means humans see the assistant's work on non-urgent matters
before trusting it on urgent ones.

### Phase 6 — Earning the next stage

Only after weeks of clean Phase 4 + 5 operation does it make sense to unlock
Stage 2 (PR authorship), then Stage 3 (canary deploys via Part 3). Each
unlock wants explicit criteria:

- **Stage 2 unlock:** N weeks of triage tickets, M% rated useful by humans,
  zero false-positive tickets wasting significant time
- **Stage 3 unlock:** K PRs merged from assistant, all green-CI first try,
  zero production incidents caused by them
- **Stage 4 unlock:** Probably never for most code paths — only for the
  pre-approved whitelist from Part 3

### The crash-to-fix flow, concretely

At Stage 2, the loop described in Part 1 looks like this end-to-end:

```
1. bongo-go panics in antispam pipeline
2. Panic middleware bumps bongo_panics_total and ships to Sentry
3. Sentry groups the panic, fires webhook with stack + context
4. Webhook receiver:
   - Dedupes (existing open ticket for this signature?)
   - Fetches /admin/diag snapshot
   - Fetches last 3 deploys
   - Creates ticket: "panic in antispam.builtin: nil map access"
   - Fires RemoteTrigger with ticket ID
5. Assistant session wakes:
   - Reads ticket
   - Pulls Sentry stack trace, Loki logs around crash time,
     Tempo traces correlated with crash
   - Locates offending code via stack trace
   - Comments on ticket: root cause + proposed fix + confidence
   - (Stage 2) Opens PR with fix + regression test
   - (Stage 1) Stops here — human picks it up
6. Human reviews ticket + PR
7. PR merged → CI → Nomad canary (Part 3) → watcher promotes → done
```

The ticket is the load-bearing artefact. It's the shared workspace where
human and assistant see the same thing and can intervene at any step.
Without it, the assistant's work is invisible until a PR appears; with it,
early intervention is cheap.

### Principles threaded through every phase

- **PII minimisation first.** Mail content never leaves the box. Recipients
  and subjects only in traces behind auth, never in metrics. Every new
  log/metric/span reviewed for leakage.
- **Least privilege.** Read-only where possible. Scoped tokens. Rotatable
  credentials. Kill switches.
- **Out-of-band alerting.** Never depend on bongo-go to tell you bongo-go
  is broken.
- **Single source of truth per concern.** One metrics package, one log
  schema, one diag endpoint, one ticket template. Don't fork these.
- **Instrument the critical path first.** Delivery, queue, Raft, antispam.
  Don't burn time instrumenting the admin UI.
- **Every phase independently valuable.** Stopping at any phase leaves the
  project better off. No phase's value depends on the next one happening.

### How to start

Phase 0 wants to be finished before Phase 1 is sprint-planned. Rushing the
Phase 0 decisions is the thing most likely to make the whole effort painful
later. Once those decisions are made, Phase 1 can be written up as a proper
sprint doc in `docs/sprints/` with task-level breakdown. Phases 2–6 stay as
design until their time comes.

### Communication channel

Slack for now. Provisional decision — revisit later if needed.

---

## Where the two loops meet

Both directions share the same infrastructure if it's built right:

- **Telemetry pipeline** (metrics, logs, traces) — used by both
- **`/admin/diag` endpoint** — both can query it
- **Alert/trigger wiring** (`RemoteTrigger` + scheduled checks) — both use it
- **Staged autonomy ladder** — same four stages apply to both
- **Audit log of AI-initiated changes** — critical for both
- **Feature flags + shadow mode + instant rollback** — both need them

The spam loop is also the best *training ground* for the generic loop. It's
lower stakes, has faster feedback, and the blast radius can be contained to
per-user state. Getting the spam loop working well is a low-risk way to find
out whether the broader idea is actually tractable before trusting it with
delivery-path code.

---

## Open questions to pick up next

Things deliberately left unresolved from the first discussion:

1. **What exactly should `/admin/diag` return?** Needs a concrete schema
   grounded in the actual current Agent struct and store internals. Worth a
   pass through `internal/agent/agent.go` and the store packages.
2. **Where does the audit log live?** External system (S3, Linear, GitHub
   issues)? Separate Pebble key space? Append-only file with off-box mirror?
3. ~~**What's the canary deploy story concretely?**~~ **Answered in Part 3:**
   Nomad jobs + CI/CD pipeline, canary targets edge role only initially,
   deployment watcher gates promotion on Prometheus metrics. Sub-questions
   moved to "Still open after this part" at the end of Part 3.
4. **`SpamScoringRecord` protobuf shape.** Needs to be designed against the
   actual `builtin`, `rspamd`, and `spamassassin` backends — each produces
   different sub-scores. Unified feature schema or backend-specific?
5. **Outcome tracker mechanics.** Where does it hook in? IMAP move events?
   A background sweep of mailbox state deltas? New pipeline stage on user action?
6. **User-facing controls.** Do users get visibility into the assistant's
   per-user weight adjustments? An override UI? A "reset my spam preferences"
   button?
7. **Bootstrap problem for ground truth.** The system needs *some* labelled
   data before any report is useful. Seed from an existing public spam corpus,
   or bootstrap from the first month of user behaviour?
8. **Governance.** Who reviews assistant-authored PRs? Is there a rate limit
   on how many the assistant can open per day? A "kill switch" to disable the
   whole loop if it starts misbehaving?

## Reference

- CLAUDE.md — architecture, Pebble key space, conventions
- `../bongo-go-prd.md` — original PRD
- `internal/agent/agent.go` — the orchestrator that would host most of this
- `internal/pipeline/antispam/` — where the spam work lands
- `docs/sprints/sprint-12b-antispam.md` — original antispam sprint, for context
