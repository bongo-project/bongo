# Sprint 13c: Accessibility, i18n & Performance

**Phase:** 1 (Foundation)
**Goal:** Add accessibility (ARIA, keyboard, focus management), internationalisation
(message catalogue + locale selector), HTTP compression and lazy-loading performance
improvements, and a comprehensive E2E smoke test.

---

## Context

Sprints 01–13b deliver a feature-complete mail/calendar/contacts server with admin panel,
but the UI has accessibility gaps (no ARIA labels, no skip-to-content, no focus management
after HTMX swaps), zero i18n infrastructure (all strings hardcoded in English), and some
performance opportunities (no HTTP compression, no infinite scroll). The E2E test exists
but only tests the storage layer — not the full user journey.

**Existing infrastructure:**
- Layout: `layout.templ` with `<html lang="en">`, `<aside class="sidebar">`,
  `<main class="main-content">` — some semantic elements already present
- Keyboard shortcuts: `bongo.js` has j/k (prev/next), Enter (open), r (reply),
  d (delete), s (star), / (search) — but no help modal or ARIA announcement
- HTMX: 7 swap targets across templates, no `hx-on::after-swap` focus management
- Alpine.js: `appState()` for theme, context menus, bulk toolbar toggle
- SSE: `/events` endpoint with newmail, oob, expunge, flagchange, reminder events
- Pagination: `?page=N` with numbered links, no infinite scroll
- UserPreferences proto: theme, layout, density, messages_per_page, default_reply,
  signature, timezone, calendar_view — no locale/language field
- Test helpers: `test/integration/helpers_test.go` with createTestNode(), HTTP client
  with cookie jar, login flow helpers
- E2E: `test/e2e/e2e_test.go` — minimal (health endpoint + SMTP deliver + Pebble verify)
- 14 .templ files, ~3,318 lines total, estimated 150–200 hardcoded UI strings
- No i18n library in go.mod
- No HTTP compression middleware

---

## Cleanup from Sprint 13b

### 0a. Verify compose multipart boundary uniqueness

**Why:** `buildRFC5322()` generates a MIME boundary for multipart/mixed messages. Verify
the boundary string is sufficiently random to avoid collisions with message content.

**Check `internal/web/handlers/compose.go` → `buildRFC5322()`:**
- If boundary is a fixed string or weak random, switch to `mime.GenerateMultipartBoundary()`
  or `fmt.Sprintf("--bongo-%s", uuid.New().String())`
- Verify boundary is not contained in any attachment content (MIME spec requirement)

**Done when:** Boundary is generated with sufficient randomness. No collision risk.

### 0b. Add Content-Length header to EML export

**Why:** `ExportEML()` streams the raw message blob but some download managers work
better with a known Content-Length.

**Modify `internal/web/handlers/mail.go` → `ExportEML()`:**
- Set `Content-Length` from BlobRef.Size (already available in MailMeta)

**Done when:** EML download responses include Content-Length header.

---

## Tasks

### 1. Accessibility — semantic landmarks and skip links

Add proper ARIA landmarks, skip-to-content link, and semantic structure.

**Modify `internal/web/templates/layout.templ`:**
- Add skip-to-content link as first child of `<body>`:
  ```html
  <a href="#main-content" class="skip-link">Skip to main content</a>
  ```
- Wrap top bar in `<header role="banner">` instead of `<div class="top-bar">`
- Ensure sidebar `<aside>` has `aria-label="Mailbox navigation"`
- Ensure `<main>` has `id="main-content"` and `role="main"` (already has id)
- Add `<footer role="contentinfo">` if footer content exists

**Modify `internal/web/templates/components.templ`:**
- Sidebar `<nav>` gets `aria-label="Mailboxes"` on mailbox list
- Sidebar `<nav>` for contacts/calendar gets `aria-label="Applications"`

**Add to `internal/web/static/css/bongo.css`:**
```css
.skip-link {
  position: absolute;
  top: -40px;
  left: 0;
  background: var(--pico-primary);
  color: white;
  padding: 8px 16px;
  z-index: 100;
  transition: top 0.2s;
}
.skip-link:focus {
  top: 0;
}
```

**Done when:** Tab key reveals "Skip to main content" link. Screen readers announce
landmark regions (banner, navigation, main, contentinfo). Skip link jumps focus to
main content area.

### 2. Accessibility — ARIA labels on interactive elements

Add aria-label, aria-live, and screen reader support across all templates.

**Modify `internal/web/templates/mailbox.templ`:**
- Message checkboxes: `aria-label="Select message from {sender}: {subject}"`
- Star toggle: `aria-label="Star message"` / `aria-label="Unstar message"`
- Bulk action toolbar: `aria-live="polite"` to announce selection count
- Unread badge: `aria-label="{count} unread messages"`
- Message row: `role="row"`, message list: `role="grid"` or keep as list

