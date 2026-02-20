# Phase 1 Sprint Backlog

Light overview of all Phase 1 sprints. Only the current sprint has a detailed task file.

## Sprint 01: Project Scaffolding & Storage Foundation ✅ DONE
Go module, Cobra CLI, HCL config, Pebble store, content-addressable blob store (shared flat
directory), per-user SQLite metadata index with LRU connection pooling, agent lifecycle with
three-tier storage init/shutdown, structured logging, health endpoints.

## Sprint 02: Raft Consensus & Cluster Membership ✅ DONE
Fix system.db credential issue (remove password_hash — credentials live in Pebble).
Pebble-backed Raft LogStore/StableStore. FSM that applies writes to Pebble and triggers
derived index updates. Memberlist gossip for node discovery. Leader forwarding for writes.
Blob sync HTTP endpoint (serving side). `bongo-go members` command. Multi-node integration
test: leader election, replication, failover.

## Sprint 03: Basic SMTP Inbound + Search Foundation ✅ DONE
**Cleanup from Sprint 02:**
- Fix `PebbleStore.DeleteRange` end-key semantics (current `append 0x00` works but should
  use proper successor calculation consistent with `prefixUpperBound` for robustness).
- Replace deprecated `filepath.HasPrefix` in `internal/cluster/tar.go` with
  `strings.HasPrefix(target, dir+string(os.PathSeparator))` for correct path-traversal
  prevention.

**New work (10 tasks):**
Protobuf definitions for Pebble values (RaftCommand, MailMeta, BlobRef, UserProfile) —
replace JSON Raft commands with protobuf. SMTP server via emersion/go-smtp (port 25/587,
STARTTLS). Mail delivery pipeline: parse with go-message/enmime → blob store → Pebble via
Raft → SQLite + Bleve indexes. Auth backend: bcrypt from Pebble profiles, `bongo-go user
create` CLI. Blob pull-from-peers (followers fetch missing blobs via HTTP from gossip peers).
Per-user Bleve index for mailbox search. Global Bleve GAL index for autocomplete. Wire
SMTP + search into agent lifecycle (dev mode auto-creates test user on port 2525).
Integration test: send email → verify all three storage tiers + search.

## Sprint 04: Integration & Dev Mode Polish ✅ DONE
**Cleanup from Sprint 03:**
- Remove unused `verifyHash` function in `internal/cluster/blobsync.go` (dead code).
- Clarify `parts` variable scoping in `internal/cluster/fsm.go` `updateDerivedIndexes()` —
  move profile check inside the `len(parts) >= 4` guard or re-split for readability.
- Add self-signed TLS certificate auto-generation in dev mode for SMTP STARTTLS
  (currently falls through to plaintext; spec called for self-signed cert).

**New work:**
Wire everything together: agent starts SMTP + Raft + health endpoints in dev mode.
Network-based Raft snapshots (new node bootstrap). TLS between nodes. Graceful shutdown
of all subsystems. CI pipeline: GitHub Actions with lint, build, test (race + cover).
Dynamic node join endpoint. Integration test helpers.

## Sprint 05: IMAP Server & Mailbox Access ✅ DONE
**Cleanup from Sprint 04:**
- Remove unused `verifyHash` function in `internal/cluster/blobsync.go` (still present).
- Add dev mode quick-start documentation (README section or standalone doc).
- Implement E2E test: start dev agent, send email via SMTP, query via health API
  (`test/e2e/` directory exists but is empty).

**New work (8 tasks):**
IMAP server via emersion/go-imap v2 for mailbox access. Mailbox management (INBOX,
Sent, Drafts, Trash, custom folders) stored in Pebble. IMAP IDLE for push notifications.
Message flags (Seen, Answered, Flagged, Deleted, Draft). EXPUNGE support. Mailbox
listing and STATUS. SEARCH command backed by Bleve. UID tracking and sequence number
mapping. Wire IMAP into agent lifecycle. Integration test: deliver mail via SMTP, read
via IMAP client, verify flags and search.

## Sprint 06: HTMX Web UI & Search Polish ← CURRENT
**Cleanup from Sprint 05:**
- Wire IMAP SEARCH to Bleve instead of in-memory string matching
  (`internal/imap/search.go` mapping IMAP criteria → Bleve queries).
- Add `messages`, `recent`, `unseen` fields to `MailboxMeta` proto (or document the
  dynamic computation approach as intentional).
- Add unit tests for `internal/imap/` package (server, backend, notify).

**New work (8 tasks):**
HTMX web UI using Templ (type-safe Go templates), HTMX, Alpine.js, and Pico CSS.
Mailbox listing page, message list with pagination, message detail view with HTML
sanitization, compose/reply form with SMTP submission, folder management, search UI
backed by Bleve, login page with session cookies, and responsive layout. Wire web
handlers into agent lifecycle. Integration test: login, browse inbox, read message,
compose reply.
