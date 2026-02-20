# Sprint 01: Project Scaffolding & Storage Foundation

**Phase:** 1 (Foundation)
**Goal:** Get a Go binary that boots, loads config, opens Pebble + SQLite, and shuts down cleanly.

---

## Context

This is the very first sprint. We're building the skeleton that everything else hangs on:
the CLI, config loading, storage engines, and the agent lifecycle. No protocols yet —
just the foundation.

## Tasks

### 1. Initialize Go module and project structure

Create `go.mod` with module path `github.com/bongo-go/bongo-go` (or your preferred path).
Create the directory structure from CLAUDE.md. Add a `.gitignore` for Go projects.

**Done when:** `go build ./cmd/bongo-go` produces a binary (even if it does nothing).

### 2. Cobra CLI with agent command

Set up `spf13/cobra` with the root command and `agent` subcommand.
The agent command accepts flags: `-mode` (server/client/dev), `-config`, `-bind`, `-join`, `-bootstrap-expect`.

```
bongo-go agent -mode=dev
bongo-go version
```

**Done when:** `bongo-go agent -mode=dev` prints "starting agent in dev mode" and exits.
`bongo-go version` prints version info.

### 3. HCL config loading

Create `internal/config/config.go` with a Config struct covering the top-level HCL blocks
from the PRD (agent, cluster, storage, smtp, imap, pop3, web, caldav, telemetry).
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

### 5. SQLite metadata index

Create `internal/store/metadata.go` with:
- `NewMetadataIndex(dbPath string) (*MetadataIndex, error)`
- Schema creation (storeobject, maildocument, eventdocument, conversation tables from PRD Section 5.2)
- `IndexObject(obj StoreObject) error` — upsert into SQLite
- `QueryByType(userID string, docType int) ([]StoreObject, error)`
- `QueryByDateRange(userID string, docType int, start, end time.Time) ([]StoreObject, error)`
- `Close() error`

Use `modernc.org/sqlite` (pure Go, no CGo).

**Done when:** `go test ./internal/store/...` passes with SQLite schema creation, insert, and query tests.

### 6. Agent lifecycle

Create `internal/agent/agent.go` with:
- `Agent` struct holding config, Pebble store, metadata index, and a `context.Context` for shutdown
- `NewAgent(cfg *config.Config) (*Agent, error)` — opens storage
- `Run(ctx context.Context) error` — blocks until context cancelled
- `Shutdown() error` — closes storage cleanly

Wire this into the Cobra `agent` command. Handle SIGINT/SIGTERM for graceful shutdown.

**Done when:** `bongo-go agent -mode=dev` starts, opens Pebble + SQLite in a temp dir,
logs "agent started", and shuts down cleanly on Ctrl+C. Storage files are created and cleaned up.

### 7. Structured logging

Set up `log/slog` with JSON handler. Log level configurable via config and CLI flag.
All agent lifecycle events logged: starting, config loaded, storage opened, shutting down.

**Done when:** Running in dev mode produces structured JSON log lines to stdout.

### 8. Health check endpoint

Start a minimal HTTP server (chi) on a configurable port (default 8080 in dev mode).
Serve `/health/live` (returns 200 if process is running) and `/health/ready` (returns 200 if storage is open).

**Done when:** `curl localhost:8080/health/live` returns 200 while agent is running.

## Exit Criteria

- [ ] `go build ./cmd/bongo-go` produces a static binary
- [ ] `go test -race ./...` passes with no failures
- [ ] `bongo-go agent -mode=dev` starts, opens storage, serves health endpoints, shuts down on SIGINT
- [ ] Config loads from HCL file, env vars, and CLI flags with correct precedence
- [ ] Pebble get/set/delete/scan operations work
- [ ] SQLite schema creates and queries work
- [ ] Structured JSON logging to stdout

## What NOT to do in this sprint

- No Raft or clustering (sprint 2)
- No SMTP or any protocol servers (sprint 3+)
- No web UI
- No protobuf definitions yet (use simple []byte values for now)
