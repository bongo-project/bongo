# Sprint 08b: Dragonfly Polish — Layouts, Gestures & Mobile

**Phase:** 1 (Foundation)
**Goal:** Complete the Dragonfly mail experience with reading pane layouts, drag-and-drop,
context menus, desktop notifications, mobile gestures, density/signature polish, and
responsive mobile sidebar. Depends on Sprint 08a (preferences, threading, appState).

---

## Context

Sprint 08a established the foundation: MailMeta threading fields, UserPreferences
proto/store, `appState()` Alpine component, dark mode toggle, conversation threading
(backend + UI), bulk operations, virtual folders with SSE badge updates.

This sprint layers the remaining Dragonfly features on top of that foundation.

**What Sprint 08a delivered (prerequisites):**
- `appState()` Alpine component with theme, layoutMode, density hydrated from `/api/user-prefs`
- `PreferenceStore` with Get/Set for all user preferences
- `messageSelect` component wired up with checkboxes and bulk actions
- SSE newmail event now includes `from` and `subject` in JSON payload
- Thread grouping in message list with expand/collapse

---

## Cleanup from Sprint 08a

### 0a. Fix HTML theme attribute binding

**Why:** layout.templ has a static `data-theme="auto"` on the `<html>` element instead
of an Alpine.js reactive binding. The dark mode toggle works because `applyTheme()`
sets it via JavaScript, but the spec calls for `:data-theme="theme"` so Pico CSS
reacts immediately on page load without a JS round-trip.

**Changes:**
- `internal/web/templates/layout.templ`: Change `<html data-theme="auto">` to
  `<html x-data="appState()" :data-theme="theme">` (or ensure the existing x-data
  binding includes the `:data-theme` directive)

**Done when:** View page source → `<html>` has Alpine `:data-theme` binding. Theme
applies on first paint without flash of wrong theme.

### 0b. Wire messages-per-page preference into message list

**Why:** `ListMessages()` in `mail.go` hardcodes `const messagesPerPage = 25` instead
of reading the user's preference from PreferenceStore. The preference saves correctly
but has no effect on pagination.

**Changes:**
- `internal/web/handlers/mail.go`: In `ListMessages()`, replace hardcoded
  `messagesPerPage = 25` with a lookup from `PreferenceStore.Get(userID).MessagesPerPage`.
  Fall back to 25 if the value is 0 or unset.

**Done when:** Set messages_per_page=10 in preferences → inbox shows 10 messages per page.

### 0c. Wire default reply mode into compose handler

**Why:** The `default_reply` preference is saved in PreferenceStore but the compose
handler ignores it. When a user sets their default to Reply All, clicking "Reply"
should pre-populate the To/Cc fields with all recipients.

**Changes:**
- `internal/web/handlers/compose.go`: In `Reply()`, read `PreferenceStore.Get(userID).DefaultReply`.
  If `REPLY_MODE_ALL`, populate both To (original sender) and Cc (original To + Cc minus
  current user) fields. If `REPLY_MODE_SENDER` (or unset), keep existing behaviour (To = sender only).
- Update the reply form template to reflect pre-populated Cc field when reply-all is default.

**Done when:** Set default_reply=REPLY_MODE_ALL in preferences → click Reply on a
message with multiple recipients → To and Cc fields pre-populated with all recipients.

---

## Tasks

### 1. Reading pane (two-pane / three-pane)

**Modify `internal/web/templates/mailbox.templ`:**
- Wrap content in layout container with dynamic class:
  ```
  <div class="mail-layout" :class="'layout-' + layoutMode">
    <div class="message-list-pane"> ... </div>
    <div class="reading-pane" x-show="layoutMode !== 'list'"> ... </div>
  </div>
  ```
- In pane modes, message row click targets reading pane instead of full navigation:
  - `hx-get="/mail/{mailbox}/{uid}?pane=1"` `hx-target=".reading-pane"` `hx-swap="innerHTML"`

**Modify `internal/web/handlers/mail.go`:**
- `ShowMessage()`: check `?pane=1` query param
  - If pane=1: return HTMX partial (message content only, no layout wrapper)
  - Else: return full page (existing behavior)

**Add layout toggle to toolbar:**
- Three icon buttons: list icon, split-horizontal icon, split-vertical icon
- `@click="layoutMode = 'list'"` etc., saves to preferences via fetch POST

**Modify `bongo.css`:**
- `.layout-list .reading-pane { display: none; }`
- `.layout-two-pane { display: flex; flex-direction: column; }` — list 50%, pane 50%
- `.layout-three-pane { display: grid; grid-template-columns: 1fr 1fr; }` — list left, pane right
  (sidebar already separate in the outer layout)
- `.reading-pane` — overflow-y auto, border-left or border-top depending on mode
- Responsive: force list mode below 768px

