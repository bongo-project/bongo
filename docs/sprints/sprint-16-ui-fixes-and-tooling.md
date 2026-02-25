# Sprint 16: UI Fixes, Seed Data & Operational Tooling

> **REVISION 5 (2026-02-24):** This doc has been updated. RE-READ IT FULLY.
> Key additions since revision 2:
> - **FIXED: `main-content` → `content` class** in security.templ and rules.templ
> - **FIXED: Skip-link visibility** — changed from `top: -100%` to `left: -9999px`
> - **FIXED: Settings tabs** — added Alpine.js x-data/x-show for tab switching
> - **FIXED: Context menu** — added viewport clamping to prevent off-screen positioning
> - **FIXED: Sidebar active state** — added `updateSidebarActive()` in bongo.js
> - **IMPORTANT:** Run `templ generate` after pulling these changes to regenerate _templ.go files

**Phase:** Post-Phase 1 Polish
**Goal:** Fix the broken layout issues in the web UI, add a seed-data script that
populates realistic mail so the UI can be visually verified, fix VCF contact import,
and add an HTTP-only mode for running behind a load balancer.

---

## Context

Phase 1 is complete. The UI looks significantly better after the Sprint 15a/15b
prototype-first redesign, but there are several layout bugs that make the UI feel
broken: overlapping navigation bars, oversized sidebars, broken menu links, and
spacing issues. Additionally, the existing integration tests verify protocol
correctness programmatically but don't leave any visible mail in the system — we
need a seed script that sends real mail through SMTP so a developer can see the UI
with realistic content.

**Three additional fixes requested:**
1. VCF contact import not populating the system properly
2. HTTP-only mode for web UI when running behind a load balancer
3. Seed data script for visual testing

---

## Reference material

**Current CSS:** `internal/web/static/css/bongo.css` (2,724 lines)
**Templates:** `internal/web/templates/*.templ` (14 files)
**Layout:** `layout.templ` — top-bar (z-index 2100), app-layout, sidebar, main-content
**Admin:** `admin.templ` — admin-sidebar (240px), admin-main
**Components:** `components.templ` — Sidebar(), Pagination, FlashMessage
**Config:** `internal/config/config.go` — WebConfig struct
**Agent:** `internal/agent/agent.go` — TLS setup (lines 520-541), HTTP server start
**Contacts handler:** `internal/web/handlers/contacts.go` — ImportContacts()

**Known UI issues (from user screenshots — 6 screenshots reviewed 2026-02-23):**

**Screenshot 1 & 2 (Inbox views):**
- Sidebar extends behind/under the top bar header (visible in screenshot 4 where
  Compose button is partially hidden by top bar)
- The inbox list layout itself works but column alignment could be tighter

**Screenshot 3 (Compose page) — BADLY BROKEN:**
- Compose form has lost all styling — raw form inputs in a single horizontal line
- Textarea for message body is a tiny box floating in the upper-right area
- No card/container wrapping the compose form
- This is the highest-priority fix — compose.templ is not picking up the CSS
  from the prototype shared.css that was ported to bongo.css
- Compare with `prototype/compose.html` to see what it should look like

**Screenshot 5 (Contacts list) — VCF IMPORT BUG VISIBLE:**
- Contact names imported but email and phone columns are EMPTY for most entries
- One entry shows raw quoted-printable encoding: `=48=65=69=6B=6F=20=4B=...`
  instead of decoded text — the vCard decoder is not handling QP encoding
- This confirms the VCF import handler is parsing names but not extracting
  EMAIL and TEL properties properly (likely a vCard version compatibility issue)

**Screenshot 6 (Contact detail/edit form) — CSS BUG:**
- Form input fields have dark/black backgrounds making text nearly unreadable
- The input background colour is not being set properly — likely inheriting from
  Pico CSS dark mode or a missing override in bongo.css
- Fix: ensure `.main-content input, .main-content textarea, .main-content select`
  have explicit `background: var(--input-bg)` and `color: var(--input-text)`

**General issues across all screenshots:**
- Sidebar bleeds under top bar (z-index / positioning conflict)
- Content area spacing inconsistencies between pages
- Mobile responsive breakpoint (768px only) may not cover tablets

---

## Tasks

### 1. Fix layout structure and z-index hierarchy

The top-bar, sidebar, and main-content need a clean, non-overlapping layout.

**Audit and fix in `bongo.css`:**
- Establish a clear z-index scale: top-bar=100, sidebar=50, main-content=1,
  overlays/modals=200+, mobile-sidebar=150, mobile-overlay=149
- Fix `.app-layout` to use proper CSS Grid instead of flexbox:
  ```
  .app-layout { display: grid; grid-template-columns: var(--sidebar-width) 1fr; }
  ```
- Ensure `.top-bar` doesn't overlap `.app-layout` (use `grid-template-rows` or
  proper margin-top on app-layout to account for top-bar height)
- Fix sidebar height: should be `calc(100vh - var(--top-bar-height))` not `100vh`
- Remove any competing `position: fixed` / `position: absolute` that causes overlap

**Verify in `layout.templ`:**
- The DOM order should be: `<header class="top-bar">` → `<div class="app-layout">`
  containing `<aside class="sidebar">` + `<main class="main-content">`
- No duplicate sidebar or nav elements being rendered

**Done when:** Top bar sits above content, sidebar sits beside content, nothing overlaps.

### 2. Fix sidebar width and responsive behaviour

**In `bongo.css`:**
- Reduce default `--sidebar-width` from 260px to 220px (more content space)
- Add intermediate breakpoint at 1024px: sidebar collapses to icons-only (48px)
  with tooltips, or hides entirely with hamburger toggle
- At 768px and below: sidebar is hidden, hamburger shows slide-over (280px)
- Ensure sidebar doesn't push content off-screen on 1024px-1280px viewports
- Fix `.sidebar-section` padding to be tighter (reduce from `var(--space-md)`)
- Compose button: ensure it doesn't overflow sidebar at narrow widths

**Done when:** Sidebar fits comfortably at all viewport sizes from 320px to 2560px.

### 3. Fix admin sidebar / mail sidebar transition

When navigating between mail UI and admin panel, the sidebars should swap cleanly.

**In `bongo.css`:**
- Admin layout uses `LayoutWithContent` (no mail sidebar) — verify no mail sidebar
  bleeds through
