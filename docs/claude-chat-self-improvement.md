# Observability and Self-Improvement — Captured Planning Discussion

**Status:** Raw transcript of a design conversation, 2026-04-11. Unedited, and it
reads like a conversation because that is what it is — it begins mid-thread, in
reply to a question about which piece to build first.

Kept because the phased breakdown (Phases 0–6 below) is the most concrete plan
anyone has written for this, and nothing here has been built. The distilled,
readable version is in `docs/continuous-improvement-loop.md`; **read that first**
and treat this as the appendix it is.

Nothing in here is a commitment. Phase numbering is local to this document and
unrelated to the project's sprint or phase numbering.

---

Agreed — this is the right next step. Everything downstream (spam analysis, code improvement
  loop, Nomad rollout watcher) depends on having real data flowing through a clean, well-scoped
  observability layer. If we get this wrong, everything built on top inherits the mess.

  Here's how I'd phase it, with security and PII hygiene threaded through every phase rather than
   bolted on at the end.

  Phase 0 — Decisions you need to make before code is written

  These are choices that affect everything downstream. I can propose defaults, but you have to
  pick:

  ┌─────────────┬─────────────────────────────────┬──────────────────────────────────────────┐
  │  Decision   │       Default I'd suggest       │                   Why                    │
  ├─────────────┼─────────────────────────────────┼──────────────────────────────────────────┤
  │             │ Self-hosted Prometheus +        │ Control over PII, no per-series billing  │
  │ Telemetry   │ Grafana + Loki + Tempo on a     │ surprises, matches the self-hosted ethos │
  │ hosting     │ separate VM                     │  of bongo-go. Grafana Cloud is fine for  │
  │             │                                 │ early prototyping if you want zero ops.  │
  ├─────────────┼─────────────────────────────────┼──────────────────────────────────────────┤
  │ Secret      │ Vault (fits the HashiCorp stack │ DKIM keys, TLS certs, webhook secrets    │
  │ management  │  alignment from Part 3)         │ all want the same home.                  │
  ├─────────────┼─────────────────────────────────┼──────────────────────────────────────────┤
  │ Ticket      │ Linear or GitHub Issues         │ Both have MCP servers I can use. GitHub  │
  │ system      │                                 │ is simpler if PRs live there anyway.     │
  ├─────────────┼─────────────────────────────────┼──────────────────────────────────────────┤
  │ Alert       │ Alertmanager → webhook receiver │ Out-of-band — never email through the    │
  │ routing     │  → {Slack, ticket,              │ thing being monitored.                   │
  │             │ RemoteTrigger}                  │                                          │
  ├─────────────┼─────────────────────────────────┼──────────────────────────────────────────┤
  │ Log         │ slog with JSON handler → stdout │ slog is stdlib, no extra dependency.     │
  │ shipping    │  → promtail → Loki              │                                          │
  ├─────────────┼─────────────────────────────────┼──────────────────────────────────────────┤
  │ Metrics     │ prometheus/client_golang        │ Canonical, boring, works.                │
  │ library     │                                 │                                          │
  ├─────────────┼─────────────────────────────────┼──────────────────────────────────────────┤
  │ Tracing     │ OpenTelemetry SDK + OTLP export │ Standard, vendor-neutral.                │
  │             │  to Tempo                       │                                          │
  ├─────────────┼─────────────────────────────────┼──────────────────────────────────────────┤
  │ Panic       │ Sentry (self-hosted via         │ Best-in-class for panic triage.          │
  │ capture     │ GlitchTip if you want lighter)  │                                          │
  └─────────────┴─────────────────────────────────┴──────────────────────────────────────────┘

  These choices want to be made once and stuck with. Changing the metrics library or logging
  framework six months in is painful because every instrumentation call site has to be updated.

  Phase 1 — In-process instrumentation (no external deps)

  The goal of this phase: bongo-go emits everything, but nothing external has to exist yet. You
  can verify the instrumentation works by curling /metrics and tailing JSON logs.

  Deliverables:

  1. internal/observability/ package. Single home for metric definitions, tracer setup, and
  logger construction. Don't scatter promauto.NewCounter calls across the codebase — they all
  live here, named and documented.
  2. Metric naming convention. bongo_<subsystem>_<metric>_<unit> (Prometheus convention). Initial
   set, kept deliberately small:
    - bongo_smtp_messages_total{verdict="accept|reject|defer"}
    - bongo_smtp_delivery_duration_seconds (histogram)
    - bongo_queue_depth{state="pending|retrying|dead"}
    - bongo_queue_oldest_age_seconds
    - bongo_raft_state{role="leader|follower|candidate"}
    - bongo_raft_last_index
    - bongo_raft_leader_changes_total
    - bongo_pebble_disk_bytes
    - bongo_blob_store_bytes
    - bongo_blob_gc_last_run_timestamp
    - bongo_antispam_verdicts_total{backend, verdict}
    - bongo_panics_total{subsystem}
    - bongo_imap_connections_active
    - bongo_pop3_connections_active
  3. Structured logging migration. Switch log calls to slog with a consistent field schema:
  user_id (hashed), message_guid, subsystem, stage, latency_ms. Never log mail body, subject, or
  recipient address as plain text — this is the PII line.
  4. OpenTelemetry spans on the pipeline. Each pipeline stage (antispam, antivirus, rules,
  deliver) becomes a span. SMTP session is the root span. This gives you end-to-end traces for
  every message.
  5. /admin/diag endpoint. Single route, admin-auth-gated, rate-limited, returning a JSON
  snapshot grounded in the actual Agent struct. We need to read internal/agent/agent.go to design
   this concretely — but the rough shape:
  {
    "timestamp": "...",
    "version": "...",
    "uptime_seconds": ...,
    "raft": { "role", "leader", "last_index", "last_applied", "peers", "lag" },
    "queue": { "pending", "retrying", "dead", "oldest_age_seconds" },
    "stores": { "pebble_bytes", "blob_bytes", "blob_gc_last_run" },
    "protocols": { "smtp_connections", "imap_connections", "pop3_connections" },
    "antispam": { "verdicts_1h": {...}, "backend_health": {...} },
    "goroutines": { "count", "profile_url" },
    "recent_panics": [...]
  }
  5. No PII. No mail content. No user identifiers beyond counts. The endpoint is safe to share
  with the assistant.
  6. Panic recovery middleware that bumps bongo_panics_total and ships the panic to Sentry (once
  Sentry exists) — never just crashes silently.

  Security gates for Phase 1:
  - PII audit of every new log statement before merge. A linter rule or commit hook that flags
  slog.String("body", ...) etc.
  - /admin/diag is behind the existing admin auth middleware and rate-limited.
  - Metric cardinality audit: no high-cardinality labels (no user IDs, no message GUIDs, no IP
  addresses as labels — those go in logs or traces, not metrics).

  Phase 1 is independently valuable even if nothing else happens. You get local observability and
   can run curl localhost:port/metrics | grep bongo_ to see real numbers.

  Phase 2 — Observability stack stand-up

  External systems come online. Nothing about bongo-go changes in this phase — it's pure ops.

  Deliverables:

  - Prometheus scraping bongo-go /metrics (mTLS-preferred)
  - Grafana with dashboards: Overview, SMTP, IMAP, Queue, Raft, Antispam, Storage
  - Loki + promtail (or OTel collector) ingesting structured JSON logs
  - Tempo ingesting OTLP traces
  - Sentry / GlitchTip ingesting panics
  - Retention: metrics 90 days, logs 30 days, traces 7 days, panics 90 days (tune later)
  - All dashboards behind auth. Grafana OIDC against whatever identity provider you use.
  - TLS everywhere, not just on the public edge.

  Security gates for Phase 2:
  - The observability stack runs on separate hosts from bongo-go. Compromise of Grafana should
  not compromise mail.
  - Read-only credentials for the assistant are provisioned here, not shared with operator
  credentials.
  - Network segmentation: bongo-go pushes metrics out; the observability stack does not have
  write access back.

  Phase 3 — Alerting and ticket glue

  This is where the loop actually closes. Alerts turn into actionable tickets instead of
  notification fatigue.

  Deliverables:

  1. Alertmanager rules. Start small. Five alerts, not fifty:
    - BongoDown — scrape failure for 2 minutes
    - QueueBackingUp — bongo_queue_oldest_age_seconds > 300 for 5 minutes
    - RaftLeaderFlapping — rate(bongo_raft_leader_changes_total[10m]) > 0.1
    - DeliveryErrorSpike — reject rate > baseline + 3σ for 10 minutes
    - PanicObserved — increase(bongo_panics_total[5m]) > 0
  2. Webhook receiver service. Small Go binary (lives in cmd/bongo-alert-bridge or similar, or as
   a separate repo). Responsibilities:
    - Receive Alertmanager and Sentry webhooks
    - Deduplicate — one incident, one ticket
    - Enrich with a /admin/diag snapshot captured at alert time
    - Create a ticket with structured payload (title, summary, diag snapshot, links to
  Grafana/Loki/traces)
    - Optionally fire a RemoteTrigger to wake an assistant session with the ticket ID
  3. Ticket template. Every auto-opened ticket has the same shape so the assistant knows where to
   look:
  ## Alert
  <alert name, severity, fired_at>

  ## Diag snapshot
  <JSON from /admin/diag at alert time>

  ## Relevant dashboards
  - Grafana: <link>
  - Loki query: <link with label selectors pre-filled>
  - Tempo traces: <link>

  ## Recent deploys
  <last 3 deploys, SHA + timestamp — answers "did we just ship something">

  ## Suspected area
  <derived from alert name → subsystem mapping>
  4. Out-of-band alert channel. Slack / ntfy / PagerDuty. Never email.

  Security gates for Phase 3:
  - Webhook receiver validates signed payloads (Alertmanager webhook signing, Sentry HMAC).
  - Ticket creation uses a scoped API token, not a personal one.
  - The webhook receiver never has write access back to bongo-go — only read access to
  /admin/diag.
  - RemoteTrigger auth is a shared secret, rotated.

  Phase 4 — Assistant integration

  Only at this point does the assistant (me) get access. Everything upstream has been validated
  without AI involvement.

  Deliverables:

  - Read-only credentials: Grafana, Loki, Prometheus, Sentry, ticket system, /admin/diag
  - MCP servers configured: Grafana MCP, Linear/GitHub MCP
  - CLAUDE.md updated with: where telemetry lives, ticket template format, which metrics mean
  what, which dashboards to check first for each alert type
  - A "runbook" doc explaining what to do for each of the five initial alerts — both as guidance
  for me and as human documentation. docs/runbooks/ directory.
  - RemoteTrigger endpoint hooked up to the webhook receiver
  - Stage 1 only. No PR-writing permissions yet. The assistant can only: read, triage, comment on
   tickets, propose fixes in ticket comments. No code changes.

  Security gates for Phase 4:
  - Credentials are scoped, rotatable, and revocable in a single place
  - Every assistant action is logged: which ticket, what was read, what was written
  - A visible "kill switch" — one command turns off the assistant's access entirely

  Phase 5 — Scheduled passes and trust-building

  Once alert-driven triage is working well:

  - Daily cron trigger: queue age scan, blob GC health, error budget review
  - Weekly cron trigger: blob store growth, performance trends, deploy correlation analysis
  - Monthly: architectural drift review (did new code follow the observability conventions?)

  Each scheduled pass produces a ticket or Slack post, not a PR. Building trust gradually means
  letting humans see my triage work on non-urgent stuff before trusting me on urgent stuff.

  Phase 6 — Earning the next stage

  Only after weeks of Phase 4 + 5 running cleanly does it make sense to unlock Stage 2 (PR
  authorship), then Stage 3 (canary deploys via the Nomad pipeline from Part 3). Each stage
  unlock wants explicit criteria:

  - Stage 2 unlock criteria: N weeks of triage tickets, M% rated useful by humans, zero
  false-positive tickets that wasted significant time
  - Stage 3 unlock criteria: K PRs merged from assistant, all with green CI first try, zero
  production incidents caused by them
  - Stage 4 unlock criteria: Probably never for most code paths — only for the pre-approved
  whitelist from Part 3

  ---
  The crash-to-fix flow, concretely

  You described it well: "crash report or trigger for slow performance → raise a ticket → start
  work on fixes." Here's what that actually looks like at Stage 2:

  1. bongo-go panics in antispam pipeline
  2. Panic middleware bumps bongo_panics_total and ships to Sentry
  3. Sentry groups the panic, fires webhook (with stack trace + context)
  4. Webhook receiver:
     - Dedupes (checks: is there already an open ticket for this signature?)
     - Fetches /admin/diag snapshot
     - Fetches last 3 deploys
     - Creates ticket: "panic in antispam.builtin: nil map access"
     - Fires RemoteTrigger with ticket ID
  5. Assistant session wakes:
     - Reads ticket
     - Pulls stack trace from Sentry, logs around crash time from Loki
     - Traces correlated with crash from Tempo
     - Locates offending code via stack trace
     - Comments on ticket with triage: "root cause is X, fix is Y, confidence Z"
     - (Stage 2) Opens PR with fix + regression test
     - (Stage 1) Stops here — human picks it up
  6. Human reviews ticket + PR
  7. PR merged → CI → Nomad canary (Part 3) → watcher promotes → done

  The ticket is the load-bearing artefact. It's the shared workspace where human and assistant
  see the same thing and can intervene at any step.

  Principles threaded through all phases

  - PII minimisation first. Every new log/metric/span reviewed for PII leakage. Mail content
  never leaves the box. Recipients and subjects only in traces behind auth, never in metrics.
  - Least privilege everywhere. Read-only where possible. Scoped tokens. Rotatable credentials.
  Kill switches.
  - Out-of-band alerting. Never depend on bongo-go to tell you bongo-go is broken.
  - Single source of truth per concern. One metrics package, one log schema, one diag endpoint,
  one ticket template. Don't fork these.
  - Instrument the critical path first. Delivery, queue, Raft, antispam. Don't burn time
  instrumenting the admin UI.
  - Every phase is independently valuable. If we stop after Phase 2, you still have dashboards.
  Stop after Phase 3, you still have alerts and tickets. No phase is wasted if the next one never
   happens.

  ---
  Proposal for how to actually start this

  Two options for how to turn this into executable work, depending on how formal you want it:

  Option A — Lightweight. Add a Part 4 to docs/continuous-improvement-loop.md capturing this
  phased plan, so it's ready for a future conversation to pick up from. No sprint, no commitment,
   just captured thinking.

  Option B — Heavier. Turn Phase 1 specifically into a proper sprint doc in docs/sprints/
  (matching the existing sprint format) with task-level breakdown — observability package, metric
   definitions, slog migration, OTel spans, /admin/diag endpoint, PII lint hook. The rest of the
  phases stay as design, to be sprint-planned later.

  I'd lean towards Option A for now — capture the plan, let it sit, and only commit to Phase 1 as
   a sprint when you've had time to sit with the Phase 0 decisions. Rushing Phase 0 is the thing
  most likely to make the whole effort painful later.

  Which way do you want to go?