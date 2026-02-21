# Sprint 08a: Dragonfly Foundation — Threading, Preferences & Core Features

**Phase:** 1 (Foundation)
**Goal:** Lay the groundwork for the Dragonfly mail experience: add threading fields to
the proto, build a user preferences system, implement conversation threading, bulk
operations, virtual folders with SSE badges, and dark mode.

---

## Context

Sprints 01–07 built the full mail server: storage, Raft, SMTP in/out with DKIM/SPF,
search, IMAP, and a basic HTMX web UI. Users can read, compose, and send mail via
browser. But the UI is minimal — single-column list, no threading, no bulk ops, no
preferences. This sprint adds the foundational features everything else depends on.

**Existing infrastructure:**
- 7 Templ files (layout, components, mailbox, message, compose, search, login)
- Alpine.js `messageSelect` component **defined but unused** (has toggle, selectAll, shift-click)
- Alpine.js `galAutocomplete` (functional), keyboard shortcuts j/k/r/d/s/slash
- SSE handler with newmail/expunge/flagchange events + 30s heartbeat
- Pico CSS + bongo.css (332 lines), responsive at 768px
- Message list: HTML table with pagination (25/page), HTMX partial swaps
- Notifier fan-out with 64-buffer channels per mailbox

**Key gaps addressed in this sprint:**
- CSS framework needs upgrading: Pico CSS alone is document-focused, not app-focused.
  Add Open Props design tokens for polished app components (shadows, animations, gradients).
  Vendor both locally — no CDN dependency, works offline.
- `MailMeta` proto missing `in_reply_to`, `references`, `cc` → threading impossible
- `UserProfile` proto has no preference fields → need new `UserPreferences` proto
- No conversation grouping, bulk operations, virtual folders, or dark mode

---

## Cleanup from Sprint 07

### 0. Vendor CSS frameworks locally and add Open Props

**Why:** Pico CSS is great for semantic defaults (forms, tables, typography) but lacks
design tokens for app-like components. Open Props provides beautiful shadows, easing
curves, gradients, and spacing tokens — all as plain CSS custom properties. Both must
be vendored locally so the app works offline with no CDN dependency.

**Steps:**
- Download Pico CSS (`pico.min.css`, currently v2.x) and save to
  `internal/web/static/css/pico.min.css`
- Download Open Props (`open-props.min.css` + `normalize.min.css`) and save to
  `internal/web/static/css/open-props.min.css`
- Update `internal/web/templates/layout.templ`:
  - Remove any CDN `<link>` tags for Pico
  - Add local references:
    ```html
    <link rel="stylesheet" href="/static/css/pico.min.css">
    <link rel="stylesheet" href="/static/css/open-props.min.css">
    <link rel="stylesheet" href="/static/css/bongo.css">
    ```
  - Order matters: Pico base → Open Props tokens → bongo.css overrides
- Also vendor HTMX and Alpine.js locally if not already done:
  - `internal/web/static/js/htmx.min.js`
  - `internal/web/static/js/alpine.min.js`
  - Update `layout.templ` script tags to local paths
- Verify: page loads with no external network requests (check browser dev tools)

**Done when:** All CSS and JS loaded from `/static/`. No CDN references in layout.templ.
Page renders correctly with Pico defaults + Open Props tokens available in bongo.css.
Open Props variables like `--shadow-2`, `--ease-out-3`, `--gradient-1` usable in bongo.css.

### 0a. Add threading fields to MailMeta proto

**Why:** Conversation threading requires `In-Reply-To` and `References` headers to
link messages into chains. Currently `MailMeta` has `message_id` but no way to
express reply relationships.

**Changes:**
- `proto/bongo/v1/mail.proto`: Add to `MailMeta`:
  - `string in_reply_to = 11` — Message-ID of parent message
  - `string references = 12` — space-separated chain of ancestor Message-IDs
  - `repeated string cc = 13` — Cc recipients (needed for accurate Reply All)
- `internal/smtp/delivery.go`: In `DeliveryPipeline.Deliver()`, after parsing with
  enmime, extract `envelope.GetHeader("In-Reply-To")`, `envelope.GetHeader("References")`,
  and parsed Cc addresses. Populate on `MailMeta` before Raft apply.
- `internal/web/handlers/compose.go`: In `Send()` for reply mode:
  - Set outgoing `In-Reply-To` header to original message's `message_id`
  - Set `References` to original's `references` + " " + original's `message_id`
  - For forward: leave `In-Reply-To` empty