- Admin sidebar gradient and width (240px) should be independent of mail sidebar
- "Back to Mail" link in admin footer should cleanly restore mail layout

**In `admin.templ` and `layout.templ`:**
- Verify `AdminLayout` does NOT render the mail `Sidebar()` component
- Verify admin routes use `AdminLayout` not `Layout`
- Check for any stale Alpine.js state (`sidebarOpen`) leaking between layouts

**Done when:** Switching between mail and admin shows the correct sidebar for each.

### 4. Fix broken menu links and navigation

Audit every link in both sidebars and top bar. Fix any that 404 or don't navigate.

**Mail sidebar links to verify:**
- Compose → `/compose` (or opens compose form)
- Unread → `/mail/unread`
- Starred → `/mail/starred`
- All Mail → `/mail/all-mail`
- Inbox, Sent, Drafts, Trash, Junk → `/mail/{folder}`
- Contacts → `/contacts`
- Calendar → `/calendar`
- Admin → `/admin`

**Top bar links to verify:**
- Search → submits to `/search`
- Settings → `/settings`
- Logout → `/logout`

**Admin sidebar links to verify:**
- Dashboard → `/admin`
- Users → `/admin/users`
- Domains → `/admin/domains`
- Aliases → `/admin/aliases`
- Queue → `/admin/queue`
- Cluster → `/admin/cluster`
- Logs → `/admin/logs`

**In route registration (agent.go):**
- Verify every link above has a corresponding route registered
- Add any missing routes (even if handler returns placeholder content)

**Done when:** Every sidebar and top-bar link navigates to a real page with no 404s.

### 5. Fix content area spacing and page consistency

**In `bongo.css`:**
- Ensure `.main-content` fills remaining horizontal space (flex: 1 or grid column)
- Consistent padding on all page content areas (mail list, message view, contacts,
  calendar, settings, admin pages)
- Fix any pages where content is cut off or has excessive whitespace
- Ensure message list table fills width properly
- Fix reading pane sizing in two-pane and three-pane modes

**Cross-page consistency:**
- Page headers: consistent size, weight, spacing
- Action buttons: consistent styling (primary = orange, secondary = outlined)
- Form inputs: consistent width and padding
- Tables: consistent column sizing and row height
- Empty states: consistent messaging and styling

**Done when:** All pages feel like they belong to the same app.

### 5b. Fix compose page layout (HIGHEST PRIORITY)

The compose page is badly broken — form inputs are in a raw horizontal line, the
textarea is tiny and misplaced, there's no card/container wrapping.

**Compare `compose.templ` with `prototype/compose.html`:**
- The prototype has: `.compose-container` → `.compose-header` + `.compose-form`
- Inside form: recipient chips input, subject line, format toolbar, textarea, attachments
- All wrapped in a card with proper spacing

**Fix in `compose.templ`:**
- Ensure the compose form uses the same CSS classes as the prototype
- Verify the HTML structure matches: container → header → form → fields → actions
- Check that `.compose-container`, `.compose-form`, `.compose-body` classes exist in
  bongo.css (they were supposed to be ported from shared.css)

**Fix in `bongo.css`:**
- Verify all compose-related classes were ported from `prototype/shared.css`
- Key classes: `.compose-container`, `.compose-form`, `.compose-field`, `.compose-body`,
  `.compose-actions`, `.compose-toolbar`
- The textarea should be: `min-height: 300px; width: 100%;`

**Done when:** Compose page matches the prototype — card container, proper field layout,
full-width textarea, action buttons at bottom.

### 5c. Fix form input styling (contacts, settings, all forms)

Contact detail/edit form has dark/black input backgrounds making text unreadable.

**Fix in `bongo.css`:**
- Add explicit form input overrides that work in both light and dark mode:
  ```css
  .main-content input[type="text"],
  .main-content input[type="email"],
  .main-content input[type="tel"],
  .main-content input[type="password"],
  .main-content input[type="search"],
  .main-content textarea,
  .main-content select {
    background: var(--input-bg, #fff);
    color: var(--input-text, #1a1a2e);
    border: 1px solid var(--input-border, #d0d5dd);
  }
  [data-theme="dark"] .main-content input,
  [data-theme="dark"] .main-content textarea,
  [data-theme="dark"] .main-content select {
    background: var(--input-bg-dark, #1e2a3a);
    color: var(--input-text-dark, #e0e0e0);
    border-color: var(--input-border-dark, #3a4a5a);
  }
  ```
- Check if Pico CSS dark mode is overriding input colours — may need `!important`
  or more specific selectors
- Test on contacts page, settings page, compose page, admin user create form

**Done when:** All form inputs are readable in both light and dark mode.

### 6. Fix VCF contact import

**Confirmed bugs from screenshots:**
- Contact NAMES import correctly but EMAIL and PHONE columns are EMPTY
- One entry shows raw quoted-printable hex: `=48=65=69=6B=6F=20=4B=...` instead
  of decoded text — the decoder is not handling QP encoding from vCard 2.1

**Investigate in `internal/web/handlers/contacts.go`:**
- Debug `ImportContacts()` — check vCard parsing with `emersion/go-vcard`
- The EMAIL property extraction is likely wrong — vCard uses `EMAIL;TYPE=INTERNET:`
  in v2.1, `EMAIL:` in v3.0+, and may have multiple EMAIL entries
- The TEL property extraction is likely wrong — vCard uses `TEL;TYPE=CELL:` etc.
- The quoted-printable decoding issue: vCard 2.1 uses `ENCODING=QUOTED-PRINTABLE`
  on property values — the go-vcard library may not auto-decode these
- Ensure imported contacts are stored via Raft (not just local)
- Verify Bleve index is updated after import

**Fix priorities:**
1. Fix EMAIL extraction: handle `EMAIL;TYPE=*:`, `EMAIL:`, multiple values (take first)
2. Fix TEL extraction: handle `TEL;TYPE=*:`, `TEL:`, multiple values
3. Fix quoted-printable decoding: decode QP-encoded values before storing
4. Handle all vCard versions (2.1, 3.0, 4.0)
5. Gracefully skip malformed entries instead of failing entire import
6. Show import results: "Imported 42 contacts (38 with email, 25 with phone), 3 skipped"

**Test:** Export contacts from macOS Contacts → import .vcf → verify names, emails,
and phone numbers all appear correctly in the contacts list.

