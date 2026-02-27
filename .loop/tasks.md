# Project: Message Processing Pipeline

> Replace synchronous SMTP delivery with a staged pipeline supporting pluggable processors, external scanner backends, distributed agents via gRPC, and retroactive mailbox scanning.

Started: 2026-02-27
Status: in-progress
Source: tasks/prd-message-processing-pipeline.md

---

## Tasks

### Phase 1: Pipeline Core + In-Process

- [ ] 1. Proto definitions for pipeline (US-001 partial) - Create `proto/bongo/v1/pipeline.proto` with QueueEnvelope and StageResult messages as specified in PRD. Add processing flag constants in Go. Compile proto to `.pb.go`. Unit tests for marshal/unmarshal round-trip.

- [ ] 2. Filesystem spool queue (US-001) - Create `internal/pipeline/spool.go` with SpoolStore struct. Methods: Enqueue() writes .env to incoming/, Dequeue() moves to processing/ (atomic rename), Move() between directories, List() per directory, RecoverCrashed() moves processing/ back to incoming/ on startup. Create spool directories on init: incoming, processing, deliver, quarantine, error. Unit tests with t.TempDir(). (needs: 1)

- [ ] 3. Processor interface and pipeline runner (US-002) - Create `internal/pipeline/processor.go` with Processor interface (Name(), Process()). Create `internal/pipeline/runner.go` with Runner struct: configurable worker count, poll interval, ordered stage list. Processing loop: dequeue from incoming, run each stage, update envelope flags, move to quarantine/error/deliver based on results. Idempotent via flag checks. Graceful shutdown drains in-flight. Unit tests with mock processors. (needs: 2)

- [ ] 4. Built-in antispam processor (US-004) - Create `internal/pipeline/antispam/builtin.go` wrapping existing antispam.Scorer + BayesFilter as a Processor. Returns StageResult with score and reasons. Score >= threshold (0.5): action "tag" sets target_mailbox to Junk. Score >= high threshold (0.9): action "quarantine". Configurable thresholds. Unit tests. (needs: 3)

- [ ] 5. Deliver processor - Extract final delivery logic from `internal/smtp/delivery.go` into `internal/pipeline/deliver/deliver.go` as a Processor. Handles Pebble/BlobStore write via Raft, creates MailboxMessage with spam_score from envelope. Respects target_mailbox from prior stages. Unit tests with mock applier. (needs: 3)

- [ ] 6. Filter rules processor - Create `internal/pipeline/rules/rules.go` wrapping existing rules.Engine as a Processor. Evaluates user filter rules against message, sets target_mailbox/flags in envelope. Unit tests. (needs: 3)

- [ ] 7. Refactor SMTP delivery to use pipeline (US-003) - Modify `internal/smtp/delivery.go`: instead of synchronous scoring+storing, write blob then create QueueEnvelope in spool/incoming/. Remove inline antispam scoring from delivery.go. Wire pipeline Runner into agent.go startup (after stores, before SMTP). Dev mode: pipeline runs in-process with zero config. Ensure end-to-end: SMTP receipt -> spool -> pipeline -> Pebble. Update existing delivery tests. (needs: 4, 5, 6)

- [ ] 8. Pipeline configuration (US-008) - Add PipelineConfig to `internal/config/config.go`: workers, poll_interval, error_retention, quarantine_retention, ordered stage list with enabled/backend per stage. Wire into Runner construction in agent.go. Dev mode defaults: builtin antispam only. Validate config on startup (unknown stages, missing backends). (needs: 7)

- [ ] 9. Config file auto-discovery (US-009) - Modify config loading: search order `-c` flag > `BONGO_CONFIG` env > `./bongo.hcl` > `/etc/bongo/bongo.hcl` > defaults (dev mode). Log which config file was loaded. Unit tests for discovery order with t.TempDir(). (needs: 8)

### Phase 2: External Backends

