# PRD: Web UI rebuild v0.13.0

## 1. Introduction / Overview

The operator supplied a design handoff on 2026-10-24: a static mockup of the
whole web app ("Bongo Mail", reference HTML with its own CSS and icon set)
and a gap list, screen by screen. The live app should look like the mockup.
An earlier CSS-only reskin was not enough, because the mockup contains
markup the templates do not render, so this is a markup and CSS rebuild of
the templ templates.

The handoff files stay outside the repository (they are kept locally and
excluded from git). This PRD describes the work without copying them: where a
story says "the mockup", it means the matching screen of the reference HTML,
and "the reference stylesheet" means the handoff's skin CSS. Implementers get
the handoff path from the orchestrator.

**The scope rule.** The in-scope list is section 3. Review findings outside
a story go into section 8.2, not into TASKS.md, unless the story's own change
introduced them.

| Phase | Theme | Stories | TASKS rows |
|-------|-------|---------|------------|
| A | Foundation | US-001 to US-004 | 27.1 to 27.4 |
| B | Mail screens | US-005 to US-008 | 27.5 to 27.8 |
| C | Other screens | US-009 to US-015 | 27.9 to 27.15 |
| D | Fixes from operator testing | US-016 to US-024 | 27.16 to 27.24 |

One PR per story or small group of stories; Phase A merges first, because
every screen builds on the shell, tokens and icons.

## 2. Goals

- Each live route matches its mockup screen at 1440 px in light mode and is
  coherent in dark mode.
- Every behaviour survives: HTMX swaps, Alpine bindings, forms, CSRF, keyboard
  shortcuts, mobile navigation and swipe, translations.
- No CSP violation, no console error, no 404 for an asset.
- No emoji in the UI (the theme-toggle glyph excepted); icons are inline SVG.

## 3. User Stories

### Phase A: Foundation

### US-001: Design tokens, fonts and the icon set
**Description:** the handoff's ComponentLib token file is newer than the copy
in `internal/web/static/css/componentlib/colors_and_type.css`. The mockup's
icons are Lucide SVGs (1.8 stroke) and a drum logo glyph, defined in the
handoff's data script.

**Acceptance Criteria:**
- [ ] The token file is updated from the handoff, with font URLs rewritten to
      `/static/fonts/` and `@font-face` rules only for font files that ship
      (static WOFF2 files for every weight of Geist and Geist Mono 1.8.2,
      8.1 item 2). No 404 for any font.
- [ ] A templ icon component renders each icon the screens use by name, as
      inline SVG with `aria-hidden` and `currentColor`, plus the logo glyph.
      No icon is loaded from a CDN or an inline style.
- [ ] Accent, theme, sidebar and density variants are driven by `data-*`
      attributes on `<html>`, as the mockup does; the mockup's Tweaks panel
      itself (a design tool) is not shipped.
- [ ] `THIRD_PARTY_NOTICES.md` records Lucide (ISC) for the icon paths.

### US-002: The shell: top bar, sidebar and layouts
**Description:** `layout.templ` and the sidebar in `components.templ`.

**Acceptance Criteria:**
- [ ] Top bar: a three-column grid that lines up with the sidebar width; the
      brand is the drum glyph in the accent colour and the "Bongo·mail"
      wordmark; a search pill with an icon and a `⌘K` hint, with Ctrl-K /
      ⌘K added to `bongo.js` to focus it (no such shortcut exists today, and
      it must not fire while typing in a field); icon buttons for theme and
      notifications; an avatar chip (initials and address) opening an
      account menu with Settings, Source code and Logout (Alpine dropdown,
      keyboard accessible, closes on Escape and outside click).
- [ ] Sidebar (charcoal in both themes): an accent Compose button; groups
      Mail, Folders, Tags and Workspace with uppercase labels; SVG icons; an
      unread pill on Inbox and muted counts elsewhere; the active item has a
      3 px accent bar and a tinted background; a storage meter at the bottom
      from the user's quota where it is known, hidden otherwise.
- [ ] `data-accent="bongo" data-sidebar="dark"` on `<html>` in all three
      layouts.
