# Sprint 05: IMAP Server & Mailbox Access

**Phase:** 1 (Foundation)
**Goal:** Implement an IMAP4rev1 server so mail clients (Thunderbird, Apple Mail, etc.) can connect, list mailboxes, read messages, set flags, and search — all backed by the existing three-tier storage engine.

---

## Context

Sprints 01–04 built the storage engine, Raft consensus, SMTP inbound, search, TLS, and
CI pipeline. Mail arrives and is stored, but there is no way to read it. This sprint
adds IMAP via `emersion/go-imap` v2 so standard mail clients can connect immediately.

The config already defines `imap.port` (143) and `imap.port_tls` (993), with dev-mode
defaults of 1143 and 1993. The `internal/imap/` package directory exists but is empty.

## Cleanup from Sprint 04

### 0a. Remove unused `verifyHash` in blobsync.go

**Why:** Dead code flagged in sprint 03 verification, still present after sprint 04.

**Changes:**
- `internal/cluster/blobsync.go`: Delete the `verifyHash` function and any imports
  it was the sole consumer of.

**Done when:** `go test -race ./internal/cluster/...` passes; `verifyHash` is gone.

### 0b. Dev mode quick-start documentation

**Why:** Sprint 04 backlog called for dev mode quick-start docs but none were written.

**Changes:**
- Add a `## Quick Start (Dev Mode)` section to the top-level `README.md` (or create
  `docs/dev-quickstart.md` if the README is already long). Cover:
  - `go build ./cmd/bongo-go && ./bongo-go serve --dev`
  - Default ports (HTTP 8080, SMTP 2525/2587, IMAP 1143/1993)
  - Test user credentials (admin@localhost / admin)
  - How to send a test email with swaks
  - How to connect Thunderbird to the dev server

**Done when:** Documentation exists and is accurate for the current dev mode behavior.

### 0c. E2E test skeleton

**Why:** `test/e2e/` directory was created in sprint 04 but left empty.

**Changes:**
- `test/e2e/e2e_test.go` (build tag `e2e`):
  1. Start a dev-mode agent programmatically
  2. Send an email via `net/smtp` to port 2525
  3. Query the health endpoint, assert 200
  4. (After IMAP is done in this sprint, extend to verify IMAP fetch too)

**Done when:** `go test -tags=e2e ./test/e2e/...` passes.

---

## Tasks

### 1. Mailbox schema in Pebble

Define the key layout for IMAP mailboxes in Pebble. Each user gets a set of default
mailboxes (INBOX, Sent, Drafts, Trash, Junk) plus custom folders.

**Key format:**
- `/user/{userID}/mailbox/{mailboxName}/meta` → protobuf `MailboxMeta { string name, uint32 uid_validity, uint32 uid_next, uint64 messages, uint64 recent, uint64 unseen }`
- `/user/{userID}/mailbox/{mailboxName}/msg/{uid}` → protobuf `MailboxMessage { uint32 uid, string mail_guid, uint32 flags, int64 internal_date }`
- Existing mail key: `/user/{userID}/mail/{guid}` → `MailMeta` (unchanged)

**Changes:**
- `proto/bongo/v1/mailbox.proto`: Define `MailboxMeta` and `MailboxMessage` messages.
  Add a `Flags` enum: `SEEN = 1, ANSWERED = 2, FLAGGED = 4, DELETED = 8, DRAFT = 16`.
- `internal/cluster/fsm.go`: When a new mail is delivered (PUT to `/user/{id}/mail/{guid}`),
  also create a `/user/{id}/mailbox/INBOX/msg/{uid}` entry and bump `uid_next` in the
  mailbox meta. This happens inside the FSM Apply so it's Raft-consistent.
- `internal/smtp/delivery.go`: After Raft apply, the FSM handles mailbox placement — no
  changes needed here.

**Done when:** Delivering an email via SMTP creates both the `/mail/` and `/mailbox/INBOX/msg/`
entries. Unit test verifies the key layout.

### 2. Mailbox management operations

Implement CRUD operations on mailboxes that the IMAP backend will call.

**Create `internal/store/mailbox.go`:**
- `MailboxStore` struct (wraps PebbleStore + Raft apply)
- `ListMailboxes(userID string) ([]MailboxMeta, error)` — prefix scan `/user/{id}/mailbox/`
- `GetMailbox(userID, name string) (*MailboxMeta, error)`
- `CreateMailbox(userID, name string) error` — Raft command to create meta key
- `DeleteMailbox(userID, name string) error` — Raft command to delete meta + all msg keys
- `RenameMailbox(userID, oldName, newName string) error` — Raft command
- `EnsureDefaultMailboxes(userID string) error` — idempotently creates INBOX, Sent, Drafts, Trash, Junk

**Done when:** Unit tests for list, create, delete, rename. Default mailboxes created
on user creation.

### 3. Message flag operations

Implement flag get/set/clear operations for IMAP STORE command.