**Done when:** Select two-pane → list shrinks, reading pane appears. Click message →
loads in pane without full navigation. Three-pane: side-by-side. Preference persists.

### 2. Drag-and-drop between folders

**Modify `internal/web/templates/layout.templ`:**
- Add Sortable.js: `<script src="https://cdn.jsdelivr.net/npm/sortablejs@1.15.0/Sortable.min.js"></script>`
  (or embed in static/js/)

**Add to `bongo.js`:**
- `dragDrop()` Alpine component, initialized on page load:
  - Make `.message-list tbody` a Sortable group (sort: false, just drag)
  - Make each `.sidebar .mailbox-link` a drop target
  - `onEnd(evt)`: extract `data-uid` from dragged row, `data-folder` from drop target
  - POST `/mail/{srcMailbox}/{uid}/move` with `destination={folder}`
  - On success: remove row from list, flash "Moved to {folder}"

**Modify `internal/web/templates/mailbox.templ`:**
- Add `draggable="true"` attribute to message rows (data-uid already present from 08a)

**Modify `internal/web/templates/components.templ`:**
- Add `data-folder={name}` to sidebar folder links

**Modify `bongo.css`:**
- `.sortable-ghost { opacity: 0.4; }`
- `.drag-over { background: var(--pico-primary-focus); border-radius: 4px; }`

**Done when:** Drag message row to sidebar folder → message moves, row disappears.
Drop target highlights on hover.

### 3. Right-click context menu

**Add to `bongo.js`:**
- `contextMenu()` Alpine component:
  - `visible = false`, `x = 0`, `y = 0`, `uid = null`, `mailbox = null`
  - `open(event, uid, mailbox)`: preventDefault, set coordinates, show menu
  - `action(act)`: dispatch action (open, reply, reply-all, forward, mark-read,
    mark-unread, flag, unflag, move, delete), then close menu
  - Actions use fetch POST to existing endpoints or navigate via `window.location`

**Modify `internal/web/templates/mailbox.templ`:**
- Message rows: `@contextmenu="contextMenu.open($event, uid, mailbox)"`

**Add to `internal/web/templates/components.templ`:**
- Context menu template (positioned absolute):
  - Items: Open, Reply, Reply All, Forward, separator, Mark Read/Unread, Flag/Unflag,
    separator, Move to → (sub-list of folders), Delete
  - `@click.away="contextMenu.visible = false"`
  - `@keydown.escape.window="contextMenu.visible = false"`

**Modify `bongo.css`:**
- `.context-menu` — position fixed, z-index 1000, background, border, box-shadow,
  min-width 180px
- `.context-menu a` — display block, padding, hover background
- `.context-menu hr` — separator styling

**Done when:** Right-click message → menu at cursor. Click Reply → navigates to reply.
Click Delete → deletes message. Click away → menu closes. Escape closes.

### 4. Desktop notifications

**Add to `bongo.js`:**
- `notifications()` Alpine component:
  - `enabled`: check `Notification.permission`
  - `requestPermission()`: call `Notification.requestPermission()`
  - `show(title, body)`: create `new Notification(title, { body, icon: '/static/logo.png' })`
- Hook into SSE EventSource:
  - On `newmail` event: if permission granted, call `show(from, subject)`

**Modify `internal/web/templates/layout.templ`:**
- Add notification permission button in top bar (conditionally shown):
  ```
  <button x-show="Notification.permission === 'default'"
          @click="notifications.requestPermission()">
    Enable notifications
  </button>
  ```
- Add SSE connection: `<div hx-ext="sse" sse-connect="/events?mailbox=INBOX">`

**Done when:** Grant permission → new mail triggers browser notification showing
sender and subject. Click notification → focuses tab.

### 5. Mobile swipe gestures

**Add to `bongo.js`:**
- `swipeHandler()` Alpine component (only on `pointer:coarse` devices):
  - `startX`, `startY`, `currentX`, `swiping = false`, `direction = null`
  - `start(event, uid)`: record touch start position
  - `move(event)`: compute deltaX, if |deltaX| > 20 and |deltaY| < 30, set swiping
    - Left swipe: reveal gray "Archive" background
    - Right swipe: reveal red "Delete" background
  - `end(event, uid, mailbox)`:
    - If deltaX < -60: POST move to Archive folder
    - If deltaX > 60: POST delete
    - Else: cancel, reset position

**Modify `internal/web/templates/mailbox.templ`:**
- Wrap message row content in swipe container on mobile:
  ```
  @touchstart="swipe.start($event, uid)"
  @touchmove="swipe.move($event)"
  @touchend="swipe.end($event, uid, mailbox)"
  ```