- [ ] The source-code link (AGPL) stays reachable from every page.
- [ ] Mobile (at most 768 px): the existing mobile nav, overflow menu and
      swipe still work.

### US-003: Message list preview and attachment data
**Description:** the mockup's message row shows a preview line and a
paperclip for attachments. Neither is stored today: `MessageSummary` has no
preview, the per-user SQLite index has no snippet column, and there is no
attachment flag for messages that were not split.

**Acceptance Criteria:**
- [ ] The derived index gains a short plain-text preview (about 160
      characters, from the first text part, HTML stripped, whitespace
      collapsed) and a has-attachments flag, written when a message is
      indexed. The schema change follows the existing pattern (idempotent
      `CREATE ... IF NOT EXISTS`, plus an idempotent column addition for
      existing databases).
- [ ] Existing messages are backfilled by a background job that is
      resumable, rate-limited and safe to run on a live node, or the preview
      is filled lazily on first listing; the choice is justified. Rows
      without a preview render without line 3.
- [ ] `MessageSummary` carries `Preview` and `HasAttachments`; listing does
      not read message blobs.
- [ ] The preview never includes content from a message the user cannot
      read, and HTML is never rendered (text only, escaped by templ).

### US-004: Visual verification harness
**Description:** "visually indistinguishable" needs a repeatable check.

**Acceptance Criteria:**
- [ ] A script (under `scripts/`, run locally, not in CI) starts a dev
      server with seeded data (the synthetic demo data), captures each route
      at 1440 px light, 1440 px dark and 390 px light with a headless
      browser, and writes the screenshots to a directory outside the
      repository, alongside the matching mockup screens.
- [ ] It also records console errors, CSP violation reports and failed
      asset requests per route, and exits non-zero on any.
- [ ] Each later story's PR attaches (or links) its before and after
      screenshots in the PR description, not in the repository.

### Phase B: Mail screens

### US-005: Mailbox list
**Acceptance Criteria:**
- [ ] A page header replaces the flat toolbar: mailbox name, muted "· N
      unread · N total", a segmented layout toggle (SVG icons, the same
      `setLayout()` calls), and quiet Refresh and Scan for spam buttons. The
      bulk toolbar keeps its logic, restyled as an accent-subtle bar.
- [ ] Message row as a grid: checkbox, star, main. Main line 1 is the sender
      (bold if unread) with the date on the right; line 2 the subject, a
      thread-count pill and a paperclip if attachments; line 3 the preview
      (US-003); line 4 tag pills (colour dot and name). The selected row is
      accent-subtle with a 3 px accent bar.
- [ ] Compact density hides the preview line.
- [ ] The empty reading pane shows a centred icon, "Select a message" and a
      keyboard hint, all translated.
- [ ] `layout_test.go` selectors still exist; `#message-list`,
      `#reading-pane`, `thread-*` ids and every `hx-*`/`data-*` hook survive.

### US-006: Message and thread view
**Acceptance Criteria:**
- [ ] A header block with a large subject and a tag-chip row with a dashed
      "+ Add tag".
- [ ] A sender bar: tinted initials avatar, name, `<address>`, "to …" and
      date; icon buttons for Reply, Reply all and Forward, then Archive and
      Delete, and a More menu holding Export, Print and Move.
- [ ] Body max-width 760 px and line-height 1.7; attachments in a card of
      typed tiles (extension badge, name, size); a quick-reply card with Send
      (accent) and attach, and 1.75 rem of space below it.
- [ ] The sanitised HTML body rendering, remote-content blocking and the
      View Original link are unchanged.

### US-007: Compose
**Acceptance Criteria:**
- [ ] A centred card (max 760 px): title and close; To, Cc, Bcc and Subject
      as borderless rows with muted labels; recipient pills (avatar and name)
      for parsed addresses, editable as text; the rich editor or textarea;
      a footer with Send (accent, SVG icon), Save draft (outline), an attach
      button and a "Draft saved · just now" status.
- [ ] The hidden `send_id`, CSRF token, From selector, attachment and inline
      image handling, and every translated message are unchanged.

