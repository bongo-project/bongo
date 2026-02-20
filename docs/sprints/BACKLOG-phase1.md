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

## Sprint 03: Basic SMTP Inbound + Search Foundation ← CURRENT
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

## Sprint 04: Integration & Dev Mode Polish
Wire everything together: agent starts SMTP + Raft + health endpoints in dev mode.
Network-based Raft snapshots (new node bootstrap). TLS between nodes. Graceful shutdown
of all subsystems. CI pipeline: GitHub Actions with lint, build, test (race + cover).
Dev mode quick-start documentation. End-to-end test: start dev agent, send email via
SMTP, query via health API.
