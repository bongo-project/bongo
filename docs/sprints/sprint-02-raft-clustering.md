# Sprint 02: Raft Consensus & Cluster Membership

**Phase:** 1 (Foundation)
**Goal:** Single-node and multi-node Raft consensus with Pebble FSM, memberlist gossip, blob sync stub, and the system.db credential fix from sprint 1.

---

## Context

Sprint 01 gave us the storage foundation: Pebble, blob store, per-user SQLite, and the agent
lifecycle. This sprint adds distributed consensus so writes are replicated across nodes. We also
fix a design issue carried over from sprint 01: `system.db` currently stores `password_hash` in
the users table, but credentials must live in Pebble (the source of truth), not in a derived index.

## Prerequisite Fix (from Sprint 01)

### 0. Remove password_hash from system.db users table

**Why:** `system.db` is a derived cache — it must be fully rebuildable from Pebble. Credentials
(password_hash, TOTP secrets) live in Pebble at `/user/{user_id}/profile`. The system.db `users`
table should only contain data needed for admin directory queries (listing users, checking enabled
status, looking up by username).

**Changes:**
- `internal/store/metadata.go`: Remove `password_hash` column from the `users` table schema.
- Update any tests that reference the `password_hash` column in system.db.
- Add a comment in the schema explaining that credentials live in Pebble, not here.

**Done when:** `system.db` users table has no `password_hash` column. Tests pass.

---

## Tasks

### 1. Pebble-backed Raft LogStore and StableStore

`hashicorp/raft` requires three store interfaces: `LogStore`, `StableStore`, and `SnapshotStore`.
Pebble can back the first two using dedicated key prefixes to avoid collision with application data.

Create `internal/cluster/raftstore.go` with:
- `RaftStore` struct wrapping our existing `PebbleStore`
- Implements `raft.LogStore`: `FirstIndex()`, `LastIndex()`, `GetLog()`, `StoreLog()`, `StoreLogs()`, `DeleteRange()`
- Implements `raft.StableStore`: `Set()`, `Get()`, `SetUint64()`, `GetUint64()`
- Raft log keys: `_raft/log/{index}` (zero-padded uint64 for sort order)
- Stable store keys: `_raft/stable/{key}`

Use a dedicated Pebble prefix (`_raft/`) that is separate from application data (`/user/`, `/system/`, etc.).

**Done when:** `go test ./internal/cluster/...` passes with: store logs, retrieve by index,
delete range, stable store round-trip. Uses `t.TempDir()` with a real Pebble instance.

### 2. FSM (Finite State Machine) implementation

The FSM applies committed Raft log entries to Pebble application state. Each log entry is a
serialized command (put or delete on the application key space).

Create `internal/cluster/fsm.go` with:
- `FSM` struct holding the application `PebbleStore`, `BlobStore`, and `MetadataManager`
- `Apply(log *raft.Log) interface{}` — deserialize command, apply to Pebble, trigger derived index updates
- `Snapshot() (raft.FSMSnapshot, error)` — create a Pebble checkpoint
- `Restore(rc io.ReadCloser) error` — restore Pebble from snapshot, rebuild derived indexes

Command format (use a simple envelope for now, protobufs in a later sprint):
```go
type Command struct {
    Op    string // "put" or "delete"
    Key   []byte
    Value []byte
}
```
JSON-encode commands for now (simple, debuggable). Switch to protobuf when we add protocol handlers.

**Derived index updates after Apply:**
- If key starts with `/user/{id}/mail/` or `/user/{id}/event/` → update that user's SQLite index
- If key starts with `/user/{id}/profile` → update system.db users table (username, display_name, enabled — NOT credentials)
- Blob sync: if the applied value contains blob references, check `BlobStore.Exists()` — log a warning if missing (actual blob fetch is task 5)

**Done when:** `go test ./internal/cluster/...` passes with: apply put/delete commands,
verify Pebble state after apply, snapshot creates valid checkpoint, restore rebuilds state.

### 3. Raft node setup and bootstrap

Create `internal/cluster/raft.go` with:
- `NewRaftNode(cfg RaftConfig, fsm raft.FSM, store *RaftStore, transport raft.Transport) (*raft.Raft, error)`
- Bootstrap logic for single-node cluster (dev mode and first server)
- Configuration: `raft.DefaultConfig()` with tuned timeouts for our use case

Wire into the agent lifecycle:
- `server` role: create Raft node, bootstrap if `bootstrap-expect` is met
- `dev` role: single-node bootstrap with in-memory transport (fast, no network)
- `edge` role: no Raft (edge nodes proxy to servers)

**Done when:** `bongo-go serve --dev` starts a single-node Raft cluster, becomes leader,
and logs "raft: leadership acquired". Integration test confirms leader election.

### 4. Leader forwarding for writes

Only the Raft leader can propose log entries. Non-leader servers must forward writes to the leader.

