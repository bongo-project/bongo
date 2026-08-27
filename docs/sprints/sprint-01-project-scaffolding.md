# Sprint 01: Project Scaffolding & Storage Foundation

**Phase:** 1 (Foundation)
**Goal:** Get a Go binary that boots, loads config, opens all three storage tiers (Pebble + blob store + per-user SQLite), and shuts down cleanly.

---

## Context

This is the very first sprint. We're building the skeleton that everything else hangs on:
the CLI, config loading, storage engines, and the agent lifecycle. No protocols yet —
just the foundation.

## Tasks

### 1. Initialize Go module and project structure

Create `go.mod` with module path `github.com/bongo-project/bongo` (or your preferred path).
Create the directory structure from CLAUDE.md. Add a `.gitignore` for Go projects.

**Done when:** `go build ./cmd/bongo-go` produces a binary (even if it does nothing).

### 2. Cobra CLI with agent command

Set up `spf13/cobra` with the root command and `serve` subcommand.
The serve command accepts flags: `--role` (server/edge/dev), `--dev`, `--config`, `--bind`, `--join`, `--bootstrap-expect`.

```
bongo-go serve --dev
bongo-go serve --role=server
bongo-go version
```

**Done when:** `bongo-go serve --dev` prints "starting bongo-go in dev role" and exits.
`bongo-go version` prints version info.

### 3. HCL config loading

Create `internal/config/config.go` with a Config struct covering the top-level HCL blocks
from the PRD (server, cluster, storage, smtp, imap, pop3, web, caldav, telemetry).
Use `hashicorp/hcl` v2 for parsing and `spf13/viper` for merging (CLI flags > env > file > defaults).

All fields must have sensible defaults so dev mode needs zero config.

Write a test config file at `test/testdata/config.hcl` with example values.

**Done when:** `go test ./internal/config/...` passes. Config loads from file, env, and flags.

### 4. Pebble storage engine

Create `internal/store/pebble.go` with:
- `NewPebbleStore(dataDir string, opts ...Option) (*PebbleStore, error)`
- `Get(key []byte) ([]byte, error)`
- `Set(key, value []byte) error`
- `Delete(key []byte) error`
- `PrefixScan(prefix []byte) (Iterator, error)`
- `Close() error`

The key space follows CLAUDE.md conventions. For dev mode, use a temp directory.

**Done when:** `go test ./internal/store/...` passes with Pebble read/write/scan tests using `t.TempDir()`.

### 5. Content-addressable blob store

The blob store is a **shared, flat directory** — NOT per-user. All agents (IMAP, SMTP, etc.)
and all users reference the same pool of blobs. A blob is identified solely by its SHA-256 hash.
No blob "belongs" to any user — users' Pebble metadata contains BlobRef pointers to shared blobs.

Create `internal/store/blobstore.go` with:
- `NewBlobStore(dataDir string) (*BlobStore, error)` — creates `{dataDir}/blobs/` and `{dataDir}/blobs/tmp/`
- `Put(data []byte) (hash string, err error)` — SHA-256 hash, skip write if exists (dedup), return hash
- `PutReader(r io.Reader) (hash string, size int64, err error)` — streaming variant for large files
- `Get(hash string) (io.ReadCloser, error)` — open blob by hash, return reader
- `Exists(hash string) bool` — check if blob file exists on disk
- `Delete(hash string) error` — remove blob file (called by GC only)
- `Verify(hash string) (bool, error)` — re-hash file content and compare to filename
- `List() ([]string, error)` — list all blob hashes (for GC mark-and-sweep)

**File layout:** `{dataDir}/blobs/{first-2-hex}/{full-sha256-hash}`

Example: hash `a1b2c3d4e5f6...7890` → `data/blobs/a1/a1b2c3d4e5f6...7890`

**Atomic writes:** All writes go to `{dataDir}/blobs/tmp/{random}` first, then `os.Rename()`
into the final path. This guarantees no partial blobs — a blob either fully exists or doesn't.
If the target already exists (same hash = same content), skip the rename and delete the temp file.

**Design notes for future sprints (do NOT implement yet, but keep the API compatible):**
- **Garbage collection (sprint 2+):** A background GC process will scan Pebble for all referenced
  hashes (mark phase), then compare against `List()` output and delete unreferenced blobs (sweep phase).
  Blobs younger than a grace period (e.g., 1 hour) are never deleted to avoid racing with in-flight
  writes. The `Delete()` and `List()` methods exist to support this.
- **Backup/restore:** A user backup = their Pebble key prefix + all referenced blob hashes. The backup
  process collects every BlobRef from the user's Pebble keys and copies those blob files into the backup
  archive. On restore, blobs are written back via `Put()` — dedup handles the case where the blob
  already exists from another user's reference.