**Add to `internal/store/mailbox.go`:**
- `GetMessage(userID, mailbox string, uid uint32) (*MailboxMessage, error)`
- `SetFlags(userID, mailbox string, uid uint32, flags uint32) error` — Raft command
- `AddFlags(userID, mailbox string, uid uint32, flags uint32) error` — OR with existing
- `RemoveFlags(userID, mailbox string, uid uint32, flags uint32) error` — AND NOT
- `CopyMessage(userID, srcMailbox string, uid uint32, dstMailbox string) error` — Raft command
- `MoveMessage(userID, srcMailbox string, uid uint32, dstMailbox string) error` — copy + delete
- `Expunge(userID, mailbox string) ([]uint32, error)` — delete all messages with `\Deleted` flag

**Done when:** Unit tests for flag set/add/remove, copy, move, and expunge.

### 4. IMAP server core (emersion/go-imap v2)

Create the IMAP server using `emersion/go-imap` v2.

**Create `internal/imap/server.go`:**
- `Server` struct with `Start(addr, addrTLS string) error` and `Shutdown() error`
- Configure `imapserver.Server` with our backend
- STARTTLS on port 143 (1143 dev), implicit TLS on port 993 (1993 dev)
- Use the same `internal/security` TLS cert loading as SMTP

**Create `internal/imap/backend.go`:**
- Implement `imapserver.Backend`:
  - `Login(username, password string) (imapserver.Session, error)` — delegate to `auth.LocalAuth`
- Implement `imapserver.Session`:
  - `Mailboxes() ([]imap.MailboxInfo, error)` — delegate to `MailboxStore.ListMailboxes`
  - `Select(name string) (*imap.SelectedMailbox, error)` — load mailbox meta, compute sequence numbers
  - `Fetch(seqSet, items)` — resolve UIDs, load MailMeta + blob content from BlobStore
  - `Store(seqSet, flags, action)` — delegate to flag operations
  - `Copy(seqSet, dest)` — delegate to CopyMessage
  - `Move(seqSet, dest)` — delegate to MoveMessage (IMAP MOVE extension)
  - `Expunge()` — delegate to Expunge
  - `Search(criteria)` — delegate to Bleve search, map results to sequence numbers
  - `Create/Delete/Rename(mailbox)` — delegate to MailboxStore
  - `List(ref, pattern)` — standard mailbox listing with pattern matching
  - `Status(mailbox, items)` — return counts from mailbox meta
  - `Close()` — auto-expunge if configured, cleanup

**Done when:** IMAP server starts, accepts connection, LOGIN succeeds with valid credentials,
LIST returns mailboxes, SELECT INBOX works.

### 5. IMAP FETCH implementation

The most complex IMAP command. Needs to reconstruct RFC 5322 messages from blobs.

**Add to `internal/imap/backend.go` (or split into `fetch.go`):**
- Resolve sequence numbers ↔ UIDs via the mailbox message index
- For each requested message:
  1. Look up `/user/{id}/mailbox/{name}/msg/{uid}` → get `mail_guid`
  2. Look up `/user/{id}/mail/{guid}` → get `MailMeta` with BlobRefs
  3. For `BODY[]` / `RFC822`: read raw message blob from BlobStore
  4. For `BODY[HEADER]`: parse headers from blob (cache parsed headers in memory)
  5. For `BODY[TEXT]`: return body after headers
  6. For `BODY[n]` / `BODY[n.MIME]`: parse MIME structure, return specific part
  7. For `ENVELOPE`: extract structured envelope from headers
  8. For `FLAGS`: return from MailboxMessage
  9. For `INTERNALDATE`: return from MailboxMessage
  10. For `BODYSTRUCTURE`: parse MIME, return structure tree
- Mark `\Seen` flag when `BODY[]` is fetched (unless `.PEEK`)

**Done when:** Thunderbird or `imaptest` can FETCH messages: headers, body, specific
MIME parts. Peek vs non-peek flag behavior works.

### 6. IMAP SEARCH backed by Bleve

Map IMAP SEARCH criteria to Bleve queries.