Create `internal/cluster/forward.go` with:
- `ApplyCommand(raftNode *raft.Raft, cmd Command, timeout time.Duration) error`
- If this node is the leader: `raftNode.Apply(encoded, timeout)`
- If not leader: return `ErrNotLeader` with leader address (for the caller to forward or return an error)

For now, the agent exposes a simple internal API for testing:
- `PUT /internal/kv/{key}` → apply a put command through Raft
- `GET /internal/kv/{key}` → read directly from local Pebble (reads are local, not through Raft)
- `DELETE /internal/kv/{key}` → apply a delete command through Raft

These internal endpoints are for testing Raft consensus — they'll be replaced by protocol-specific
handlers (SMTP delivery, IMAP flag updates, etc.) in later sprints.

**Done when:** Integration test: write via internal API on leader, read from follower (after Raft
replication). Write to non-leader returns `ErrNotLeader` with leader address.

### 5. Memberlist gossip for node discovery

Create `internal/cluster/memberlist.go` with:
- `NewMemberlist(cfg MemberlistConfig) (*memberlist.Memberlist, error)`
- Node metadata in gossip: node name, Raft address, HTTP address, role (server/client)
- Event delegate: track node joins/leaves, log them
- Wire into serve command: join existing cluster via `-join` flag, discover peers via gossip

The gossip layer provides:
- Service discovery: clients find servers without hardcoded addresses
- Failure detection: SWIM protocol detects node failures in seconds
- Raft peer discovery: servers learn about other servers to form/join the Raft cluster

**Done when:** Integration test with 3 nodes: start node 1, nodes 2 and 3 join via `-join`,
all three see each other in memberlist. `bongo-go cluster status` command lists all cluster members
with their roles and addresses.

### 6. `bongo-go cluster status` command

Add a `cluster` Cobra command group with a `status` subcommand that queries the local node's
memberlist and prints cluster membership.

```
$ bongo-go cluster status
Node         Address          Role     Status
bongo-01     10.0.1.1:8301    server   alive (leader)
bongo-02     10.0.1.2:8301    server   alive
bongo-03     10.0.1.3:8301    server   alive
```

The command connects to the local node's HTTP API: `GET /internal/cluster/members`.
The handler queries memberlist and returns JSON. Future subcommands under `cluster`
will include `join`, `leave`, etc.

**Done when:** `bongo-go cluster status` connects to local node and displays member list.
Works in dev mode (shows single node).

### 7. Blob sync stub (HTTP endpoint)

The blob sync protocol fetches missing blobs from peers. In this sprint we implement only the
serving side — the pull-from-peers logic comes in sprint 3 when SMTP delivery creates real blobs.

Add to the agent's internal HTTP routes:
- `GET /internal/blob/{hash}` — serves a blob from local blob store. Returns 404 if not found.
- `HEAD /internal/blob/{hash}` — checks existence without transferring content.

**Done when:** Integration test: store a blob via BlobStore.Put(), fetch it via HTTP
`GET /internal/blob/{hash}`, verify content matches. HEAD returns 200 for existing, 404 for missing.

### 8. Multi-node integration test

A comprehensive integration test that validates the full Raft lifecycle:

1. Start 3 nodes with `--role=server` (using in-memory transport or localhost with different ports)
2. Wait for leader election
3. Write a key-value pair via the leader's internal API
4. Read the value from all three nodes (verify replication)
5. Store a blob on the leader, verify blob HTTP endpoint serves it
6. Kill the leader, verify a new leader is elected
7. Write via the new leader, verify replication to surviving nodes

This test should live in `test/integration/raft_test.go` and use build tag `// +build integration`
so it doesn't run on every `go test ./...` (it's slower due to Raft timeouts).

**Done when:** `go test -tags=integration ./test/integration/...` passes with all 7 steps.

## Exit Criteria

- [ ] `system.db` users table no longer has `password_hash` column
- [ ] `go test -race ./...` passes (unit tests, no integration tag)
- [ ] `go test -race -tags=integration ./test/integration/...` passes
- [ ] Dev mode (`bongo-go serve --dev`) starts single-node Raft cluster and becomes leader
- [ ] Pebble-backed LogStore and StableStore pass all raft interface tests
- [ ] FSM applies put/delete commands to Pebble and triggers derived index updates
- [ ] Leader forwarding: writes through Raft, reads local
- [ ] Memberlist gossip: nodes discover each other via `--join`
- [ ] `bongo-go cluster status` lists cluster members
- [ ] Blob HTTP endpoint serves blobs from local store
- [ ] Multi-node integration test: replication, leader failover, blob serving

## What NOT to do in this sprint

- No SMTP or any protocol servers (sprint 3)
- No Bleve search indexes (sprint 3)
- No protobuf serialization yet — use JSON for Raft commands (switch in sprint 3)
- No blob pull-from-peers (only the serving endpoint — pull logic comes with SMTP in sprint 3)
- No Raft snapshots over the network (local snapshot/restore only — network snapshots in sprint 4)
- No TLS between nodes (sprint 4)
- No web UI
