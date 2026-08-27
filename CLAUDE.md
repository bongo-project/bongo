# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Bongo-Go: a mail and calendar server (IMAP, SMTP, POP3, CalDAV/CardDAV) with an HTMX web UI,
embedded storage (no standalone database), and Raft-based clustering. Single Go binary.

## Reference Documents

- `prds/` — Requirements, task list and progress for current work (see Development Workflow)
- `docs/sprints/` — Past sprint files with task details, kept as a historical record
- `docs/walkthrough.md` — End-to-end tour of the running server
- `SECURITY.md` — Reporting policy and known limitations

The original C/Python Bongo, which this reimplements, is not vendored here. It is
at <https://github.com/bongo-project/bongo-project> if you want to consult it.

## Development Workflow

This repository ships the workflow it is built with, in `.claude/`. Claude Code
picks it up automatically — there is nothing to install.

- `.claude/skills/` — `norman` (plan, execute, verify with crash recovery), `prd`
  (requirements documents) and `sweep` (post-implementation cleanup)
- `.claude/agents/` — 16 specialist subagents, trimmed to this project's stack

```bash
norman prd        # write requirements into prds/research/
norman import     # move the PRD to backlog, extract tasks into prds/TASKS.md
norman            # execute the next task
norman verify     # validate the implementation against the PRD
norman sweep      # prove tests guard their invariants; tidy stale comments
```

For a small change, skip the PRD and go straight to `norman plan`. For a one-file
fix with obvious scope, just make the change.

See `.claude/skills/README.md` for provenance. Other assistants: see `AGENTS.md`.

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
make dev              # build + run dev server, validate, seed test data, wait (preferred for dev)
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
- `agent` — Stateless remote protocol processor (SMTP, IMAP, etc.). Registers with servers via gRPC. No storage.
- `dev` — All-in-one, single node, ephemeral storage. Zero config.

## Pebble Key Space

```
/user/{user_id}/profile              → UserProfile protobuf
/user/{user_id}/mail/{guid}          → MailMeta protobuf + BlobRef
/user/{user_id}/event/{guid}         → calendar event + BlobRef
/user/{user_id}/contact/{guid}       → contact + BlobRef (vCard)
/user/{user_id}/calendar/{guid}      → calendar metadata
/user/{user_id}/mailbox/{name}/meta  → mailbox metadata (uid_next, uid_validity)
/user/{user_id}/mailbox/{name}/msg/{uid} → MailboxMessage (flags, tags, mail guid)
/user/{user_id}/preferences          → user preferences
/user/{user_id}/rule/{guid}          → mail filter rule
/user/{user_id}/tag/{name}           → TagMeta protobuf (colour, sort_order)
/user/{user_id}/addrverify/{address} → send-as verification challenge
/user/{user_id}/bayes/{category}/{token} → Bayes token counts
/system/domain/{domain}              → domain config
/system/domain_admin/{domain}/{user_id} → domain-admin grant marker
/system/alias/{alias}                → alias → target user
/system/catchall/{domain}            → per-domain catch-all target
/queue/pending/{unix_ts}-{id}        → queued outbound message (zero-padded ts)
/queue/bounced/{id}                  → bounced outbound message

# Blob store (content-addressable, separate from Pebble)
/data/blobs/{first-2-hex}/{sha256-hash} → raw file content
```

Blob lifecycle: background GC does mark-and-sweep, deletes unreferenced blobs older than 1 hour.
The GC is the ONLY component that deletes blobs. They are content-addressed and shared across
users, so a byte-identical message delivered to several people is one blob — deleting it from any
single owner's code path destroys everyone's copy. The reference set spans Pebble mail, the
outbound queue, and the on-disk spool.

There is no generic ACL or share key space. Sharing is three separate mechanisms: domain-admin
delegation (`/system/domain_admin/...`), send-as grants (`AddressRecord.CanSend`/`Verified`), and
calendar sharing (the `shared_with` field on the Calendar protobuf). An earlier revision of this
file documented `/system/acl/{resource}/{principal}` and `/share/{target}/...`; neither was ever
built.

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
- **Testing:** Table-driven tests. `t.TempDir()` for storage tests. `httptest` for handlers. `-race` always. Full-stack integration tests in `test/integration/`.
- **Naming:** Go standard — `camelCase` unexported, `PascalCase` exported. No abbreviations except well-known (SMTP, IMAP, etc.).
- **Config:** All values have sensible defaults. Dev mode requires zero config.
- **Imports:** stdlib, then third-party, then internal (separated by blank lines).
- **Protobuf:** Definitions in `proto/bongo/v1/`, compiled `.pb.go` files in same directory.
- **Static assets:** Always local minified files in `internal/web/static/` — never CDN links.

