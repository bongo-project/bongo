# Sprint 06: HTMX Web UI & Search Polish

**Phase:** 1 (Foundation)
**Goal:** Build a functional web-based email client using Templ, HTMX, Alpine.js, and Pico CSS. Fix IMAP search to use Bleve. Add missing IMAP unit tests.

---

## Context

Sprints 01–05 built the full backend: storage, Raft, SMTP inbound, search, IMAP.
Users can send and read mail via standard clients (Thunderbird, Apple Mail). This
sprint adds the HTMX web UI so users can access mail from a browser with zero
client setup.

**Tech stack** (from CLAUDE.md):
- **Templates:** `a-h/templ` — type-safe, compiled to Go
- **CSS:** Pico CSS (~10KB, semantic HTML, class-light)
- **Client JS:** HTMX (partial page swaps) + Alpine.js (client-side state) + Sortable.js (drag/drop)
- **Rich text:** TipTap (email composer)
- **Real-time:** SSE (Server-Sent Events) for new mail notifications

**Existing infrastructure:**
- `internal/web/handlers/` — chi router with health, cluster, kv, blob, user, join endpoints
- `internal/web/templates/` — empty directory, ready for Templ files
- `internal/web/static/` — empty directory, ready for CSS/JS assets
- `internal/store/mailbox.go` — MailboxStore with full CRUD + flag operations
- `internal/store/search.go` — Bleve SearchManager with IndexMail, Search, SearchGAL
- `internal/auth/local.go` — LocalAuth with bcrypt password verification

## Cleanup from Sprint 05

### 0a. Wire IMAP SEARCH to Bleve

**Why:** IMAP SEARCH currently uses in-memory `strings.Contains()` matching in
`matchesCriteria()`. The Bleve infrastructure in `internal/store/search.go` already
supports full-text queries but isn't wired into IMAP.

**Changes:**
- Create `internal/imap/search.go`:
  - `mapSearchCriteria(criteria, userID string) (*bleve.SearchRequest, error)`
  - Map IMAP keys to Bleve:
    - `SUBJECT "text"` → Bleve match query on `subject` field
    - `FROM "text"` → Bleve match query on `from` field
    - `TO "text"` → Bleve match query on `to` field
    - `BODY "text"` / `TEXT "text"` → Bleve match query on `body` / all fields
    - `SINCE date` / `BEFORE date` → Bleve date range on `date` field
    - `OR`, `NOT`, `ALL` → Bleve boolean queries (must/must_not/should)
  - Flag-based criteria (`SEEN`, `UNSEEN`, `FLAGGED`, etc.) stay as post-filter
    against Pebble since Bleve doesn't index flags.
- Update `internal/imap/backend.go`: Replace `matchesCriteria()` calls with
  Bleve-backed search, falling back to in-memory filter only for flag criteria.
- Add unit test in `internal/imap/search_test.go`.

**Done when:** IMAP `SEARCH SUBJECT "hello"` uses Bleve. Integration test still passes.

### 0b. MailboxMeta proto field documentation

**Why:** `MailboxMeta` proto is missing `messages`, `recent`, `unseen` count fields.
These are computed dynamically via `CountMessages()` prefix scan. This is a deliberate
trade-off (no stale counters) but should be documented.

**Changes:**
- Add a comment block in `proto/bongo/v1/mailbox.proto` explaining the design decision:
  counters are computed on demand from Pebble prefix scans rather than maintained as
  proto fields, to avoid counter drift under concurrent Raft applies.
- If performance profiling later shows the prefix scans are too slow, the counters can
  be added as cached fields with invalidation.

**Done when:** Proto file has clear documentation of the design decision.

### 0c. IMAP package unit tests

**Why:** `internal/imap/` has no `*_test.go` files. Integration tests cover happy paths
but edge cases and error handling are untested.

