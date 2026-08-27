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

## Sprint 10a: Calendar Foundation — CalDAV, Event CRUD & Month View ✅ DONE
Event and Calendar protos + stores in Pebble (`/user/{id}/event/{guid}`,
`/user/{id}/calendar/{guid}`). CalDAV server (RFC 4791) via `emersion/go-webdav`
for sync with Apple Calendar, Thunderbird, DAVx5. Month-view web UI with event
chips on day cells, HTMX prev/next navigation. Event CRUD: create/edit/delete with
start/end time, location, description, all-day toggle. iCalendar import/export.
Event search via Bleve. Default calendar auto-created. Integration tests for CRUD,
CalDAV sync, and iCal round-trip.

## Sprint 10b: Calendar Polish — Views, Recurrence, Reminders & Sharing ✅ DONE
Week and day views with time-positioned event blocks. Recurring events (RRULE:
daily/weekly/monthly/yearly with exception dates). Drag-and-drop event rescheduling
in week/day views (snap to 30-min). Mini calendar navigator in sidebar. Multiple
calendars with colour coding and visibility toggle. Event reminders/alarms via SSE
browser notifications (configurable 5/10/15/30/60 min before). Shared calendar
display (read-only). Free/busy lookup for scheduling. Integration tests for views,
recurrence, drag-drop, sharing.

## Sprint 11: Admin Panel (Hawkeye) ✅ DONE
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

## Sprint 12a: POP3 Server ✅ DONE
POP3 server via `internal/pop3/` for legacy client support (RFC 1939). Commands:
USER, PASS, STAT, LIST, RETR, DELE, QUIT, UIDL, TOP, CAPA, STLS, NOOP, RSET.
Wire into agent lifecycle with configurable ports (110/995, dev 1110/1995). INBOX
snapshot model, byte-stuffing, STLS upgrade. Integration tests for auth, retrieval,
delete, TLS.

## Sprint 12b: Antispam Scoring ✅ DONE
Add spam_score/spf_result/dkim_result/dkim_domain fields to MailMeta proto. Inbound
DKIM verification via DNS. Heuristic spam scorer (SPF, DKIM, header analysis,
phrase patterns) producing 0.0–1.0 score. Hook into delivery pipeline — auto-route
to Junk above threshold. Per-user Bayesian filter trained by "Mark as Spam" / "Not
Spam" web UI actions. Bleve indexes spam_score. Integration tests for auto-junk,
clean mail routing, mark spam, Bayesian training.

## Sprint 12c: Mail Filter Rules ✅ DONE
Per-user server-side filter rules. FilterRule proto with conditions (from, to,
subject, body, header, size, has_attachment), operators (contains, equals, matches,
not_contains, greater/less_than), actions (move, copy, flag, mark_read, delete).
Rule engine evaluates in priority order (AND logic). Hooks into delivery pipeline
after spam scoring. Web UI: list with drag reorder, create/edit/delete forms with
dynamic Alpine.js condition rows, rule testing against recent mail. Integration
tests for routing, priority, disabled rules, CRUD, spam override.

## Sprint 13a: Security & Login Hardening ✅ DONE
Login security: rate limiting on login attempts, TOTP two-factor auth (totp_secret
already in UserProfile proto), QR code setup flow in preferences, recovery codes.
HTTPS redirect and HSTS header. CSP headers for XSS prevention. Security headers
(X-Frame-Options, X-Content-Type-Options, Referrer-Policy). Remote image blocking
in HTML mail display with click-to-load. Integration tests for rate limiting, TOTP
flow, security headers.

## Sprint 13b: Email Rendering, Attachments & Import/Export ✅ DONE
Email template rendering improvements: inline CSS for HTML mail display, safe HTML
sanitization, print-friendly view. Attachment preview: inline image display, PDF
viewer (pdf.js embed), text/code file syntax highlighting. Import/export: mbox
import for mailbox migration (parse mbox → deliver each message), EML file
download for individual messages. Integration tests for HTML rendering, attachment
preview, mbox import round-trip.

## Sprint 13c: Accessibility, i18n & Performance ✅ DONE
Accessibility audit: ARIA labels, keyboard navigation for all interactive elements,
focus management on HTMX swaps, skip-to-content links, screen reader landmarks.
Internationalisation (i18n): extract all UI strings to message catalogue, add locale
selector in preferences (start with en, fr, de, es), templ helper for translations.
Performance: lazy-load message list on scroll (infinite scroll option), prefetch
next page via HTMX `hx-trigger="revealed"`, compress SSE payloads, HTTP response
compression (gzip). E2E smoke test covering full user journey (login → compose →
send → receive → search → calendar → contacts → admin → logout).

## Sprint 13d: i18n Template Wiring ✅ DONE
Wire all 14 `.templ` files to use `i18n.T()` instead of hardcoded English strings.
Add `t(ctx, messageID)` template helper. Replace ~175 hardcoded strings across
layout, mailbox, message, compose, contacts, calendar, admin, settings, security,
login, rules, search, and import templates. Verify message catalogue completeness
with coverage test. Integration tests for French rendering, fallback, and context
propagation.