- Lazy backfill: In `ShowMessage()`, if `meta.InReplyTo == ""` and raw blob exists,
  parse from blob and update Pebble key via Raft (handles pre-sprint-08 messages).

**Done when:** Deliver inbound email with In-Reply-To header → MailMeta stores it.
Reply via web UI → outgoing message has correct In-Reply-To and References. Unit test
verifies header parsing.

---

## Tasks

### 1. User preferences proto and store

Define persistent user preferences.

**Create `proto/bongo/v1/preferences.proto`:**
- `enum Theme { THEME_UNSPECIFIED=0; THEME_AUTO=1; THEME_LIGHT=2; THEME_DARK=3; }`
- `enum LayoutMode { LAYOUT_UNSPECIFIED=0; LAYOUT_LIST=1; LAYOUT_TWO_PANE=2; LAYOUT_THREE_PANE=3; }`
- `enum DisplayDensity { DENSITY_UNSPECIFIED=0; DENSITY_COMPACT=1; DENSITY_COMFORTABLE=2; DENSITY_SPACIOUS=3; }`
- `enum DefaultReplyMode { REPLY_MODE_UNSPECIFIED=0; REPLY_MODE_SENDER=1; REPLY_MODE_ALL=2; }`
- `message UserPreferences`:
  - `Theme theme = 1`
  - `LayoutMode layout_mode = 2`
  - `DisplayDensity density = 3`
  - `int32 messages_per_page = 4` (10, 25, 50, 100)
  - `DefaultReplyMode default_reply = 5`
  - `string signature = 6`
  - `string timezone = 7` (IANA name)
  - `int64 updated_at = 8`

**Create `internal/store/preferences.go`:**
- `PreferenceStore` wrapping `*PebbleStore` + `RaftApplier`
- Key: `/user/{id}/preferences` → UserPreferences proto
- `Get(userID) → *UserPreferences` (returns defaults if not found)
- `Set(userID, prefs) → error` (marshals proto, applies via Raft)
- Defaults: theme=AUTO, layout=LIST, density=COMFORTABLE, messages_per_page=25,
  default_reply=SENDER, timezone="UTC"

**Create `internal/store/preferences_test.go`:**
- Test set/get round-trip, defaults for missing user, timestamp update

**Create `internal/web/handlers/preferences.go`:**
- `GET /settings` → render settings form with current values
- `POST /settings` → parse form, validate, save via PreferenceStore, redirect with flash
- `GET /api/user-prefs` → JSON response for Alpine.js hydration

**Create `internal/web/templates/settings.templ`:**
- Form with fieldsets: Appearance (theme, layout, density), Display (messages_per_page),
  Composition (default_reply, signature textarea), Localization (timezone select)
- Save and Cancel buttons

**Wire into `internal/agent/agent.go`:**
- Create PreferenceStore after Pebble/Raft init
- Register `/settings` and `/api/user-prefs` routes

**Done when:** POST to `/settings` persists preferences. GET returns saved values.
`/api/user-prefs` returns JSON. Defaults applied for new users.

### 2. Dark mode toggle

**Modify `internal/web/templates/layout.templ`:**
- `<html x-data="appState()" :data-theme="theme">` — Alpine.js controls theme attribute
- Add toggle button in top bar: sun/moon icon, `@click="toggleTheme()"`

**Add to `bongo.js`:**
- `appState()` Alpine component:
  - `theme`: initially from localStorage fallback to 'auto'
  - `toggleTheme()`: cycle auto → light → dark → auto
  - `init()`: fetch `/api/user-prefs`, apply theme + layout + density
  - Watch `prefers-color-scheme` media query when theme=auto
- Persist to localStorage for instant apply, POST to `/settings` for server persistence

**Done when:** Click toggle → theme changes. Refresh → theme persists. Auto respects OS preference.

### 3. Conversation threading (backend)

**Create `internal/store/threads.go`:**
- `type ThreadNode struct` — GUID, MessageID, InReplyTo, Subject, From, ReceivedAt,
  Flags, Children []*ThreadNode, Depth int
- `type ThreadSummary struct` — Root *ThreadNode, AllNodes []*ThreadNode, Count int
- `type ThreadStore struct` — wraps PebbleStore, MailboxStore
- `BuildThread(userID, mailGUID string) (*ThreadSummary, error)`:
  1. Load MailMeta → get message_id, in_reply_to
  2. Walk up In-Reply-To chain to find root (stop when in_reply_to is empty)
  3. From root, scan all user mail keys, find children (where in_reply_to matches)
  4. Build tree recursively, assign depth levels
  5. Sort children by received_at at each level