- [ ] 10. SpamAssassin backend (US-005) - Create `internal/pipeline/antispam/spamassassin.go` implementing Processor. Connect to spamd via TCP, send message using SPAMC/1.5 SYMBOLS command, parse score and matched rules, normalise to 0.0-1.0. Graceful fallback if unreachable. Config: antispam.spamassassin.enabled, .host, .port, .threshold. Unit tests with mock spamd server. (needs: 4)

- [ ] 11. Rspamd backend (US-006) - Create `internal/pipeline/antispam/rspamd.go` implementing Processor. POST raw message to /checkv2, parse JSON response (score, action, symbols), map Rspamd actions to pipeline actions. Configurable URL, timeout, custom headers. Graceful fallback if unreachable. Unit tests with httptest mock. (needs: 4)

- [ ] 12. ClamAV antivirus processor (US-007) - Create `internal/pipeline/antivirus/clamav.go` implementing Processor. Connect to clamd via Unix socket or TCP, send via INSTREAM (chunked), parse OK/FOUND response. Virus = always quarantine. Graceful fallback if unreachable. Config: antivirus.clamav.enabled, .socket, .host, .port, .timeout. Unit tests with mock clamd. (needs: 3)

### Phase 3: Distribution

- [ ] 13. Agent proto and gRPC service (US-011) - Create `proto/bongo/v1/agent.proto` with AgentService: Register, Heartbeat, ProcessMessage, DepositMessage, FetchBlob RPCs. Generate Go code. Create `internal/agent/grpc/server.go` (server-side: gRPC listener on port 8400, agent registry, dispatch to registered agents, fallback to in-process). Create `internal/agent/grpc/client.go` (agent-side: register, heartbeat, receive ProcessMessage calls). mTLS config. Unit tests. (needs: 8)

- [ ] 14. Config-driven agent role (US-010) - Extend ServerConfig with role "agent" and `Agents []string` field. Agent role skips Raft/Pebble/BlobStore init. Reads agents list, starts only specified services. Connects to server(s) via gRPC. Multiple agent types per process. Startup logs active agents. Graceful shutdown deregisters. Invalid agent name = startup error. (needs: 13)

- [ ] 15. SMTP agent type (US-012) - When `agents = ["smtp"]`, start SMTP/submission listeners that deposit mail via DepositMessage gRPC to server. Server stores blob and creates spool envelope. SPF result, source IP passed through. Config reuses existing smtp section. (needs: 14)

### Phase 4: UI + Management

- [ ] 16. Retroactive mailbox scan (US-013) - Add "Scan for spam" button to mailbox toolbar. POST /api/mailbox/{name}/scan starts background scan job. Iterates messages, sends each to antispam processor. Messages above threshold moved to Junk. Progress via SSE. Cancellable. One scan per mailbox. Skip already-scanned unless force=true. Templ template + handler + routes. (needs: 7)

- [ ] 17. Quarantine management UI (US-014) - Admin page listing quarantined messages (date, sender, subject, reason). Release action: move back to pipeline at deliver stage. Delete action: remove message + blob. Bulk actions: select multiple, release all, delete all. Auto-purge after configurable retention (default 30 days). Templ template + handler + routes. (needs: 7)

---

## Notes

- MailMeta proto already has spam_score, spf_result, dkim_result, dkim_domain, target_mailbox fields
- antispam.Scorer + BayesFilter already implemented, just need Processor wrapper
- rules.Engine already implemented, just need Processor wrapper
- delivery.go is synchronous today: parse -> score -> store. Must be refactored to: write blob -> enqueue -> return
- Config uses hashicorp/hcl + spf13/viper, existing AntispamConfig/AntivirusConfig structs exist
- Proto compilation: .pb.go files are pre-compiled in repo, no protoc step in Makefile currently
- HTTP router is chi v5 (NOT fiber for web routes)
- All writes go through Raft via ApplyCommand pattern
