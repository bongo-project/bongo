# PRD: Web UI fixes v0.15.2

## 1. Introduction / Overview

v0.15.1 shipped on 2026-11-04. Building the bongo-project.org screenshots and
the operator's own use turned up a set of web UI defects and one CalDAV
interoperability problem. They were recorded in section 8.2 of
`prds/research/prd-sharing-contacts-v0.16.md`. The operator scoped them into
v0.15.2 on 2026-11-04 ("v0.15.2 now").

Each item was traced to its cause on 2026-11-04 by a read-only review of
`main` at `2d39903`. The causes are recorded per story.

**The scope rule.** The in-scope list is section 3. Anything a review finds
during this release goes into section 8.2, not into TASKS.md, unless the
change under review introduced it.

| Phase | Theme | Stories |
|-------|-------|---------|
| A | Calendar web UI | US-001 to US-003 |
| B | Admin, seed and tasks | US-004 to US-006 |

All stories are independent. US-001 and US-002 both edit `calendar.templ`
and `bongo.css`, so they run in one batch or one after the other.

### Size key

S: one focused session. M: two or three packages, a proto change or a new
form.

## 2. Goals

- Every calendar page uses the same form, card and button styles as the
  settings pages restyled in v0.13.
- The week and day views show which day each column is.
- The admin header shows the product name in both themes.
- Apple Reminders finds the Tasks list on every account and never creates a
  list named `DEFAULT_TASK_CALENDAR_NAME`.
- `make seed` produces tasks, so `/tasks` can be checked and photographed.

## 3. User Stories

### Phase A: Calendar web UI

### US-001: Calendar settings page is styled (M)

**Cause.** None of the classes on `/calendar/settings` has a CSS rule
(`calendar-settings-card`, `calendar-settings-form`, `calendar-new-*`,
`calendar-subscribe-*`, `calendar-subscription`, `share-*`, `publish-*`).
Labels wrap their inputs inline and four submit buttons have no class, so the
page renders with browser defaults. `.card` has no padding of its own. The
page title is a bare `<h3>` in the toolbar.

Acceptance criteria:
- [ ] The page uses `.page-head` with an `<h1>`, as other v0.13 pages do.
- [ ] Each card (new calendar, subscribe, one per calendar) has padding and
      a two-column label/control grid that collapses to one column at 390 px,
      reusing the `.settings-card-pad .form-group` rules or equivalent.
- [ ] Text, email, password, colour inputs and selects use the shared input
      style; every submit is `.btn-primary` or `.tool-btn.primary`; Delete,
      Unpublish and Remove are secondary or danger buttons.
- [ ] The share list shows each grantee on one row with state, level and
      Remove aligned. The publish section (detail, link, regenerate,
      password) uses the same field styles.
- [ ] The "Holds tasks" checkbox uses `.form-check`.
- [ ] Correct in light, dark and mobile (`make ui-shots`). Form actions,
      field names and CSRF are unchanged.
- [ ] A test fails when a class used on the page has no CSS rule.

### US-002: Calendar sidebar is styled (S)

**Cause.** The checkbox rules target dead classes (`.cal-list`,
`.cal-list-item`, `.cal-dot`), which no template uses. `.cal-color-dot` has
no size, so the colour dot is invisible. `.calendar-settings-link` has no
rule. The import control is the browser's file input. `.calendar-list-panel`
has a light 2 px border inside the dark sidebar.

Acceptance criteria:
- [ ] Each calendar row shows an accent-coloured checkbox, a visible colour
      dot and the name, aligned with the sidebar's nav items.
- [ ] "Calendar settings" looks like the other sidebar links.
- [ ] Import shows one button styled like Export; the chosen file name is
      shown, or the form submits on choosing a file.
- [ ] The dead `.cal-list*` rules are removed.
- [ ] The show/hide filter still works (Alpine `calendarFilter`, CSP build).

### US-003: Week and day views show the day (S)

**Cause.** `WeekViewGrid` never rendered a day header (since `e7298bb`). The
only row above the hour grid is the all-day strip, empty when there are no
all-day events. The day view has the same gap. `DayColumn.Date` and
`IsToday` are already filled.

Acceptance criteria:
- [ ] A header row above the all-day strip shows the localised weekday and
      day number for each column, with today highlighted and each cell
      linking to that day's view.
- [ ] The day view shows its date the same way.
- [ ] Columns line up with the hour grid (gutter included) at desktop and
      390 px.
- [ ] Weekday names are translated (en, de, es, fr).

### Phase B: Admin, seed and tasks

### US-004: Admin header wordmark and footer (S)

**Cause.** `.brand .wordmark { color: var(--text-primary) }` overrides the
admin and dark-topbar brand colour, so the wordmark is dark text on the dark
slate header in the light theme. `.admin-footer`, `.admin-sidebar-footer`
and `.admin-back-link` lost their rules after sprint 15b.

Acceptance criteria:
- [ ] The wordmark is readable on the admin header in light, dark and auto
      themes.
- [ ] The admin footer and the sidebar back link are styled with the
      current tokens.
- [ ] A test fails if the admin wordmark colour equals the header background.

### US-005: Every account has a Tasks list (S)

**Cause.** `EnsureDefault` creates the Tasks calendar only for a user with no
calendars. An account created before v0.15 has Personal only, so Apple
Reminders finds no VTODO collection, sends MKCALENDAR (supported since
v0.15.1) and names the list with its untranslated placeholder
`DEFAULT_TASK_CALENDAR_NAME`.

