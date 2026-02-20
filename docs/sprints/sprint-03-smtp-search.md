# Sprint 03: Basic SMTP Inbound + Search Foundation

**Phase:** 1 (Foundation)
**Goal:** Accept inbound email via SMTP, store in the three-tier storage engine, enable full-text search via Bleve, switch Raft commands to protobuf, and add blob pull-from-peers so followers can fetch missing blobs.

---

## Context

Sprint 02 gave us Raft consensus, gossip, and the internal KV/blob HTTP endpoints.
This sprint makes bongo-go actually do something useful: receive email. We also lay
the search foundation (Bleve) so mailboxes are queryable from day one.

## Cleanup from Sprint 02

### 0a. Fix `PebbleStore.DeleteRange` end-key semantics

**Why:** The current implementation appends a `0x00` byte to the end key to make the
range inclusive. While this works for the Raft log key format, it's fragile — a key
ending in `0x00` would be incorrectly excluded. Replace with a proper successor
calculation consistent with `prefixUpperBound`.

**Changes:**
- `internal/store/pebble.go`: Replace the `append 0x00` logic in `DeleteRange` with
  a `keySuccessor(end)` helper that increments the last byte (matching the approach in
  `prefixUpperBound`). If the end key is all `0xFF` bytes, fall back to a full prefix
  scan + individual deletes.
- Add unit test in `pebble_test.go` for `DeleteRange` with edge-case keys (empty,
  single byte, all-0xFF, key ending in 0x00).

**Done when:** `go test -race ./internal/store/...` passes with the new edge-case tests.

### 0b. Replace deprecated `filepath.HasPrefix` in tar.go

**Why:** `filepath.HasPrefix` was deprecated in Go 1.8 and may not exist in future
Go versions. It also has incorrect behavior on some platforms (e.g. case-insensitive
filesystems on macOS).

**Changes:**
- `internal/cluster/tar.go`: Replace `filepath.HasPrefix(target, dir)` with
  `strings.HasPrefix(filepath.Clean(target), filepath.Clean(dir)+string(os.PathSeparator))`.
- Add unit test in a new `tar_test.go` that verifies path traversal is rejected
  (e.g. a tar entry with `../../etc/passwd` as the name).

**Done when:** `go test -race ./internal/cluster/...` passes with the path-traversal test.

---

## Tasks

### 1. Protocol Buffer definitions for Pebble values

Define protobuf messages for the values stored in Pebble. This replaces the JSON
encoding used in sprint 2 for Raft commands and establishes the canonical wire format
for all stored objects.

Create `proto/bongo/v1/`:
- `command.proto` — `RaftCommand { enum Op { PUT, DELETE } bytes key, bytes value }`
- `mail.proto` — `MailMeta { string guid, string message_id, string subject, string from, repeated string to, int64 received_at, BlobRef raw_message, repeated BlobRef attachments }`
- `blob.proto` — `BlobRef { string hash, int64 size, string content_type }`
- `profile.proto` — `UserProfile { string email, string display_name, bytes password_hash, bytes totp_secret, int64 quota_bytes }`

Install `protoc-gen-go` and add a `go generate` directive in the proto directory.

**Changes:**
- `internal/cluster/fsm.go`: Decode `RaftCommand` protobuf instead of JSON in `Apply()`.
- `internal/cluster/forward.go`: Encode commands as protobuf in `ApplyCommand()`.
- `internal/web/handlers/kv.go`: Encode commands as protobuf for internal API.
- Keep JSON fallback for one release cycle (try protobuf, fall back to JSON if decode fails)
  so any in-flight Raft logs from an upgrade don't break.

**Done when:** `go test -race ./...` passes. Raft commands use protobuf encoding. Integration test still passes.

### 2. SMTP server (emersion/go-smtp)

Create `internal/smtp/server.go` using `emersion/go-smtp`:
- Listen on port 25 (configurable via `smtp.port` in HCL config)
- Listen on port 587 for submission (configurable via `smtp.submission_port`)
- `Backend` implementation with `NewSession()` returning our `Session`
- `Session` implementation:
  - `AuthPlain(username, password string)` — validate against Pebble `/user/{id}/profile` password hash (bcrypt)
  - `Mail(from string, opts *smtp.MailOptions)` — validate sender domain against `/system/domain/{domain}`
  - `Rcpt(to string)` — resolve recipient: check `/system/alias/{alias}` then `/user/{id}/profile`
  - `Data(r io.Reader)` — hand off to delivery pipeline (task 3)
  - `Reset()`, `Logout()` — cleanup