### US-008: Junk (quarantine)
**Acceptance Criteria:**
- [ ] A header with Rescan and Empty junk (danger); a warning banner; a
      table card with the spam score in mono red and Restore (outline) and
      Delete (danger) per row.
- [ ] Bulk actions and confirmation behaviour are unchanged.

### Phase C: Other screens

### US-009: Calendar
**Acceptance Criteria:**
- [ ] A page header with a Month / Week / Day / Agenda segmented control and
      an accent "New event"; Prev, Today and Next outline buttons and a large
      month title.
- [ ] The month grid as one card; today as an accent circle; event chips
      with a left colour bar and a tinted background, keeping per-calendar
      colours; all-day placement by UTC date as today.
- [ ] Week and day views take the same chrome.
- [ ] A new Agenda view (operator decision, 8.1 item 3): a chronological
      list of upcoming events from today, grouped by day with a sticky day
      heading, each row showing time (or "All day"), the calendar colour
      bar, title and location; recurring events expanded within the range;
      Prev/Next move by the visible range (default 30 days) and Today
      returns to now; reachable as `/calendar/agenda` with the same HTMX
      navigation as the other views; empty state translated. Times use the
      user's preference time zone, all-day events their UTC date, as the
      month grid does. A handler test covers ordering, grouping, recurrence
      expansion and the time-zone edge at midnight.

### US-010: Contacts
**Acceptance Criteria:**
- [ ] A header with Import vCard and Export (outline) and New contact
      (accent); a list pane with avatar, name and address rows (selected is
      accent-subtle); a detail card with a large avatar, name, company, Edit
      and Email buttons and a `dt`/`dd` field grid.
- [ ] `#contact-detail` swaps and the import and export flows are unchanged.

### US-011: Rules
**Acceptance Criteria:**
- [ ] An intro paragraph; a card listing rules as rows with a toggle, "When"
      and a mono condition chip, an arrow, the accent action text and a more
      menu.
- [ ] Rule ordering (Sortable), enable/disable and editing are unchanged.

### US-012: Settings, security and tags
**Acceptance Criteria:**
- [ ] Tabs with an accent underline; each section a card with a two-column
      label/control grid; ComponentLib toggles; a primary Save button.
- [ ] Every settings form, 2FA flow, recovery code display, app-specific
      pages and tag management keep their behaviour and CSRF protection.

### US-013: Admin
**Acceptance Criteria:**
- [ ] A KPI row of four stat cards (uppercase label, big number, delta in the
      success or error colour) from data the admin pages already have; a
      KPI without a data source is omitted rather than invented.
- [ ] Table cards with uppercase muted headers, row hover and status pills
      (dot and label, semantic tints). The slate admin sidebar stays, with
      the new nav item styling. The log viewer stays dark.
- [ ] Domain-admin scoping and every admin action are unchanged.

### US-014: Login and the other public pages
**Acceptance Criteria:**
- [ ] A centred card (radius-xl, shadow-lg) with the drum logo and wordmark,
      "Welcome back", a subtitle, labelled inputs with an accent focus ring,
      a Remember me row, and a full-width accent Sign in. There is no
      self-service password reset (only an admin reset), so the mockup's
      Forgot link is omitted rather than pointing nowhere.
- [ ] The 2FA prompt, the address-verification and error pages use the same
      card. The login page keeps its source-code link.

### US-015: Remaining templates and emoji removal
**Acceptance Criteria:**
- [ ] Search results, import, quota, address verification and source views
      adopt the shell and card styles.
- [ ] A test fails if any `.templ` file contains an emoji character outside
      the allowed theme-toggle glyph.

### US-016: Rule editor works under the CSP build
Added 2026-10-28 from operator testing. The new and edit rule pages
(`/settings/rules/new`, `/settings/rules/{id}/edit`) throw "CSP Parser
Error" on load: the conditions `x-data` and `actionTypeData` define methods
(and a regex) inline, which the Alpine CSP build cannot parse, so the
condition rows and the action controls do not initialise.

