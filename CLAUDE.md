# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Bongo-Go: a mail and calendar server (IMAP, SMTP, POP3, CalDAV/CardDAV) with an HTMX web UI,
embedded storage (no standalone database), and Raft-based clustering. Single Go binary.

## Reference Documents

- `../bongo-go-prd.md` — Full PRD with architecture decisions, rationale, and phase plan
- `../bongo.original/` — Original C/Python Bongo source (reference only, do not modify)
- `docs/sprints/` — Current and past sprint files with task details

## Build & Test Commands

```bash
make all              # generate templ + build both binaries
make build            # generate + go build bin/bongo-go + bin/bongo-import
make generate         # templ generate (required before build)
make test             # go test ./...
make test-race        # go test -race ./...
make test-cover       # go test -race -cover ./...
make lint             # golangci-lint run
make sec              # gosec ./... (security scanning)
make run              # build + ./bin/bongo-go serve --dev
make clean            # rm -rf bin/

# Run a single test
go test -race -run TestFunctionName ./internal/store/...

# Run tests for a single package
go test -race ./internal/cluster/...
```

Two binaries: `bongo-go` (the server) and `bongo-import` (mail import from mbox/maildir/eml/IMAP/Thunderbird).

## Architecture Overview

### Three-Tier Storage

- **Pebble** = metadata source of truth (small protobufs with blob references). Replicated via Raft.
- **Blob store** = content-addressable files on disk (SHA-256 hash = filename). Shared flat directory, NOT per-user. Synced between nodes via HTTP pull (not Raft log). Deduplicated automatically.
- **SQLite + Bleve** = derived query indexes. Per-user databases. Rebuildable from Pebble + blobs.

Backup = export Pebble keys + copy referenced blobs. Restore = write blobs, replay keys, rebuild indexes.

### Server Startup (internal/agent/agent.go)

The `Agent` struct is the central orchestrator. Startup order:
1. Config loading (viper: CLI flags > env vars > HCL file > defaults)
2. Storage init: Pebble, BlobStore, MetadataManager (per-user SQLite), SearchManager (per-user Bleve)
3. Raft + gossip cluster setup (dev mode: in-memory single-node; server: TCP + file snapshots)
4. HTTP routes (chi router), protocol servers (SMTP :25/:587, IMAP :143/:993, POP3 :110/:995)
5. Background workers: queue delivery (leader-only), blob sync, event reminders
6. Dev mode: auto-creates `admin@localhost` / `admin` with default mailboxes

Graceful shutdown reverses the order: HTTP > SMTP > IMAP > POP3 > workers > Raft > gossip > stores.

### Request Flow (HTTP)

```
Request → chi router → SecurityHeaders → RequireAuth (HMAC-SHA256 cookie) →
  Require2FA → InjectLocale → Handler → Store (Pebble/Blobs) → Templ template → Response
```

Admin routes add `RequireAdmin` middleware. CalDAV/CardDAV use HTTP Basic Auth instead of cookies.

### Key Architectural Patterns

**RaftApplier interface**: All writes go through Raft consensus. Handlers never write to Pebble directly — they call `ApplyCommand()` which serializes a `RaftCommand` protobuf (PUT/DELETE) through the Raft log. The FSM applies committed entries to Pebble.

**Handler dependency injection via setters**: Handlers receive stores via `SetXXX()` methods, enabling nil-check graceful degradation on edge nodes:
```go
func (h *MailHandler) SetApplier(applier store.RaftApplier) { h.applier = applier }
func (h *MailHandler) SetThreadStore(ts *store.ThreadStore) { h.threads = ts }
```

**FSM side effects**: On Apply(), the FSM also triggers search indexing (Bleve), blob sync enqueueing, and IMAP notifications for new mail.

**Templ + HTMX + Alpine.js**: Templates compile to Go functions (`templ generate` required). HTMX handles interactivity, Alpine.js for reactive data binding. No JS build step.

### Server Roles

- `server` — Raft voter, storage, all protocols. 3 or 5 form a quorum.
- `edge` — Stateless proxy. Runs protocol listeners, forwards to servers. No Raft, no local storage.
- `dev` — All-in-one, single node, ephemeral storage. Zero config.

## Pebble Key Space

```
/user/{user_id}/profile              → UserProfile protobuf
/user/{user_id}/mail/{guid}          → MailMeta protobuf + BlobRef
/user/{user_id}/event/{guid}         → calendar event + BlobRef
/user/{user_id}/contact/{guid}       → contact + BlobRef (vCard)
/user/{user_id}/folder/{guid}        → folder metadata
/user/{user_id}/config/{key}         → user preferences
/user/{user_id}/rule/{guid}          → mail filter rule
/system/config/{key}                 → system config
/system/domain/{domain}              → domain config
/system/alias/{alias}                → alias → target user
/system/acl/{resource}/{principal}   → ACL entry
/share/{target_user}/{type}/{owner}/{guid} → shared resource
/queue/message/{timestamp}/{guid}    → queued outbound message

# Blob store (content-addressable, separate from Pebble)
/data/blobs/{first-2-hex}/{sha256-hash} → raw file content
```

Blob lifecycle: background GC does mark-and-sweep, deletes unreferenced blobs older than 1 hour.

## Key Technology Choices

| Component | Technology |
|-----------|-----------|
| CLI | spf13/cobra |
| Config | hashicorp/hcl + spf13/viper |
| Storage | cockroachdb/pebble (data), modernc.org/sqlite (index), blevesearch/bleve (FTS) |
| Consensus | hashicorp/raft + hashicorp/memberlist (gossip) |
| Protocols | emersion/go-smtp, emersion/go-imap v2, emersion/go-webdav |
| MIME | emersion/go-message + jhillyerd/enmime |
| HTTP | go-chi/chi v5 |
| Templates | a-h/templ |
| Frontend | HTMX + Alpine.js + Sortable.js + Pico CSS + TipTap (composer) |
| Serialization | Protocol Buffers (Pebble values, Raft commands) |

## Conventions

- **Packages:** `internal/` for all private code. Public API only in `cmd/`.
- **Errors:** Wrap with `fmt.Errorf("context: %w", err)`. No sentinel errors unless needed for `errors.Is()`.
- **Interfaces:** Define at the consumer, not the implementer. Storage, auth, and antispam backends are interfaces.
- **Testing:** Table-driven tests. `t.TempDir()` for storage tests. `httptest` for handlers. `-race` always.
- **Naming:** Go standard — `camelCase` unexported, `PascalCase` exported. No abbreviations except well-known (SMTP, IMAP, etc.).
- **Config:** All values have sensible defaults. Dev mode requires zero config.
- **Imports:** stdlib, then third-party, then internal (separated by blank lines).
- **Protobuf:** Definitions in `proto/bongo/v1/`, compiled `.pb.go` files in same directory.
- **Static assets:** Always local minified files in `internal/web/static/` — never CDN links.
