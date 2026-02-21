# Phase 1 Sprint Backlog

Light overview of all Phase 1 sprints. Only the current sprint has a detailed task file.

## Sprint 01: Project Scaffolding & Storage Foundation ✅ DONE
Go module, Cobra CLI, HCL config, Pebble store, content-addressable blob store (shared flat
directory), per-user SQLite metadata index with LRU connection pooling, agent lifecycle with
three-tier storage init/shutdown, structured logging, health endpoints.

## Sprint 02: Raft Consensus & Cluster Membership ✅ DONE
Fix system.db credential issue (remove password_hash — credentials live in Pebble).
Pebble-backed Raft LogStore/StableStore. FSM that applies writes to Pebble and triggers
derived index updates. Memberlist gossip for node discovery. Leader forwarding for writes.
Blob sync HTTP endpoint (serving side). `bongo-go members` command. Multi-node integration
test: leader election, replication, failover.

## Sprint 03: Basic SMTP Inbound + Search Foundation ✅ DONE
**Cleanup from Sprint 02:**
- Fix `PebbleStore.DeleteRange` end-key semantics (current `append 0x00` works but should
  use proper successor calculation consistent with `prefixUpperBound` for robustness).
- Replace deprecated `filepath.HasPrefix` in `internal/cluster/tar.go` with
  `strings.HasPrefix(target, dir+string(os.PathSeparator))` for correct path-traversal
  prevention.

**New work (10 tasks):**
Protobuf definitions for Pebble values (RaftCommand, MailMeta, BlobRef, UserProfile) —
replace JSON Raft commands with protobuf. SMTP server via emersion/go-smtp (port 25/587,
STARTTLS). Mail delivery pipeline: parse with go-message/enmime → blob store → Pebble via
Raft → SQLite + Bleve indexes. Auth backend: bcrypt from Pebble profiles, `bongo-go user
create` CLI. Blob pull-from-peers (followers fetch missing blobs via HTTP from gossip peers).
Per-user Bleve index for mailbox search. Global Bleve GAL index for autocomplete. Wire
SMTP + search into agent lifecycle (dev mode auto-creates test user on port 2525).
Integration test: send email → verify all three storage tiers + search.

## Sprint 04: Integration & Dev Mode Polish ✅ DONE
**Cleanup from Sprint 03:**
- Remove unused `verifyHash` function in `internal/cluster/blobsync.go` (dead code).
- Clarify `parts` variable scoping in `internal/cluster/fsm.go` `updateDerivedIndexes()` —
  move profile check inside the `len(parts) >= 4` guard or re-split for readability.
- Add self-signed TLS certificate auto-generation in dev mode for SMTP STARTTLS
  (currently falls through to plaintext; spec called for self-signed cert).

**New work:**
Wire everything together: agent starts SMTP + Raft + health endpoints in dev mode.
Network-based Raft snapshots (new node bootstrap). TLS between nodes. Graceful shutdown
of all subsystems. CI pipeline: GitHub Actions with lint, build, test (race + cover).
Dynamic node join endpoint. Integration test helpers.

## Sprint 05: IMAP Server & Mailbox Access ✅ DONE
**Cleanup from Sprint 04:**
- Remove unused `verifyHash` function in `internal/cluster/blobsync.go` (still present).
- Add dev mode quick-start documentation (README section or standalone doc).
- Implement E2E test: start dev agent, send email via SMTP, query via health API
  (`test/e2e/` directory exists but is empty).

**New work (8 tasks):**
IMAP server via emersion/go-imap v2 for mailbox access. Mailbox management (INBOX,
Sent, Drafts, Trash, custom folders) stored in Pebble. IMAP IDLE for push notifications.
Message flags (Seen, Answered, Flagged, Deleted, Draft). EXPUNGE support. Mailbox
listing and STATUS. SEARCH command backed by Bleve. UID tracking and sequence number
mapping. Wire IMAP into agent lifecycle. Integration test: deliver mail via SMTP, read
via IMAP client, verify flags and search.