- `GetThreadRoot(userID, guid string) (string, error)` — returns root GUID for grouping
- Internal cache: `sync.Map` of message_id → guid for fast lookups within a request

**Create `internal/store/threads_test.go`:**
- Test: 3-message chain (M1 → M2 → M3) → correct tree with depths 0, 1, 2
- Test: single message → thread count = 1
- Test: branching replies (M2a and M2b both reply to M1) → correct structure

**Done when:** `go test -race ./internal/store/...` passes. BuildThread reconstructs
chains from In-Reply-To/References headers.

### 4. Conversation threading (UI)

**Modify `internal/web/handlers/mail.go`:**
- In `ListMessages()`: after loading messages, call `ThreadStore.GetThreadRoot()` for
  each. Group messages sharing the same root. For each group:
  - Show only the most recent message as the visible row
  - Attach thread count as badge
- `GET /mail/{mailbox}/thread/{rootGUID}` — new endpoint:
  - Call `ThreadStore.BuildThread(userID, rootGUID)`
  - Return HTMX partial: child message rows indented by depth

**Modify `internal/web/templates/mailbox.templ`:**
- Thread badge on root rows: `<span class="thread-badge">{count}</span>`
  - `hx-get="/mail/{mailbox}/thread/{rootGUID}"` `hx-target="next .thread-children"`
  - `hx-swap="innerHTML"` toggles expand/collapse
- `<tbody class="thread-children">` — initially empty, filled by HTMX

**Add to `bongo.js`:**
- `threadExpander()` Alpine component:
  - `expanded = new Set()` — tracks expanded root GUIDs
  - `toggle(rootGUID)` — add/remove from set, triggers HTMX fetch

**Modify `bongo.css`:**
- `.thread-badge` — inline-block, pill shape, cursor pointer, font-size 0.75rem
- `.thread-child` — padding-left proportional to depth (1.5rem per level)
- `.thread-child td.from-col` — includes reply arrow icon

**Done when:** Message list groups threads. Badge shows count. Click badge →
child messages expand inline with indentation. Click again → collapse.

### 5. Bulk operations

**Modify `internal/web/templates/mailbox.templ`:**
- Add checkbox column as first column in message table:
  ```
  <input type="checkbox" @change="messageSelect.toggle(uid, $event)"
         :checked="messageSelect.isSelected(uid)">
  ```
- Bulk toolbar above message list (shown when `messageSelect.count > 0`):
  - Select All / Deselect All buttons
  - Mark Read, Mark Unread, Flag, Unflag, Delete buttons
  - Move dropdown (lists folders)
  - Selected count indicator

**Extend `messageSelect` in `bongo.js`:**
- Add bulk action methods: `bulkAction(action)` — collects selected UIDs, POSTs to
  `/mail/{mailbox}/bulk` via fetch, refreshes message list on success
- `selectAll()` — selects all visible UIDs on current page

**Create handler in `internal/web/handlers/mail.go`:**
- `POST /mail/{mailbox}/bulk`:
  - Parse: `action` (mark-read/mark-unread/flag/unflag/move/delete), `uids[]`, `destination`
  - For each UID: apply appropriate flag/move/delete operation
  - Return HTMX partial: updated message list (reflects changes)

**Modify `bongo.css`:**
- `.bulk-toolbar` — flex row, gap, padding, border-bottom, background highlight
- Checkbox column width: 2rem fixed

**Done when:** Check messages → toolbar appears. Click "Mark Read" → flags update.
Click "Delete" → messages removed. Select All works. Shift-click range selects.

### 6. Virtual folders and SSE badge updates

**Modify `internal/store/mailbox.go`:**
- `ListVirtualMessages(userID, virtual string, offset, limit int) ([]MailboxMessage, int, error)`:
  - `"unread"`: scan all `/user/{id}/mailbox/*/msg/` keys, filter `flags & SEEN == 0`
  - `"starred"`: scan all, filter `flags & FLAGGED != 0`
  - `"all-mail"`: scan all, no filter (union of all mailboxes)
  - Return paginated slice + total count

**Modify `internal/web/handlers/mail.go`:**
- In `ListMessages()`: if mailbox is "unread", "starred", or "all-mail", call
  `ListVirtualMessages()` instead of `ListMessagesPaged()`

**Modify `internal/web/templates/components.templ` (Sidebar):**
- Add virtual folder section above real mailboxes:
  - Unread (with live badge), Starred, All Mail
  - Links to `/mail/unread`, `/mail/starred`, `/mail/all-mail`