**Done when:** Importing a .vcf file from macOS Contacts or Google Contacts correctly
populates name, email, AND phone for all contacts.

### 7. Add HTTP-only mode for load balancer deployments

When Bongo runs behind a reverse proxy (nginx, Caddy, Traefik, AWS ALB), the proxy
terminates TLS and forwards plain HTTP. Currently dev mode auto-generates a self-signed
cert, and production mode requires TLS config or fails.

**In `internal/config/config.go`:**
- Add `PlainHTTP bool` to `WebConfig`:
  ```go
  PlainHTTP bool `hcl:"plain_http,optional" mapstructure:"plain_http"`
  ```

**In `internal/agent/agent.go`** (TLS setup, ~line 520):
- If `cfg.Web.PlainHTTP` is true, skip ALL TLS setup (no self-signed generation,
  no cert loading) — start with `ListenAndServe()` not `ListenAndServeTLS()`
- Log a clear warning: "Web server running in plain HTTP mode (no TLS)"
- Also ensure cookie `Secure` flag is NOT set when in plain HTTP mode (otherwise
  cookies won't be sent over HTTP, breaking sessions)

**In CLI flags (if applicable):**
- Add `--plain-http` flag as convenience alias for `web.plain_http = true`

**In HCL config example:**
```hcl
web {
  enabled    = true
  port       = 8080
  plain_http = true  // Use when running behind a TLS-terminating load balancer
}
```

**Security note:** Add comment that plain_http should ONLY be used behind a
trusted reverse proxy on a private network.

**Done when:** `bongo-go dev --plain-http` starts web UI on HTTP (no TLS), sessions work.

### 8. Add `bongo-import directory` subcommand (bulk directory scanner)

The existing `bongo-import` CLI (`cmd/bongo-import/`) already supports mbox, eml,
maildir, and Thunderbird profiles — but each requires a separate command and only
handles mail. We need a single command that scans a directory and imports everything:
mail, contacts, and calendar entries.

**New file: `cmd/bongo-import/directory.go`**

Add a new Cobra subcommand: `bongo-import directory <path> [flags]`

**Behaviour:**
1. Recursively walk the directory tree
2. Classify files by extension:
   - `.mbox`, `.mbx` → mail (use existing `NewMboxSource()` from `internal/importer/`)
   - `.eml` → mail (use existing `NewEMLSource()`)
   - `Maildir/` directories → mail (use existing `NewMaildirSource()`)
   - `.vcf` → contacts (new: import via web API `POST /contacts/import` or
     directly via CardDAV)
   - `.ics` → calendar (new: import via web API `POST /calendar/import` or
     directly via CalDAV)
3. Report discovery summary before importing:
   ```
   Found in /path/to/data:
     Mail:     23 .mbox files, 145 .eml files
     Contacts: 2 .vcf files
     Calendar: 3 .ics files
   Proceed? [Y/n]
   ```
4. Import in order: contacts first (for autocomplete), then calendar, then mail
5. Mail imports use IMAP APPEND (existing pipeline) with flag/date preservation
6. Contact/calendar imports use HTTP multipart upload to the web API endpoints
   (reuse the same parsing logic already in `handlers/contacts.go` and
   `handlers/calendar.go`)
7. Track state for resume on failure (extend existing `state.go`)
8. Print final summary with counts and errors

**Flags (inherit from existing commands where possible):**
- `--dst-host` — Bongo server address (default: `localhost`)
- `--dst-port` — IMAP port (default: 143 or 1143 for dev)
- `--dst-user` — destination user email (default: `admin@localhost`)
- `--dst-pass` — destination user password (default: `admin`)
- `--web-url` — web API base URL for contact/calendar import (default: `https://localhost:8080`)
- `--state` — state file path for resume capability
- `--dry-run` — scan and report but don't import
- `--mail-only`, `--contacts-only`, `--calendar-only` — filter by type
- `--yes` — skip confirmation prompt

**Contact import path (new code needed):**
- Read .vcf file
- HTTP POST multipart to `{web-url}/contacts/import` with auth cookie/session
- OR: use CardDAV PUT to `{web-url}/carddav/{user}/contacts/` for each contact
- Parse response for success/error counts

**Calendar import path (new code needed):**
- Read .ics file
- HTTP POST multipart to `{web-url}/calendar/import` with auth cookie/session
- OR: use CalDAV PUT to `{web-url}/caldav/{user}/calendars/default/` for each event
- Parse response for success/error counts

**For 34,000 emails:** The existing IMAP APPEND pipeline already handles this —
`NewMboxSource()` streams messages one at a time without loading the entire file
into memory. The state tracker allows resume on failure. Consider adding a
`--workers N` flag for parallel IMAP connections (default: 4) to speed up large
imports.

**Done when:** `bongo-import directory testdata/demo/ --dst-user admin@localhost --dst-pass admin`
imports all mail, contacts, and calendar events from the directory.

### 8b. Create demo dataset in `testdata/demo/`

Ship a curated demo dataset in the repo for testing and showcasing.

**Directory structure:**
```
testdata/demo/
  README.md           — explains the dataset and how to import it
  mail/
    inbox.mbox        — 20-30 realistic emails in mbox format
    sent.mbox         — 5-10 sent messages
    threads.mbox      — 3-4 threaded conversations (proper In-Reply-To/References)
  contacts/
    team.vcf          — 10 contacts with names, emails, phones, companies
  calendar/
    meetings.ics      — 5 calendar events (meetings, all-day, recurring)
```

**Mail content guidelines:**
- Mix of plain text and HTML (multipart/alternative)
- Various realistic subjects: project updates, meeting requests, newsletters
- Proper email headers (Message-ID, Date, From, To, Subject)
- Thread conversations with correct References/In-Reply-To chains
- Some messages with attachment MIME headers (even if attachments are small/dummy)
- Dates spread over the past week for visual variety
- Senders: alice@example.com, bob@example.com, charlie@example.com,
  newsletter@bongoproject.org

**Contact content:**
- vCard 3.0 format (most compatible)
- Include: FN, EMAIL, TEL, ORG, NOTE fields
- 10 contacts with realistic but fictional data

**Calendar content:**
- iCalendar format
- Mix of: single events, all-day events, one recurring weekly event
- Events spread across current week and next week
- Include: SUMMARY, DTSTART, DTEND, LOCATION, DESCRIPTION

**Usage documented in README.md:**
```bash
# Start Bongo in dev mode
bongo-go dev &

# Import all demo data
bongo-import directory testdata/demo/ \
  --dst-user admin@localhost \
  --dst-pass admin

# Open browser
open https://localhost:8080
```

**Done when:** `testdata/demo/` contains realistic data that populates a visually
rich UI when imported.

### 8c. Create server validation script (`scripts/validate-server.go`)

A Go program that exercises every server subsystem end-to-end. Unlike the demo
dataset (static files for UI showcase), this script actively tests the live server
by creating users, sending mail through SMTP, reading it back via IMAP and POP3,
verifying search, testing antispam scoring, triggering outbound bounces, and
checking that contacts/calendar round-trip correctly.

**Run:** `go run scripts/validate-server.go [--host localhost] [--web-port 8080] [--smtp-port 2525] [--imap-port 1143] [--pop3-port 1110]`

**Dependencies:** `net/smtp`, `emersion/go-imap/v2`, `emersion/go-vcard`,
`emersion/go-ical`, `net/http` — all already in the project's go.mod.

---

**Phase 1: User setup (via admin web API)**

1. Log in as admin@localhost / admin to get a session cookie
2. Create 3 test users via `POST /admin/users`:
   - alice@localhost (password: alice123)
   - bob@localhost (password: bob123)
   - charlie@localhost (password: charlie123)
3. **Verify:** Each user can authenticate via IMAP (PLAIN auth)

---

**Phase 2: SMTP inbound delivery**

4. Alice sends 5 plain-text emails to bob@localhost via SMTP (port 587/2525,
   authenticated as alice@localhost):
   - "Project kickoff" — short text
   - "Meeting notes" — longer text (500+ words)
   - "Re: Project kickoff" — reply with proper In-Reply-To / References headers
   - "Re: Re: Project kickoff" — second reply (3-message thread)
   - "Budget spreadsheet" — with a small attachment (text/plain attachment, ~1KB)
5. Bob sends 3 emails to alice@localhost:
   - "Welcome aboard" — plain text
   - "Q3 Report" — HTML body (multipart/alternative with text + HTML parts)
   - "Photo from the team event" — with an inline image attachment (small PNG)
6. Charlie sends 2 emails to both alice and bob (CC):
   - "Team lunch Friday" — To: alice, CC: bob
   - "Updated schedule" — To: bob, CC: alice
7. **Verify each delivery:**
   - Connect as recipient via IMAP
   - Check INBOX message count matches expected
   - Verify message subjects, From headers, dates
   - Verify threading: the 3-message thread should have matching References
   - Verify attachment: "Budget spreadsheet" message has MIME multipart with
     attachment part; can fetch attachment body section via IMAP
   - Verify HTML: "Q3 Report" has text/html part accessible via IMAP BODY[2]
   - Verify CC: Charlie's emails appear in both alice's and bob's INBOX

---

**Phase 3: IMAP operations**

8. As bob, perform IMAP operations:
   - LIST mailboxes — verify INBOX, Sent, Drafts, Trash, Junk exist
   - FETCH envelope for all messages
   - Set \Seen flag on first 3 messages
   - Set \Flagged (starred) on 1 message
   - Create new folder "Archive"
   - COPY 1 message to Archive
   - SEARCH for "budget" — verify the attachment message is found
   - IDLE for 2 seconds (verify no crash)
9. **Verify:**
   - \Seen flags persisted (re-fetch and check)
   - \Flagged flag persisted
   - Archive folder exists and has 1 message
   - SEARCH returned correct UID

---

**Phase 4: POP3 retrieval**

10. As alice, connect via POP3:
    - STAT — verify message count and total size
    - LIST — verify per-message sizes
    - UIDL — verify unique IDs
    - RETR 1 — fetch first message, verify headers
    - TOP 1 5 — fetch first 5 lines of message 1
    - DELE 1 — mark message for deletion
    - RSET — undo deletion
    - QUIT
11. **Verify:** All POP3 commands return expected responses, no errors

---

**Phase 5: Antispam scoring**

12. Send a "spammy" email to bob@localhost from an unauthenticated external sender
    (connect to port 25/2525 without AUTH):
    - From: winner@free-prizes-now.example.com
    - Subject: "CONGRATULATIONS!!! You WON $1,000,000!!!"
    - Body contains spam trigger phrases: "click here", "act now", "limited time",
      "wire transfer", "Nigerian prince", all-caps text, multiple exclamation marks
    - Include suspicious headers: X-Mailer: Mass-Mailer-Pro
13. Send a clean email to bob from alice (authenticated) for comparison
14. **Verify:**
    - Spammy email has spam_score > 0.5 (check via IMAP FETCH or web API)
    - Spammy email was routed to Junk folder (not INBOX)
    - Clean email has spam_score < 0.3 and is in INBOX
15. As bob, mark the spammy email as "Not Spam" via web API:
    - POST to mark-not-spam endpoint
    - **Verify:** Message moved from Junk to INBOX (Bayesian learning)
16. As bob, mark a clean email as "Spam" via web API:
    - **Verify:** Message moved to Junk (Bayesian learning)

---

**Phase 6: Outbound relay & bounce**

17. As alice, compose and send an email to nonexistent@invalid-domain-that-does-not-exist.example:
    - Submit via the compose web API (POST /compose or SMTP submission)
18. **Verify:**
    - Message appears in outbound queue (`/admin/queue` or queue store)
    - After delivery attempt fails (DNS lookup failure), message is marked failed
    - A bounce notification (DSN) is generated and delivered to alice's INBOX
    - Bounce message contains original subject and failure reason

---

**Phase 7: Contacts**

19. As alice, create 5 contacts via web API (POST /contacts):
    - Bob Smith (bob@localhost), with phone and company
    - Charlie Brown (charlie@localhost)
    - External contact: Diana Prince (diana@example.com)
    - Contact with all fields: name, email, phone, company, notes
    - Contact with minimal fields: just name and email
20. Export contacts as VCF (GET /contacts/export)
21. Delete all contacts
22. Re-import the exported VCF (POST /contacts/import)
23. **Verify:**
    - All 5 contacts restored with correct name, email, phone
    - Contact search works (search "Bob" returns Bob Smith)
    - Compose autocomplete includes contacts (search via GAL endpoint)

---

**Phase 8: Calendar**

24. As bob, create 4 calendar events via web API (POST /calendar/events):
    - "Team standup" — today, 9:00-9:30, location: "Zoom"
    - "Sprint planning" — tomorrow, 14:00-15:00
    - "Company holiday" — next Monday, all-day event
    - "Weekly sync" — recurring weekly on Wednesdays
25. Export calendar as ICS (GET /calendar/export)
26. Delete all events
27. Re-import the exported ICS (POST /calendar/import)
28. **Verify:**
    - All 4 events restored with correct dates, times, summaries
    - Recurring event has RRULE
    - All-day event has correct DATE (not DATETIME) format
    - Month view API returns events for current week

---

**Phase 9: Search integration**

29. As bob, test full-text search:
    - Search "budget" — should find alice's attachment email
    - Search "project kickoff" — should find thread
    - Search "team lunch" — should find charlie's CC email
    - Search from:alice — should find all emails from alice
30. **Verify:** Each search returns the expected message(s) by subject

---

**Phase 10: Web UI session & auth**

31. Test login flow via HTTP:
    - POST /login with bob's credentials → verify session cookie set
    - GET /mail/INBOX with session cookie → verify 200 response
    - GET /settings → verify 200
    - GET /admin → verify 200 (if bob is not admin, verify 403)
    - POST /logout → verify session invalidated
    - GET /mail/INBOX without cookie → verify redirect to /login

---

**Phase 11: Final summary**

Print a structured report:
```
═══════════════════════════════════════════════════
  Bongo Server Validation Report
═══════════════════════════════════════════════════

  Users created:      3 (alice, bob, charlie)

  SMTP Delivery:      10/10 messages delivered     ✓
  Threading:          3-message thread verified     ✓
  Attachments:        2/2 attachments retrievable   ✓
  HTML mail:          1/1 multipart verified        ✓
  CC routing:         2/2 CC'd messages delivered   ✓

  IMAP Operations:    9/9 passed                    ✓
  POP3 Operations:    7/7 passed                    ✓

  Antispam:           Spam scored > 0.5, routed     ✓
  Bayesian learning:  Mark spam/not-spam working    ✓

  Outbound queue:     Message queued                ✓
  Bounce generation:  DSN delivered to sender       ✓

  Contacts:           5 created, exported, re-imported ✓
  Contact search:     Working                       ✓

  Calendar:           4 events, exported, re-imported  ✓
  Recurring events:   RRULE preserved               ✓

  Search:             4/4 queries returned expected  ✓

  Web auth:           Login/logout/session verified  ✓

  ─────────────────────────────────────────────────
  TOTAL: 45/45 checks passed                       ✓

  Data left in server for UI browsing:
  - bob: 8 emails (5 unread, 3 read, 1 starred)
  - alice: 7 emails (includes 1 bounce notification)
  - bob: 5 contacts, 4 calendar events
═══════════════════════════════════════════════════
```

Exit code: 0 if all checks pass, 1 if any fail (with failed checks listed).

**Done when:** `go run scripts/validate-server.go` against a fresh `bongo-go dev`
passes all checks, leaves realistic data in the server for UI browsing, and exits 0.

### 9. Visual verification

After all fixes, import demo data (`bongo-import directory testdata/demo/`) and
verify every page in the running app.

**Mail UI pages:**
1. Login — clean, no layout issues
2. Inbox with demo messages — unread/read/starred states visible
3. Message thread — bubbles render correctly
4. Compose — form works, recipients autocomplete from seeded contacts
5. Calendar — seeded events visible on month view
6. Contacts — seeded contacts visible in list
7. Settings — all sections accessible
8. Search — returns results from seeded data

**Admin UI pages:**
9. Dashboard — status cards, no sidebar overlap
10. Users — seeded users visible
11. Domains — list renders
12. Queue — renders (may be empty)
13. Cluster — node status
14. Logs — streaming works

**Test at viewports:** 1440px, 1024px, 768px, 375px (iPhone SE)

**Done when:** All pages render correctly with no overlapping elements, broken links,
or layout issues at all tested viewports.

---

## Exit Criteria (30 items)

**Layout & z-index (Tasks 1-3):**
- [ ] No z-index overlap between top bar, sidebar, and content
- [ ] Sidebar doesn't extend behind/under the top bar
- [ ] Sidebar width appropriate at all viewport sizes (220px default)
- [ ] Intermediate responsive breakpoint at 1024px works
- [ ] Admin sidebar and mail sidebar don't conflict when switching

**Compose page (Task 5b — HIGHEST PRIORITY):**
- [ ] Compose page has card container wrapping the form
- [ ] Compose textarea is full-width, min 300px height
- [ ] Compose form layout matches prototype/compose.html

**Form inputs (Task 5c):**
- [ ] All form inputs readable in light mode (contacts, settings, admin)
- [ ] All form inputs readable in dark mode

**Navigation (Task 4):**
- [ ] All mail sidebar links navigate correctly (no 404s)
- [ ] All admin sidebar links navigate correctly (no 404s)
- [ ] All top bar links work (search, settings, logout)

**Content consistency (Task 5):**
- [ ] Content area fills available space on all pages
- [ ] Cross-page styling is consistent (buttons, forms, tables, headers)

**VCF import (Task 6):**
- [ ] VCF import populates email AND phone (not just name)
- [ ] Quoted-printable encoded values are decoded properly
- [ ] VCF import handles vCard 2.1, 3.0, and 4.0

**HTTP-only mode (Task 7):**
- [ ] `plain_http` config option added to WebConfig
- [ ] Plain HTTP mode skips TLS, starts on HTTP, sessions work

**Directory scanner & demo data (Tasks 8-8b):**
- [ ] `bongo-import directory <path>` command exists and scans recursively
- [ ] Handles .mbox, .eml, .vcf, .ics files automatically by extension
- [ ] Supports --dry-run, --state (resume), --yes (skip prompt)
- [ ] Contact and calendar files imported via web API or DAV endpoints
- [ ] `testdata/demo/` contains 20+ emails, 10 contacts, 5 calendar events
- [ ] Demo data imports successfully into a fresh `bongo-go dev` instance

**Server validation script (Task 8c):**
- [ ] Script creates 3 users and sends 10+ emails via SMTP
- [ ] Verifies delivery via IMAP (message count, subjects, flags)
- [ ] Verifies threading (In-Reply-To/References chain)
- [ ] Verifies attachments retrievable via IMAP BODY sections
- [ ] POP3 retrieval works (STAT, LIST, RETR, UIDL)
- [ ] Spam email scored > 0.5 and routed to Junk
- [ ] Bayesian mark-spam / mark-not-spam moves messages correctly
- [ ] Outbound to invalid domain generates bounce DSN to sender
- [ ] Contact round-trip: create → export VCF → delete → re-import → verify
- [ ] Calendar round-trip: create → export ICS → delete → re-import → verify
- [ ] Full-text search returns expected results
- [ ] Web auth: login/logout/session cookie flow works
- [ ] Script exits 0 on success, 1 on failure with details

**Visual verification (Task 9):**
- [ ] Running app with validation data looks correct at all tested viewports

## V2 Prototype-First UI Rework (REPLACES Tasks 1-5c)

The original plan was to patch bongo.css. We've now taken a better approach:
**18 self-contained v2 prototype HTML files** were created with ALL CSS inline,
zero external dependencies, full dark mode, responsive design (3 breakpoints),
and WCAG 2.1 AA accessibility. These are the single source of truth for the UI.
Every page in the application is covered.

**V2 prototype files — User pages (all in `prototype/`):**
- `v2-inbox.html` — message list, toolbar, pagination, right-click context menu (establishes the design system)
- `v2-compose.html` — compose card with recipient chips, Cc/Bcc toggle, format toolbar, attachments
- `v2-thread.html` — conversation view with collapsible messages, avatars, quick reply
- `v2-contacts.html` — two-pane layout (list + detail card with recent emails)
- `v2-calendar.html` — month grid with color-coded event chips, mini calendar
- `v2-settings.html` — tab-based settings with toggle switches
- `v2-search.html` — search results with highlighted query terms, mailbox badges
- `v2-login.html` — login form + two-factor authentication (view toggle for both states)
- `v2-security.html` — password change, 2FA enable/disable, TOTP setup with QR code, recovery codes
- `v2-rules.html` — filter rules list (sortable, test, delete) + rule form (conditions builder, action selector)
- `v2-import.html` — mbox file upload with drag-drop zone, target mailbox selector, import results

**V2 prototype files — Admin pages (all in `prototype/`):**
- `v2-admin.html` — admin dashboard with stat cards, service status, user tables
- `v2-admin-users.html` — user list with avatars, quota bars, create user form
- `v2-admin-domains.html` — domain list with MX status, add domain form, DNS hint
- `v2-admin-aliases.html` — alias list with monospace addresses, add alias form with arrow
- `v2-admin-queue.html` — queue stat cards, pending/failed message tables with retry/drop
- `v2-admin-cluster.html` — raft state table, node cards with status dots, leader highlight
- `v2-admin-logs.html` — real-time log viewer with dark terminal theme, level/component filters

**Design system notes:**
- Mail pages use blue sidebar (`--sidebar-bg: #2c4a72`), brand shows "BongoMail"
- Admin pages use slate sidebar (`--sidebar-bg: #1e293b`), brand shows "BongoAdmin"
- Login page is standalone (no sidebar) with centred card layout
- All pages share the same CSS variable system, breakpoints, and accessibility patterns
- All admin pages include `--bongo-yellow: #f59e0b` (light) / `#fbbf24` (dark) for status indicators
- All mail pages include `--text-inverse`, `--shadow-lg`, and `--bongo-green` consistently

**Context menu (right-click) implementation notes:**
- `v2-inbox.html` includes a working right-click context menu with reply/forward/star/delete actions
- Position: fixed, z-index: 200, positioned at cursor via `event.clientX`/`event.clientY`
- **Production fix needed:** In `mailbox.templ`, Alpine.js `$dispatch('ctx-menu', {event: $event})`
  wraps the original MouseEvent in a CustomEvent, losing clientX/clientY. Fix by passing
  coordinates directly: `$dispatch('ctx-menu', {x: $event.clientX, y: $event.clientY, uid, mailbox})`
  and update `bongo.js` contextMenu's `open()` to use `detail.x`/`detail.y` instead of `event.clientX`

**What the coding agent should do (replaces Tasks 1-5c):**

1. **Extract CSS from v2 prototypes into a new `bongo.css`** — replace the existing
   2,724-line file entirely. The v2 CSS is organized by component and includes:
   - CSS variables (`:root` and `[data-theme="dark"]`)
   - Shared layout (topbar, sidebar, layout container, responsive breakpoints)
   - Page-specific styles (inbox, compose, thread, contacts, calendar, settings, admin)
   - Accessibility (skip link, focus-visible indicators)
   - No Pico CSS dependency — remove the Pico CSS import

2. **Update Go templates to match v2 HTML structure** — for each `.templ` file,
   align the HTML element structure, CSS classes, and ARIA attributes with the
   corresponding v2 prototype. Key structural changes:
   - Topbar: `<header class="topbar">` with `<a class="brand">` (clickable logo)
   - Layout: `<div class="layout">` with fixed positioning
   - Sidebar: `<aside class="sidebar" id="sidebar">` with overlay div for mobile
   - Content: `<main class="content" id="main-content">`
   - Admin: separate dark theme for topbar and sidebar

3. **Wire up interactive elements** — the prototypes use vanilla JS for:
   - `toggleSidebar()` — hamburger menu with aria-expanded
   - `toggleTheme()` — dark mode toggle
   - Message collapse/expand in thread view
   - Cc/Bcc toggle in compose
   - Contact selection in two-pane contacts view
   Replace these with the existing Alpine.js equivalents or htmx where appropriate.

4. **Remove Pico CSS** — delete the import and the `pico.min.css` file.
   All styling now comes from the v2 CSS extracted in step 1.

5. **Test at viewports:** 1440px, 1024px, 768px, 480px, 375px (iPhone SE)
   The v2 prototypes define 3 breakpoints: tablet (1024px), mobile (768px),
   small phone (480px).

**Done when:** Opening each page in the running app looks identical to the
corresponding v2 prototype HTML file at all viewport sizes.

---

## CRITICAL FIX: Admin template class name mismatch

**Root cause of broken admin pages:** `admin.templ` used class names that didn't
exist in `bongo.css`. The CSS was correctly ported from v2 prototypes, but the
template HTML needed to be updated to use the matching class names.

> **STATUS: FIXED (2026-02-24)** — The class renames in `admin.templ` and `rules.templ`
> have been applied. The mapping table below documents what was changed for reference.
> Rebuild and restart the app to see the fixes.

**Most visible symptom was:** Every admin page used `<div class="admin-content">` as
the content wrapper, but the CSS class is `.admin-scroll` (which provides `padding: 24px`
on all sides and `overflow-y: auto`). Since `.admin-content` had no CSS definition,
there's zero padding — content runs flush to all edges with no spacing on the right.

**Class name mapping — admin.templ → bongo.css (from v2 prototypes):**

### All admin pages
| Template uses | CSS has (correct) |
|---|---|
| `.admin-content` | `.admin-scroll` |
| `.admin-page-header` | (remove — use `.admin-title` + `.admin-subtitle` directly) |
| `.admin-btn` | `.btn-outline` |
| `.admin-btn--primary` | `.btn-primary` |
| `.admin-btn--danger` | `.btn-flush` |
| `.admin-section-title` | `.section-title` |
| `.admin-table-wrap` | `.admin-table-card` |
| `.admin-actions` | `.row-actions` |
| `.status-dot--green` (BEM) | `.status-dot.green` (compound class) |
| `.status-dot--red` | `.status-dot.red` |

### Dashboard (`AdminDashboard`)
| Template uses | CSS has (correct) |
|---|---|
| `.admin-cards` | `.stat-grid` |
| `.admin-card` | `.stat-card` |
| `.admin-card-label` | `.stat-label` |
| `.admin-card-value` | `.stat-value` |
| `.admin-card-detail` | `.stat-change` |
| `.admin-card-icon` + `--green/blue/orange/purple` | Not in CSS — ADD to bongo.css or restructure card HTML to match v2-admin.html prototype |
| `.hawkeye-status-table` + `.hawkeye-status-header` | Not in CSS — rename to `.admin-table-card` + `.admin-table-header` |
| `.admin-form-section` | `.form-card` |

### Users (`AdminUserList` / `AdminUserForm`)
| Template uses | CSS has (correct) |
|---|---|
| `.admin-avatar` | `.user-avatar` |
| `.badge-follower` | `.badge-user` |
| `.badge-enabled` | `.badge-active` |
| `.admin-toolbar-group` | (already part of `.admin-toolbar` — remove wrapper or style it) |

### Queue (`AdminQueueDashboard`)
| Template uses | CSS has (correct) |
|---|---|
| `.admin-stat-cards` | `.stat-grid` |
| `.admin-stat-card` | `.stat-card` |
| `.stat-number--blue/red/orange` (BEM) | `.stat-number.blue/.red/.orange` (compound class) |
| `.admin-bulk-bar` | `.bulk-bar` |

### Cluster (`AdminClusterOverview`)
| Template uses | CSS has (correct) |
|---|---|
| `.admin-node-grid` | `.node-grid` |
| `.admin-node-card` | `.node-card` |
| `.admin-node-card-header` | `.node-card-header` |
| `.admin-node-name` | `.node-name` |
| `.admin-node-detail` | `.node-detail` (wrap all in `.node-details`) |

### Logs (`AdminLogViewer`)
| Template uses | CSS has (correct) |
|---|---|
| `.admin-log-viewer` | `.log-viewer` |
| `.admin-log-toolbar` | `.log-toolbar` |
| `.admin-log-output` | `.log-output` |
| `.admin-log-line` | `.log-line` |

**Action for coding agent:**
1. Rename ALL classes in `admin.templ` (lines 114-700+) to match the CSS names above
2. For compound classes (`.status-dot.green`), use `templ.KV()` or space-separated class attrs
3. Add `.admin-card-icon` styles to bongo.css if keeping the icon+body card layout from admin.templ
4. Alternatively, restructure dashboard card HTML to match v2-admin.html prototype exactly
5. Test each admin page visually after renaming

---

## What NOT to do

- Don't patch the old bongo.css — replace it with CSS extracted from v2 prototypes
- Don't invent new styles — copy exactly from the v2 prototype files
- Don't change admin API endpoints or handlers (except adding plain_http support
  and any new endpoints needed for the validation script)