## Sprint 06: HTMX Web UI & Search Polish ✅ DONE
**Cleanup from Sprint 05:**
- Wire IMAP SEARCH to Bleve instead of in-memory string matching
  (`internal/imap/search.go` mapping IMAP criteria → Bleve queries).
- Add `messages`, `recent`, `unseen` fields to `MailboxMeta` proto (or document the
  dynamic computation approach as intentional).
- Add unit tests for `internal/imap/` package (server, backend, notify).

**New work (8 tasks):**
HTMX web UI using Templ (type-safe Go templates), HTMX, Alpine.js, and Pico CSS.
Mailbox listing page, message list with pagination, message detail view with HTML
sanitization, compose/reply form with SMTP submission, folder management, search UI
backed by Bleve, login page with session cookies, and responsive layout. Wire web
handlers into agent lifecycle. Integration test: login, browse inbox, read message,
compose reply.

## Sprint 07: Outbound SMTP Relay & Message Queue ✅ DONE
Outbound mail delivery to external servers. `internal/queue/` message queue with
persistent storage in Pebble, retry with exponential backoff, bounce generation on
permanent failure. MX record lookup and delivery. DKIM signing (ed25519 + rsa-sha256).
SPF checking on inbound. Rate limiting per sender. Wire into compose "Send" flow so
external recipients actually receive mail. `bongo-go queue status` CLI. Integration
test: send to external, verify DKIM header, test bounce on bad domain.

## Sprint 08a: Dragonfly Foundation — Threading, Preferences & Core Features ✅ DONE
Add threading fields to MailMeta proto (`in_reply_to`, `references`, `cc`).
UserPreferences proto and PreferenceStore (theme, layout, density, signature,
timezone, messages_per_page, default_reply). Dark mode toggle via `appState()`
Alpine component. Conversation threading backend (ThreadStore) and UI (thread
grouping with expand/collapse badges). Bulk operations toolbar (checkboxes,
mark read/unread, flag, delete, move). Virtual folders (Unread, Starred, All Mail)
with SSE badge updates. Integration tests for threading, bulk ops, preferences,
virtual folders.

## Sprint 08b: Dragonfly Polish — Layouts, Gestures & Mobile ✅ DONE
Reading pane layouts (list / two-pane / three-pane toggle persisted in preferences).
Drag-and-drop messages between folders (Sortable.js). Right-click context menu on
messages (Alpine.js). Desktop browser notifications for new mail (Notification API).
Mobile swipe gestures (archive left, delete right). UI polish: density classes,
signature in compose, messages-per-page from preferences, default reply mode.
Responsive mobile sidebar with hamburger toggle and slide-in animation. Integration
tests for reading pane, drag-drop, density, signature, messages-per-page.

## Sprint 09: Contacts & Address Book (CardDAV) ✅ DONE
Wire `internal/caldav/` for CardDAV (RFC 6352) via `emersion/go-webdav`. Contact
storage in Pebble: `/user/{id}/contact/{guid}` with VCard protobuf. Contact CRUD
in web UI: list, create, edit, delete with inline editing. Contact groups/distribution
lists. vCard import/export (`.vcf` file upload/download). Contact photo storage in
blob store. Merge duplicate contacts. Link contacts to mail senders (click sender →
view/create contact). Integrate address book with compose autocomplete (supplement
GAL with personal contacts). Contact search via Bleve (name, email, phone, company).
CardDAV sync so contacts work with macOS Contacts, iOS, DAVx5. Integration test:
create contact via web UI, sync via CardDAV client, verify in compose autocomplete.

## Sprint 10a: Calendar Foundation — CalDAV, Event CRUD & Month View ← CURRENT
Event and Calendar protos + stores in Pebble (`/user/{id}/event/{guid}`,
`/user/{id}/calendar/{guid}`). CalDAV server (RFC 4791) via `emersion/go-webdav`
for sync with Apple Calendar, Thunderbird, DAVx5. Month-view web UI with event
chips on day cells, HTMX prev/next navigation. Event CRUD: create/edit/delete with
start/end time, location, description, all-day toggle. iCalendar import/export.
Event search via Bleve. Default calendar auto-created. Integration tests for CRUD,
CalDAV sync, and iCal round-trip.

