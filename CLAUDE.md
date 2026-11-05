# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Bongo: a mail and calendar server (IMAP, SMTP, POP3, CalDAV/CardDAV) with an HTMX web UI,
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
make build            # generate + go build bin/bongo + bin/bongo-import
make generate         # templ generate (required before build)
make test             # go test ./... (unit only; integration needs make test-integration)
make test-all         # fmt-check + test-race + test-integration
make test-race        # go test -race ./...
make test-cover       # go test -race -cover ./...
make lint             # golangci-lint run
make sec              # govulncheck + gosec (medium and above)
make run              # build + ./bin/bongo serve --dev
make dev              # build + run dev server, validate, seed test data, wait (preferred for dev)
make seed             # seed test data into a running dev server
make ui-shots OUT=dir # screenshot every route (light, dark, mobile); not in CI
make dist             # linux amd64/arm64 release zips under dist/
make clean            # rm -rf bin/

# Run a single test
go test -race -run TestFunctionName ./internal/store/...

# Run tests for a single package
go test -race ./internal/cluster/...
```

Two binaries: `bongo` (the server) and `bongo-import` (mail import from mbox/maildir/eml/IMAP/Thunderbird).

## Architecture Overview

### Three-Tier Storage

- **Pebble** = metadata source of truth (small protobufs with blob references). Replicated via Raft.
- **Blob store** = content-addressable files on disk (SHA-256 hash = filename). Shared flat directory, NOT per-user. Synced between nodes via HTTP pull (not Raft log). Deduplicated automatically.
- **SQLite + Bleve** = derived query indexes. Per-user databases. Rebuildable from Pebble + blobs.

Backup = export Pebble keys + copy referenced blobs. Restore = write blobs, replay keys, rebuild indexes.

### Server Startup (internal/agent/agent.go)

The `Agent` struct is the central orchestrator. Config is loaded and validated in
`cmd/bongo` (viper: CLI flags > the few `BONGO_*` env vars > HCL file > defaults) before
`NewAgent` runs. Startup order:
1. `NewAgent`: storage (Pebble, BlobStore, MetadataManager per-user SQLite, SearchManager
   per-user Bleve), then Raft for `server` and `dev` (dev: in-memory transport and snapshots,
   Pebble-backed log in a temp dir). The `agent` role returns before storage.
2. `Run`: HTTP router built, wait for a Raft leader, gossip, FSM indexer, blob sync, then SMTP,
   queue worker, reminders, IMAP, POP3 (protocols only when Raft is running), dev admin user,
   orphan GC and usage recompute, control listener, and finally the public web server.

Graceful shutdown: HTTP > SMTP > IMAP > POP3 > workers > gossip > Raft > indexer > search >
metadata > Pebble.

### Request Flow (HTTP)

```
Request → chi router → SecurityHeaders → InjectSourceURL → Compress →
  RequireAuth (HMAC-SHA256 cookie) → Require2FA → InjectLocale → RequireSameOrigin →
  InjectCSRF → RequireCSRF → Handler → Store (Pebble/Blobs) → Templ template → Response
