# Bongo-Go

Reimplementation of the Bongo mail/calendar server as a single Go binary.

## What This Is

A mail and calendar server (IMAP, SMTP, POP3, CalDAV/CardDAV) with an HTMX web UI,
embedded storage (no standalone database), and Raft-based clustering.

## Reference Documents

- `../bongo-go-prd.md` — Full PRD with architecture decisions, rationale, and phase plan
- `../bongo.original/` — Original C/Python Bongo source (reference only, do not modify)
- `docs/sprints/` — Current and past sprint files with task details

## Project Structure

```
bongo-go/
├── cmd/                    # CLI entry points (Cobra commands)
│   └── bongo-go/
│       └── main.go
├── internal/               # Private application code
│   ├── agent/              # Server lifecycle (server/edge/dev roles)
│   ├── config/             # HCL config loading and validation
│   ├── store/              # Storage engine (Pebble + Blobs + SQLite)
│   │   ├── pebble.go       # Pebble key-value operations
│   │   ├── blobstore.go    # Content-addressable blob storage
│   │   ├── metadata.go     # SQLite metadata index (per-user)
│   │   └── fsm.go          # Raft FSM implementation
│   ├── cluster/            # Raft consensus + memberlist gossip
│   ├── smtp/               # SMTP server and client
│   ├── imap/               # IMAP server
│   ├── pop3/               # POP3 server
│   ├── caldav/             # CalDAV/CardDAV server
│   ├── web/                # HTMX web UI
│   │   ├── handlers/       # HTTP handlers (chi routes)
│   │   ├── templates/      # Templ templates
│   │   └── static/         # CSS, JS, images
│   ├── auth/               # Authentication backends (local, LDAP, OIDC)
│   ├── queue/              # Message queue and delivery
│   ├── antispam/           # Spam filtering pipeline
│   └── security/           # ACLs, audit logging, encryption
├── proto/                  # Protocol Buffer definitions
├── docs/
│   └── sprints/            # Sprint task files
├── test/                   # Integration and E2E tests
│   ├── integration/
│   └── e2e/
├── go.mod
├── go.sum
└── CLAUDE.md               # This file
```

## Key Technology Choices

| Component | Technology | Notes |
|-----------|-----------|-------|
| Language | Go 1.22+ | Single static binary |
| CLI | spf13/cobra | Subcommands: serve, cluster, user, domain, config, version |
| Config | hashicorp/hcl + spf13/viper | HCL config, env vars, CLI flags |
| Storage (data) | cockroachdb/pebble | LSM key-value, Raft log backend |
| Storage (index) | modernc.org/sqlite | Pure Go, no CGo, per-user DBs, rebuildable from Pebble |
| Search | blevesearch/bleve | Per-user + global full-text search, pure Go |
| Consensus | hashicorp/raft | Single-group Raft |
| Discovery | hashicorp/memberlist | SWIM gossip protocol |
| SMTP | emersion/go-smtp | Server and client |
| IMAP | emersion/go-imap v2 | IMAP4rev1 |
| POP3 | DevelHell/popgun | RFC 1939 |
| CalDAV/CardDAV | emersion/go-webdav | RFC 4791/6352 |
| MIME | emersion/go-message + jhillyerd/enmime | Email parsing |
| HTTP router | go-chi/chi v5 | Lightweight, composable |
| Templates | a-h/templ | Type-safe, compiles to Go |
| CSS | Pico CSS | ~10KB, semantic, 100+ themes |
| Client JS | HTMX + Alpine.js + Sortable.js | ~40KB total |
| Rich text | TipTap | Email composer |
| Real-time | SSE (Server-Sent Events) | New mail, calendar updates |
| Serialization | Protocol Buffers | Pebble values |
| Logging | log/slog | Structured, stdlib |
| Metrics | prometheus/client_golang | /metrics endpoint |
| Auth | go-ldap/ldap, coreos/go-oidc, pquerna/otp | LDAP, OIDC, TOTP |

## Conventions