- **Multi-node sync (sprint 2+):** Blobs sync between cluster nodes via HTTP pull (not Raft log).
  A node receiving a Pebble entry with a BlobRef it doesn't have locally will pull it from a peer.

**Done when:** `go test ./internal/store/...` passes with:
- Blob put/get round-trip (write bytes, read back, compare)
- Deduplication (same content written twice = one file on disk, same hash returned)
- Streaming large blob via PutReader (>1MB)
- Verify detects corruption (modify file on disk, Verify returns false)
- Concurrent writes to different blobs (10 goroutines)
- Concurrent writes of same content (dedup under contention)
- List returns all stored hashes
- Delete removes blob file and subsequent Get returns error

### 6. SQLite metadata index (per-user pattern)

Following the original Bongo architecture, each user gets their own SQLite database file.
This eliminates cross-user write contention and enables per-user recovery.

Create `internal/store/metadata.go` with:
- `UserIndex` — per-user SQLite database with storeobject, maildocument, eventdocument, conversation tables (see PRD Section 5.2)
- `SystemIndex` — single system SQLite database with users, domains, aliases tables
- `MetadataManager` — manages both, with an LRU cache of open user database connections (default 256, idle timeout 5 minutes)
  - `OpenUser(userID string) (*UserIndex, error)` — opens or creates a user's SQLite DB
  - `CloseUser(userID string) error`
  - `IndexObject(userID string, obj StoreObject) error`
  - `QueryByType(userID string, docType int) ([]StoreObject, error)`
  - `QueryByDateRange(userID string, docType int, start, end time.Time) ([]StoreObject, error)`
  - `Close() error` — closes all open connections

File layout: `{dataDir}/metadata/system.db` and `{dataDir}/metadata/users/{user_id}.db`

Use `modernc.org/sqlite` (pure Go, no CGo).

**Done when:** `go test ./internal/store/...` passes with: system DB schema creation, user DB creation,
per-user insert and query, LRU eviction of idle connections, and multiple concurrent user DBs.

### 7. Agent lifecycle

Create `internal/agent/agent.go` with:
- `Agent` struct holding config, Pebble store, blob store, metadata manager, and a `context.Context` for shutdown
- `NewAgent(cfg *config.Config) (*Agent, error)` — opens all three storage tiers in order:
  1. Pebble store (source of truth)
  2. Blob store (shared content-addressable files)
  3. MetadataManager (per-user SQLite + system.db derived indexes)
- `Run(ctx context.Context) error` — blocks until context cancelled
- `Shutdown() error` — closes storage cleanly in reverse order (metadata → blobs → pebble)

Wire this into the Cobra `agent` command. Handle SIGINT/SIGTERM for graceful shutdown.

**Done when:** `bongo-go serve --dev` starts, opens Pebble + blob store + SQLite in a temp dir,
logs "bongo-go started" with storage paths, and shuts down cleanly on Ctrl+C with "bongo-go stopped" log.
Storage directory structure matches: `{dataDir}/pebble/`, `{dataDir}/blobs/`, `{dataDir}/metadata/`.

### 8. Structured logging

Set up `log/slog` with JSON handler. Log level configurable via config and CLI flag.
All agent lifecycle events logged: starting, config loaded, storage opened, shutting down.

**Done when:** Running in dev mode produces structured JSON log lines to stdout.

### 9. Health check endpoint

Start a minimal HTTP server (chi) on a configurable port (default 8080 in dev mode).
Serve `/health/live` (returns 200 if process is running) and `/health/ready` (returns 200 if storage is open).

**Done when:** `curl localhost:8080/health/live` returns 200 while agent is running.

## Exit Criteria

- [ ] `go build ./cmd/bongo-go` produces a static binary
- [ ] `go test -race ./...` passes with no failures
- [ ] `bongo-go serve --dev` starts, opens all three storage tiers, serves health endpoints, shuts down on SIGINT
- [ ] Blob store shared directory layout: `data/blobs/{2-hex}/{hash}` with atomic writes
- [ ] Config loads from HCL file, env vars, and CLI flags with correct precedence
- [ ] Pebble get/set/delete/scan operations work
- [ ] Blob store put/get with deduplication works
- [ ] SQLite schema creates and queries work
- [ ] Structured JSON logging to stdout

## What NOT to do in this sprint

- No Raft or clustering (sprint 2)
- No SMTP or any protocol servers (sprint 3+)
- No web UI
- No protobuf definitions yet (use simple []byte values for now)