Acceptance criteria:
- [ ] A user with no VTODO-capable calendar (subscriptions excluded) gets
      the Tasks calendar (GUID `tasks`) the next time the web calendar or
      CalDAV `ListCalendars` runs. A user who already has a VTODO calendar,
      including one created by a client, gets none.
- [ ] Creating it is exempt from the 5-calendar cap, as `EnsureDefault` is.
- [ ] MKCALENDAR stores the display name `DEFAULT_TASK_CALENDAR_NAME` as
      "Tasks".
- [ ] Tests: a Personal-only user gets Tasks once (repeat calls create no
      second calendar); a user with a client-created VTODO list gets none;
      the placeholder name maps to "Tasks". Each mutation-checked.

### US-006: The dev seed creates tasks (S)

**Cause.** `seedContactsAndEvents` in `scripts/validate-server/main.go`
seeds contacts and events only.

Acceptance criteria:
- [ ] `make seed` creates at least five tasks per seeded user through
      `POST /tasks`: overdue, due today, due later, undated and one
      completed, with mixed priorities.
- [ ] `make dev` then shows them on `/tasks` and the agenda.

## 4. Functional Requirements

- FR-1: No calendar or admin page renders a form control with browser
  default styling.
- FR-2: Week and day columns are labelled with their date.
- FR-3: Every user has a VTODO collection without client action.

## 5. Non-Goals (Out of Scope)

- Reordering the mail sidebar (v0.16 candidate).
- A redesign of the calendar views beyond the header row.
- Renaming lists clients have already created.

## 6. Technical Considerations

- CSS lives in `internal/web/static/css/bongo.css`; no new files. Reuse the
  v0.13 settings patterns before adding rules.
- Alpine runs as the CSP build: no inline expressions with assignments.
- `templ generate` after any template edit; `make ui-shots` for screenshots.
- US-005 runs in the request path of every calendar page; it must reuse the
  `List` the caller already does.

## 7. Success Metrics

- `make ui-shots` shows no browser-default controls on calendar, calendar
  settings and admin pages, in light, dark and mobile.
- Apple Reminders on an older account lists "Tasks" and creates nothing.

## 8. Open Questions

### 8.1 Decisions

- Secondary submits on the settings page (share level Save, Send link,
  Regenerate, publish Save, Save password) use `.btn-secondary`, not primary
  as US-001 worded it. One primary action per form reads correctly.
- A failed Tasks back-fill for a user who already has a default calendar is
  logged and not returned: `EnsureDefault` runs on read paths, which must keep
  working on a node that cannot write.
- The mini-calendar month title was white on a white card in the light theme.
  Fixed under US-002, though not listed in it, as a calendar sidebar defect.
- `TestEventReschedule` assumed a UTC host and failed on `main` in any other
  zone. Fixed here: the gate could not pass on the operator's machine.
- Back-filling the Tasks calendar is exempt from the 5-calendar cap
  (operator rule: at most 5 calendars on every create path a user starts;
  this one is started by the server).

### 8.2 Candidates for the next release

Found while building v0.15.2 (2026-11-04):

- **"Enable notifications" is unreadable in dark mode:** the prompt's text is
  dark on a dark card, on every page.
- Unpublish (now a danger button) has no confirmation step.
- `TasksHandler.targetCalendar` is still a blind `Create`: a user with an
  events calendar at GUID `tasks` and no VTODO calendar would have it
  overwritten by saving a web task. `/tasks` does not call `EnsureDefault`.
- The sidebar link to `/calendar/settings` sits in `CalendarListPanel`, which
  renders only when the user has a calendar.
- The day-view toolbar title and `monthName()` are English only.
- `.settings-card-pad` is defined twice in `bongo.css`.
- `make ui-shots` seeds no shares, published calendars or subscriptions, so
  those states of the settings page are not screenshotted.
- An older account holding 4 event calendars and no Tasks loses a slot when
  Tasks is back-filled: its next create answers 507.

Found by verification (2026-11-04), not fixed in this release:

- From the month view the Week switcher linked to ISO week 44 when today was in
  week 45. Not investigated.
- `/calendar/search` renders `CalendarSidebar(nil)`: no calendar rows, filter
  or settings link on that page.
- The contacts import has the same no-JS gap the calendar import had: its
  submit is visually hidden.
- These pages do not reach `EnsureDefault`, so they do not back-fill Tasks:
  `/tasks`, calendar search, export, the mini calendar, free/busy,
  `/calendar/shares`, the new-event form. `docs/caldav-carddav.md` lists the
  pages that do.
- An events calendar at GUID `tasks` with no VTODO calendar blocks the
  back-fill, so that user has no Tasks list.
- A client that sends MKCALENDAR for a list without listing first, on an
  account with Personal only, ends up with Tasks and that list. If it sends the
  placeholder, both are named "Tasks".
- On a node that cannot write, a user without Tasks logs one warning per
  calendar read, with no rate limit.
- `make seed` is not idempotent and exits 0 when seeding fails.
- `make ui-shots` captures the viewport only, so the lower part of long pages
  is not in the screenshots, and it has no dark mobile variant.
- `message.templ` has a Load remote images button with no class.
- No test exercises the calendar show/hide filter in a browser, or column
  alignment in the week view; both were checked by hand.
- The event form keeps the older toolbar and heading, not `.page-head`, and its
  date and time picker icons are dark on dark in the dark theme.