```

RequireCSRF does not enforce in the dev role. Admin routes add `RequireAdminOrDomainAdmin`;
the quarantine pages use `RequireAdmin` (global admins only). CalDAV/CardDAV use HTTP Basic
Auth with a lockout limiter instead of cookies.

### Key Architectural Patterns

**RaftApplier interface**: All writes go through Raft consensus. Handlers never write to Pebble directly — they call `ApplyCommand()` which serializes a `RaftCommand` protobuf (PUT/DELETE) through the Raft log. The FSM applies committed entries to Pebble. Multi-key operations travel as composite commands under synthetic key prefixes (`/__mailbox_{append,copy,delete,deliver,expunge,move,rename,subscribe}__/`, `/__queue_move__/`, `/__bayes_train__/`, `/__acl__/`, `/__apppassword_touch__/`) that the FSM expands.

**Handler dependency injection via setters**: Handlers receive stores via `SetXXX()` methods, enabling nil-check graceful degradation on edge nodes:
```go
func (h *MailHandler) SetApplier(applier store.RaftApplier) { h.applier = applier }
func (h *MailHandler) SetThreadStore(ts *store.ThreadStore) { h.threads = ts }
```

**FSM side effects**: On Apply(), the FSM enqueues search indexing (Bleve) for a single async worker (`FSM.StartIndexer`; inline only when no worker is started), blob sync enqueueing, and IMAP notifications for new mail.

**Templ + HTMX + Alpine.js**: Templates compile to Go functions (`templ generate` required). HTMX handles interactivity, Alpine.js for reactive data binding. No JS build step.

### Server Roles

- `server` — Raft voter, storage, all protocols. 3 or 5 form a quorum.
- `edge` — Intended stateless proxy. Incomplete: it opens local stores but starts no Raft and therefore no SMTP/IMAP/POP3 listeners, and no forwarding exists.
- `agent` — Intended remote processor (smtp, antispam, antivirus; imap/pop3/caldav log "not yet implemented"). Incomplete: `grpc.NewAgentServer` is only constructed in tests, so no server accepts agent registration.
- `dev` — All-in-one, single node, ephemeral storage. Zero config.

## Pebble Key Space

```
/user/{user_id}/profile              → UserProfile protobuf (kind "resource" = bookable room or equipment: no login, answers REQUESTs itself)
/user/{user_id}/mail/{guid}          → MailMeta protobuf + BlobRef
/user/{user_id}/event/{guid}         → Event protobuf (ical_raw inline, NOT a blob)
/user/{user_id}/contact/{guid}       → Contact protobuf (vcard_raw inline, NOT a blob)
/user/{user_id}/task/{guid}          → Task protobuf (VTODO: summary, due, start, completed, status, priority, percent; ical_raw inline, NOT a blob)
/user/{user_id}/calendar/{guid}      → calendar metadata (also the publish state: published, publish_token = the feed capability, publish_detail, publish_password_hash; the token is in backups and is never logged); a subscription calendar (external ICS feed) holds `Calendar.subscription`: source_url (a secret: logs and the UI show its host only) and refresh state. Read-only for everyone. `Calendar.components` (VEVENT, VTODO or both; empty means VEVENT) says what the collection holds; a new user gets "default" (events), and any user with no VTODO calendar gets "tasks" (`EnsureDefault`, outside the 5-calendar cap)
/user/{user_id}/mailbox/{name}/meta  → mailbox metadata (uid_next, uid_validity)
/user/{user_id}/mailbox/{name}/msg/{uid} → MailboxMessage (flags, tags, mail guid)
/user/{user_id}/preferences          → user preferences
/user/{user_id}/rule/{guid}          → mail filter rule (FilterRule: 1-10 `actions`; field `action` holds the first, for older nodes)
/user/{user_id}/tag/{name}           → TagMeta protobuf (colour, sort_order)
/user/{user_id}/addrverify/{address} → send-as verification challenge
/user/{user_id}/sendlog/{send_id}/{step} → web compose send step done (40-byte record, not a protobuf)
/user/{user_id}/schedinbox/{20-digit unix nanos}-{id} → CalDAV schedule inbox item (sanitised iTIP REQUEST/REPLY/CANCEL as iCalendar text, not a protobuf; oldest first, at most 500 per user, id derived from the delivery so a retry places it once)
/user/{user_id}/bayes/{category}/{token} → Bayes token counts (uvarint)
/user/{user_id}/bayes/trained        → Bayes trained-message count (uvarint)
/user/{user_id}/bayes/msg/{guid}     → category a message was last trained as (spam|ham)
/system/domain/{domain}              → domain config
/system/domain_admin/{domain}/{user_id} → domain-admin grant marker
/system/alias/{alias}                → alias → target user
/system/catchall/{domain}            → per-domain catch-all target
/system/domain/{domain}/dkim         → per-domain DKIM key (generated in the admin UI)
/system/domain/{domain}/quota        → per-domain quota; /usage holds the recomputed usage
/system/domain/{domain}/owner        → domain owner user ID
/system/domain/{domain}/alias_of     → target domain (alias domain; also a hosted domain, written before its own key)
/system/address/{address}            → address → owning user (primary and send-as)
/system/settings/require_2fa         → global 2FA policy flag
/system/raft/applied_index           → last applied Raft index (replay skips up to it)
/system/migration/fwdtargets         → forwarding-target migration done marker
/system/migrations/rules-first-match-v1 → rules first-match migration done marker (sets stop_processing on pre-v0.14 rules)
/system/migrations/vtodo-blank-events-v1 → one-time log of events a VTODO PUT left behind is done (nothing deleted)
/system/migrations/calendar-grants-v1 → calendar-grants migration done marker (shared_with turned into grants; written last)
/acl/{kind}/{owner_id}/{resource_id}/u:{grantee_id} → Grant protobuf (level, rights, state, granted_by); kind is `calendar`; every ID segment is url.PathEscape'd
/aclrev/u:{grantee_id}/{kind}/{owner_id}/{resource_id} → empty reverse marker; only ever written and deleted together with its forward key
/user/{user_id}/fwdtarget/{address}  → verified forwarding target
/user/{user_id}/fwdverify/{address}  → pending forwarding verification
/user/{user_id}/apppassword/{id}     → AppPassword protobuf: per-device protocol credential (label, created_at, last_used_at as 00:00 UTC of the last day used, bcrypt hash of the 18-letter secret). The id is the public 6-letter lookup group of the 24-letter password, so an attempt compares one hash. Accepted by IMAP, POP3, SMTP submission and CalDAV/CardDAV only (`LocalAuth.AuthenticateProtocol`); web login and re-authentication never see it. Removed with the rest of /user/{id}/ on user deletion
/user/{user_id}/vacation/sent/{hex(sha256(lower(sender)))} → 8-byte BE unix seconds of the last vacation reply to that sender (not a protobuf); leader sweep after 30 days; deleted on disable
/user/{user_id}/remindersent/{pathescape(event_guid)}/{occurrence_unix} → 8-byte BE unix seconds when sent (not a protobuf); written before the email reminder is enqueued (delivery is at-most-once); leader sweep after 2 days
/queue/pending/{unix_ts}-{id}        → queued outbound message (zero-padded ts)
/queue/bounced/{id}                  → bounced outbound message