## Sprint 10b: Calendar Polish — Views, Recurrence, Reminders & Sharing
Week and day views with time-positioned event blocks. Recurring events (RRULE:
daily/weekly/monthly/yearly with exception dates). Drag-and-drop event rescheduling
in week/day views (snap to 30-min). Mini calendar navigator in sidebar. Multiple
calendars with colour coding and visibility toggle. Event reminders/alarms via SSE
browser notifications (configurable 5/10/15/30/60 min before). Shared calendar
display (read-only). Free/busy lookup for scheduling. Integration tests for views,
recurrence, drag-drop, sharing.

## Sprint 11: Admin Panel (Hawkeye)
Replicate the original Bongo "Hawkeye" admin interface. Admin role flag on
UserProfile protobuf. Admin middleware (require admin flag). Dashboard: system
health (Raft leader status, node count, uptime), storage usage (Pebble size, blob
count, total bytes), active connections (SMTP, IMAP, HTTP), message throughput
graph (messages/hour via Bleve date facet). User management: list all users with
search/filter, create/disable/delete users, per-user quota management (quota_bytes
in UserProfile), password reset, force logout. Domain management: add/remove
domains, MX record verification check. Email alias management: create/edit/delete
aliases (`/system/alias/{alias}` in Pebble). Queue management: view pending/failed
messages, retry/drop individual items, flush queue. Cluster management: node list
with health status, promote/demote voters, decommission node. Log viewer: tail
structured logs with level/component filters (stream via SSE). All admin pages use
the same HTMX/Templ/Pico CSS stack as user UI. Integration test: admin login, user
CRUD, domain management, queue inspection.

## Sprint 12: POP3, Mail Filters & Antispam
POP3 server via `internal/pop3/` for legacy client support (RFC 1939). Basic
commands: USER, PASS, STAT, LIST, RETR, DELE, QUIT. Wire into agent lifecycle
with configurable ports (110/995). Mail filter rules engine (`internal/rules/`):
Sieve-like DSL stored per user in Pebble, evaluated in delivery pipeline before
mailbox placement. Rule actions: move to folder, mark read, flag, delete, forward,
auto-reply. Rule conditions: from, to, subject, header match, size, has attachment.
Web UI for rule management: list, create, edit, reorder priority, test against
existing messages. Antispam scoring (`internal/antispam/`): SPF verification
(already partially done), DKIM verification on inbound, basic header analysis
(missing Message-ID, suspicious Date, known spam patterns), per-message spam score
stored in MailMeta, Junk folder auto-filing above threshold, "Mark as spam" /
"Not spam" buttons in web UI that train a simple Bayesian filter. Integration test:
POP3 round-trip, filter rule evaluation, spam scoring and junk filing.

## Sprint 13: Web UI Polish & Production Readiness
Final UI polish pass to reach production quality. Accessibility audit: ARIA labels,
keyboard navigation for all interactive elements, screen reader testing, focus
management on HTMX swaps. Internationalisation (i18n): extract all UI strings to
message catalogue, add locale selector in preferences (start with en, fr, de, es).
Email template rendering improvements: inline CSS for HTML mail display, remote
image blocking with click-to-load, print-friendly view. Attachment preview: inline
image display, PDF viewer (pdf.js), text file syntax highlighting. Import/export:
mbox import for mailbox migration, EML file download. Login security: rate limiting
on login attempts, TOTP two-factor auth (totp_secret already in UserProfile proto),
QR code setup flow in preferences. HTTPS redirect and HSTS header. CSP headers for
XSS prevention. Performance: lazy-load message list on scroll (infinite scroll
option), prefetch next page via HTMX `hx-trigger="revealed"`, compress SSE payloads.
E2E test suite: Playwright or similar headless browser tests covering full user
journeys (login → compose → send → receive → search → calendar → contacts → logout).