**Create `internal/imap/search.go`:**
- `mapSearchCriteria(criteria *imap.SearchCriteria, userID string) (bleve.SearchRequest, error)`
- Support these IMAP search keys mapped to Bleve:
  - `SUBJECT "text"` → Bleve query on `subject` field
  - `FROM "text"` → Bleve query on `from` field
  - `TO "text"` → Bleve query on `to` field
  - `BODY "text"` → Bleve query on `body` field
  - `TEXT "text"` → Bleve query on all text fields (subject + from + to + body)
  - `SINCE date` / `BEFORE date` → Bleve date range on `date` field
  - `SEEN` / `UNSEEN` / `FLAGGED` / `ANSWERED` / `DELETED` → flag-based filter
    (post-filter against Pebble flags since Bleve doesn't index flags)
  - `OR`, `NOT`, `ALL` → compose Bleve boolean queries
- Return UIDs, let the session convert to sequence numbers

**Done when:** IMAP `SEARCH SUBJECT "hello"` returns matching UIDs. `SEARCH UNSEEN`
returns unflagged messages. Combined criteria (`SEARCH FROM "alice" SINCE 1-Jan-2026`) works.

### 7. IMAP IDLE (push notifications)

Implement IMAP IDLE so clients get real-time updates when new mail arrives.

**Create `internal/imap/notify.go`:**
- `Notifier` struct — fan-out notification hub
- `Subscribe(userID, mailbox string) <-chan Notification`
- `Unsubscribe(ch <-chan Notification)`
- `Notify(userID, mailbox string, event Notification)`
- Notification types: `NewMail { uid, exists_count }`, `FlagChange { uid, flags }`, `Expunge { uid }`

**Wire into FSM:**
- `internal/cluster/fsm.go`: After applying a mailbox message put (new mail delivery),
  call `Notifier.Notify()` with `NewMail` event.

**Wire into IMAP session:**
- When client issues IDLE, subscribe to the selected mailbox's notification channel
- Send `* n EXISTS` when new mail arrives
- Send `* n EXPUNGE` when messages are expunged

**Done when:** Thunderbird in IDLE mode sees new mail within seconds of SMTP delivery
(no manual refresh needed).

### 8. Wire IMAP into agent lifecycle

**Changes to `internal/agent/agent.go`:**
- Add `IMAP *bongoimap.Server` field
- Add `Notifier *bongoimap.Notifier` field
- Create Notifier, pass to FSM and IMAP backend
- Create MailboxStore, pass to IMAP backend
- Start IMAP server after Raft is ready (same pattern as SMTP)
- Dev mode ports: 1143 (IMAP) and 1993 (IMAPS)
- EnsureDefaultMailboxes for the dev test user
- Shutdown order: HTTP → SMTP → **IMAP** → blob sync → gossip → raft → search → metadata → pebble

**Done when:** `bongo-go serve --dev` starts IMAP on 1143/1993. `openssl s_client` or
`curl imaps://localhost:1993` connects with TLS. Login with admin@localhost works.

### 9. Integration test: SMTP → IMAP round-trip

**Create `test/integration/imap_test.go` (build tag `integration`):**

1. Start a dev-mode agent programmatically
2. Create a test user via auth package
3. Send an email with attachment via `net/smtp` to port 2525
4. Connect to IMAP on port 1143 using `emersion/go-imap` client
5. LOGIN with test credentials
6. LIST → verify INBOX, Sent, Drafts, Trash, Junk
7. SELECT INBOX → verify EXISTS count = 1
8. FETCH 1 (ENVELOPE FLAGS BODY[]) → verify subject, sender, body content
9. STORE 1 +FLAGS (\Seen) → verify flag is set
10. SEARCH SUBJECT "test" → verify returns UID
11. CREATE "Archive" → verify LIST includes it
12. COPY 1 "Archive" → verify message in both mailboxes
13. STORE 1 +FLAGS (\Deleted) in INBOX → EXPUNGE → verify removed
14. Extend E2E test in `test/e2e/` to also verify IMAP fetch after SMTP delivery

**Done when:** `go test -race -tags=integration ./test/integration/...` passes with
the IMAP round-trip test.

---

## Exit Criteria

- [ ] `verifyHash` dead code removed from blobsync.go
- [ ] Dev mode quick-start documentation exists
- [ ] E2E test skeleton in `test/e2e/` passes
- [ ] `go test -race ./...` passes (all unit tests)
- [ ] `go test -race -tags=integration ./test/integration/...` passes
- [ ] Mailbox schema in Pebble: `/user/{id}/mailbox/{name}/meta` and `/msg/{uid}` keys
- [ ] Default mailboxes (INBOX, Sent, Drafts, Trash, Junk) created on user creation
- [ ] IMAP server accepts connections on configurable ports (1143/1993 in dev mode)
- [ ] IMAP LOGIN authenticates against Pebble credentials
- [ ] LIST returns all user mailboxes with correct hierarchy
- [ ] SELECT returns mailbox status (EXISTS, RECENT, UNSEEN, UIDNEXT, UIDVALIDITY)
- [ ] FETCH retrieves headers, body, specific MIME parts, envelope, flags
- [ ] FETCH without .PEEK sets \Seen flag automatically
- [ ] STORE sets/adds/removes flags correctly
- [ ] COPY and MOVE work across mailboxes
- [ ] EXPUNGE removes \Deleted messages
- [ ] SEARCH maps to Bleve queries (subject, from, to, body, date range, flags)
- [ ] IDLE pushes EXISTS/EXPUNGE notifications in real time
- [ ] Integration test: full SMTP→IMAP round-trip with flags, search, copy, expunge
- [ ] Thunderbird or Apple Mail can connect, browse INBOX, and read messages

## What NOT to do in this sprint

- No outbound SMTP relay (only inbound/local delivery)
- No IMAP APPEND (client-side upload — needed for Sent folder, later sprint)
- No IMAP SORT or THREAD extensions (later sprint)
- No IMAP COMPRESS (later sprint)
- No IMAP QUOTA extension (later sprint)
- No POP3 server (separate sprint)
- No web UI (separate sprint)
- No CalDAV or CardDAV (later phase)
- No DKIM signing or verification
- No antispam or antivirus hooks
- No mail filter/sieve rules