# Blob store (content-addressable, separate from Pebble)
/data/blobs/{first-2-hex}/{sha256-hash} → raw file content
```

Send log: `step` is `local/{hex16(sha256(rcpt))}`, `external` or `sent`. The value is 8 bytes of
big-endian unix seconds plus the 32-byte content fingerprint of the submission; `send_id` is the
128-bit hex ID the compose form carries. It makes a retried web send idempotent (object IDs are
derived from the send ID, finished steps are skipped, different content under a used ID is a 409).
The leader's orphan GC deletes entries older than `store.SendLogTTL` (7 days) via `sweepSendLogs`;
deleting a user covers the rest. Delivery is at-least-once: a crash between an object write and
its marker repeats that one write, and two nodes handling one send ID at once are not serialised.

Only MailMeta and the outbound queue reference blobs. An earlier revision of this table said
events and contacts carried BlobRefs; they do not. `Event.ical_raw`, `Contact.vcard_raw` and
`Task.ical_raw` are inline `bytes` in the protobuf. This is not a cosmetic correction: a maintainer who believes
events and contacts pin blobs will either add pointless scanning to the GC or, worse, assume
coverage that was never needed and reason wrongly about what the reference set must contain.
The authoritative list is `knownBlobBearingFields` in `internal/agent/blobref_enumeration_test.go`,
which is checked against the proto descriptors at test time and fails if a new blob-bearing field
appears without being classified. `TestInlineRecordsNameNoBlob` in the same file asserts Task,
Event, Contact and Calendar have no such field.

Blob lifecycle: background GC does mark-and-sweep, deletes unreferenced blobs older than 1 hour.
The GC is the ONLY component that deletes blobs. They are content-addressed and shared across
users, so a byte-identical message delivered to several people is one blob — deleting it from any
single owner's code path destroys everyone's copy. The reference set spans Pebble mail, the
outbound queue, and the on-disk spool.

Calendar sharing is the grant key space (`/acl/...` and `/aclrev/...`, `internal/store/aclcommand.go`).
A grant has a level (`freebusy`, `read`, `write`; anything else is refused) and a state (`pending`,
`accepted`); only an accepted grant authorises, and a grant on a deleted calendar authorises nothing.
The forward key and its reverse marker are written and removed only by the composite `/__acl__/`
command (`set`, `accept`, `revoke`, `revoke_resource`, `revoke_owner`, `revoke_grantee`), applied in one Pebble
batch, so they never disagree. A web share creates the grant `pending` and mails the grantee a link to
`/calendar/shares`; `accept` is conditional inside the FSM (it never recreates a revoked grant) and a
`set` with `keep_state` leaves an existing grant's state alone. Grants made before the invitation flow are
`accepted`. `accept` and `keep_state` are unknown to a node on an earlier v0.15 batch binary: upgrade every
node before anyone shares. "What have I shared" is a prefix scan of `/acl/{kind}/{owner}/`; "what is
shared with me" is a prefix scan of `/aclrev/u:{me}/{kind}/`. Every prefix ends in `/`, so owner `ab`
never matches `abc`. `Calendar.shared_with` is legacy: the `calendar-grants-v1` leader migration turns
each entry into an accepted `freebusy` grant and clears the field, and nothing else reads it.
Deleting a calendar or a user revokes their grants in the same request, and a leader sweep removes
grants whose owner, calendar or grantee is gone (two consecutive sightings, `ErrNotFound` only).
Domain-admin delegation (`/system/domain_admin/...`) and send-as grants (`AddressRecord.CanSend`/`Verified`)
stay separate mechanisms.

Calendar subscriptions add no key. A subscribed calendar is an ordinary `/user/{id}/calendar/{guid}` record
with `Calendar.subscription` set; its events are ordinary event keys under a guid derived from
(calendar, feed UID), `sub-` plus 32 hex, never the UID. The calendar is read-only for everyone, owner and
write grantees included: `calendarAccess` (web) and `resolve` / `inSubscription` (CalDAV) check the event's
STORED calendar. A leader worker (`internal/agent/calsubscriptions.go`) refreshes it; the feed is parsed
whole before any write, and the writes are convergent, not atomic (see that file's comment). Fetching goes
through `internal/netx`, whose dialer refuses non-public addresses on every connection.

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
| Frontend | HTMX + Alpine.js (CSP build) + Sortable.js, all vendored locally; first-party rich editor for the composer (`richeditor.js`, contenteditable); Geist fonts |
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

- Header views (`/mail/view/to-me`, `/mail/view/from-contacts`, `/mail/view/lists`, `/mail/view/lists/{id}`, registered before `/mail/{mailbox}` and identified internally by a view argument, never a folder name, so a folder called `to-me` or `lists` stays reachable) are filters over node-local SQLite columns (`to_me`, `sender_addr`, `list_id`, `list_name` on `maildocument`), not folders and not in `store.VirtualMailboxes`. No Raft command or Pebble key backs them. `to_me IS NULL` marks a row the `HeaderBackfill` worker has not derived yet; `IndexMail` must never reset these columns. `to_me` is judged against the user's addresses when the row is indexed (`users`, `addresses`, `aliases`; a rebuild also reads `/system/address/`), so a later alias does not retro-fit old rows. "From my contacts" reads the contact book per request. Rows carry `data-real-mailbox` and `data-sel-key` (`uid:mailbox`); every action URL for these rows uses the real mailbox, and the view bulk routes (`/mail/view/{view}/bulk`, `BulkToMe`, `BulkFromContacts`, `BulkMailingList`) take `targets=uid:mailbox` pairs (a bare UID is refused, since UIDs are only unique per mailbox).

### UI Patterns
- Alpine.js `fetch()` for JSON API endpoints; HTMX for HTML fragment swaps. Don't use HTMX for JSON-returning endpoints.
- Chi route ordering: static segments (`/mail/tag/{name}`) must be registered before wildcard segments (`/mail/{mailbox}`).

## Repo-Specific Guardrails

- **Grants are one composite Raft command (`/__acl__/`, `internal/store/aclcommand.go`).** An older-binary node applies it as a literal key and holds no grants, so replicas diverge for as long as the cluster runs mixed versions. Upgrade every node before anyone shares a calendar, and before the leader runs the `calendar-grants-v1` migration (it writes `/__acl__/` commands too). Production runs one node. The accept op also refuses a pending grant older than `store.PendingGrantTTL` (30 days, judged from the stored `updated_at` and the command's `at`, never the clock), so a node on an earlier binary would accept what an upgraded one refuses; upgrade every node together. Expired pending grants are never deleted and sit outside the 50-grant cap. Never write `/acl/` or `/aclrev/` keys any other way, and give every test applier that can see a grant an `IsACLSyntheticCommand` dispatch, or the command is stored as a literal key and negative tests pass vacuously.

- **Bayes training is one composite Raft command** (`/__bayes_train__/{user}`, `internal/store/bayescommand.go`) counted inside the FSM, capped at 1024 tokens. An older-binary node applies it as a literal key and counts nothing, so replicas diverge for as long as the cluster runs mixed versions. Upgrade every node before anyone trains; counts trained during a mixed window are not recoverable by replay.

- **App password last-used is a conditional composite command** (`/__apppassword_touch__/`, `internal/store/apppasswordcommand.go`), never a Put of the whole record: a read-modify-write Put from the login path could land after a revoke's Delete and bring a revoked credential back with a working hash. The FSM does nothing when the record is absent or already at that day. Create and revoke stay plain Put and Delete. An older-binary node stores the touch as a literal key and its last_used_at lags; authentication is unaffected, but upgrade every node together. Credentials are verified only through `LocalAuth.AuthenticateProtocol`, which costs exactly one bcrypt comparison on every branch and holds the per-account switch (`UserProfile.require_app_passwords`, enforced only with `totp_enabled`, and part of `ProtocolEpoch`, so turning it on ends sessions opened with the account password); `Authenticate` and `AuthenticateProfile` stay account-password-only. Give any test applier that sees a real protocol login an `IsAppPasswordSyntheticCommand` dispatch, or the touch is stored literally and negative tests pass vacuously.

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