**Modify `internal/web/handlers/sse.go`:**
- On `newmail` event: include `from` and `subject` in JSON payload (for notifications)
- After sending newmail event: compute total unread count, send HTMX OOB swap:
  ```
  event: oob
  data: <span id="unread-badge" hx-swap-oob="true">{count}</span>
  ```

**Done when:** Click "Unread" → shows all unseen messages across mailboxes. New mail
arrives → sidebar badge updates via SSE without page refresh. Starred shows flagged messages.

### 7. Integration test

**Create `test/integration/dragonfly_a_test.go` (build tag `integration`):**

**TestConversationThreading:**
1. Start dev agent, create two users (alice, bob)
2. Alice sends M1 "Thread Test" to bob
3. Bob replies M2 with In-Reply-To: M1.MessageID, Subject "Re: Thread Test"
4. Alice replies M3 with In-Reply-To: M2.MessageID
5. Bob: GET `/mail/INBOX` → verify M1 shows thread badge "2" (M1 + M2 are in his inbox)
6. GET `/mail/INBOX/thread/{rootGUID}` → verify child messages returned
7. Verify child messages include correct depth ordering

**TestBulkOperations:**
1. Deliver 3 messages to user's INBOX
2. POST `/mail/INBOX/bulk` with action=mark-read, uids=[1,2]
3. Verify message 1 and 2 have SEEN flag, message 3 does not
4. POST `/mail/INBOX/bulk` with action=delete, uids=[3]
5. Verify message 3 expunged from INBOX

**TestPreferences:**
1. POST `/settings` with theme=dark, layout_mode=two-pane, density=spacious,
   messages_per_page=10, signature="-- Best, Alice"
2. Read Pebble key `/user/{id}/preferences` → verify stored proto matches
3. GET `/api/user-prefs` → verify JSON response matches
4. GET `/mail/INBOX` → verify page size is 10 (only 10 messages per page)

**TestVirtualFolders:**
1. Deliver 2 messages, mark one as seen
2. GET `/mail/unread` → verify only unseen message returned
3. Flag one message
4. GET `/mail/starred` → verify only flagged message returned
5. GET `/mail/all-mail` → verify both messages returned

**Done when:** `go test -race -tags=integration ./test/integration/dragonfly_a_test.go` passes.

---

## Exit Criteria

- [ ] Pico CSS, Open Props CSS, HTMX JS, and Alpine.js JS vendored in `internal/web/static/`
- [ ] No CDN references in layout.templ — all assets loaded from `/static/`
- [ ] Open Props variables (e.g. `--shadow-2`, `--ease-out-3`) usable in bongo.css
- [ ] MailMeta proto has `in_reply_to`, `references`, `cc` fields
- [ ] Inbound delivery populates threading fields from parsed headers
- [ ] Reply/forward sets In-Reply-To and References correctly
- [ ] Lazy backfill parses headers from blob for pre-sprint messages
- [ ] `go test -race ./...` passes (all unit tests)
- [ ] `go test -race -tags=integration ./test/integration/...` passes
- [ ] UserPreferences proto and PreferenceStore created and functional
- [ ] GET/POST `/settings` renders and saves preferences
- [ ] `/api/user-prefs` returns JSON for frontend hydration
- [ ] Dark mode toggle changes `data-theme`, persists across refresh
- [ ] `appState()` Alpine component hydrates from `/api/user-prefs`
- [ ] ThreadStore builds conversation threads from In-Reply-To chains
- [ ] Message list groups threads with expand badge (shows count)
- [ ] Click thread badge → HTMX expands/collapses child messages (indented)
- [ ] Checkboxes in message list, bulk toolbar appears when selected
- [ ] Bulk mark-read/mark-unread/flag/unflag/move/delete work via HTMX
- [ ] Virtual folders: Unread, Starred, All Mail in sidebar
- [ ] Unread folder shows unseen messages across all mailboxes
- [ ] SSE updates sidebar unread badges in real time
- [ ] SSE newmail event includes from + subject in JSON payload

## What NOT to do in this sprint

- No reading pane layouts (Sprint 08b)
- No drag-and-drop between folders (Sprint 08b)
- No right-click context menu (Sprint 08b)
- No desktop notifications (Sprint 08b)
- No mobile swipe gestures (Sprint 08b)
- No density/signature/responsive polish (Sprint 08b)
- No Sieve/mail filter rules (Sprint 12)
- No CalDAV/CardDAV (Sprints 09–10)
- No DMARC/ARC checking
- No rich text compose editor
