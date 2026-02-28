# Progress Log

## Patterns Discovered
(Add reusable patterns here as you find them)

---

## Execution Log

### 2026-02-27 - Project Setup
- Imported 17 tasks from PRD across 4 phases
- Archived previous loop (sprint 13a security hardening)
- Key existing code: antispam.Scorer, BayesFilter, rules.Engine, delivery.go (synchronous)
- MailMeta proto already has spam_score, spf_result, target_mailbox fields
- No internal/pipeline/ directory yet, no pipeline.proto or agent.proto
---

### 2026-02-27 - Task 1: Proto definitions for pipeline [sonnet]
- Created proto/bongo/v1/pipeline.proto with QueueEnvelope + StageResult
- Created internal/pipeline/flags.go with 6 flag constants + HasFlag/SetFlag/ClearFlag helpers
- Proto compiled with protoc, tests pass
- Files: pipeline.proto, pipeline.pb.go, pipeline_test.go, flags.go, flags_test.go
---

### 2026-02-27 - Task 2: Filesystem spool queue [sonnet]
- Created internal/pipeline/spool.go with SpoolStore (Enqueue, Dequeue, Move, List, ReadEnvelope, WriteEnvelope, Remove, RecoverCrashed)
- Atomic writes via temp+rename pattern
- Dequeue picks oldest file (lexicographic sort)
- RecoverCrashed moves processing/ -> incoming/ on startup
- 12 test cases covering round-trip, ordering, recovery, atomicity
- PATTERN: filesystem - atomic writes via temp+rename pattern for spool files
---

### 2026-02-27 - Task 3: Processor interface and pipeline runner [sonnet]
- Created processor.go with Processor and BlobReader interfaces
- Created runner.go with channel-based worker pool, ticker polling, idempotent stage skipping, graceful drain
- 7 test cases: pass/quarantine/error/idempotent/multi-stage/shutdown/empty-queue
- PATTERN: concurrency - worker pool with buffered channel + WaitGroup for graceful drain
- PATTERN: idempotency - flag-based guard clauses before I/O for crash-safe re-runs
---

### 2026-02-27 - Tasks 4, 5, 6: Processors (parallel) [sonnet]
- Task 4: internal/pipeline/antispam/builtin.go — wraps Scorer+BayesFilter, threshold-based pass/tag/quarantine
- Task 5: internal/pipeline/deliver/deliver.go — extracts store step, builds MailMeta, applies Raft per recipient
- Task 6: internal/pipeline/rules/rules.go — wraps rules.Engine, per-recipient evaluation, move/flag/delete actions
- All three ran in parallel, all tests pass
- PATTERN: enmime fallback - all processors fall back to basic header extraction if enmime parse fails
- PATTERN: interface adapting - RuleProvider interface matches rules.RuleStore.List() for zero-adapter wiring
---

### 2026-02-27 - Task 7: Refactor SMTP delivery to use pipeline [opus]
- Rewrote delivery.go: now stores blob + enqueues QueueEnvelope (removed inline scoring/rules/Raft)
- Added BlobStoreAdapter wrapping store.BlobStore as pipeline.BlobReader
- Wired pipeline Runner into agent.go startup (spool -> processors -> runner goroutine)
- Processor chain: antispam -> rules -> deliver
- Shutdown sequence: cancel pipeline runner before queue worker
- Updated delivery_test.go to verify spool-based flow
- Build compiles clean, all tests pass
- PATTERN: architecture - spool-based async pipeline replaces synchronous inline processing
---

### 2026-02-27 - Tasks 8, 12: Config + ClamAV (parallel) [sonnet]
- Task 8: Added PipelineConfig with StageConfig blocks, Validate(), PollInterval helper. Agent.go builds processors from config stages.
- Task 12: ClamAV processor with INSTREAM protocol, chunked streaming, graceful fallback. Mock clamd in tests.
- Added test/testdata/config.hcl pipeline block for HCL decoder
---

### 2026-02-27 - Tasks 9, 10, 11: Config discovery + SA + Rspamd (parallel) [sonnet]
- Task 9: DiscoverConfigFile() with 4-level search order, LoadWithDiscovery(), logs loaded path
- Task 10: SpamAssassin SPAMC/1.5 protocol, score normalisation, mock spamd tests
- Task 11: Rspamd HTTP /checkv2, JSON parsing, action mapping, httptest mock
- All three have graceful fallback on connection failure
- Phase 1 complete (9 tasks), Phase 2 complete (3 tasks)
---

### 2026-02-28 - Tasks 13, 16, 17: gRPC + Scan + Quarantine (parallel) [opus/sonnet]
- Task 13: agent.proto with 5 RPCs, AgentServer with registry + reaper, AgentClient with heartbeat, DispatchOrLocal
- Task 16: ScanHandler with SSE progress, Alpine.js UI, one-scan-per-mailbox, force rescan option
- Task 17: QuarantineHandler with list/release/delete/bulk, auto-purge goroutine, admin nav link
- All wired into agent.go with proper shutdown order
---

### 2026-02-28 - Tasks 14, 15: Agent role + SMTP agent [opus/sonnet]
- Task 14: Added Agents []string to ServerConfig, Config.Validate() for agent role, runAgent()/shutdownAgent() in agent.go
- Task 15: AgentDeliveryPipeline deposits via gRPC DepositMessage, startSMTPAgent() in agent mode
- All 17 tasks complete
---

### 2026-02-28 - Verification: 15/20 PASS, 4 PARTIAL, 1 FAIL
- Extracted 65 requirements from PRD, verified against codebase
- 5 gaps identified, fix tasks 18-22 created
---

### 2026-02-28 - Tasks 18-22: Verification fixes (parallel) [sonnet]
- Task 18: Wired source_ip from SMTP RemoteAddr into QueueEnvelope via DeliverWithSPF sourceIP param
- Task 19: Added "agent" to valid roles in cmd/bongo-go/agent.go CLI switch
- Task 20: Added MsgFlagDelivered constant + idempotency check in deliver processor
- Task 21: Created AgentProcessorServer in grpc/agent_processor.go for antispam/antivirus agent processing
- Task 22: Created CompositeAntispamProcessor with Rspamd -> SpamAssassin -> builtin fallback chain
- All 5 ran in parallel, all tests pass, build clean
- All 22 tasks complete. Project verified.
---