**Modify `internal/web/templates/message.templ`:**
- Reply/Forward/Delete buttons: verify `aria-label` present
- Attachment links: `aria-label="Download {filename} ({size})"`
- Thread expand: `aria-expanded="true/false"` toggle
- Blocked images: `aria-label="External images blocked. Click to load."`

**Modify `internal/web/templates/compose.templ`:**
- To/Cc/Bcc fields: `aria-label="Recipients"`
- Subject: `aria-label="Subject"`
- Attachment file list: `aria-live="polite"` for file add/remove announcements

**Modify `internal/web/templates/calendar.templ`:**
- Date cells: `aria-label="{month} {day}, {year}"`
- Event chips: `aria-label="{event title} at {time}"`
- Navigation: prev/next buttons `aria-label="Previous month"` / `aria-label="Next month"`
- View toggle: `aria-pressed="true/false"` for current view

**Modify `internal/web/templates/contacts.templ`:**
- Contact rows: `aria-label="{name} — {email}"`
- Edit/delete buttons: `aria-label="Edit {name}"` / `aria-label="Delete {name}"`

**Modify `internal/web/templates/admin.templ`:**
- Dashboard cards: `aria-label` on stat widgets
- User management table: proper `<th scope="col">` headers

**Modify `internal/web/templates/login.templ`:**
- Error messages: `role="alert"` for login failure messages
- 2FA input: `aria-label="Two-factor authentication code"`

**Modify all templates with flash messages:**
- Flash success/error: `role="alert"` and `aria-live="assertive"`

**Done when:** Screen reader can navigate all interactive elements with meaningful
labels. Dynamic content changes are announced. Form errors are announced immediately.

### 3. Accessibility — keyboard navigation and focus management

Ensure full keyboard accessibility and proper focus after HTMX swaps.

**Modify `internal/web/static/js/bongo.js`:**
- Add keyboard shortcut help modal (`?` key to toggle):
  - List all shortcuts: j/k, Enter, r, d, s, /, Escape, etc.
  - Modal has `role="dialog"` with `aria-labelledby` and `aria-modal="true"`
  - Focus trapped inside modal while open
- Add focus management after HTMX swaps:
  ```javascript
  document.body.addEventListener('htmx:afterSwap', function(evt) {
    // Focus first heading or first focusable element in swapped content
    const target = evt.detail.target;
    const focusable = target.querySelector('h1, h2, [autofocus], a, button, input');
    if (focusable) focusable.focus();
  });
  ```
- Add `aria-live="polite"` region for HTMX status announcements
- Context menu: add `role="menu"`, `role="menuitem"`, arrow key navigation,
  Escape to close

**Modify `internal/web/templates/layout.templ`:**
- Add live region for announcements: `<div id="aria-announcements" aria-live="polite" class="sr-only"></div>`

**Add CSS for screen-reader-only class:**
```css
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
```

**Modify `internal/web/templates/rules.templ`:**
- Sortable.js drag-and-drop: add keyboard alternative (move up/down buttons)
  with `aria-label="Move rule up"` / `aria-label="Move rule down"`

**Done when:** All UI usable with keyboard only. HTMX swaps move focus to new content.
`?` shows keyboard shortcut help. Context menu navigable with arrow keys. Drag-and-drop
has keyboard alternative.

### 4. Internationalisation — i18n infrastructure and message catalogue

Set up translation infrastructure and extract UI strings.

**Add dependency:**
- `go get github.com/nicksnyder/go-i18n/v2` (widely-used Go i18n library)

**Create `internal/i18n/i18n.go`:**
- `I18n` struct wrapping `i18n.Bundle`
- `NewI18n(defaultLocale string) *I18n`:
  - Create bundle with `language.English` as default
  - Register JSON unmarshal function
  - Load message files from embedded FS
- `T(locale, messageID string, data ...interface{}) string`:
  - Create localizer for locale, return localized string
  - Fallback to English if translation missing
- `Localizer(locale string) *i18n.Localizer` — for template use
- `AvailableLocales() []LocaleInfo` — list of {Code, Name} for selector

**Create `internal/i18n/messages/` directory:**
- `en.json` — default English message catalogue (all extracted strings)
- `fr.json` — French translations (stub with key placeholders for now)
- `de.json` — German translations (stub)
- `es.json` — Spanish translations (stub)
- Use `//go:embed messages/*.json` to embed in binary

**Extract strings from all .templ files:**
- Replace hardcoded strings with `{ i18n.T(locale, "msg_id") }` calls
- Message ID convention: `section.element` (e.g., `mail.compose`, `mail.no_messages`,
  `nav.inbox`, `admin.dashboard`, `calendar.month_view`)
- Estimated 150–200 strings to extract across 14 .templ files
- Priority: navigation, common actions, error messages first. Detailed admin/calendar
  strings can follow.