**Acceptance Criteria:**
- [ ] The conditions and action state move into `Alpine.data` components in
      `bongo.js`; the templates call them with plain arguments (no inline
      methods, arrow functions, regex, template literals or statement
      lists in any x-* attribute of `rules.templ`).
- [ ] On both pages a user can add and remove conditions, change field and
      operator (the size and has-attachment operator rules still apply),
      pick every action type, and add and remove tags for the tag action;
      the form posts the same field names as today.
- [ ] Loading the new and edit pages logs no Alpine expression errors in
      the browser.
- [ ] A template test fails if an x-* attribute in the rule editor holds an
      expression the CSP build cannot parse.

### US-017: Calendar show/hide checkbox works under the CSP build
Added 2026-10-28 from operator testing. Each calendar's checkbox in the
calendar sidebar (`calendar.templ`, `hiddenCals`) hides that calendar's
events with an `x-on:change` that uses an arrow function, `document.` and a
statement list, none of which the Alpine CSP build parses, so unticking a
calendar does nothing.

**Acceptance Criteria:**
- [ ] The toggle moves into a method on an `Alpine.data` component in
      `bongo.js`; the template passes the calendar ID as a plain argument or
      data attribute.
- [ ] Unticking a calendar hides its events in the month, week, day and
      agenda views, and ticking it shows them again.
- [ ] Loading the calendar and toggling a calendar logs no Alpine
      expression errors in the browser.
- [ ] A template test fails if an x-* attribute in the calendar templates
      holds an expression the CSP build cannot parse.

### US-018: Live unread counts
Added 2026-10-28 at the operator's request. The per-folder unread counts in
the sidebar are rendered with the page and never change, so marking a
message read leaves the number as it was until a reload. Only the Unread
view badge updates live, and only for new INBOX mail (`sse.go`).

**Acceptance Criteria:**
- [ ] Marking messages read or unread (toolbar, bulk bar, context menu,
      keyboard), opening an unread message, and deleting, moving or
      archiving unread messages update the affected folders' counts and the
      Unread badge without a reload.
- [ ] New mail updates the receiving folder's count through the existing
      SSE stream, not only the INBOX badge.
- [ ] A folder whose count reaches zero shows no count, as on page load;
      INBOX keeps its current rule for subfolder mail (the 8.2 note on
      sidebar versus header counts stays open).
- [ ] The count's accessible label ("%d unread messages") is translated in
      en, de, es and fr.
- [ ] The counts come from one endpoint the page re-reads after a mail
      action; a handler test asserts it reflects marking read and unread,
      deleting, and subfolder mail.

### US-019: Contact editor works under the CSP build
Added 2026-10-29 at the operator's request (found in 27.16). The contact
editor bound its repeated email, phone and address fields with
`x-model="em.value"` and similar; the CSP build forbids property writes, so
the model never updated and adding or removing a row could lose typed
values.

**Acceptance Criteria:**
- [ ] The rows move into a `contactEditor` Alpine.data component seeded
      from JSON data attributes; fields write through a setter; add and
      remove are method calls.
- [ ] Typing into rows, adding and removing rows, and changing a row's
      type keep every other row's values; the contact saves and reloads
      with them.
- [ ] `assertCSPSafe` passes for the editor.

### US-020: Create a filter from a message
Added 2026-10-29 at the operator's request.

**Acceptance Criteria:**
- [ ] The message view's More menu and the message-row context menu offer
      "Create filter", which opens the new-rule page with one condition,
      From contains the sender's address, and a suggested rule name; the
      action is left for the user to choose.
- [ ] The prefill arrives as query parameters, is validated and length
      capped server-side, and is rendered only through the editor's JSON
      data attributes (no splicing into an expression).
- [ ] The new label is translated in en, de, es and fr.
- [ ] Handler and template tests cover the prefill, including hostile
      input.

### US-021: Fix the TestSweepGhostUserTrees SQLITE_BUSY flake
Added 2026-10-29 at the operator's request. The test failed once in CI with
`SQLITE_BUSY` from `auth.CreateUser` writing the address record.

**Acceptance Criteria:**
- [ ] The cause is identified (which connection holds the lock and why a
      writer gives up instead of waiting) and fixed at that cause, not by
      retrying the test.