- Don't break existing tests
- Don't add unrelated features beyond what's specified in this sprint

---

## REVISION 3 FIXES (applied directly to source files)

These fixes were applied directly to the source `.templ`, `.css`, and `.js` files.
Run `templ generate` to regenerate the `_templ.go` files before building.

### Fix 1: `main-content` → `content` class (security.templ, rules.templ)

**Root cause:** security.templ and rules.templ used `<main class="main-content">`
but bongo.css only defines `.content` for the main content area. This caused the
security and filter rules pages to have no styling at all.

**Files changed:**
- `internal/web/templates/security.templ` — 3 occurrences changed
- `internal/web/templates/rules.templ` — 2 occurrences changed (replace_all)

### Fix 2: Skip-link visibility (bongo.css)

**Root cause:** `.skip-link` used `top: -100%` which may not resolve correctly when
the containing block has no explicit height. Changed to `left: -9999px` approach with
`width: 1px; height: 1px; overflow: hidden` which is the WCAG-recommended pattern.
On `:focus`, the link becomes `position: fixed` at `left: 16px; top: 8px`.

**Files changed:**
- `internal/web/static/css/bongo.css` — `.skip-link` and `.skip-link:focus` rules

### Fix 3: Settings tab switching (settings.templ)

**Root cause:** Settings tabs were plain `<button>` elements with no click handlers
or JavaScript. Clicking a tab did nothing.