## Learnings

### Store Patterns
- New store structs follow the TagStore/MailboxStore/PreferenceStore pattern: pebble field for reads, applier field for Raft writes, 5s timeout on all ApplyCommand calls.
- SQLite schema uses `CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS` — idempotent, no migration system.
- Junction table "replace all" semantics: DELETE-then-INSERT in a single transaction.
- Per-user aggregate limits (e.g. 100 tags/user) enforced via prefix scan helper at handler layer, not inside mutation functions.
- `directApplier` (in `mailbox_test.go`) is reused across all store package tests.

### Search Patterns
- Bleve keyword field (`NewKeywordFieldMapping()`) for exact-match filtering — prevents "news" matching "newsletter". Standard text field would tokenise.

### Validation Patterns
- Regex `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` handles single-char names correctly (inner group optional). Alternative `^[a-z0-9][a-z0-9-]*[a-z0-9]$` would reject single-char.

### Architecture Patterns
- Three-tier update for metadata changes: Pebble (via Raft, source of truth), SQLite (derived index), Bleve (search index). Secondary indexes are best-effort (non-fatal on error).
- Pipeline tag flow: rules stage accumulates in `QueueEnvelope.PendingTags`, deliver stage applies via `store.AddTags` before Raft write.
- Pipeline forwarding flow: rules stage "forward" action and account-level `ForwardAddress` preference both append to `QueueEnvelope.ForwardAddresses`. Deliver stage enqueues an `OutboundMessage` via `ForwardQueuer` after local delivery. `ForwardKeepCopy=false` skips local delivery. Forwarding errors are non-fatal (logged as warnings).
- IMAP keyword convention: `$` prefix for custom keywords. `separateFlags()` splits system flags from keywords, `tagsToIMAPKeywords()` adds prefix.

### UI Patterns
- Alpine.js `fetch()` for JSON API endpoints; HTMX for HTML fragment swaps. Don't use HTMX for JSON-returning endpoints.
- Chi route ordering: static segments (`/mail/tag/{name}`) must be registered before wildcard segments (`/mail/{mailbox}`).

## Repo-Specific Guardrails

- **Never expose `/internal/*` routes on the public web router without real protection.**
  Naming a route "internal" is not a security boundary. Internal control-plane endpoints must either live on a private listener, require explicit auth, or both.

- **Do not ignore `ApplyCommand()` or other persistence errors on user-visible write paths.**
  If the request would be shown as successful to a user or admin, write failures must normally fail the request. Best-effort writes are allowed only when:
  1. the code comment says the write is best-effort,
  2. the user-visible behavior still makes sense without it, and
  3. the failure is logged with context.

- **Do not claim multi-step Raft mutations are atomic unless they actually are.**
  Many mailbox/message operations update multiple keys. If a flow uses multiple `ApplyCommand()` calls, either:
  1. implement a composite command,
  2. add compensation/rollback logic, or
  3. document the partial-failure semantics explicitly.

- **Do not trust `X-Forwarded-For` by default.**
  Only trust forwarded client IP headers when the request came from a configured trusted proxy. Shared helper logic should be used instead of duplicating ad hoc parsing in handlers and middleware.

- **Set cookie security policy explicitly.**
  Auth-related cookies should not rely on browser defaults for `SameSite`. New auth or setup cookies must set `HttpOnly`, `Secure` where appropriate, and an explicit `SameSite` value.

- **All cookie-authenticated mutating routes need CSRF protection or strict origin validation.**
  POST/PUT/DELETE routes behind session cookies should not depend on browser defaults alone.

- **Never log secrets, tokens, generated passwords, or session material.**
  This includes session secrets, recovery material, API keys, and other values that can be replayed.

- **HTTP servers should have explicit timeouts.**
  New `http.Server` instances should set sensible `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` values unless there is a documented reason not to.
