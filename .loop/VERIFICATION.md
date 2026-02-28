# Verification Report

Project: Message Processing Pipeline
Source: tasks/prd-message-processing-pipeline.md
Date: 2026-02-28
Result: 15/20 core requirements PASS (75%), 19/20 PASS+PARTIAL (95%)

## Pass (15)

- REQ-1: US-001 | Filesystem spool queue | spool.go, pipeline.proto, 13 tests
- REQ-2: US-002 | Pipeline runner | runner.go, 7 tests with mocks
- REQ-4: US-004 | Built-in antispam | builtin.go wraps Scorer+BayesFilter, 7 tests
- REQ-5: US-005 | SpamAssassin | spamc TCP protocol, score normalisation, 7 tests
- REQ-6: US-006 | Rspamd | HTTP /checkv2, action mapping, 7 tests
- REQ-7: US-007 | ClamAV | INSTREAM protocol, chunked, 5 tests
- REQ-8: US-008 | Pipeline configuration | HCL stage blocks, validation, defaults
- REQ-9: US-009 | Config auto-discovery | 4-level search order, 9 tests
- REQ-11: US-011 | gRPC agent service | agent.proto, registry, heartbeat, dispatch
- REQ-12: US-012 | SMTP agent type | AgentDeliveryPipeline via DepositMessage gRPC
- REQ-13: US-013 | Retroactive scan | Scan button, SSE progress, cancellable
- REQ-14: US-014 | Quarantine management | Admin page, release/delete/bulk, auto-purge
- REQ-26: FR-12 | spam_score in MailMeta | deliver processor stores from StageResult
- REQ-49: Crash recovery | RecoverCrashed moves processing/ -> incoming/
- REQ-54: Blob not in spool | Spool envelope only has blob_hash reference

## Partial (4)

- REQ-3: US-003 | SMTP delivery refactor
  Has: blob storage, spool enqueue, SPF result, dev mode zero config
  Missing: source_ip not populated in QueueEnvelope (wiring gap)

- REQ-10: US-010 | Agent role
  Has: Full internal support (config validation, NewAgent, runAgent, gRPC client)
  Missing: CLI serve command rejects role="agent" (one-line fix in cmd/bongo-go/agent.go)

- REQ-25: FR-11 | Backend fallback chain
  Has: Individual graceful fallback per backend (unreachable = pass)
  Missing: Cascading chain (Rspamd -> SA -> builtin) not implemented; each is selected via config

- REQ-57: Idempotent stages
  Has: antispam/antivirus/rules stages fully idempotent via flag checks
  Missing: deliver stage has no flag protection; crash during deliver could cause duplicates

## Fail (1)

- REQ-39: FR-25 | Multiple agent types functional
  Expected: antispam/antivirus agent types process messages via ProcessMessage RPC
  Found: Only SMTP agent type is functional; antispam/antivirus stubs log registration but don't receive/process messages

## Notes

- Test coverage is strong: 70+ unit tests across all pipeline packages
- The CLI agent role gap (REQ-10) is a one-line fix in cmd/bongo-go/agent.go
- The source_ip gap (REQ-3) is a wiring issue, not architectural
- Backend fallback chain (REQ-25) is a design choice: individual fallback vs cascading chain
- Deliver idempotency (REQ-57) could cause duplicate deliveries on crash recovery