**Fix:** Added `x-data="{ activeTab: 0 }"` on `.settings-container`, `x-on:click`
and `x-bind:class` on each tab button, and `x-show="activeTab === N"` on each
`.settings-section`. All sections are in a single `<form>` so all values are
submitted regardless of which tab is visible.

**Files changed:**
- `internal/web/templates/settings.templ`

### Fix 4: Context menu viewport clamping (bongo.js)

**Root cause:** `contextMenu.openAt()` set x/y directly from mouse coordinates
without checking if the menu would overflow the viewport edge.

**Fix:** Added viewport clamping logic: if menu would overflow right edge,
clamp to `window.innerWidth - menuW - 8`; same for bottom edge.

**Files changed:**
- `internal/web/static/js/bongo.js` — `openAt()` method in `contextMenu` Alpine data

### Fix 5: Sidebar active state (bongo.js)

**Root cause:** No JavaScript existed to highlight the current sidebar link. Users
could click folders/settings but the sidebar never showed which item was active.

**Fix:** Added `updateSidebarActive()` function that reads `window.location.pathname`
and toggles `.active` class on matching `.nav-list a`, `.nav-item a`, and
`.admin-nav-item a` elements. Runs on `DOMContentLoaded`, `htmx:pushedIntoHistory`,
and `htmx:afterSettle`.