**Changes:**
- `internal/imap/server_test.go`: Test server start/stop lifecycle, TLS config loading.
- `internal/imap/backend_test.go`: Test session login (valid/invalid), mailbox listing,
  SELECT with empty mailbox, FETCH with missing blob (error path), flag operations.
- `internal/imap/notify_test.go`: Test subscribe/unsubscribe, notification fan-out,
  channel buffer overflow behavior, concurrent access.

**Done when:** `go test -race ./internal/imap/...` passes with meaningful coverage.

---

## Tasks

### 1. Templ setup and base layout

Install `a-h/templ` and create the base layout that all pages share.

**Changes:**
- `go.mod`: Add `github.com/a-h/templ`
- Add `go generate` directive for `templ generate`
- `internal/web/templates/layout.templ`:
  - HTML5 doctype, `<head>` with Pico CSS CDN, HTMX CDN, Alpine.js CDN
  - `<body>` with navigation sidebar (mailbox list) and main content area
  - Navigation: links for INBOX, Sent, Drafts, Trash, Junk + custom folders
  - Top bar: search input, user menu (profile, logout)
  - Flash message area for success/error notifications
  - `{children}` slot for page-specific content
- `internal/web/templates/components.templ`:
  - `MailboxLink(name string, unread int, selected bool)` — sidebar item with unread badge
  - `Pagination(page, totalPages int, baseURL string)` — HTMX-powered page nav
  - `FlashMessage(msg, level string)` — dismissible notification
  - `EmptyState(icon, title, subtitle string)` — placeholder for empty mailboxes

**Done when:** `templ generate` compiles without errors. Base layout renders correctly
in a browser with Pico CSS styling.

### 2. Authentication: login page and session middleware

Create login/logout flow with session cookies.

**Changes:**
- `internal/web/templates/login.templ`:
  - Email + password form, POST to `/login`
  - Error message display on bad credentials
  - "Remember me" checkbox (extends session expiry)
- `internal/web/handlers/auth.go`:
  - `GET /login` — render login page (redirect to `/mail/INBOX` if already logged in)
  - `POST /login` — validate via `auth.LocalAuth`, create session cookie, redirect
  - `POST /logout` — clear session cookie, redirect to `/login`
  - Session cookie: `bongo_session`, HttpOnly, Secure, SameSite=Lax
  - Session store: signed cookie with `crypto/hmac` (stateless) containing
    `{userID, email, exp}` — no server-side session storage needed
  - Secret key from config `web.session_secret` (auto-generated in dev mode)
- `internal/web/middleware/auth.go`:
  - `RequireAuth` middleware: validate session cookie, inject `userID` + `email`
    into `context.Context`, redirect to `/login` if expired/invalid
  - `UserFromContext(ctx) (userID, email string)` helper
- Wire into chi router:
  - Public routes: `/login`, `/health`, `/internal/*`
  - Protected routes: `/mail/*`, `/search/*`, `/settings/*`, `/logout`

**Done when:** Login with admin@localhost / admin sets a cookie. Subsequent requests
to `/mail/INBOX` succeed. Expired/missing cookie redirects to login.

### 3. Mailbox list and message list page

The main inbox view: sidebar with folder list, main area with paginated message list.

**Changes:**
- `internal/web/templates/mailbox.templ`:
  - `MailboxPage(mailbox string, messages []MessageSummary, page, total int)`
  - Message list table: checkbox, star, from, subject (truncated), date, size
  - Unread messages in bold (no `\Seen` flag)
  - Click row → HTMX `hx-get="/mail/{mailbox}/{uid}"` swaps main content area
  - Toolbar: Refresh, Delete, Move to…, Mark read/unread
  - Pagination: `hx-get="/mail/{mailbox}?page=2"` with `hx-target="#message-list"`