**Group strings by section in en.json:**
- `nav.*` — sidebar labels (Inbox, Sent, Drafts, Trash, Contacts, Calendar, etc.)
- `mail.*` — mailbox and message actions (Compose, Reply, Forward, Delete, etc.)
- `compose.*` — compose form labels
- `calendar.*` — calendar views and event labels
- `contacts.*` — contact management labels
- `admin.*` — admin panel labels
- `settings.*` — preferences page labels
- `auth.*` — login, 2FA, password labels
- `common.*` — shared (Save, Cancel, Delete, Loading, Error, etc.)

**Done when:** i18n.Bundle loads embedded message files. `T("en", "mail.compose")`
returns "Compose". `T("fr", "mail.compose")` returns "Composer" (or falls back to English
for missing translations). All .templ files use `i18n.T()` instead of hardcoded strings.

### 5. Internationalisation — locale selector and preference storage

Let users choose their language.

**Modify `proto/bongo/v1/preferences.proto`:**
- Add `string locale = 10;` field (e.g., "en", "fr", "de", "es")
- Run `proto/generate.go` to regenerate

**Modify `internal/web/handlers/preferences.go`:**
- Read locale from UserPreferences on each request
- Pass locale to template rendering context
- Default to "en" if not set
- Detect browser `Accept-Language` header as initial suggestion

**Modify `internal/web/templates/settings.templ`:**
- Add "Language" section to preferences page:
  - Dropdown of available locales (from `I18n.AvailableLocales()`)
  - Current selection highlighted
  - Save triggers HTMX partial swap

**Modify template rendering context:**
- Add `Locale string` to all template data structs (or use a shared context struct)
- Pass locale from handler → template → i18n.T() calls

**Modify `internal/web/templates/layout.templ`:**
- Set `<html lang="{ locale }">` dynamically from user preference

**Done when:** User selects "Français" in Settings → UI renders in French (where
translations exist, English fallback for untranslated strings). Locale persists in
preferences. HTML lang attribute matches selection.

### 6. Performance — HTTP compression and response optimization

Add gzip compression and response optimization.