**Files changed:**
- `internal/web/static/js/bongo.js` — new `updateSidebarActive()` function + event listeners

---

## REVISION 4 FIXES (applied directly to source files)

### Fix 6: Settings tabs — vanilla JS fallback (bongo.js)

**Root cause:** Alpine.js `x-data`/`x-show` approach for tab switching was not working
in production (possibly Alpine initialization timing issue or scope conflict).

**Fix:** Added `initSettingsTabs()` vanilla JS function that directly shows/hides
`.settings-section` elements and toggles `.active` class on `.settings-tab` buttons.
Runs on `DOMContentLoaded` and `htmx:afterSettle`.

**Files changed:**
- `internal/web/static/js/bongo.js`

### Fix 7: Security page form styling (security.templ)

**Root cause:** All `<label>` and `<input>` elements were missing `form-label` and
`form-input` CSS classes. Also used raw `<fieldset>`/`<legend>` without styling.
Content was not wrapped in the standard `settings-scroll > settings-container` layout.

**Fix:** Added `form-label` and `form-input` classes to all form elements. Replaced
`<fieldset>`/`<legend>` with `settings-card` containers using styled `<h3>` headers.
Wrapped content in `settings-scroll > settings-container`.

**Files changed:**
- `internal/web/templates/security.templ`