- `internal/web/handlers/mail.go`:
  - `GET /mail/{mailbox}` — list messages, paginated (25 per page default)
    - Query `MailboxStore.ListMessages(userID, mailbox, offset, limit)`
    - For each message, load `MailMeta` from Pebble for subject/from/date
    - Return full page on normal request, partial on HTMX request (`HX-Request` header)
  - Add `ListMessages` to `internal/store/mailbox.go`:
    - Prefix scan `/user/{id}/mailbox/{name}/msg/` with offset/limit
    - Return `[]MailboxMessage` with UIDs
- `internal/web/templates/sidebar.templ`:
  - `Sidebar(mailboxes []MailboxInfo, selected string)`
  - Each mailbox shows unread count badge
  - HTMX: clicking a folder swaps `#message-list` without full page reload
  - "New folder" button at bottom

**Done when:** Browse to `/mail/INBOX`, see message list with subjects and senders.
Click a different folder in sidebar, list updates via HTMX partial swap.

### 4. Message detail view

Read a single email with full headers, HTML body (sanitized), and attachments.

**Changes:**
- `internal/web/templates/message.templ`:
  - `MessagePage(msg MessageDetail)` — full message view
  - Header section: From, To, Cc, Date, Subject
  - Body: HTML sanitized with `bluemonday` (allow safe HTML, strip scripts/forms)
    - Fallback to `<pre>` for plain-text-only messages
  - Attachment list: filename, size, download link (`/mail/blob/{hash}`)
  - Action buttons: Reply, Reply All, Forward, Delete, Move, Flag/Unflag
  - "Back to list" link with HTMX to swap back to message list
- `internal/web/handlers/mail.go` (additions):
  - `GET /mail/{mailbox}/{uid}` — load message detail
    - Fetch `MailboxMessage` → `MailMeta` → raw blob from BlobStore
    - Parse with enmime for HTML/text body + attachments
    - Auto-set `\Seen` flag (unless already seen)
    - Return full page or HTMX partial
  - `GET /mail/blob/{hash}` — serve blob content with correct MIME type
    - Stream from BlobStore, set `Content-Type` and `Content-Disposition`
  - `POST /mail/{mailbox}/{uid}/flags` — HTMX endpoint for toggling flags
    - Parameters: `action` (add/remove), `flag` (seen/flagged/deleted)
    - Returns updated flag icons via HTMX swap
  - `POST /mail/{mailbox}/{uid}/move` — move message to another folder
    - Parameter: `destination` mailbox name
    - HTMX: remove message from list, show flash "Moved to {dest}"
  - `POST /mail/{mailbox}/{uid}/delete` — set `\Deleted` flag + expunge
    - HTMX: remove message row from list
- Add `bluemonday` dependency for HTML sanitization:
  - `go.mod`: `github.com/microcosm-cc/bluemonday`
  - Policy: `bluemonday.UGCPolicy()` + block `<form>`, `<iframe>`, `<object>`

**Done when:** Click a message in the list, see the full email with formatted HTML body.
Attachments are downloadable. Flag/delete/move actions work via HTMX without page reload.

### 5. Compose and reply

Email composition form with TipTap rich text editor.

**Changes:**
- `internal/web/templates/compose.templ`:
  - `ComposePage(mode string, replyTo *MessageDetail)` — compose/reply/forward
  - Form fields: To (with autocomplete from GAL), Cc, Bcc, Subject, Body
  - TipTap editor for rich text body (load from CDN or bundle)
  - Attachment upload: file input with drag-and-drop zone
  - "Send" button, "Save Draft" button, "Discard" button
  - For reply: pre-fill To, Subject ("Re: …"), quote original body
  - For forward: pre-fill Subject ("Fwd: …"), include original as quote + attachments
