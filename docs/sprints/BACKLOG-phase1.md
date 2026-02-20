# Phase 1 Sprint Backlog

Light overview of all Phase 1 sprints. Only the current sprint has a detailed task file.

## Sprint 01: Project Scaffolding & Storage Foundation ✅ DONE
Go module, Cobra CLI, HCL config, Pebble store, content-addressable blob store (shared flat
directory), per-user SQLite metadata index with LRU connection pooling, agent lifecycle with
three-tier storage init/shutdown, structured logging, health endpoints.

## Sprint 02: Raft Consensus & Cluster Membership ← CURRENT
Fix system.db credential issue (remove password_hash — credentials live in Pebble).
Pebble-backed Raft LogStore/StableStore. FSM that applies writes to Pebble and triggers
derived index updates. Memberlist gossip for node discovery. Leader forwarding for writes.
Blob sync HTTP endpoint (serving side). `bongo-go members` command. Multi-node integration
test: leader election, replication, failover.

## Sprint 03: Basic SMTP Inbound + Search Foundation
SMTP server via emersion/go-smtp on port 25. Accept mail, parse with go-message, store
metadata in Pebble via Raft, write raw message + attachments to blob store, index in
per-user SQLite AND Bleve. STARTTLS support. Basic auth (credentials read from Pebble).
Blob pull-from-peers (follower fetches missing blobs from leader/peers). Per-user Bleve
index for mailbox search. Global Bleve index for GAL (Global Address List) autocomplete.
Protobuf serialization for Raft commands (replace JSON). Verify with swaks and telnet.
Integration test: send email via SMTP, confirm in Pebble + blob store, search via Bleve.

## Sprint 04: Integration & Dev Mode Polish
Wire everything together: agent starts SMTP + Raft + health endpoints in dev mode.
Network-based Raft snapshots (new node bootstrap). TLS between nodes. Graceful shutdown
of all subsystems. CI pipeline: GitHub Actions with lint, build, test (race + cover).
Dev mode quick-start documentation. End-to-end test: start dev agent, send email via
SMTP, query via health API.
