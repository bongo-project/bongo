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
make loadtest-bundle VERSION=vX.Y.Z  # load test bundle for two VMs under dist/ (docs/load-testing.md)
make clean            # rm -rf bin/

# Run a single test
go test -race -run TestFunctionName ./internal/store/...

# Run tests for a single package
go test -race ./internal/cluster/...
```

Two binaries: `bongo` (the server) and `bongo-import` (mail import from mbox/maildir/eml/IMAP/Thunderbird).

## Release Checklist

1. In the release PR, bump the image in `deploy/docker-compose.yml` and
   `TestComposeImageTag` in `internal/depfloor/depfloor_test.go` to the new version,
   together.
2. Merge the PR and wait for `main` CI to go green.
3. Tag with an annotated tag once the operator approves. The tag message becomes the
   release notes. The push runs "Release binaries" and "Publish image".
4. Take the image digest from the "exporting manifest list sha256" line of the
   publish log, and hand the digest, notes and checklist to the InfraWork session.
   Production deploys need the operator's go there.
5. Sync the website (`scripts/sync_docs.py`, `scripts/sync_releases.py`,
   `latest_release` in `_config.yml`). Publishing it needs the operator's go.

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
   Pebble-backed log in a temp dir). The `agent` role returns before storage. Before raft
   starts, `NewRaftNode` skips raft's snapshot restore unless Pebble is behind the newest
   snapshot, and rebuilds SQLite and Bleve from Pebble unless the last run left the
   clean-shutdown marker `{data_dir}/derived-indexes.clean` at the current applied index
   (`internal/cluster/startup.go`). The start removes the marker; `Shutdown` writes it after
   the index worker drains and the index stores close.
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

**RaftApplier interface**: All writes go through Raft consensus. Handlers never write to Pebble directly — they call `ApplyCommand()` which serializes a `RaftCommand` protobuf (PUT/DELETE) through the Raft log. The FSM applies committed entries to Pebble. Multi-key operations travel as composite commands under synthetic key prefixes (`/__mailbox_{append,assignid,copy,delete,deliver,expunge,move,rename,subscribe,xfer}__/`, `/__queue_move__/`, `/__bayes_train__/`, `/__acl__/`, `/__apppassword_touch__/`, `/__apppassword_ticket__/`, `/__user_usage__/`, `/__group__/`) that the FSM expands.

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
/user/{user_id}/profile              → UserProfile protobuf (kind "resource" = bookable room or equipment: no login, answers REQUESTs itself; kind "shared" = shared inbox: no login, `shared_members` lists its members)
/user/{user_id}/mail/{guid}          → MailMeta protobuf + BlobRef
/user/{user_id}/event/{guid}         → Event protobuf (ical_raw inline, NOT a blob)
/user/{user_id}/contact/{guid}       → Contact protobuf (vcard_raw inline, NOT a blob); `addressbook_id` (field 12) names the book it is filed in, empty means the default book; `kind` (field 10, `group` or empty) and `members` (field 11, repeated canonical member references, at most `store.MaxContactGroupMembers` = 500 distinct) make it a **contact group** (always say "contact group": "group" alone means a distribution group). `vcard_raw` stays the source of truth: `ContactStore.Create`/`Update` derive both fields from it on every write (`deriveContactGroup`), so CardDAV, web, import and quick-add agree, and a card over the cap is refused with `ErrContactGroupTooLarge` before anything is written (CardDAV answers 403). A card is a group when `KIND` or `X-ADDRESSBOOKSERVER-KIND` is `group` (any VERSION); members are the union of `MEMBER` and `X-ADDRESSBOOKSERVER-MEMBER`, each canonicalised by `ContactMemberRef` (a member card's UID: `urn:uuid:` dropped, a UUID or 32 hex becomes lower-case 32 hex, `mailto:` kept as `mailto:{bare address}`). Cards are written back in the form they arrived in; web-made groups use Apple's form, VERSION 3.0. Readers go through `ContactGroupInfo`, which falls back to `vcard_raw` for a record with no `kind` (a group stored before the fields, or written by an older node's CardDAV PUT, which drops fields 10 and 11). Members are resolved at read time, never stored as GUIDs: `ResolveContactGroup` is one prefix scan of the owner's contacts filtered to the group's own book, mapping `ContactIdentity` (the UID property, else the GUID) to the card. A reference to another book, a deleted card or a card not synced yet is dropped silently; a nested group or the group itself is counted and skipped (no nesting). Web-created cards get UID = GUID formatted as a UUID; the web writes a UID into a member card that has none before adding it. No new Raft command or key: plain Put and Delete, so the web serialises a group's read-modify-write with a per-node `keyedMutex` only (a concurrent edit on another node can lose an add; CardDAV has If-Match). Compose expands a group in the browser to ordinary addresses (`GET /api/contact-groups/{guid}/addresses`, owner-scoped), so every send-time guard (100-recipient cap, budgets, distribution-group posting policy) applies unchanged. **"This is me"** is `UserPreferences.self_contact_guid` (field 22), a GUID in the owner's own keyspace (a plain PUT of the preferences record, no new command or key; an older node keeps the unknown field on a Settings save and ignores `attach_self`). `ContactStore.SelfContact` is the only reader and treats an empty, dangling or contact-group value as unset (the lazy guard); `SetSelfContact` refuses a missing card (`ErrContactNotFound`) and a group (`ErrSelfContactGroup`), `ClearSelfContact` clears only when the stored value equals the GUID, so a stale tab cannot unmark another card. `ContactStore.Delete` clears the mark eagerly through `clearSelfIfMatch` (one preferences read, a write only on a match, never creates a record; a failure is logged and swallowed), which covers web, CardDAV (a write grantee clears the OWNER's mark), `PurgeAddressBook` and import compensation: the lazy guard alone would let a card PUT at the same CardDAV href inherit the mark. A card in an own book shared with write access can be marked (a grantee can then change what gets attached). **Attach my card** (compose `attach_self=1`) builds the attachment from `contactio.ShareCard`, an allowlist (FN, N, NICKNAME, ORG, TITLE, ROLE, EMAIL, TEL, ADR, URL, BDAY, with their groups and only the parameters TYPE, PREF, VALUE and CHARSET, so no GEO, LABEL or X- parameter goes out; the stored VERSION is kept; no NOTE, UID, PHOTO or any X-*) and `contactio.CardFilename`; the bytes sit in `attachmentFiles`, so `sendFingerprint` covers them (an edited card or a toggled box on a retried send ID is a 409). A ticked box with no resolvable card is a 400 before any write; a draft saves without it instead. A draft that carries the card has the drafts-only `X-Bongo-Attach-Self: 1` header so EditDraft ticks the box again; Send never adds it. Sending as a shared inbox still attaches the user's own card.
/user/{user_id}/addressbook/{guid}   → AddressBook protobuf (id, name, description, created_at, updated_at; guid is 32 lowercase hex, the shape address-book grants require). The default book is implicit: it has no key, its CardDAV segment is `default`, and a contact with an empty `addressbook_id` belongs to it. At most `store.MaxAddressBooksPerUser` (20) books besides the default, counted then written per node (not atomic across nodes). Plain Put and Delete of one key, no composite command. Deleted by `store.PurgeAddressBook`: contacts first, the book key last, then the grants, so a retry after a failure finds the book and finishes (see below)
/user/{user_id}/task/{guid}          → Task protobuf (VTODO: summary, due, start, completed, status, priority, percent; ical_raw inline, NOT a blob)
/user/{user_id}/calendar/{guid}      → calendar metadata (also the publish state: published, publish_token = the feed capability, publish_detail, publish_password_hash; the token is in backups and is never logged); a subscription calendar (external ICS feed) holds `Calendar.subscription`: source_url (a secret: logs and the UI show its host only) and refresh state. Read-only for everyone. `Calendar.components` (VEVENT, VTODO or both; empty means VEVENT) says what the collection holds; a new user gets "default" (events), and any user with no VTODO calendar gets "tasks" (`EnsureDefault`, outside the 5-calendar cap)
/user/{user_id}/mailbox/{name}/meta  → mailbox metadata (uid_next, uid_validity, unsubscribed, `id`: stable 32-hex handle, empty until assigned)
/user/{user_id}/mailboxid/{id}       → mailbox name (raw bytes, not a protobuf); id is 32 lowercase hex. Written only by the assign command (`/__mailbox_assignid__/`) and moved or removed by rename and delete in the same Pebble batch. Resolved by point read plus a check that the mailbox meta still carries the id (`MailboxStore.MailboxNameByID`), never by scanning mailboxes
/user/{user_id}/mailbox/{name}/msg/{uid} → MailboxMessage (flags, tags, mail guid)
/user/{user_id}/preferences          → user preferences (field 22 `self_contact_guid`: see the contact key)
/user/{user_id}/sharedunsub/{owner_id}/{mailbox_id} → empty value; presence means the user unsubscribed (IMAP UNSUBSCRIBE) from a shared folder. A shared folder with an accepted `l` grant (direct, group or shared-inbox member) is subscribed by default and lists in LSUB and LIST (SUBSCRIBED) with no SUBSCRIBE; SUBSCRIBE deletes the marker. Plain Put and Delete, no synthetic command. Keyed by the folder id so a rename or rights change keeps it; it authorises nothing (LIST/LSUB take visibility from live grants, UNSUBSCRIBE of an unseen folder answers OK and writes nothing). Goes with the rest of /user/{id}/ on user deletion. The old `/user/{id}/sharedsub/` keys are no longer read; a leader pass deletes them
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
/system/address/{address}            → address → owning user (primary and send-as); `AddressRecord.kind` "group" has an empty user_id and names its group in `group_id` (field 6), and is never a user: the resolver answers it with `GroupRecipientError` (matches `ErrRecipientUnavailable` and `ErrRecipientGroup`), `CanSendAs` refuses it, address-free checks count it as taken
/system/group/{group_id}             → Group protobuf (distribution group: address, display_name, domain, member_ids, owner_ids, posting_policy anyone|domain|members|owners, timestamps). Written only by `/__group__/`; at most 1000 members, local users only, no group as a member or owner
/system/groupmember/{user_id}/{group_id} → empty marker, the member index (owners are not indexed); prefix `/system/groupmember/{user_id}/` ends in `/`
/system/settings/require_2fa         → global 2FA policy flag
/system/raft/applied_index           → last applied Raft index (replay skips up to it)
/system/migration/fwdtargets         → forwarding-target migration done marker
/system/migrations/rules-first-match-v1 → rules first-match migration done marker (sets stop_processing on pre-v0.14 rules)
/system/migrations/vtodo-blank-events-v1 → one-time log of events a VTODO PUT left behind is done (nothing deleted)
/system/migrations/calendar-grants-v1 → calendar-grants migration done marker (shared_with turned into grants; written last)
/system/migrations/sharedsub-cleanup-v1 → one-time deletion of the legacy `/user/{id}/sharedsub/` keys is done (written only after a run with no failed delete)
/acl/{kind}/{owner_id}/{resource_id}/{grantee} → Grant protobuf (level, rights, state, granted_by); kind is `calendar`, `mailbox` or `addressbook`; grantee is `u:{user_id}` or `g:{group_id}` (a `g:` grant is live: created accepted, counted in the effective rights of every member, removed with its group); a mailbox resource_id is `MailboxMeta.id` and its `rights` hold RFC 4314 letters (`lrswipkxtea`, no level); calendar and address-book grants carry a `level` and no rights; every ID segment is url.PathEscape'd
/aclrev/{grantee}/{kind}/{owner_id}/{resource_id} → empty reverse marker; only ever written and deleted together with its forward key
/user/{user_id}/fwdtarget/{address}  → verified forwarding target
/user/{user_id}/fwdverify/{address}  → pending forwarding verification
/user/{user_id}/apppassword/{id}     → AppPassword protobuf: per-device protocol credential (label, created_at, last_used_at as 00:00 UTC of the last day used, bcrypt hash of the 18-letter secret). The id is the public 6-letter lookup group of the 24-letter password, so an attempt compares one hash. Accepted by IMAP, POP3, SMTP submission and CalDAV/CardDAV only (`LocalAuth.AuthenticateProtocol`); web login and re-authentication never see it. Removed with the rest of /user/{id}/ on user deletion
/user/{user_id}/devicelink/{hex(sha256(token))} → AppPasswordTicket protobuf: the one-time link behind the Add a device QR code (label, the mail host resolved on the desktop, the session epoch at creation, created_at, expires_at 5 minutes on, used_at). Only the hash of the 32-byte token is stored or sent through Raft, never the token. The record stays after use (used_at set) so the desktop can show used versus expired; a leader sweep deletes it an hour after expiry, and Revoke all and user deletion remove it. `appPasswordPrefix` ends in `apppassword/`, so listing app passwords never sees it
/user/{user_id}/fetch/{guid}         → FetchAccount protobuf: one remote POP3 or IMAP account the server collects mail from (protocol imap|pop3, security tls|starttls, host, derived port, username, folder, leave_on_server, interval_minutes, label, `generation`, and `credential`: the AES-256-GCM sealed password, never the password). `guid` is 32 lowercase hex (`store.ValidFetchGUID`); at most `store.MaxFetchAccountsPerUser` (5), checked at the handler layer. **Single writer: the user's own web requests** (`handlers.FetchHandler`, `/settings/fetch`; every save and every resume bumps `generation`, and the worker's rotation re-seal keeps it). Plain Put and Delete, no composite command. A node without the credentials key lists and deletes only (every other POST is 503). Delete removes the account and, best effort, its state; the seen records wait for the sweep
/user/{user_id}/fetchstate/{guid}    → FetchState protobuf: the worker's progress for one account (next_run_at, last_error as a fixed code never server text, auth_failures, paused and paused_reason, failures, uid_validity, per-run counts). **Single writer: the leader worker** (`internal/agent/fetchworker.go`). It carries the `generation` of the account it describes: a state whose generation is not the account's is treated as absent (zero counters, not paused, due now), so a resume or password change (generation++ on the account) can never be undone by a worker pause written a moment later
/user/{user_id}/fetchseen/{guid}/{32 hex} → FetchSeen protobuf (at, status 1 imported or 2 too_large, uid_validity): this remote message was handled. **Single writer: the leader worker.** The last segment is a hash, so server text never reaches a key: IMAP `hex(sha256("imap\x00" + folder + "\x00" + uidvalidity + "\x00" + uid))[:32]` (`store.IMAPSeenHash`), POP3 `hex(sha256("pop3\x00" + uidl))[:32]` (`store.POP3SeenHash`). Left behind when an account is deleted and removed by a leader sweep (`FetchStore.ListAccountGUIDsWithLeftovers`, second sighting), never by the web request, because a mailbox left on the server can hold tens of thousands of them; user deletion takes all three kinds with `/user/{id}/`. Every prefix ends in `/`, so `fetch/` never lists `fetchstate/` or `fetchseen/` (pinned by a test)
/user/{user_id}/usage              → 8-byte little-endian int64: the user's recomputed storage (mail raw size plus stored event, contact and task records; same accounting as the domain total, so a domain's users sum to it). Not a protobuf. A non-zero `UserProfile.quota_bytes` is enforced against it. Written only by the leader's usage recompute through `/__user_usage__/`, and only while `/user/{id}/profile` exists, so a pass that scanned before a delete cannot bring it back; removed with the rest of /user/{id}/
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

Address books: a user has the implicit default book and up to 20 more. CardDAV lists them all in the home set,
creates one with MKCOL, and serves each at `/carddav/{user}/addressbooks/{segment}/`; the segment is `default` or a
book id, canonicalised on every lookup (32 hex or a 36-character UUID with hyphens, any case, becomes lower-case
hex), so clients that MKCOL under a hyphenated UUID keep working. The path's user segment must equal the
authenticated user and the book is read from that user's own keys, so another user's book id is a 404. A hand-configured client that uses another user segment gets a 404 on any CardDAV path, unlike CalDAV, which still ignores the segment. A PUT
resolves its book first (409 when it is gone), never moves a GUID that exists in another book (409), and keeps an
existing contact's book on update. Rename is web-only. Deleting a book is several Raft commands and is NOT atomic:
one delete per contact, then the book key, then the grant revoke. The order is the recovery mechanism: a failure
part way leaves the book in place with the contacts that remain, so the same DELETE repeated finishes the job
(CardDAV answers 500, the web 500 with a try-again message). The grant revoke is best-effort and logged. A contact
written after the listing, or by an older node, can name a book that no longer exists: the web lists it under all
contacts, autocomplete, "from my contacts" and usage still count it, and CardDAV does not show it. Listing a book is
a Pebble prefix scan filtered on `addressbook_id`; there is no SQLite or Bleve column for it, and the contact search
index is book-agnostic. The default book has the id `default`, which is not 32 hex, so `BuildACLCommand` refuses to
share it, and that is the decision: the default book is not shareable (a share UI appears only on a created book;
not in v0.16.1, see the PRD). A book shared with a user is a collection `shared-{owner}-{book}` in the recipient's home set, named
`{book name} ({owner address})`. `parseSharedBookSegment` is stricter than CalDAV: a lower-case canonical UUID owner
and exactly 32 lower-case hex for the book (no `default`, no hyphenated or upper-case spelling). `resolveBook` returns a
`bookTarget` (keyOwner, book, segment, level): the path user must be the authenticated user, then the segment parses,
the owner is not the user, `ACLStore.AddressBookLevel` (the one resolver, `EffectiveGrant`, read on every request,
never cached) is read or write, and the owner's book record exists. Every shared failure (no grant, pending, expired,
revoked, left group, own, malformed, deleted book, another user's home) is the same plain 404 and never wraps
`ErrAddressBookNotFound`, which PUT would turn into a 409. A store read error on a shared path (grant or book read) is a 500, not the 404. Reads and writes use the OWNER's keyspace and
`checkQuota(owner)`, so the owner's domain quota is charged; hrefs sit under the recipient's shared segment. A read
share answers PUT and DELETE with 403 after resolution; a PUT of a guid filed in another of the owner's books is 403
(own books keep 409), and a DELETE of a missing guid or one filed elsewhere is the same 404; DELETE of a shared
collection is 403 for every holder (leaving is on the shares page); MKCOL on any `shared-` segment is a constant 403
before any grant read. A disabled owner's shared book stays reachable, as with CalDAV. The CardDAV handler is wrapped
in `caldav.PrivilegeFilter` with `Backend.ReadOnlyShare`, so a read share reports read only. A grantee write changes
the owner's ETag (same content ETag; there is no sync token). Contact groups are raw vCards over DAV, so their
members are not resolved there. `ListGrantsHeld` drops a grant whose book record is gone, so the shares page and the pending count agree. Mixed
versions: there is no new command or prefix, and field 12 survives an older node's web edit (unknown fields are
kept), but an older node's CardDAV shows every contact in the default book and has no MKCOL. Upgrade every node
before anyone creates a second book.

Address books on the web (sharing, `internal/web/handlers/contacts_books_share.go` and `contacts_shared.go`). The
owner's page is `/contacts/books/{id}/sharing`, with `POST .../share` and `.../unshare` (`BookShareHandler`, registered
in agent.go next to the folder form because the limiter is the calendar handler's `ShareLimiter()`: one 20-per-hour
budget shared with the calendar form, the folder form and IMAP SETACL). The "Sharing" link shows only on a created book.
Refusal order: limiter (429 with Retry-After), then the book must be the user's own and created (`default`, malformed,
another user's and missing are one 404, `contacts.errBookNotFound`), `share_with` empty (400), level `read` or `write`
(400, before any resolution), the 50-grant cap read, address parse with no display name, `ResolveAccount` or a
same-domain group (`shareableGroupOf`), self; every negative resolution and a full book for a new grantee is one 400,
`contacts.errShareRefused`. Then `offerGrant` (user: pending, invitation mail naming the book) or `offerGroupGrant`
(accepted, no mail). The cap read, the grant and the mail are separate commands and not atomic (concurrent shares can
overshoot 50), as for folders. Unshare takes a user id or `g:{id}`, is not rate limited, and a missing grant writes
nothing. `grantDisplayRow` is the one place a grant becomes a label and state for both sharing pages.
The grantee's web view is read only whatever the level: writes go over CardDAV. `/shared/contacts/{owner}/{book}` and
`/shared/contacts/{owner}/{book}/{guid}` sit outside `/contacts/{guid}` (as `/shared/mail` does) and call
`ContactsHandler.resolveSharedBook` on every request: stores wired, owner id well formed, book id 32 hex, owner is not
the session user, `AddressBookLevel` at least read, the book record exists. Every failure, and a contact that is missing
or filed in another book, is the same 404 with the constant body `address book not found`. A disabled owner's book stays
reachable (as CardDAV and shared calendars). The pages never reuse the owner's editor templates: no form, button or
action URL, and a contact group lists its members by name (`ResolveContactGroup(owner, g)`, no links). The contacts
sidebar's "Shared with me" group comes from `AddressBooksSharedWith` (accepted, book exists, at most 50), read per page.
`/shares` names an address-book row by the book's name (`CalendarHandler.SetAddressBooks`) and drops a deleted book.
Compose autocomplete (`ComposeHandler.eachSharedBookContact`) re-reads `AddressBooksSharedWith` on every request, scans
at most 20 books in owner then id order with `ListByBook(owner, book)`, matches the query as a case-insensitive
substring of name and address, skips contact groups and cards with no address, de-duplicates against the caller's own
contacts, and takes at most 10 hits with `source: "contacts"`. It must never call `SearchContacts(owner, q)`: that runs
a free-form query on the owner's search index, which also holds their mail, so it would be an oracle on mail. Contact
groups of a shared book are not offered in compose (expansion is owner-scoped). Not on the web yet: creating, editing or
deleting in a shared book, sharing the default book, moving a contact between books, shared books in "From my
contacts" and quick-add duplicate checks. No new key or command; the `/__acl__/` guardrail applies, and an older v0.16.0
node lists no shared books and answers 404 on `shared-` paths, so a client balanced across nodes would see the book
appear and vanish: upgrade every node before anyone shares an address book.

Contact import and export (`internal/contactio`, `internal/web/handlers/contacts_io.go`) add no key and no command.
`contactio.Parse` decodes the upload (UTF-8, a BOM, UTF-16 with a BOM, or Windows-1252 for bytes that are not UTF-8; a NUL
byte without a BOM is refused), names the format from the content (never the file name or content type) and returns every record
with no side effect: vCard blocks, CSV rows (Google new and legacy headers, Outlook with English headers, matched by name,
case-insensitively; `,` `;` or tab) and LDIF entries (Thunderbird `mozillaAbPersonAlpha`, `groupOfNames`). An LDIF change record
or `attr:< url` value is refused and nothing is fetched. A file whose headers are not recognised, including a localised Outlook
export, is refused with a message asking for an English export. Import is validated whole before the first write, vCard included
(a bad block, a trailing `BEGIN:VCARD` with no `END`, a bad birthday, a ragged CSV row or a group over `MaxContactGroupMembers`
refuses the file and names the row, card or entry); more than `contactsImportMaxRecords` (5000) records is refused; the quota is
checked on the sum of `store.ContactRecordSize` of the built contacts, not on the upload size. The write phase is one `ContactStore.Create`
per contact, people first and groups after, and is NOT atomic: a failed `Create` deletes everything written so far (best effort, each failed
delete logged by id) and the page says nothing was kept, or, when deletes failed too, how many contacts may remain. A process
crash between two writes leaves a partial import that nothing undoes. The write deadline is extended to five minutes
(`http.NewResponseController`). CSV export is the Google header set with a BOM; every data cell starting with `= + - @ TAB CR`
(phone numbers included) is prefixed with `'`, and import removes a leading `'` only when the next character is one of those six.
A value that itself starts with `'` followed by one of those six loses its apostrophe on a CSV round trip. Google and Outlook keep the apostrophe, so vCard is the format for moving between services. CSV skips contact groups; LDIF
writes them as `groupOfNames` with `member` DNs built from one UID-to-DN map. Export takes `format=vcard|csv|ldif` and an optional
`book` checked as on the list page; download names are fixed. The structured components of N, ADR and ORG are written by go-vcard,
which escapes `\`, LF and `,` but not `;`, so a `;` inside a component becomes `,` on import. `contactio.ParseLimit(raw, max)` stops with `ErrTooMany` at `max+1` records or `max*4` rows (skipped ones included) before building more, and the readers iterate lines lazily, so a 10 MiB file of tiny rows is refused cheaply; LDIF folds are joined in a builder (linear). CSV row numbers count blank lines, as a spreadsheet does. A file with CR line ends only is read as LF. Two LDIF people with one DN: group members resolve to the first. The quick Company field is the whole ORG value (as the web edit and CardDAV paths set it), not the first component.

Sharing is the grant key space (`/acl/...` and `/aclrev/...`, `internal/store/aclcommand.go`), for kinds
`calendar`, `mailbox` and `addressbook`. Validation is per kind, in `BuildACLCommand` and again in `PlanACL`:
a calendar grant has a level (`freebusy`, `read`, `write`) and no rights; an address-book grant has a level
(`read`, `write`) and no rights; a mailbox grant has non-empty `rights` (distinct letters from `lrswipkxtea`)
and no level. Resource ids are checked by shape for mailbox (32 lowercase hex, `MailboxMeta.id`) and address
book (the same 32 hex); calendar ids have no shape check because legacy ids such as `default` exist. The
grantee is typed, `u:{user_id}` or `g:{group_id}`, in the key builders and in `GrantRef.Principal`; on the wire
a user grantee stays the bare user id (so an older node and log replay compute the same keys) and only a group
carries `g:`. A `g:` grantee is accepted on every op for every kind when its id has the group shape
(`groupIDPattern`); that the group exists is the caller's check, and the FSM keeps its shape checks. Every kind is capped
at `store.MaxGrantsPerResource` (50) live grants per resource (`ACLStore.GrantCapReached`; expired pending
grants do not count). `GrantOrphaned` probes the owner always, the grantee profile for a user and
`/system/group/{id}` for a group, and the resource per kind (calendar key, mailbox id, address-book record).
**Effective rights are the union of grants:** `ACLStore.EffectiveGrant(kind, owner, resource, userID)` is the one
resolver every authorisation uses (`CalendarAccessLevel`, `HasCalendarAccess` and the held-share listings
delegate to it). It merges the user's own accepted `u:` grant with the accepted `g:` grants of every group in
their member index (`/system/groupmember/{user}/`, read on every call, never cached in a session, handler or
CalDAV backend, so leaving a group ends access on the next request). A calendar or address book takes the highest
level; a mailbox takes the union of the RFC 4314 letters in `lrswipkxtea` order. A pending grant never counts,
whoever created it. It does not check the resource exists and does not special-case the owner: the held-share
listings skip a resource the user owns that only a group reaches, and `HeldShare.Direct` / `ViaGroups` tell the
shares page to show "via {group}" instead of Leave, because `RevokeGrant` takes the session user's own `u:` grant
and would do nothing for a group-reached share. A resource reached by both a `u:` and a `g:` grant is listed once
at the strongest level.
**Group grants are created accepted, not pending.** A share with a group address (calendar share handler; the
shared `offerGroupGrant` for other kinds) writes an accepted `g:` grant with no invitation mail, because a group
has no single person to accept it and the admin-managed group is the consent boundary. Anyone
who can share a resource can therefore put it into every member's "shared with me" without their consent, so a group
is accepted as a grantee only when it exists and sits on the sharer's own domain; any other group address gets the
same uniform refusal as an unknown address, after the cap and level checks. User grants stay pending. The owner's
sharing list shows `g:` grants by group address and `UnshareCalendar` takes `g:{group_id}` for them. Deleting a
group removes its `g:` grants in the same Pebble batch: `PlanGroup`'s delete scans `/aclrev/g:{id}/` (bounded by
the 50-grant cap times the resources shared with the group) and deletes each forward and reverse key with the
group, its member index and its address record, so no grant outlives its group. An older-binary node deletes the
group but keeps the grants, and the sweep removes them on the second sighting.
A grant has a state (`pending`,
`accepted`); only an accepted grant authorises, and a grant on a deleted calendar authorises nothing.
The forward key and its reverse marker are written and removed only by the composite `/__acl__/`
command (`set`, `accept`, `revoke`, `revoke_resource`, `revoke_owner`, `revoke_grantee`), applied in one Pebble
batch, so they never disagree. A web share creates the grant `pending` and mails the grantee a link to
`/shares` (the page lists pending grants of every kind and the shares joined; `/calendar/shares` redirects there with a 302, and `POST /shares/{accept,decline,leave}` act on a grant named by `kind`, `owner` and `resource`, with the grantee always the session user; every refusal is one 404 "invitation not found"; the shared helper `offerGrant` creates the pending grant and queues the mail for any kind, and the calendar sidebar shows the pending count); `accept` is conditional inside the FSM (it never recreates a revoked grant) and a
`set` with `keep_state` leaves an existing grant's state alone. Grants made before the invitation flow are
`accepted`. `accept` and `keep_state` are unknown to a node on an earlier v0.15 batch binary: upgrade every
node before anyone shares. "What have I shared" is a prefix scan of `/acl/{kind}/{owner}/`; "what is
shared with me" is a prefix scan of `/aclrev/u:{me}/{kind}/` plus `/aclrev/g:{group}/{kind}/` for each group of the user. Every prefix ends in `/`, so owner `ab`
never matches `abc`. `Calendar.shared_with` is legacy: the `calendar-grants-v1` leader migration turns
each entry into an accepted `freebusy` grant and clears the field, and nothing else reads it.
Deleting a calendar or a user revokes their grants in the same request (`revoke_owner` and
`revoke_grantee` cover every kind). `MailboxStore.DeleteMailbox` reads the mailbox id first, applies the
delete, then issues `revoke_resource` for kind `mailbox` as a separate command (`RevokeResourceGrants`;
`PurgeAddressBook` calls it with `ACLKindAddressBook` after the book key is deleted; a failure returns
`ErrAddressBookGrantsNotRevoked`, which the web and CardDAV log while still reporting success). It is best-effort, not atomic with the
delete: a failure returns `ErrMailboxGrantsNotRevoked` (IMAP logs it and still answers OK, since the
mailbox is gone), and the stale grant is inert because the delete removes the `/user/{id}/mailboxid/{id}`
key. A mailbox with no id issues no ACL command. A rename keeps its grants, as the id is unchanged.
A leader sweep removes grants whose owner, resource or grantee is gone (two consecutive sightings,
`ErrNotFound` only) for every kind: calendars by key, mailboxes through the `mailboxid` lookup (never a
scan of the owner's mailboxes), address books by their record (`GrantOrphaned`, the backstop for `ErrAddressBookGrantsNotRevoked`), and `g:` grants whose `/system/group/{id}` is gone.
The command and key shapes are unchanged, so no new mixed-version guardrail applies to user grants; for group
grantees see the guardrail below.
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

- **Shared folders over IMAP** live under `{imap.other_users_prefix}/{owner primary address}/{folder}`. Every session method reaches a name through `session.resolve` (`internal/imap/shared.go`), by way of `resolveMailbox`, `requireMailbox` and `requireNoMailbox`; the selected folder is `sel()`, not `selectedMailbox`, and any `s.userID` left in a mail-meta, Bleve, flag, notifier or quota call on a selected folder is a bug (it reads the grantee's own namespace). **No existence oracle:** a name that does not resolve, a folder with no accepted grant, a pending or expired-pending grant, and a grant without `l` all answer exactly like a missing folder, with the caller's own not-found error (NONEXISTENT, TRYCREATE for APPEND/COPY/MOVE destinations, the reserved-name error for CREATE and for a RENAME target). Only a user who holds `l` gets NO [NOPERM] for a command needing another right. Resolve first, check rights second, read messages last. The owner address is the primary address (`ACLStore.OwnerByPrimaryAddress`: primary record, live non-resource profile, email equal; a disabled owner resolves to not found); an address containing `/` is unreachable, and a personal folder named under the prefix is unreachable over IMAP. Grants name the folder by `MailboxMeta.id`, so a rename keeps it shared and a new folder taking the old name is not shared. Rights come from `ACLStore.MailboxRights` (the one place the union with group grants belongs) and are re-read on every command by `checkSelected` (rename-following by id) and on IDLE's recheck; a lost grant answers not found, clears the session's message lists, and in Poll sends nothing and returns nil. SELECT opens read-only without any of `w`, `s`, `t`. STORE checks every right before the first write (`\Seen` s, `\Deleted` t, other flags w). EXPUNGE needs `e`; CLOSE (go-imap calls Expunge with a zero `ExpungeWriter`) succeeds without it and expunges nothing. Keywords set or cleared with `w` (any keyword change needs it; FLAGS replace needs it only where the keyword set differs) change the OWNER's tags: `applyKeywords(owner, mailbox, ...)` writes the owner's MailMeta, MailboxMessage tags and indexes, a new keyword creates the owner's tag, and `checkSharedStore` refuses before the first write (NO [LIMIT]) a keyword that is not a valid tag name, would pass the owner's `MaxTagsPerUser` (the owner's, not the grantee's) or the per-message maximum; a keyword failure on a shared folder fails the command, unlike an own folder. SELECT offers `\*` in PERMANENTFLAGS only with `w`. Two sessions changing one message's tags at once is last writer wins on the MailMeta read-modify-write. COPY and MOVE across owners (shared to own, own to shared, owner to owner) need `r` on the source (held by SELECT), `i` on the destination and, for MOVE, `t` and `e` on the source; they run as one composite `/__mailbox_xfer__/{dst owner}` command (`internal/store/mailbox_transfer.go`) that builds each destination MailMeta from the SOURCE record read inside the FSM (new caller-generated guid, same BlobRefs, no tags: the source owner's tags are another namespace and would sidestep the destination's tag cap), allocates uids from the destination's uid_next, and keeps only the flags the user's rights on the destination allow (`transferFlagMask`, as APPEND). A MOVE removes only the source MailboxMessage; the source MailMeta is left for the orphan sweep, as after EXPUNGE, so the source owner's usage falls only after that sweep and the next recompute. The destination owner's quota is checked after the rights (NO [OVERQUOTA], nothing written; a checker error fails open), so a user without `i` never learns a folder is full. Same-owner COPY and MOVE are unchanged and not quota-gated. COPYUID carries the destination's validity and the uids mapped by guid, never uid_next-1. The result's `NewMails` feed the derived indexes and blob sync on every node; only `Arrivals` and `Expunged` notify, so a client sees one EXISTS. Notifications are keyed by the owner, so IDLE and the web SSE stream of every session on a folder (owner and grantees) hear a grantee's change; a keyword-only change does not notify (PrepareNotify suppresses unchanged flags; predates this). More than `store.MaxMailboxTransferItems` (500) messages split into several commands that are not atomic together. CREATE beneath a shared folder needs `k` on the parent and copies the parent's grants to the new folder: the creator's own grant first (its failure fails the command and leaves a folder only the owner sees), the others best-effort and logged, none of it atomic with the CREATE. UID EXPUNGE ignores its UID set (every `\Deleted` message goes); that predates shared folders and is unfixed. **ACL (`internal/imap/acl.go`, user docs in `docs/shared-folders.md`):** the session implements the fork's `SessionACL`, which is what advertises `ACL`. MYRIGHTS returns the re-read `resolvedMailbox.rights` for any folder `resolveMailbox` returns and writes nothing (no id is allocated for a personal folder); GETACL, LISTRIGHTS, SETACL and DELETEACL resolve the folder first and then need `a`, and an unseen folder is the missing-folder answer for all five. GETACL lists the owner first, then every accepted and live pending grant (an expired pending one is left out, and so is a grantee whose address or group is gone), a user by `UserEmail` and a group by its address. LISTRIGHTS never resolves the identifier. SETACL charges the per-user share limiter first (the calendar handler's instance, so the web and IMAP share one 20-per-hour budget; `Server.SetShareDeps`), then validates rights (a letter outside `lrswipkxtea`, `c` and `d` included, is BAD; the string is made canonical so `offerGrant`'s `==` compare is stable), refuses reserved identifiers (empty, `anyone`, `authenticated`, a leading `-`), resolves the folder and needs `a`, reads the cap, matches the identifier against the folder's own live grants, and only then resolves it: `ResolveAccount` for a user, or a group whose domain equals the FOLDER OWNER's (never the acting grantee's). Unknown, external, disabled, catch-all-only, other-domain group, the owner and display-name identifiers all answer the one fixed `errACLIdentifier` text. At the 50-grant cap a new grantee, resolvable or not, answers `NO [LIMIT]` before any resolution, so a full folder is no oracle; an existing live grantee can still be changed. `+` and `-` start from that principal's stored grant, never `EffectiveGrant`; an empty result is a revoke. The write goes through `handlers.OfferGrant` and `OfferGroupGrant` (adapter in `internal/agent/imap_share_offer.go`, because `handlers` imports `imap`), so a new user is pending and invited, a change keeps its state and a same-domain group is accepted; `GrantOffer.GrantedBy` records the acting user, so a delegate holding `a` is the grantor. A rights change by a delegate also rewrites `granted_by` on the existing grant (`SetGrantKeepState` keeps the state, not the granter), so the recorded granter is whoever last set the rights. A personal folder with no id gets one (`EnsureMailboxID`) only right before the write, so a refused SETACL changes nothing; that, the grant and the invitation are separate commands and not atomic, and the cap read is read-then-write, so concurrent SETACLs can overshoot 50. DELETEACL matches only existing grants and resolves nothing. No new key or synthetic command; the existing mixed-version guardrail on `/__acl__/` applies, and an IMAP client can now start a folder share, so upgrade every node first. **Subscriptions:** shared folders are subscribed by default (see the `sharedunsub` key). An older node still reads `sharedsub`, so once the cleanup has run it lists every shared folder as unsubscribed, ignores the opt-out marker, and its SUBSCRIBE writes inert `sharedsub` keys that are never cleaned up: upgrade every node together (a single-node production is unaffected).
- **Shared folders on the web** (`internal/web/handlers/mail_shared.go`, `folder_share.go`) have their own tree, `/shared/mail/{owner}/{folder}` (owner user id, folder `MailboxMeta.id`), registered outside `/mail/{mailbox}` so a personal folder called `shared` stays reachable; the own-folder handlers in `mail.go` are untouched. Every handler calls `MailHandler.resolveSharedFolder` on every request (`PrimaryAddressOf` owner, `ACLStore.MailboxRights` union, `MailboxNameByID`) and caches nothing. Every failure before the `l` right is held is one 404 with the constant body `folder not found` (no grant, pending, expired pending, no `l`, own, unknown or disabled owner, malformed or unknown id), then a missing letter is 403, so only an `l` holder can tell a folder exists. Routes: list and show need `r`; flags need `s` for seen, `w` for flagged and answered, `t` for deleted, and an unknown flag or action is 400; delete needs `t` and `e`, sets `\Deleted` and expunges the OWNER's folder (permanent, no Trash move, and it removes every `\Deleted` message there, as IMAP EXPUNGE does). A message reads `MailMeta` from the owner's keyspace (`loadMailMeta(owner, ...)`), never `findOwnedBlob` or `/mail/blob`: `MessageDetail.AttachmentBase` makes `detailAttachmentURL` link attachments by index under the shared prefix, and the handler clears each attachment `Hash`. Show marks `\Seen` only with `s` and never runs `backfillThreadingFields` (it writes the owner's MailMeta); `\Seen` and `\Flagged` are the owner's flags. Not offered yet (PRD follow-ups): reply, forward, move, copy, archive, spam, tags, bulk, thread view, iMIP answer, sharing by a delegate with `a`, live SSE updates. The sidebar's "Shared with me" section is filled by the `SharedMailSidebar` middleware, a lazy loader in the template context (`templates.WithSharedSidebar`), run at most once per request: accepted grants with `l` only, owners that fail `PrimaryAddressOf` dropped, unread only with `r`, rows tagged `data-shared-folder` and `data-shared-unread` (never `data-unread-for`, which the live refresh keys on folder name). All Mail, the virtual mailboxes, header views and search read only the session user's keyspace, so shared folders are excluded by construction (pinned by tests). The owner's page is `/settings/folders` (`FolderShareHandler`): presets Read `lrs`, Write `lrswite`, Full `lrswipkxte`, and `a` only on Full; the order is rate limit (the calendar handler's `ShareLimiter`, one budget with the calendar form and IMAP), folder, share_with, preset (before resolving), cap, address, resolve (user, or a same-domain group), self, `EnsureMailboxID`, `offerGrant`/`offerGroupGrant`; every negative resolution is `cannot share with that address`. No new key or command, so the existing `/__acl__/` guardrail applies: upgrade every node before anyone shares a folder from the web. **Folder create, rename and delete** (`internal/web/handlers/folder_manage.go`, same handler; `POST /settings/folders/{create,rename,delete}` and the confirm page `GET /settings/folders/delete?folder=`) take the folder from the session user only. `store.ValidateMailboxName` (`internal/store/mailboxname.go`) is the single validator for a NEW name: IMAP CREATE and RENAME (`ValidateOwnerMailboxName` for the owner-side name of a shared create or rename), web create and rename and the mbox import target. It reads configuration only (not empty, at most `MaxMailboxNameLen` 255 bytes, valid UTF-8, no control characters, no leading, trailing or doubled `/`, no sharing prefix, no exact lower-case virtual view name); a rename source and a delete target are never validated, so a legacy folder can still be renamed away or deleted. IMAP CREATE trims one trailing `/` first (RFC 3501 6.3.3). `store.ProtectedMailbox` (DefaultMailboxes and Archive) cannot be renamed or deleted from the web (403); an unknown, empty or reserved-prefix name is 404. A rename moves only that folder (subfolders keep their names, id and grants follow it); a delete removes only that folder and its messages, the grant revoke is best-effort (`ErrMailboxGrantsNotRevoked` is logged and reported as success) and blobs are left to the GC. The 409 for an existing target is a pre-check (check-then-act). Older nodes still apply the old IMAP name rules until every node is upgraded. The mail sidebar always shows the Folders group with New folder and Manage folders links.
- **Shared inboxes** (`internal/store/sharedinbox.go`, `internal/web/handlers/admin_shared.go`) are accounts of `UserProfile.kind` `shared` (`store.SharedInboxKind`, `auth.SharedKind`), created by `auth.CreateShared` (random discarded bcrypt hash, kind in the first and only profile write) from the admin form (`kind=shared`, `/admin/shared-inboxes/new`) and never able to log in: `auth.CheckStanding` refuses any non-empty kind on every path (web, `AuthenticateProtocol` with account and app passwords, device links), so no password, app password or device link exists for them, and admin reset-password answers 400. The member list is `UserProfile.shared_members` (field 16, typed principals `u:{id}` or `g:{id}`, at most `store.MaxSharedMembers` = 50), stored with the account and read on every check, never cached. **Folder access is real grants:** `store.SyncSharedInboxGrants` writes an accepted `lrswite` (`store.MailboxRightsWrite`) mailbox grant per member on every folder (no invitation, `EnsureMailboxID` first, vanished principals skipped, strict: any other principal's grant on the folder is revoked and a revoke failure is returned, the admin gets a 500 and repeats). Members hold no `k`, `x` or `a`, so they cannot create, rename or delete folders or share them; any future path that creates a folder on a shared account must call the sync. `OwnerByPrimaryAddress` still resolves a shared owner (only `resource` is refused), which IMAP `Other Users/{address}` and the web shared tree need. **Send-as is membership, not rights:** `store.CanSendAs` also allows an `AddressRecord` with `CanSend` and `Verified` owned by a live, enabled shared inbox when the user is in its `shared_members`, directly or through a group (`sharedMember`: one `GroupMemberKey` read per listed group, every call). Folder grants never count. It covers SMTP submission, web compose, DKIM forward origin and the antispam bypass, and the web From picker (`ComposeHandler.sharedFromOptions`, candidates from `MailboxesSharedWith`, filtered by `CanSendAs`). **Own address is separate from send-as:** `store.CanSendAsOwn` (and `SendAsPolicy.CanSendAsOwn`, `deliver.OwnAddressChecker`) is the old rule without membership, and `CanSendAs` is own or member, so membership only widens sending. Callers asking "is this me?" use the own rule: reply and reply-all (`ComposeHandler.isOwnAddress`, so reply-all keeps the shared address and a message from it is not the member's own), calendar invitations (`inviteRecipients`, so inviting the shared address sends it an invitation), the attendee cap and vacation (`vacationSenderUser`, `vacationAddressedTo`, `vacationFromAddress`: a member's vacation does not answer for the shared address). Callers asking "may this user send as it?" use `CanSendAs`: SMTP `SendAsPolicy`, web send authorisation, the From picker, the calendar organiser check (`maySendAs`), DKIM forward origin, the antispam bypass and the calendar-reply sender check. Sent copies: web compose files the member's own copy and, best-effort and logged, a second one in the shared account's Sent when that folder exists (`fileSharedSentCopy`, GUID derived from the send ID, send log step `sent_shared`, shared quota checked, skipped when over); SMTP submission files no server-side copy (clients APPEND, members hold `i`). **Removal:** the admin handlers write the profile first (send-as ends on the next request), then sync (folder access ends on the next request, `EffectiveGrant` re-reads on every call). `GrantOrphaned` treats a mailbox grant on a shared owner whose principal is not in `shared_members` as orphaned, so the two-pass leader sweep removes what a failed revoke left; `EffectiveGrant` itself does not check membership. Members must be on the shared inbox's primary domain for every admin; a user member is a live person (`Kind == ""`, enabled), a group member passes `ShareableGroup`; the shared account itself, another shared account, a resource, another domain and a duplicate get the one 422 refusal. `GroupStore.checkAccounts` and the admin groups handler refuse any non-empty kind, so a shared account is never a group member. Deleting a shared account uses the existing user-delete cascade. No new key or command (the profile is a plain PUT; grants use `/__acl__/`), so the grant guardrail below applies; see the shared inbox guardrail for mixed versions.
- **Unsubscribe** (`internal/unsubscribe`, `internal/web/handlers/mail_unsubscribe.go`, `compose_unsubscribe.go`, `netx.Client.PostOneClick`, user docs in `docs/unsubscribe.md`) adds no key, command or `MailMeta` field: `List-Unsubscribe` and `List-Unsubscribe-Post` are read from the stored blob when a message is shown, and no request carries a URI. Every route names the message by mailbox and uid and resolves it in the session user's own keyspace (`resolveMessage`), so only the owner reaches it; only `ShowMessage` and the Mailing lists page set the view model, so a shared folder never gets the button. `unsubscribe.Parse` takes `<...>` entries (header at most 2048 bytes, 8 entries; the first `https` and the first `mailto` win; `http` and other schemes are ignored; an https URI with user information, no host or a port other than 443 and 8443 is dropped) and offers one-click only for exactly ONE `List-Unsubscribe` and ONE `List-Unsubscribe-Post` header, because DKIM takes header instances from the bottom up and a forged unsigned header above a signed one would still verify. **DKIM is checked on click** (`unsubscribe.VerifyOneClick`: some signature valid, at most 5 verified, DNS bounded by a 5 second context, `h=` covering both headers) on the stored copy; deliver strips and re-stamps trace headers, so a signature covering `Authentication-Results` or `X-Spam-*` will not verify and the POST answers with the link panel instead (accepted, not worked around). Only then `netx.Client.PostOneClick` sends `List-Unsubscribe=One-Click`: https only, a client built with default `Options` (never the calendar client's private-network opt-out), no redirect followed (a 3xx is a failure), no cookies, no proxy, fixed `User-Agent: Bongo-Unsubscribe/1`, 10 second timeout, a 16 KiB discarded response, errors that name the host only. The POST (`/mail/{mailbox}/{uid}/unsubscribe`) charges the per-user budget (`UnsubscribesPerHour` = 20, in memory and per node) before any read, then applies the Junk-or-below and spam gate again (`unsubscribeBlocked`: `store.InOrBelowJunk`, or `MailMeta.spam_score` at or above the antispam tag threshold, chosen over the stamped `X-Spam-Status` because "not spam" resets the score), and never renders the remote body. A non-one-click https URI is a `<details>` disclosure naming the host with a `noopener noreferrer` link the user opens; the server never fetches it. `mailto:` goes through `GET /compose/unsubscribe/{mailbox}/{uid}` (one address via `composeToPrefill`, subject without control characters and at most 998 bytes, body at most 2000 runes, cc, bcc and every other field dropped). The Mailing lists page links `/mail/view/lists/{listID}/unsubscribe`, which uses the newest message of `headerViewMessages` (Junk, Trash, Sent and Drafts excluded). Logs carry user id, message guid and URI host only. No Raft write, so no mixed-version guardrail: an older node answers 404 and shows no button.
- Header views (`/mail/view/to-me`, `/mail/view/from-contacts`, `/mail/view/lists`, `/mail/view/lists/{id}`, registered before `/mail/{mailbox}` and identified internally by a view argument, never a folder name, so a folder called `to-me` or `lists` stays reachable) are filters over node-local SQLite columns (`to_me`, `sender_addr`, `list_id`, `list_name` on `maildocument`), not folders and not in `store.VirtualMailboxes`. No Raft command or Pebble key backs them. `to_me IS NULL` marks a row the `HeaderBackfill` worker has not derived yet; `IndexMail` must never reset these columns. `to_me` is judged against the user's addresses when the row is indexed (`users`, `addresses`, `aliases`; a rebuild also reads `/system/address/`), so a later alias does not retro-fit old rows. "From my contacts" reads the contact book per request. Rows carry `data-real-mailbox` and `data-sel-key` (`uid:mailbox`); every action URL for these rows uses the real mailbox, and the view bulk routes (`/mail/view/{view}/bulk`, `BulkToMe`, `BulkFromContacts`, `BulkMailingList`) take `targets=uid:mailbox` pairs (a bare UID is refused, since UIDs are only unique per mailbox). **Participants** (`/search?participant={address}`, repeatable, an OR; `HeaderView{Kind: ViewParticipant}`, `MailHandler.ListParticipants`, `SearchHandler.SetParticipantHandler`) are the conversations with an address: the per-user SQLite table `participants (address, mail_guid, roles)`, primary key (address, mail_guid), roles a bitmask (1 From, 2 To, 4 Cc), one row per address and message, no mailbox column, lower-cased `BareAddress`, From then To then Cc only (no Reply-To, no Bcc: MailMeta has none), the user's own addresses included, at most `store.MaxParticipantsPerMessage` (200) per message. **`SetMailHeaders` owns the table**, replacing a message's rows in the transaction that updates the header columns (and writing nothing when the maildocument row is missing); `IndexMail` must never touch it, `DeleteMail` removes the rows, and the live indexer, rebuild and `HeaderBackfill` fill it through `indexMailHeaders`. `headers_rev` is 2 (`store.HeadersRevCurrent`) and `PendingHeaders` selects `headers_rev < 2`, so every v0.15 row is revisited once, Cc read from the blob; that pass also judges `to_me` again against today's addresses (one-time change), and conversation results on a node are partial until its pass is done. The list is the session user's own mail from every folder except Junk and Trash and below them (Sent and Drafts stay; `HeaderView.Excludes`, `MailboxStore.ListGUIDMessages`), grouped by thread; `q` is ignored when `participant` is present; 400 for no valid address or more than `store.MaxParticipantQuery` (20); bulk is `/search/bulk?participant=` and takes `uid:mailbox` targets only. Addresses in URLs are query-escaped (`a+b@x` is `%2B`). The contact card (own and shared-book, not groups) links there with `store.ContactEmails`. Known limitation: the live indexer writes `Complete` from MailMeta alone, so a message whose MailMeta lacks Cc (an old IMAP append or an import) loses the Cc participants the backfill took from its blob the next time its MailMeta is written (any flag or tag change); the row is then rev 2 and is not revisited, and the same applies to `to_me`. No Raft command, Pebble key or Bleve change.

### UI Patterns
- Alpine.js `fetch()` for JSON API endpoints; HTMX for HTML fragment swaps. Don't use HTMX for JSON-returning endpoints.
- Chi route ordering: static segments (`/mail/tag/{name}`) must be registered before wildcard segments (`/mail/{mailbox}`).

## Repo-Specific Guardrails

- **Grants are one composite Raft command (`/__acl__/`, `internal/store/aclcommand.go`).** A v0.15 node refuses a `mailbox` or `addressbook` grant at validation (the FSM logs the error and writes nothing, yet the applied index advances), so its replicas stay diverged after an upgrade and replay does not repair them: upgrade every node before anyone shares a folder or address book. An older-binary node applies it as a literal key and holds no grants, so replicas diverge for as long as the cluster runs mixed versions. An older v0.16.0 node lists no shared books and answers 404 on `shared-` address-book paths, so a client balanced across nodes would see a book appear and vanish and could drop its local copy: upgrade every node before anyone shares an address book. Upgrade every node before anyone shares a calendar, and before the leader runs the `calendar-grants-v1` migration (it writes `/__acl__/` commands too). Production runs one node. The accept op also refuses a pending grant older than `store.PendingGrantTTL` (30 days, judged from the stored `updated_at` and the command's `at`, never the clock), so a node on an earlier binary would accept what an upgraded one refuses; upgrade every node together. Expired pending grants are never deleted and sit outside the 50-grant cap. Never write `/acl/` or `/aclrev/` keys any other way, and give every test applier that can see a grant an `IsACLSyntheticCommand` dispatch, or the command is stored as a literal key and negative tests pass vacuously.

- **Cross-owner COPY and MOVE are one composite Raft command** (`/__mailbox_xfer__/{dst owner}`, `internal/store/mailbox_transfer.go`, dispatched with the rest of the mailbox family by `IsMailboxSyntheticCommand`). The caller supplies the new guids (32 lowercase hex) and the flag mask; the FSM reads the source records, the destination mailbox and uid_next, refuses the whole command (writing nothing, so a COPY is all or nothing) on a missing destination mailbox or source, an existing or duplicate guid, a bad guid, equal owners or an owner containing `/`, and never reads the clock or randomness. Blobs are never copied or deleted. An older-binary node stores the command as a literal key, so the destination copy is missing there and, for a MOVE, the source is still present; replicas diverge and replay does not repair them: upgrade every node before anyone copies across owners. A request beyond `MaxMailboxTransferItems` is several commands that are NOT atomic together; an earlier chunk stays applied when a later one is refused, and a MOVE answers the error without per-message expunges. Test appliers must route it through `IsMailboxSyntheticCommand`, serialised as the FSM is (the IMAP test applier has `mailboxMu`), or it is stored literally and negative tests pass vacuously.

- **Mailbox ids are assigned by a composite command** (`/__mailbox_assignid__/{user}/{mailbox}`, `internal/store/mailbox.go`). The caller generates the id (the FSM never reads randomness or the clock) and the FSM writes `MailboxMeta.id` and `/user/{id}/mailboxid/{id}` in one batch only when the mailbox has none, so concurrent assigns yield one id. An older-binary node stores the command as a literal key and holds no id; its rename drops the id and leaves the lookup key stale, and its delete leaves the key behind. `MailboxNameByID` checks that the mailbox still carries the id, so those cases read as not found. Upgrade every node before anyone shares a folder. Rename does not rename inferior mailboxes (`A/x` stays when `A` moves), so each folder keeps its own id and key.
- **Per-user usage is one composite Raft command per recompute pass** (`/__user_usage__/{chunk}`, `internal/store/user_usage.go`): the leader's recompute writes `/user/{id}/usage` for every user in one Pebble batch, each entry conditional on the profile existing (decided from Pebble inside the FSM, never the clock). Beyond `store.MaxUserUsageEntries` (5000) a pass splits into several commands that are NOT atomic together: some users can be a pass behind, and the next pass converges. An older-binary node stores the command as a literal key and holds no per-user usage, so replicas diverge until every node is upgraded. `UserProfile.quota_bytes` is enforced from it in `DomainQuotaStore.RecipientWouldExceedQuota` (every protocol, best effort, up to one 10-minute recompute interval stale) and served over IMAP QUOTA. An older-binary node neither enforces `quota_bytes` nor holds the figure (it reads 0), so enforcement depends on which node takes the request: upgrade every node before relying on per-user quotas. No new command or key was added for this. Give any test applier that sees a recompute pass an `IsUserUsageSyntheticCommand` dispatch, or the figure is stored literally and negative tests pass vacuously.

- **Distribution groups are one composite Raft command** (`/__group__/` ops create, update, delete, remove_user; `internal/store/groupcommand.go`). The FSM writes the group, its `/system/address/` record (kind `group`) and the member index in one batch, and create is a compare-and-set: it is refused with `store.ErrGroupAddressTaken` (returned through ApplyCommand, not logged) unless the group id, the address record and `/system/alias/{address}` are all absent. Update derives the member-index diff from the stored group and is last-writer-wins on the lists; the address never changes. Delete removes the address record only while it still names that group. The compare-and-set protects one direction: CreateUser, admin AddAddress, alias create and send-as verification Put `/system/address/` after a check-then-act pre-check and can still overwrite a group record in that window. An older-binary node stores `/__group__/` as a literal key and has no group, and an older resolver reads kind `group` with an empty user_id and falls through to the catch-all, so upgrade every node before creating any group. User deletion sends `/__group__/remove_user` even when no group exists, so an older node also gains a harmless literal key on every user deletion. An admin cannot remove a group's address from a user's page (409); it goes only with the group. Field 6 of AddressRecord is unknown to old binaries, which preserve it. Deleting a user issues `remove_user` (one batch for all their groups) best-effort after the grant cascade, logged on failure; a leftover id is inert and the code that expands a group must skip members whose profile is gone (no sweep exists yet). Give any test applier that can see a group command an `IsGroupSyntheticCommand` dispatch, serialised as the FSM is, or the command is stored as a literal key and negative tests pass vacuously. The SQLite `addresses` table is not maintained by the FSM and holds no group rows.

- **Group grantees need every node upgraded first.** A node on an earlier binary (v0.15 or an earlier v0.16 batch) refuses or stores as a literal key a `/__acl__/` set naming a `g:` grantee, and its `GroupOpDelete` deletes the group without its grants, so replicas diverge for as long as the cluster runs mixed versions. Upgrade every node before anyone shares with a group. Test appliers that see a group delete or a group grant need both `IsGroupSyntheticCommand` and `IsACLSyntheticCommand` dispatches, serialised as the FSM is, or the delete-revokes tests pass vacuously: assert that the `/acl/` keys exist before the delete. Group addresses join the GAL and compose autocomplete in `ComposeHandler.GALSearch` from `GroupStore.ListGroups(callerDomain)`, never through the Bleve GAL, which is keyed by user id and rebuilt from profiles; a caller with no usable domain gets no groups. The admin UI is `/admin/groups` (`AdminGroupsHandler`): every route authorises on the stored group's domain, a member or owner must be an existing non-resource user whose primary address is on that domain, and a domain admin gets the same 403 for an absent and an out-of-scope group.

- **Groups are expanded at RCPT and by web compose, not in the pipeline.** A group address reaches the SMTP `Session.Rcpt` as `GroupRecipientError`; `GroupStore.ExpandMembers` returns the members in order, skipping any whose profile is gone, who is disabled or who is a resource, and each becomes an ordinary local recipient with their own envelope, rules, antispam result and quota check (one shared blob). Only group-derived copies are de-duplicated: a member reached through a group is skipped when that user already has an envelope in the transaction (direct or from another group), and when a direct RCPT follows a group copy for the same user, that copy is rewritten in place to the direct address and DSN parameters (and no longer counts against the fan-out cap). Direct recipients are never merged with one another, as before groups: bob@ and bob+lists@, or two aliases of one account, stay two envelopes (`Session.addDirectLocal`). A member over quota is skipped and logged, unless every member is (552). `X-Bongo-Group: {group_id}` is added to the copy local recipients receive and a message that already names the group is accepted and discarded for it (never a 554 or a bounce, which could loop); the header is never put in the blob for external relay recipients or in the sender's Sent copy. Anyone who learns a group id can add the header and suppress that group's expansion; accepted, with the Received hop limit as the backstop for real loops. NOTIFY=SUCCESS and DELAY are not passed to member envelopes (a SUCCESS-only request becomes NEVER) and ORCPT names the group. The per-sender fan-out cap (`smtp.limits.group_fanout_per_hour`, separate from `outbound_recipients_per_hour`, `mail.FanoutLimiter`, in memory and per node) is peeked at RCPT and charged at DATA; keys are `u:{user_id}` for authenticated submission and web compose, `ip:{source ip}` for MX (not the forgeable envelope sender; behind a trusted relay everyone shares one key). Over the cap RCPT answers 452 4.5.3 for the whole group, web compose 429. `GroupStore.PostingAllowed` enforces the posting policy (`anyone`, `domain`, `members`, `owners`) at RCPT (550 5.7.1; MX is never authenticated, so only `anyone` passes there) and in web compose (403, whole send refused before any write). It takes the authenticated account only, never the envelope sender, the From header or a send-as address; `domain` means the account's primary address domain equals the group's domain (alias domains do not count); an unknown policy or a missing profile refuses. A node on an earlier binary runs the old hook and lets anyone post, so upgrade every node before setting a policy other than `anyone`. No new key or command, but a node on an older binary answers 550 for a group address (fail closed), so group mail bounces if it reaches one: upgrade every node before creating a group. `EnqueueLocal` is still not atomic and a group makes up to 1000 envelopes of that partial-failure window.

- **Bayes training is one composite Raft command** (`/__bayes_train__/{user}`, `internal/store/bayescommand.go`) counted inside the FSM, capped at 1024 tokens. An older-binary node applies it as a literal key and counts nothing, so replicas diverge for as long as the cluster runs mixed versions. Upgrade every node before anyone trains; counts trained during a mixed window are not recoverable by replay.

- **App password last-used is a conditional composite command** (`/__apppassword_touch__/`, `internal/store/apppasswordcommand.go`), never a Put of the whole record: a read-modify-write Put from the login path could land after a revoke's Delete and bring a revoked credential back with a working hash. The FSM does nothing when the record is absent or already at that day. Create and revoke stay plain Put and Delete. An older-binary node stores the touch as a literal key and its last_used_at lags; authentication is unaffected, but upgrade every node together. Credentials are verified only through `LocalAuth.AuthenticateProtocol`, which costs exactly one bcrypt comparison on every branch and holds the per-account switch (`UserProfile.require_app_passwords`, enforced only with `totp_enabled`, and part of `ProtocolEpoch`, so turning it on ends sessions opened with the account password); `Authenticate` and `AuthenticateProfile` stay account-password-only. `AuthenticateProtocol` also returns the lookup ID of the app password used, "" for the account password; a session keeps it and passes it to `AccountState`, so a revoke ends that session on its next command, or within the 30-second recheck in IDLE. Callers forgive limiter failures only when that ID is "": an app password proves nothing about the account password, and forgiving would give its holder unlimited guesses. Log a protocol user name only through `auth.LogUsername`, because a client with swapped fields sends the password there. A node on an earlier binary ignores `require_app_passwords` and has no `AuthenticateProtocol`, so while a cluster runs mixed versions it accepts the account password with the switch on and refuses app passwords; a flag can also survive a 2FA re-enrolment there and be enforced after the upgrade (the off route recovers it). Give any test applier that sees a real protocol login an `IsAppPasswordSyntheticCommand` dispatch, or the touch is stored literally and negative tests pass vacuously.

- **The device link is redeemed by a conditional composite command** (`/__apppassword_ticket__/consume`, `internal/store/apppasswordticket.go`), so a QR link works exactly once across nodes and under concurrent fetches. The FSM refuses (returns `store.ErrTicketRefused`, writes nothing, logs no error) when the record is absent, already used, or `at >= expires_at`; the caller tells winner from loser with `errors.Is`. `at` is in the payload: the FSM never reads the clock. An older-binary node stores the consume as a literal key and leaves the ticket unused, so replicas diverge for as long as the cluster runs mixed versions: upgrade every node together. There is no follower forwarding, so a link opened on a follower is refused unspent; it works only when the request reaches the leader (at-most-once, not available everywhere). Redemption (`internal/web/handlers/apppassword_link.go`) re-checks at open time what can change after the link was made: account disabled, deleted or a resource, password or session epoch moved, `require_2fa` started, cap reached; those pre-checks write nothing. Consume comes before `AppPasswords.Create`, so delivery is at-most-once. The route is on the root router outside session and CSRF because it reads and sets no cookie and the token is the credential; it has its own per-IP throttle (login limits, `middleware.ClientIP`), and every refusal is one fixed 404 with `Referrer-Policy: no-referrer`. The profile host comes from the ticket, never from the phone's `Host`. Never log the token or the path. Give any test applier that sees a redemption an `IsAppPasswordTicketCommand` dispatch and serialise applies as the FSM does, or the second fetch succeeds and concurrency tests pass vacuously.

- **Remote-account fetch holds third-party passwords; its worker and page are live once `fetch.credentials_key` is set** (`internal/fetch`, `internal/netx/maildial.go`, `internal/store/fetch.go`, `internal/agent/fetchworker.go`, `internal/web/handlers/fetch.go`, user docs in `docs/fetching-mail.md`). The rules every change keeps: (1) **The key is never in Pebble, a snapshot, a backup or the data dir.** `fetch.credentials_key` (std base64 of exactly 32 bytes) and `previous_credentials_keys` (decrypt-only, for rotation) come from the config or `BONGO_FETCH_CREDENTIALS_KEY`; startup is refused when a config file that holds them lies inside `storage.data_dir` (`Config.ValidateFetchKeyPlacement`); validation errors name the key, never its value; the current key must differ from every previous key and from `web.session_secret`. Every node of a cluster needs the same keys: a node with no key serves the page read-only and its worker does not start; only a leader holding a different key marks accounts `needs_password`. Dev with no key may use `Config.ResolveFetchKeys`' throwaway key. (2) **A sealed password is bound to its context.** `credcrypt.Seal` uses AES-256-GCM, a fresh random 96-bit nonce per seal, and associated data that length-prefixes the user id, account guid, protocol, security, host, user name and key id, so a record cannot be moved to another user or account, and an account cannot be pointed at another host or user name without the password being entered again: the handler must refuse a blank password when any of protocol, security, host or username changed, and must never render the password. `Open` returns only `ErrKeyUnknown` or `ErrCredentialInvalid` (both map to `needs_password`) and no route accepts ciphertext. (3) **Every connection goes through `netx.MailDialer`**, whose Control hook (`controlFor`, shared literally with the HTTP safe client) judges every resolved address on every attempt, and only ports 993, 995, 143 and 110 are dialled; the port is derived from protocol and security (`fetch.PortFor`), never typed. `fetch.allow_private_networks` lifts the address rule only. The tests inject a loopback dialer through the `fetch.Dialer` interface; there is no config path to it. (4) **TLS only, verified, no fallback.** `ServerName` is the typed host, TLS 1.2 minimum, no option to skip verification, STARTTLS and STLS failures are `ErrTLS`, bytes buffered after the POP3 STLS `+OK` refuse the connection, PREAUTH on STARTTLS is refused. (5) **No server text is kept.** Errors are the `fetchx` sentinels and a state or UI code (`fetch.Code`); the go-imap `DebugWriter` is never set (a test scans for it). Every connection has a byte budget, an idle deadline and the caller's context deadline. (6) **Delivery order is at-least-once:** spool acceptance, then the seen record, then the remote delete, so a crash can import one message twice and never lose one; IMAP deletes only flag `\Deleted` and expunge only the listed UIDs with UIDPLUS, never a plain EXPUNGE or CLOSE; POP3 deletions take effect at QUIT. (7) `QueueEnvelope.fetch_account` (field 25) is set only by the fetcher. It grants no trust, no SPF result and no authenticated sender (null envelope sender, no AuthUserId, no SourceIp), and content scoring, DKIM and Bayes all run. Its one effect is in the antispam stage: a DMARC `p=reject` verdict on a fetched message is capped at Junk (action "tag") instead of the admin quarantine, because SPF can never align for fetched mail and the remote provider already applied DMARC at its own MX; a harsher scorer verdict is never softened. (8) The leader worker (`internal/agent/fetchworker.go`) skips disabled, resource and shared users and a low-disk node, and a run stopped part way still deletes remotely only what already has a seen record. It re-reads the account before any remote delete and skips the delete when the account is gone or its `generation` changed; it trusts a state only when the state's generation equals the account's; it marks a message whose body overflows its announced size as seen too-large and ends the run (nothing is deleted for it). Each stored message gets an `X-Bongo-Fetched` header whose token derives from the credentials key and the account (`Keyring.LoopTokens`, current key first, then the previous keys, so rotation keeps loop detection); a message carrying one is marked seen and not stored again. The token is visible in stored mail, so anyone who learns it can make that account skip a message (it stays on the remote, never imported): accepted. (9) **The page** (`handlers.FetchHandler`) never renders or logs the password, the ciphertext or the remote user name (logs carry user id, account guid, host and `fetch.Code`; no host for an unsaved test). Create is refused with 409 at five accounts, then validated (protocol imap|pop3, security tls|starttls, `PortFor != 0`, host through `netx.NormalizeMailHost`, interval in {15, 30, 60, 180, 360, 720, 1440}); a blank password on update keeps the credential only while protocol, security, host and user name are unchanged (else 422, record untouched), and a new password is sealed AFTER the new fields are applied because the associated data covers them. A connection test of a saved account uses only the stored host, port and sealed password, never form values. Test and resume share one 10-per-hour per-user budget (plus 120 per node, key `*`), answer 429 with Retry-After, and tests also take one of 4 per-node slots (503 when busy). The routes are registered with `FetchHandler.Register` inside the authenticated, CSRF-protected group, `/settings/fetch/test` before `/settings/fetch/{guid}`; the CSRF router test pins every POST. Create, and an update that sets a password, changes protocol, security, host or user name, or clears a pause, also spend that budget (a save bumps `generation`, which lets the worker try the password at once); label, interval, folder and leave-on-server edits are free. A blank password on update is refused (422) when the stored credential no longer opens. (10) **Worker bounds:** one concurrent run per user and at most 4 at once (`dispatch`), so a hanging account holds one slot; at most 50 too-large skips per run and 50,000 seen records per account (over it the account records `seen_limit` and imports nothing until pruning brings it under); a download may read the message limit plus the 16 MiB control budget (`budgetConn.beginDownload`). Known and accepted: the account cap check is check-then-act, and a message delivered but not deleted remotely is marked seen and never retried.

- **Shared inboxes need every node upgraded first.** Field 16 of `UserProfile` survives an older node's profile rewrite (unknown fields are kept) and an older node fails closed on send-as for a shared address, but its `CheckStanding` and admin `ResetPassword` refuse only `resource`: an admin on an older node could set a password on a shared account and it would then log in there (web, IMAP, POP3, SMTP, DAV). Upgrade every node before creating a shared inbox. Grants on a shared owner are managed only by the admin member handlers (`SyncSharedInboxGrants`); nobody holds `a` there, and the orphan sweep drops a grant for a principal not in `shared_members`. Test appliers that see a sync need an `IsACLSyntheticCommand` dispatch (`IsGroupSyntheticCommand` too when a group is a member), serialised as the FSM is.

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