## Sprint 14: Visual UI Matching — Old Bongo Dragonfly/Hawkeye ✅ DONE (CSS infrastructure)
Use browser agent to view the old Bongo UI screenshots from
https://github.com/bongo-project/bongo-project/tree/gh-pages/images (11 Dragonfly
screens + 13 Hawkeye screens at various resolutions). Also try running the old UI from the
original project's `src/www/` static HTML/JS sources (a separate checkout of
bongo-project/bongo-project, not vendored here). Compare side-by-side with the new
bongo-go web UI. Iterate on CSS, layout, spacing, colours, iconography, and interaction
patterns to match the original look and feel as closely as possible while keeping the
modern stack (HTMX, Alpine.js, Pico CSS, Open Props). Focus on: mail list density,
message reading experience, compose flow, calendar views, contact cards, admin dashboard
layout. This sprint is visual/design-driven rather than feature-driven.

## Sprint 15a: Polished Mail UI — Static Prototype & Port ✅ DONE
Build static HTML prototypes for every mail-facing page (login, inbox, message thread,
compose, search, contacts, calendar, settings, security, import). Use old Dragonfly
CSS as the direct starting point — modernise it rather than rebuilding from scratch.
Get each page looking beautiful in standalone HTML, then port the final CSS and any
template structure changes back into the Go codebase. Focus: warmth, depth, visual
rhythm, not just "correct colour variables."

## Sprint 15b: Polished Admin UI — Static Prototype & Port ✅ DONE
Same prototype-first approach for the Hawkeye admin panel. Dark sidebar with colourful
icons, status dashboard with blue status table, agent config forms, user/domain
management. Port polished CSS back into admin.templ and bongo.css.

## Sprint 16: UI Fixes, Bulk Import & Server Validation — 🔄 CURRENT
Fix broken layout (overlapping top bar/sidebar, oversized sidebar, broken compose
page, unreadable form inputs, broken links), fix VCF contact import bug, add
HTTP-only mode for load balancer deployments, add `bongo-import directory` bulk
scanner command, ship curated demo dataset in `testdata/demo/`, and create a
comprehensive server validation script that exercises every subsystem (multi-user
SMTP, IMAP, POP3, antispam, outbound bounce, contacts, calendar, search, auth).
12 tasks, 30 exit criteria.

## Future (Phase 2): Easy Mail Migration — "Bring Your Inbox"
Non-technical users need a dead-simple way to import their old email. Two modes:

**Self-service (user-facing):** Settings → Import → pick provider (Gmail, Outlook,
Yahoo, iCloud, Other IMAP). For Gmail/Microsoft: OAuth2 flow (user clicks "Sign in
with Google" → grants read-only access → Bongo pulls all mail via IMAP with XOAUTH2).
For others: enter IMAP host + email + app password. Wizard-style UI: connect → pick
folders to import → go. Background worker with progress bar + SSE updates ("Importing
Sent Mail... 342 of 1,203"). Resume on failure. Folder mapping suggestions (Gmail
labels → Bongo folders). Preserve dates, flags, read/unread status, threading.

**Admin bulk migration:** Admin panel → Migration tool. Upload CSV (user email, source
IMAP host, credentials) or connect to source server. Run imports for all users in
parallel with per-user progress dashboard. Useful for org cutover from Google Workspace
or Exchange/365. Could also support Google Takeout mbox files (users export from Google
and upload the .mbox to Bongo).

Key requirement: must work for non-technical users. No CLI, no config files. Just
"click here to bring your Gmail over."

## Future: Migrate Old Bongo Website to GitHub Pages
The bongo-project/bongo-project repo has a `gh-pages` branch with images and some site
content. Migrate the full old Bongo project website (docs, screenshots, downloads page)
to GitHub Pages so it's preserved and publicly accessible. May need to pull content from
bongo-project.org archives or reconstruct from available sources.

## UI/UX & Feature Backlog (from example.com real-world testing, 2026-07)

- [ ] **Rich / visual email signatures.** Currently plain-text only
  (`UserPreferences.Signature`, a `<textarea>` in Settings, appended by
  `compose.go signatureBody()` as `"\n\n-- \n" + sig`). Add an HTML signature (logo,
  colours, formatting) with a plain-text fallback: store HTML (plus a derived/editable
  plain version), send `multipart/alternative` (text/html + text/plain), wire into the
  TipTap composer, and embed the logo via a `cid:` inline attachment or base64 data URI
  (web CSP `img-src` already allows `cid:` and `data:`).

- [ ] **Visual consistency: bring the main UI up to the admin UI.** The admin interface
  (`AdminLayout` / `admin.templ`) looks noticeably more polished than the main mail and
  calendar UI. Align styling, spacing, and component treatment.

- [ ] **Inbox action buttons look like buttons.** The mailbox toolbar actions currently
  render as plain text / links (`tool-btn`). Give them a clear button treatment so the
  available actions are obvious.

- [ ] **Calendar event-creation discoverability.** No visible "New event" affordance —
  add one. Also support double-click to create: on a day cell (month view) and on a time
  slot (week/day view). Today the only entry point is clicking the day *number* (the plain
  `<a href="/calendar/event/new?date=...">` in `CalendarMonthGrid`).

- [ ] **Display vCard address labels on contacts.** vCards can carry an address label —
  the `LABEL` property (vCard 3.0: `LABEL;TYPE=home:` formatted block) or `LABEL="..."`
  parameter (4.0) — in addition to the `TYPE` (home/work) already shown and edited. Parse
  and display the label text on the contact view.