### Fix 8: Rules page layout (rules.templ)

**Root cause:** Rules list and form pages were missing the `settings-scroll >
settings-container` wrapper, causing content to render without proper padding/centering.
Form inputs also lacked `form-input` class.

**Fix:** Wrapped both `RulesListPage` and `RuleFormPage` content in
`settings-scroll > settings-container`. Added `form-input` class to text input.

**Files changed:**
- `internal/web/templates/rules.templ`

### Fix 9: Context menu always visible (bongo.css)

**Root cause:** `.context-menu` CSS had no `display: none` default. Before Alpine.js
initialized, the menu was visible. The `x-cloak` attribute should handle this, but
if Alpine loads late, there's a visible flash or permanent visibility.

**Fix:** Added `display: none` to `.context-menu` base CSS. Alpine's `x-show` will
override this with `display: block` when `visible` is true.

**Files changed:**
- `internal/web/static/css/bongo.css`

### Fix 10: Admin sidebar alignment (bongo.css)

**Root cause:** CSS used `.admin-nav-item a` selector (descendant), but admin.templ
puts the `admin-nav-item` class directly on the `<a>` element. The descendant selector
didn't match, so sidebar links had no flex alignment, padding, or styling.

**Fix:** Changed selector from `.admin-nav-item a` to `a.admin-nav-item, .admin-nav-item`
(covers both the class-on-anchor pattern and potential future parent-child patterns).