- [ ] A test reproduces the contention deterministically or the fix is
      shown under a stress run (-race, -count, parallel load).

### US-022: Typed tag names are accepted, and a rejected tag is reported
Added 2026-10-29 from operator testing. Typing "Bob" in a message's tag
picker and clicking "Create tag" closed the dropdown and added nothing: the
server accepts only lowercase letters, digits and hyphens, answered 400, and
the picker discarded the error.

**Acceptance Criteria:**
- [ ] Every endpoint that takes a typed tag name (message tags, bulk tags,
      tag create and rename, rule tag actions) folds it into stored form
      ("Bob" to "bob", "My Tag" to "my-tag") with the importer's existing
      rules, shared rather than copied.
- [ ] A name that folds to nothing is still rejected with 400.
- [ ] The picker previews the folded name, and when the server rejects a tag
      it stays open with a translated error instead of closing.

### US-023: Tag colour swatches are announced by name
Added 2026-10-29 at the operator's request. Screen readers announced the
Settings > Tags colour swatches as "Colour 0" to "Colour 15".

**Acceptance Criteria:**
- [ ] Each swatch's accessible name uses a translated colour name ("Colour
      blue"), in all four locales, for both the new-tag and edit pickers.

### US-024: Reading pane layouts work
Added 2026-10-30 from operator testing. The two-pane and three-pane buttons
produced the same side-by-side layout, and clicking a message in either mode
replaced the whole view instead of opening it in the reading pane.

**Acceptance Criteria:**
- [ ] Two-pane shows the list above the reading pane; three-pane shows them
      side by side. The two-pane icon shows the top and bottom split.
- [ ] In either pane mode, clicking a message opens it in the reading pane and
      marks its row. List mode, modified clicks and narrow screens still open
      the full view.
- [ ] In the pane modes the list and the reading pane scroll separately, and
      infinite scroll still loads the next page.

## 4. Functional Requirements

FR-1 to FR-24 are the acceptance criteria of US-001 to US-024. In every
story:

- Keep every `hx-*` attribute, `x-*` binding, form `action`/`method`, CSRF
  token, `id` used as an HTMX target, `data-*` hook read by `bongo.js`, and
  `t(ctx, …)` call. Restructure around them.
- No inline `<script>`, inline event handler or new inline `style`; geometry
  goes in `generated_geometry.css` or `bongo.css`.
- Use ComponentLib variables (`--surface-*`, `--text-*`, `--border-*`,
  `--accent*`, `--shadow-*`, `--radius-*`), not hex; never override
  `--success`, `--warning` or `--error`.
- New user-facing text goes through `t(ctx, …)` with en, de, es and fr.
- Template tests may change only where they assert presentation, never
  where they assert security (CSRF, escaping, authorisation).

## 5. Non-Goals (Out of Scope)

- New features the mockup only implies (snooze, scheduling, notifications
  beyond what exists), except the preview and attachment data in US-003.
- The mockup's Tweaks panel.
- The website and help documentation (separate scope).
- Any change to protocols, storage semantics or authentication.

## 6. Technical Considerations