- `internal/web/handlers/compose.go`:
  - `GET /compose` — new message form
  - `GET /compose/reply/{mailbox}/{uid}` — reply form (pre-filled)
  - `GET /compose/forward/{mailbox}/{uid}` — forward form (pre-filled)
  - `POST /compose/send` — send the email:
    1. Parse form (to, cc, bcc, subject, body HTML, attachments)
    2. Build RFC 5322 message with MIME multipart if attachments
    3. For local recipients: deliver via `DeliveryPipeline` directly
    4. For external recipients: queue for outbound relay (stub for now —
       return error "outbound relay not yet implemented")
    5. Store copy in Sent folder via `MailboxStore.CopyToSent()`
    6. If reply: set `\Answered` flag on original
    7. Redirect to Sent folder with flash "Message sent"
  - `POST /compose/draft` — save as draft:
    1. Store message blob + MailMeta
    2. Place in Drafts mailbox
    3. Return draft UID for future editing
  - `GET /api/gal?q={prefix}` — GAL autocomplete JSON endpoint
    - Returns `[{email, display_name}]` from `SearchManager.SearchGAL()`
    - Used by Alpine.js for To/Cc/Bcc autocomplete dropdown

**Done when:** Compose a new email to a local user, click Send, message appears in
recipient's INBOX (via IMAP or web UI). Reply pre-fills correctly. GAL autocomplete
works. Draft save/resume works.

### 6. Search UI

Web search interface backed by Bleve.

**Changes:**
- `internal/web/templates/search.templ`:
  - `SearchPage(query string, results []SearchResult, page, total int)`
  - Search input in top bar: `hx-get="/search?q={query}"` with `hx-trigger="keyup changed delay:300ms"`
  - Results list: subject, from, date, snippet (highlighted match)
  - Click result → navigate to message detail view
  - Advanced search toggle: from, to, date range, has:attachment
- `internal/web/handlers/search.go`:
  - `GET /search?q={query}&page={n}` — full-text search
    - Delegate to `SearchManager.Search(userID, query, limit)`
    - Map Bleve results back to message summaries
    - Return full page or HTMX partial
  - `GET /search/advanced` — form for structured search
    - Build Bleve query from individual fields
  - Support search syntax shortcuts: `from:alice subject:meeting since:2026-01-01`

**Done when:** Type in search box, results appear after 300ms debounce. Click result
opens the message. Advanced search filters work.

### 7. SSE for real-time updates

Server-Sent Events so the web UI shows new mail without polling.

**Changes:**
- `internal/web/handlers/sse.go`:
  - `GET /events` — SSE endpoint (requires auth)
  - Subscribe to `Notifier` for the authenticated user's selected mailbox
  - Event types:
    - `newmail` — `{mailbox, uid, from, subject}` → HTMX `hx-trigger="sse:newmail"`
      inserts new row at top of message list
    - `expunge` — `{mailbox, uid}` → remove row from message list
    - `flagchange` — `{mailbox, uid, flags}` → update flag icons
  - Heartbeat every 30s to keep connection alive
  - Clean up subscription on client disconnect
- Update `internal/web/templates/layout.templ`:
  - Add `hx-ext="sse"` and `sse-connect="/events"` on the message list container
  - `hx-trigger="sse:newmail"` on the message list to prepend new messages
  - Desktop notification via Alpine.js `Notification.requestPermission()` for new mail
- Wire into agent: the existing `Notifier` from sprint 05 provides the events.

**Done when:** Send an email via SMTP while viewing the inbox in a browser — the new
message appears in the list without refreshing. Deleting a message in another client
(IMAP) removes it from the web UI.

### 8. Static assets and Pico CSS theming

Bundle static assets and configure the Pico CSS theme.

**Changes:**
- `internal/web/static/css/bongo.css`:
  - Custom overrides on top of Pico CSS (logo colours, email-specific spacing)
  - Message list styles: condensed rows, unread bold, flag icons
  - Compose editor styles: TipTap toolbar, attachment zone
  - Responsive breakpoints: sidebar collapses on mobile, message list adapts
- `internal/web/static/js/bongo.js`:
  - Alpine.js components: GAL autocomplete dropdown, folder tree toggle,
    bulk selection (select all, shift-click range), keyboard shortcuts
  - Keyboard shortcuts (Alpine.js): `j`/`k` navigate messages, `r` reply,
    `a` archive, `d` delete, `s` star, `/` focus search