**Modify `internal/agent/agent.go` (or router setup):**
- Add Chi compression middleware:
  ```go
  import "github.com/go-chi/chi/v5/middleware"
  r.Use(middleware.Compress(5, "text/html", "text/css", "application/javascript",
      "application/json", "text/plain", "image/svg+xml"))
  ```
  (Chi's built-in compress middleware — no extra dependency needed)
- Exclude blob endpoints from compression (already compressed binary data)
- Exclude SSE endpoint (chunked streaming, compression interferes)

**Add ETags and caching for static assets:**
- Set `Cache-Control: public, max-age=86400` for `/static/` assets (CSS, JS, images)
- Use `http.FileServer` with proper cache headers
- Add ETag based on file modification time or content hash

**Compress SSE payloads where beneficial:**
- SSE newmail events: minimize JSON payload (remove unnecessary fields)
- Keep SSE uncompressed (compression middleware should skip text/event-stream)

**Done when:** HTML, CSS, JS responses are gzip-compressed (verify with `curl -H
"Accept-Encoding: gzip"`). Static assets have cache headers. Blob downloads and SSE
streams are not compressed.

### 7. Performance — infinite scroll and lazy loading

Add infinite scroll option for message lists and lazy-load patterns.

**Modify `internal/web/templates/mailbox.templ`:**
- Add infinite scroll trigger at bottom of message list:
  ```html
  <div hx-get="/mail/{mailbox}?page={nextPage}&partial=1"
       hx-trigger="revealed"
       hx-swap="afterend"
       hx-indicator="#load-more-spinner">
    <span id="load-more-spinner" class="htmx-indicator">Loading...</span>
  </div>
  ```
- Only show when there are more pages to load
- Hide traditional pagination when infinite scroll is active

**Modify `internal/web/handlers/mail.go`:**
- Accept `partial=1` query parameter:
  - If partial: return only message rows HTML (no pagination, no wrapper)
  - If not partial: return full mailbox page with pagination
- This enables both traditional pagination and infinite scroll from same endpoint

**Add preference for infinite scroll:**
- Add `bool infinite_scroll = 11;` to UserPreferences proto (or use existing
  messages_per_page = 0 to mean infinite)
- Toggle in Settings → Display section

**Lazy-load contact list and admin user list:**
- Same `hx-trigger="revealed"` pattern for contact list pagination
- Admin user list: paginate with revealed trigger for large user bases

**Prefetch next page:**
- On message list, add `hx-trigger="revealed"` on a sentinel element near bottom
  (e.g., 5 items before end) to prefetch next batch into a hidden div
- Swap in on scroll

**Done when:** Message list loads more items automatically as user scrolls down.
Traditional pagination still works as fallback. Contact list and admin user list
lazy-load additional pages. No full page reload needed.

### 8. E2E smoke test — full user journey

Comprehensive end-to-end test covering the complete user experience.

**Extend `test/e2e/e2e_test.go`** (or create `test/e2e/smoke_test.go`):

**TestFullUserJourney** (build tag `e2e`):
1. Start dev agent with test user
2. **Login:** POST `/login` with admin/admin → verify session cookie + redirect
3. **Browse inbox:** GET `/mail/INBOX` → verify 200, contains mailbox list
4. **Compose & send:** POST `/compose/send` with To: admin@localhost, Subject, Body
   → verify redirect, message in Sent
5. **Receive:** Wait for delivery (poll INBOX via HTTP) → verify message appears
6. **Read message:** GET `/mail/INBOX/{uid}` → verify subject, body rendered
7. **Reply:** POST compose with in_reply_to → verify threading in INBOX
8. **Search:** GET `/search?q={subject}` → verify message found in results
9. **Star message:** POST flag toggle → verify star appears
10. **Move message:** POST move to custom folder → verify moved
11. **Create contact:** POST `/contacts` → verify in contact list
12. **Contacts in autocomplete:** GET `/api/gal?q={contact_name}` → verify returned
13. **Calendar — create event:** POST `/calendar/events` → verify event created
14. **Calendar — month view:** GET `/calendar?view=month` → verify event chip visible
15. **Admin dashboard:** GET `/admin` → verify dashboard loads with stats
16. **Admin user list:** GET `/admin/users` → verify user listed
17. **Settings:** GET `/settings` → verify preferences page loads
18. **Logout:** POST `/logout` → verify session cleared, redirect to login

**TestAccessibilityBasics** (build tag `e2e`):
1. GET `/mail/INBOX` → verify response contains `aria-label` attributes
2. Verify `<a class="skip-link">` exists
3. Verify `<main id="main-content">` exists
4. Verify `role="alert"` on flash messages (trigger a flash by invalid action)

**TestI18nLocaleSwitch** (build tag `e2e`):
1. Login, set locale to "fr" via preferences
2. GET `/mail/INBOX` → verify French strings appear (e.g., "Composer" not "Compose")
3. Set locale back to "en" → verify English strings

**Done when:** `go test -race -tags=e2e ./test/e2e/` passes. Full user journey covers
login, mail (compose, send, receive, read, reply, search, star, move), contacts,
calendar, admin, settings, and logout. Accessibility and i18n basic tests pass.

---

## Exit Criteria

- [x] Skip-to-content link visible on Tab key, jumps focus to main content
- [x] Semantic landmarks: `<header role="banner">`, `<nav aria-label>`, `<main>`,
      `<aside aria-label>`
- [x] ARIA labels on all interactive elements (checkboxes, buttons, toggles, links)
- [x] Flash messages and dynamic content use `role="alert"` / `aria-live`
- [x] Focus management after HTMX swaps (focus moves to new content heading)
- [x] Keyboard shortcut help modal (`?` key)
- [x] Context menu keyboard-navigable (arrow keys, Escape to close)
- [x] `.sr-only` CSS class for screen-reader-only text
- [x] `go-i18n` library integrated with embedded message catalogue
- [x] English message catalogue (`en.json`) with all extracted UI strings
- [x] Stub translations for French, German, Spanish (key files exist)
- [x] `locale` field in UserPreferences proto
- [x] Locale selector in Settings page
- [x] `<html lang="">` dynamic from user preference
- [ ] Template strings use `i18n.T()` instead of hardcoded English
- [x] HTTP gzip compression for HTML/CSS/JS responses (Chi middleware)
- [x] Static asset cache headers (Cache-Control, ETag)
- [x] Infinite scroll option for message list (`hx-trigger="revealed"`)
- [x] `partial=1` parameter for message list partial responses
- [x] E2E smoke test covers full user journey (18 steps: login → logout)
- [x] E2E accessibility basics test passes
- [x] E2E i18n locale switch test passes
- [x] `go test -race ./...` passes
- [x] `go test -race -tags=e2e ./test/e2e/` passes

## What NOT to do in this sprint

- No RTL (right-to-left) language support (future, needs layout changes)
- No automated accessibility testing tool (axe-core, pa11y — future CI integration)
- No WCAG AAA compliance (aim for AA level basics)
- No full translations for fr/de/es (stubs only — community can contribute)
- No HTTP/2 server push (Go stdlib http2 handles this automatically when TLS active)
- No CDN or asset bundling (single binary, static files served directly)
- No WebSocket replacement for SSE (SSE is sufficient for current needs)
- No virtual scrolling (DOM recycling) — infinite scroll with append is sufficient
- No colour contrast audit (defer to Sprint 14 visual matching)
- No automated i18n string extraction tool (manual extraction is fine for 150–200 strings)