- The reference stylesheet is the visual spec. Its rules may be reused with
  selectors renamed to the classes the templates keep; it is not copied
  wholesale (it targets the mockup's markup).
- US-003 adds derived data only (the SQLite index is rebuildable); Pebble
  and Raft are untouched.
- Screens share the shell, so Phase A merges before any Phase B or C story
  starts; B and C stories can then run in parallel worktrees, one screen
  each.
- The process per screen: open the mockup screen and the live route side by
  side, change templates and CSS, `templ generate`, compare, run
  `go test ./internal/web/...`, check light, dark and 390 px.

## 7. Success Metrics

- Every route passes the US-004 harness (no console, CSP or asset errors) and
  its screenshots are signed off by the operator against the mockup.
- `make test-all` and CI stay green; no `hx-*`, CSRF or i18n regression.

## 8. Open Questions

### 8.1 Decisions

1. (operator, 2026-10-24) The handoff stays out of the repository and its
   history.
2. (operator, 2026-10-26) Fonts stay Geist, hosted as static files: the
   official Geist and Geist Mono 1.8.2 release, nine WOFF2 weights each
   (100 to 900), replace the older TTF files and the variable font (US-001).
3. (operator, 2026-10-26) The calendar gets an Agenda view (US-009).

### 8.2 Candidates for the next release

Findings from reviews during this release go here.

From the Phase A batch 1 reviews (2026-10-26):

- The Settings > Tags page throws 16 console errors on load in production
  today: Alpine's CSP build cannot parse a template literal there, so parts
  of the page may not work. Fix it in US-012, not only restyle it. (27.4)
- The login page stays on `data-theme="auto"` under a dark colour-scheme
  preference, so it never renders dark. Fix in US-014. (27.4)
- On mobile the message view's tag popover renders open over the sender
  line. Fix in US-006. (27.4)
- `--status-red` (and six other fallback-only properties) stay undefined on
  purpose until their screen stories restyle them; `.log-line-error` and
  `.u-text-status-red` therefore inherit colour today. The token test lists
  them with the owning story. (27.1)
- The `docs/dev-quickstart.md` section for `make ui-shots` waits for the
  documentation work after the UI rebuild; usage is in the package doc.
  (27.4)

From the 27.2 shell review (2026-10-26):

- `scripts/ui-test` only runs against an https server, so `make ui-test`
  fails against `make dev`, and the new shell checks in it (Ctrl-K, "/",
  account menu, search hint) are not run anywhere automatically. Make the
  harness work against the dev server, or move these checks into ui-shots.
  (27.2)
- The search pill is hidden at 390 px (as before), so Ctrl-K and "/" have
  nothing to focus on mobile. Decide in US-015 whether mobile gets a search
  entry point. (27.2)
- The collapsible Views and Quick links sidebar groups are gone (their items
  moved into Mail and Workspace), so stored collapse state for those two
  groups is ignored. (27.2)
- The validation seed returns 409 for alice, bob and charlie when the dev
  server's storage already holds them; reseeding should be idempotent.
  (27.2)
- A per-user storage meter needs usage data the sidebar does not have; the
  mockup's meter waits for a cheap per-user usage figure. (27.2)

From the 27.8 junk review (2026-10-27):

- Phase C screens (rules, settings, admin) should reuse the shared
  `.page-head`, `.data-table` and `.banner` rules rather than add their own;
  the section sits before the responsive blocks so the mobile rules win.
  (27.8)
- The dev seed has no quarantined messages, so ui-shots only captures the
  empty Junk page; the populated state was checked by rendering the template
  with fixture data. Seed a few quarantined messages for ui-shots. (27.8)
- `confirmEmpty` and other small Alpine methods have no JS test runner; they
  were checked with a throwaway Node script. A minimal headless JS check in
  scripts/ui-shots would guard them. (27.8)

From the 27.5 mailbox review (2026-10-27):

- Search results (`search.templ`) use their own row markup, not the
  mailbox rows, so they keep the old look. Restyle them with the same row
  grid in US-015 or a follow-up. (27.5)
- ui-shots cannot select a row or move keyboard focus, so the bulk bar and
  the keyboard-focus outline were checked with a temporary harness patch.
  Add an interaction step to ui-shots. (27.5)
- The sidebar INBOX count includes unread mail in INBOX subfolders, while
  the list header counts INBOX itself, so the two can differ. Decide which
  the sidebar should show. (27.5)

From the 27.6 message view review (2026-10-27):

- "Load remote images" applies to the whole message pane; the CSP `img-src`
  cannot be narrowed to one message, so a thread with several messages
  loads images for all of them once allowed. Decide whether the setting
  should be per message. (27.6)
- `scripts/ui-shots/message_more_e2e_test.go` (More menu, quick reply) runs
  only under the `uishots` build tag, which no CI job sets. (27.6)

From the 27.7 compose review (2026-10-27):

- The drag-and-drop test drives the drop zone with synthetic drag events;
  a real file drag from the desktop has not been exercised by a test.
  (27.7)
- `scripts/ui-shots/recipient_pills_e2e_test.go` runs only under the
  `uishots` build tag, which no CI job sets. (27.7)

From the 27.11 rules review (2026-10-28):

- At 390 px the arrow between the conditions and the action stays at the
  end of the chip line when the action wraps to its own line. (27.11)

From the 27.10 contacts review (2026-10-28):

- The detail card lists fields in stored order, so a second email can
  appear after Notes; group fields by kind. (27.10)
- At 390 px the Import vCard button is hidden (an older `.import-form`
  rule), so mobile users can export but not import. (27.10)
- The calendar and contacts sidebars still use emoji icons and unstyled
  native file inputs for import/export; US-015 removes the emoji. (27.9,
  27.10)

From the 27.12 settings review (2026-10-28):

- ui-shots captures only the first settings tab and an empty tag list, so
  the switch toggles (Display, Forwarding) and a populated tag list are
  checked by hand, not by the screenshot run. Add tab and populated-state
  steps to ui-shots. (27.12)
- The 2FA-enabled variants (disable form, recovery codes) are covered by
  template tests only, not rendered in a browser. (27.12)

From the 27.14 login review (2026-10-28):

- ui-shots cannot reach `/login/2fa`, so the 2FA card is checked by its
  shared CSS and template tests only; at 390 px a 9-character recovery code
  can scroll inside the code input (as before). (27.14)
- `address_verify.templ` was not restyled in US-014; US-015, which also
  lists address verification, gives it the auth card. (27.14)

From the 27.13 admin review (2026-10-28):

- The dashboard has no message-rate, spam-rate or delivered-24h figures to
  show; `SystemHealth.MessageRate` is declared but never set. Collecting
  them is a feature for a later release (v0.17 operations work). (27.13)

From the 27.9 calendar review (2026-10-28):

- Agenda day headings use Go's English date layout, not a localised one;
  the other calendar views share the same limitation. (27.9)
- The calendar sidebar's import control is still a native file input
  (export is a styled button); 27.15 covers the sidebar emoji. (27.9)

From the 27.15 remaining-pages review (2026-10-28):

- The mobile search entry point (27.2 note) was not added: at 390 px there is
  still no search field, so Ctrl-K and "/" have nothing to focus. Deferred to
  the next release. (27.15)
- The calendar sidebar's mini-calendar, calendar list and event search are
  still the old controls (boxed day cells, native checkbox); only their emoji
  were replaced. (27.15)
- The quota-refused page and the admin DNS copy button (now a text button)
  are covered by template tests only; the dev seed has no quota-limited user
  and no domain to capture. (27.15)
- In compact density the search snippet is hidden, as in the mailbox list.
  (27.15)
- `address_verify.templ` is the signed-in send-as addresses page, not a
  public page, so it took the card and data-table styles rather than the
  auth card the 27.14 note expected. (27.15)

From operator testing of the tag controls (2026-10-28):

- Create a filter from a message. Moved into scope as US-020 (27.20).
- The rule editor throws "CSP Parser Error" on load. Moved into scope as
  US-016 (27.16).
- The calendar sidebar's show/hide checkbox fails under the CSP build.
  Moved into scope as US-017 (27.17).
- Mailbox names are spliced into Alpine expressions as `'<name>'`, so a
  name containing a quote breaks the bulk-action handlers. Pass the name
  through a data attribute instead.
- Static assets are cache-busted by commit hash only, so during development
  a rebuilt but uncommitted bongo.js is served under the old URL and the
  browser keeps the previous copy until a hard reload.

From the 27.16 rule editor work (2026-10-28):

- The contact editor's nested `x-model` bindings never update under the
  CSP build. Moved into scope as US-019 (27.19).

From CI on PR #108 (2026-10-28):

- `TestSweepGhostUserTrees` (internal/agent) failed once in CI with
  `SQLITE_BUSY` from `auth.CreateUser` writing the address record while the
  running agent's background workers held the per-user SQLite database. It
  passed on re-run and 20 of 20 times locally under -race. Moved into
  scope as US-021 (27.21).