- **Packages:** `internal/` for all private code. Public API only in `cmd/`.
- **Errors:** Wrap with `fmt.Errorf("context: %w", err)`. No sentinel errors unless needed for `errors.Is()`.
- **Interfaces:** Define at the consumer, not the implementer. Storage, auth, and antispam backends are interfaces.
- **Testing:** Table-driven tests. `t.TempDir()` for storage tests. `httptest` for handlers. `-race` always.
- **Naming:** Go standard — `camelCase` for unexported, `PascalCase` for exported. No abbreviations except well-known (SMTP, IMAP, etc.).
- **Config:** All config values have sensible defaults. Dev mode requires zero config.
- **Imports:** stdlib → third-party → internal (separated by blank lines).

## Build & Test Commands

```bash
# Generate templ templates
templ generate

# Build
go build -o bongo-go ./cmd/bongo-go

# Run in dev mode
./bongo-go serve --dev

# Test (with race detector)
go test -race ./...

# Test with coverage
go test -race -cover ./...

# Lint
golangci-lint run
```

## Pebble Key Space

**Three storage tiers:**
- **Pebble** = metadata source of truth (small protobufs with blob references). Replicated via Raft.
- **Blob store** = content-addressable files on disk (SHA-256 hash = filename). **Shared flat directory** — NOT per-user. All users and all protocol agents reference the same blob pool. Synced between nodes via HTTP pull (not Raft log). Deduplicated automatically — identical content stored once regardless of how many users reference it.
- **SQLite + Bleve** = derived query indexes. Rebuildable from Pebble + blobs.

A user's complete account = their `/user/{user_id}/*` Pebble prefix + referenced blobs. Portable between clusters.
Backup = export Pebble keys + copy every blob referenced by those keys. Restore = write blobs via Put (dedup handles existing), replay Pebble keys, rebuild SQLite/Bleve indexes.

**Blob lifecycle:** Blobs are never deleted immediately. A background GC does mark-and-sweep: scan Pebble for all referenced hashes, delete any blob with zero references that is older than a grace period (1 hour default).

```
# Pebble keys (metadata — Raft replicated)
/user/{user_id}/profile              → protobuf: profile (email, role, password hash, TOTP, quota)
/user/{user_id}/mail/{guid}          → protobuf: mail metadata + BlobRef (hash, size, content_type)
/user/{user_id}/event/{guid}         → protobuf: calendar event + BlobRef
/user/{user_id}/contact/{guid}       → protobuf: contact metadata + BlobRef (vCard)
/user/{user_id}/folder/{guid}        → protobuf: folder metadata
/user/{user_id}/config/{key}         → protobuf: user preferences
/user/{user_id}/rule/{guid}          → protobuf: mail filter rule
/system/config/{key}                 → protobuf: system config
/system/domain/{domain}              → protobuf: domain config
/system/alias/{alias}                → alias → target user mapping
/system/acl/{resource}/{principal}   → protobuf: ACL entry
/share/{target_user}/{type}/{owner}/{guid} → shared resource reference
/queue/message/{timestamp}/{guid}    → protobuf: queued message + BlobRef

# Blob store (content-addressable files — synced between nodes)
/data/blobs/{first-2-hex-chars}/{sha256-hash}  → raw file content
```

## Server Roles

- `server` — Raft voter, storage, all protocols. 3 or 5 form a quorum.
- `edge` — Stateless proxy. Runs all protocol listeners, forwards to servers. No Raft vote, no local storage.
- `dev` — All-in-one server, single node, ephemeral storage. Zero config.

## CLI Commands

```bash
bongo-go serve                    # start server (default: dev role)
bongo-go serve --role=server      # Raft voter, full stack
bongo-go serve --role=edge        # stateless proxy
bongo-go serve --dev              # shorthand for --role=dev
bongo-go cluster status           # show cluster membership
bongo-go user list                # list users (future)
bongo-go user export <email>      # export user backup (future)
bongo-go domain list              # list domains (future)
bongo-go config validate <file>   # validate HCL config (future)
bongo-go version                  # print version
```