- STARTTLS support using `tls.Config` from `smtp.tls.cert_file` / `smtp.tls.key_file`
- In dev mode, generate self-signed cert automatically or accept plaintext

Wire into agent lifecycle:
- `internal/agent/agent.go`: Start SMTP server in `Run()`, shut down in `Shutdown()`
- Only start if `cfg.SMTP.Enabled` (default: true)

**Done when:** `swaks --to user@example.test --server localhost:25` connects and gets
through EHLO, MAIL FROM, RCPT TO (auth not required for local delivery in dev mode).

### 3. Mail delivery pipeline

Create `internal/smtp/delivery.go`:
- Parse message with `emersion/go-message` (headers, body, MIME structure)
- Extract attachments via `jhillyerd/enmime`
- For each recipient:
  1. Store raw message bytes → `BlobStore.Put()` → get hash
  2. Store each attachment → `BlobStore.Put()` → get hashes
  3. Build `MailMeta` protobuf with subject, from, to, message_id, BlobRefs
  4. Apply Raft command: `PUT /user/{recipient_id}/mail/{new_guid}` → serialized `MailMeta`
  5. FSM applies the command → Pebble write → derived index update (per-user SQLite + Bleve)

**Done when:** Send email via swaks in dev mode, verify:
- Blob exists in `{dataDir}/blobs/` with correct SHA-256 name
- Pebble key `/user/{id}/mail/{guid}` contains protobuf with correct BlobRef
- Per-user SQLite `storeobject` and `maildocument` rows are populated
- Bleve index contains the message (searchable by subject/sender)

### 4. Authentication backend (credentials from Pebble)

Create `internal/auth/local.go`:
- `Authenticator` interface: `Authenticate(username, password string) (userID string, err error)`
- `LocalAuth` implementation:
  - Lookup `/user/{id}/profile` in Pebble (try exact match, then check system.db username index)
  - Decode `UserProfile` protobuf, extract `password_hash`
  - Compare with `bcrypt.CompareHashAndPassword()`
- `SetPassword(userID, password string) error` — bcrypt hash, update Pebble profile via Raft

Wire into SMTP session and add a `bongo-go user create` CLI command:
- `cmd/bongo-go/user.go`: `bongo-go user create --email user@example.test --password secret`
  - Applies Raft command to create `/user/{id}/profile` with bcrypt hash
  - Updates system.db users table via FSM derived index

**Done when:** `bongo-go user create` followed by SMTP AUTH PLAIN succeeds. Bad password returns 535.

### 5. Blob pull-from-peers