**Modify `bongo.css`:**
- `.swipe-row { position: relative; overflow: hidden; }`
- `.swipe-bg-left { position: absolute; right: 0; background: var(--pico-muted-color); }`
- `.swipe-bg-right { position: absolute; left: 0; background: var(--pico-del-color); }`
- Transition: transform 200ms ease

**Done when:** On mobile, swipe message left → archive. Swipe right → delete.
Desktop: no touch events fire.

### 6. UI polish (density, signatures, responsive)

**Modify `bongo.css` — density classes:**
```css
.density-compact .message-list td { padding: 0.25rem 0.5rem; }
.density-comfortable .message-list td { padding: 0.5rem; }
.density-spacious .message-list td { padding: 0.75rem; }
```

**Apply density to body:**
- `appState()` init (from 08a): apply `density-{value}` class to `<body>`

**Modify `internal/web/handlers/mail.go`:**
- `ListMessages()`: read `messages_per_page` from user preferences (via PreferenceStore),
  use as page size instead of hardcoded 25

**Modify `internal/web/handlers/compose.go`:**
- `NewMessage()`, `Reply()`, `Forward()`: if user has a signature, append
  `\n\n-- \n{signature}` to the compose body textarea

**Modify `internal/web/templates/layout.templ` — mobile hamburger:**
- Add hamburger button (visible only < 768px)
- Alpine.js `sidebarOpen` toggle
- Sidebar: `position: fixed` on mobile, overlay background, slide-in animation

**Modify `bongo.css` — responsive:**
- Below 768px: sidebar hidden by default, hamburger visible
- `.sidebar.open` — slide in from left, z-index overlay
- `.sidebar-overlay` — semi-transparent backdrop
- Force list layout on mobile (disable two-pane/three-pane)

**Modify `internal/web/handlers/compose.go`:**
- Read `default_reply` preference. In reply compose form, use it to decide whether
  To field includes all recipients or just sender.

**Done when:** Change density → spacing updates. Set signature → appears in compose.
Set messages_per_page=10 → list shows 10. Mobile: hamburger opens sidebar.

### 7. Integration test

**Create `test/integration/dragonfly_b_test.go` (build tag `integration`):**

**TestReadingPane:**
1. Start dev agent, login, deliver a message
2. GET `/mail/INBOX/{uid}?pane=1` → verify partial response (no full layout wrapper)
3. GET `/mail/INBOX/{uid}` (no pane param) → verify full page response
4. Verify both contain message body content

**TestDragAndDrop:**
1. Deliver message to INBOX
2. POST `/mail/INBOX/{uid}/move` with destination=Drafts
3. GET `/mail/Drafts` → verify message appears
4. GET `/mail/INBOX` → verify message gone

**TestDensityAndSignature:**
1. POST `/settings` with density=compact, signature="-- Best, Alice"
2. GET `/mail/compose` → verify signature in compose body
3. GET `/api/user-prefs` → verify density=compact in JSON

**TestMessagesPerPage:**
1. POST `/settings` with messages_per_page=10
2. Deliver 15 messages
3. GET `/mail/INBOX` → verify only 10 message rows on first page

**Done when:** `go test -race -tags=integration ./test/integration/dragonfly_b_test.go` passes.

---

## Exit Criteria

- [ ] Layout toggle: list / two-pane / three-pane
- [ ] Reading pane loads message preview without full page navigation
- [ ] Layout preference persists via PreferenceStore
- [ ] Responsive: force list mode below 768px
- [ ] Drag message row to sidebar folder → moves via POST
- [ ] Drop target highlights on hover with ghost styling
- [ ] Right-click message → context menu at cursor with all actions
- [ ] Context menu actions work (reply, delete, flag, move, etc.)
- [ ] Context menu closes on click-away and Escape
- [ ] Desktop notification fires on new mail (when permission granted)
- [ ] SSE connection established in layout for notifications
- [ ] Mobile swipe left > 60px → archive message
- [ ] Mobile swipe right > 60px → delete message
- [ ] Swipe only activates on touch devices (pointer:coarse)
- [ ] Density preference adjusts message list spacing
- [ ] Signature appended to compose body
- [ ] Messages-per-page preference applied to list pagination
- [ ] Default reply mode pre-selects reply type
- [ ] Mobile sidebar collapses to hamburger below 768px
- [ ] Sidebar slide-in animation and overlay backdrop
- [ ] `go test -race ./...` passes (all unit tests)
- [ ] `go test -race -tags=integration ./test/integration/...` passes

## What NOT to do in this sprint

- No Sieve/mail filter rules (Sprint 12)
- No CalDAV/CardDAV (Sprints 09–10)
- No DMARC/ARC checking
- No IMAP THREAD extension (web UI threading only)
- No inline attachment preview (Sprint 13)
- No push notifications / service workers
- No custom folder colours or icons
- No 2FA enhancements (Sprint 13)
- No rich text compose editor / TipTap
- No animated transitions beyond CSS