**Files changed:**
- `internal/web/static/css/bongo.css`

### Fix 11: Login page polish (bongo.css)

**Root cause:** Login card had minimal box-shadow and inputs used the generic
small `form-input` padding, making the page feel flat.

**Fix:** Enhanced `.login-card` with larger box-shadow (0 4px 24px rgba). Added
login-specific `.form-input` overrides with 12px padding, 1.5px border, 8px radius.
Added `.btn-login` glow shadow. Added subtle gradient background to `.login-page`.

**Files changed:**
- `internal/web/static/css/bongo.css`

---

## REVISION 5 FIXES (applied directly to source files)

### Fix 12: Calendar week/day view CSS (bongo.css)

**Root cause:** The v2 prototypes only covered month view. Week and day views use
classes like `.week-grid`, `.day-column`, `.hour-row`, `.event-block`, `.time-gutter`,
`.all-day-header`, etc. — none of which existed in bongo.css, so the calendar
week/day views rendered as unstyled raw HTML.

**Fix:** Added complete CSS rules for all calendar week/day view components:
`.calendar-toolbar`, `.calendar-month-title`, `.all-day-header`, `.time-gutter-header`,
`.all-day-cell`, `.week-grid`, `.time-gutter`, `.hour-label`, `.day-column`,
`.hour-row`, `.hour-row-link`, `.event-block`, `.event-block-time`,
`.event-block-title`, `.event-ghost`. Includes `.today` highlight and dark-mode
overrides.

**Files changed:**
- `internal/web/static/css/bongo.css`

### Fix 13: Contacts reading pane (contacts.templ)

**Root cause:** Clicking a contact navigated to a full-page detail view
(`/contacts/{guid}`). User requested a reading pane pattern where clicking a
contact loads the detail into a right-side pane without full page navigation.

**Fix:**
- Updated `ContactsPage` to include a two-pane layout: `.contact-list-pane` (left)
  and `.contact-detail-pane` (right) with placeholder text
- Updated `ContactRow` to use `hx-get="/contacts/{guid}/detail"` targeting
  `#contact-detail` with `hx-swap="innerHTML"` — loads detail inline via HTMX
- Added `onclick` handler to toggle `.selected` class on clicked row and add
  `.showing-detail` class to `.contacts-panes` container
- Created new `ContactDetailPartial` template that renders the detail card
  (avatar, name, editable fields, save/delete actions) without the layout wrapper

**Server-side route needed:** `GET /contacts/:guid/detail` → renders
`ContactDetailPartial(contact)` without layout. This handler needs to be added
to `internal/web/handlers/contacts.go`.

**Files changed:**
- `internal/web/templates/contacts.templ`

### Note: Admin logs (server-side issue)

The admin logs page appears not to connect to any log stream. This is a
**server-side issue** — the SSE endpoint `/admin/logs/stream` is either not
responding or not sending events. The template and JavaScript (EventSource) look
correct. Investigation needed in the Go handler that serves the SSE stream.