When the FSM applies a command containing BlobRefs, followers may not have the blob
locally (blobs don't travel through Raft). They need to pull from peers.

Create `internal/cluster/blobsync.go`:
- `BlobSyncer` struct with list of peer HTTP addresses (from gossip)
- `EnsureBlob(hash string) error`:
  1. Check `BlobStore.Exists(hash)` — return early if present
  2. Query gossip for peer HTTP addresses
  3. Try `HEAD /internal/blob/{hash}` on each peer until one returns 200
  4. `GET /internal/blob/{hash}` from that peer, pipe to `BlobStore.PutReader()`
  5. Verify hash matches after download
- Background goroutine processes a channel of missing blob hashes
- FSM `Apply()` enqueues missing blob hashes to the sync channel after applying commands

**Done when:** Integration test: 3-node cluster, store blob on leader only, apply Raft command
referencing the blob, followers detect missing blob and pull it from leader. Verify blob exists
on all nodes.

### 6. Per-user Bleve index for mailbox search

Create `internal/store/search.go`:
- `SearchManager` — manages per-user Bleve indexes (similar LRU pattern to MetadataManager)
- `OpenUser(userID string) (bleve.Index, error)` — opens or creates `{dataDir}/search/{userID}`
- `IndexMail(userID string, meta MailMeta) error` — index subject, sender, recipients, body text
- `Search(userID string, query string, limit int) ([]SearchResult, error)` — full-text search
- `Close() error` — close all open indexes

Index these fields for mail:
- `subject` (text, analyzed)
- `from` (keyword)
- `to` (keyword, multi-valued)
- `body` (text, analyzed — extract plain text from MIME)
- `date` (date)
- `has_attachment` (boolean)

Wire into FSM:
- `internal/cluster/fsm.go`: After applying a mail put command, call `SearchManager.IndexMail()`

Wire into agent:
- `internal/agent/agent.go`: Create SearchManager, pass to FSM, close on shutdown

**Done when:** Send email via SMTP, search by subject returns the message. Search by sender works.
Empty query returns nothing. Non-existent user returns empty results.

### 7. Global Address List (GAL) Bleve index

Create a single global Bleve index at `{dataDir}/search/gal` for autocomplete:
- Index user display names and email addresses from `/user/{id}/profile`
- `SearchManager.IndexProfile(profile UserProfile) error`
- `SearchManager.SearchGAL(prefix string, limit int) ([]GALEntry, error)`
  - Returns `{email, display_name}` for autocomplete

Wire into FSM:
- When a profile key is updated, re-index the GAL entry

**Done when:** Create two users, search GAL by partial name returns matches.

### 8. Wire SMTP + Search into agent lifecycle

Update `internal/agent/agent.go`:
- Add `Search *store.SearchManager` field
- Open SearchManager after MetadataManager
- Pass SearchManager to FSM
- Start SMTP server after Raft leader is ready (in `Run()`)
- Shutdown order: HTTP → SMTP → gossip → raft → search → metadata → pebble

Update dev mode:
- Auto-create a test user (`admin@localhost`) with password `admin` on first start
- Log the test credentials so the developer can send mail immediately
- Use ports 2525 (SMTP) and 2587 (submission) in dev mode to avoid requiring root

**Done when:** `bongo-go serve --dev` starts, logs test credentials, accepts email on port 2525,
stores in all three tiers, and is searchable.

### 9. Integration test: full mail flow

Create `test/integration/smtp_test.go` (build tag `integration`):

1. Start a dev-mode agent programmatically
2. Create a test user via the auth package
3. Connect to SMTP port, send an email with an attachment using `net/smtp`
4. Verify Pebble contains `/user/{id}/mail/{guid}` with correct protobuf
5. Verify blob store contains the raw message and attachment blobs
6. Verify per-user SQLite has the storeobject and maildocument rows
7. Verify Bleve search by subject and sender returns the message
8. Verify blob sync: if testing multi-node, ensure followers have the blobs

**Done when:** `go test -race -tags=integration ./test/integration/...` passes.

## Exit Criteria

- [ ] `PebbleStore.DeleteRange` uses proper successor key calculation with edge-case tests
- [ ] `filepath.HasPrefix` replaced with `strings.HasPrefix` in tar.go with path-traversal test
- [ ] `go test -race ./...` passes (all unit tests)
- [ ] `go test -race -tags=integration ./test/integration/...` passes
- [ ] Protobuf definitions exist for RaftCommand, MailMeta, BlobRef, UserProfile
- [ ] Raft commands encoded as protobuf (with JSON fallback for upgrade safety)
- [ ] SMTP server accepts mail on configurable port (2525 in dev mode)
- [ ] STARTTLS support (self-signed in dev mode)
- [ ] Mail delivery pipeline: parse → blob store → Pebble via Raft → SQLite index → Bleve index
- [ ] Authentication: bcrypt password from Pebble, SMTP AUTH PLAIN works
- [ ] `bongo-go user create` CLI command creates a user via Raft
- [ ] Blob pull-from-peers: followers fetch missing blobs via HTTP
- [ ] Per-user Bleve search returns mail by subject, sender, body text
- [ ] GAL autocomplete returns users by name/email prefix
- [ ] Dev mode auto-creates test user and logs credentials
- [ ] Full integration test: send email → verify all three storage tiers + search

## What NOT to do in this sprint

- No IMAP server (sprint 4+)
- No POP3 server (sprint 4+)
- No web UI
- No outbound SMTP relay (only inbound/local delivery)
- No DKIM signing or verification (later sprint)
- No antispam or antivirus hooks (later sprint)
- No Raft snapshots over the network (sprint 4)
- No TLS between cluster nodes (sprint 4)
- No mail filter rules
- No calendar events via CalDAV (later sprint)