- `internal/web/handlers/static.go`:
  - Serve `/static/*` from embedded filesystem (`embed.FS`)
  - Cache headers: `Cache-Control: public, max-age=31536000` with content-hash URLs
  - Embed Pico CSS, HTMX, Alpine.js for offline/air-gapped operation
- `internal/web/static/` embedded via `//go:embed static/*` in a `static.go` file

**Done when:** All CSS/JS loads from embedded assets. Works in air-gapped environment.
Responsive layout works on mobile viewport. Keyboard shortcuts work.

### 9. Wire web UI into agent and integration test

**Changes to `internal/agent/agent.go`:**
- Register Templ-rendered routes on the chi router:
  - `/login`, `/logout` (public)
  - `/mail/*`, `/compose/*`, `/search/*`, `/events`, `/static/*` (protected)
  - `/api/gal` (protected, JSON)
- Generate session secret in dev mode (random 32 bytes, logged for debugging)
- Ensure `templ generate` runs as part of `go generate ./...`

**Integration test** — `test/integration/webui_test.go` (build tag `integration`):
1. Start dev-mode agent
2. `POST /login` with admin@localhost / admin → verify session cookie set
3. `GET /mail/INBOX` → verify 200, HTML contains mailbox layout
4. Send email via `net/smtp` to port 2525
5. `GET /mail/INBOX` → verify message appears in list
6. `GET /mail/INBOX/{uid}` → verify message detail renders
7. `POST /compose/send` with local recipient → verify delivered
8. `GET /search?q=test` → verify search returns results
9. `POST /logout` → verify cookie cleared, redirect to `/login`

**Done when:** `go test -race -tags=integration ./test/integration/...` passes with
the web UI test. Manual test: open browser to `localhost:8080`, log in, browse mail.

---

## Exit Criteria

- [ ] IMAP SEARCH uses Bleve for text queries (flag filters remain as post-filter)
- [ ] MailboxMeta proto documents the dynamic counter design decision
- [ ] `internal/imap/` has unit tests with meaningful coverage
- [ ] `go test -race ./...` passes (all unit tests)
- [ ] `go test -race -tags=integration ./test/integration/...` passes
- [ ] `templ generate` compiles all templates without errors
- [ ] Login page authenticates and sets session cookie
- [ ] Session middleware protects all `/mail/*` routes
- [ ] Mailbox sidebar lists folders with unread counts
- [ ] Message list shows subject, from, date with pagination
- [ ] Message list updates via HTMX partial swaps (no full page reload)
- [ ] Message detail renders HTML body (sanitized) with attachments downloadable
- [ ] Flag, delete, move actions work via HTMX
- [ ] Compose form sends email to local recipients
- [ ] Reply and forward pre-fill correctly
- [ ] GAL autocomplete works in To/Cc/Bcc fields
- [ ] Draft save and resume works
- [ ] Search returns results with 300ms debounce
- [ ] SSE delivers new mail / expunge / flag events to open browser tabs
- [ ] Static assets embedded via `go:embed`, serve with cache headers
- [ ] Responsive layout: sidebar collapses on mobile
- [ ] Keyboard shortcuts: j/k/r/a/d/s// work in message list
- [ ] Integration test: login → browse inbox → read message → compose → search → logout

## What NOT to do in this sprint

- No outbound SMTP relay to external servers (local delivery only, stub error for external)
- No POP3 server
- No CalDAV/CardDAV
- No DKIM signing or verification
- No antispam or antivirus hooks
- No mail filter/sieve rules
- No IMAP APPEND, SORT, THREAD, COMPRESS, or QUOTA extensions
- No multi-user admin panel
- No dark mode toggle (Pico CSS supports it via `prefers-color-scheme` automatically)
- No file size limits on attachments (add in a later sprint)
- No rate limiting on login attempts (add in a later sprint)
