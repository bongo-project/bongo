# Sprint 10b: Calendar Polish — Views, Recurrence, Reminders & Sharing

**Phase:** 1 (Foundation)
**Goal:** Complete the calendar experience with week/day views, recurring event support,
drag-and-drop rescheduling, mini calendar navigator, event reminders via SSE, calendar
colour coding, shared calendar display, and free/busy lookup.
Depends on Sprint 10a (Event/Calendar stores, CalDAV, month view, event CRUD).

---

## Context

Sprint 10a established the calendar foundation: Event/Calendar protos and stores, CalDAV
server for native client sync, month-view web UI with event CRUD, iCal import/export,
and event search via Bleve.

This sprint layers the advanced calendar features on top.

**What Sprint 10a delivered (prerequisites):**
- EventStore with Create/Get/Update/Delete/ListByDateRange
- CalendarStore with CRUD and default calendar auto-creation
- CalDAV backend (PROPFIND/GET/PUT/DELETE) with two-way sync
- Month grid view with event chips, HTMX prev/next navigation
- Event create/edit/delete forms
- iCal import/export
- Event search via Bleve
- `rrule` and `color` fields stored in protos (not yet used in UI)

---

## Tasks

### 1. Week and day views

**Add to `internal/web/handlers/calendar.go`:**
- `GET /calendar/week/{year}/{week}` → render week view
  - Load events for 7-day range via `ListByDateRange()`
  - Group by day + compute vertical positioning from start/end times
- `GET /calendar/day/{year}/{month}/{day}` → render day view
  - Load events for single day
  - Show hour-by-hour timeline with events as positioned blocks

**Add to `internal/web/templates/calendar.templ`:**
- `WeekView(year, week int, days []DayColumn, calendars []*Calendar)`:
  - 7-column grid with hour rows (00:00–23:00)
  - Events as positioned blocks (top = start time, height = duration)
  - All-day events in header row
- `DayView(date time.Time, hours []HourSlot, calendars []*Calendar)`:
  - Single column, hour-by-hour slots
  - Events as blocks spanning their duration
  - Click empty slot → create event at that time

**View switcher in toolbar:**
- Three buttons: Month / Week / Day
- HTMX navigation to appropriate URL
- Persist last-used view in user preferences (extend UserPreferences if needed)

**Modify `bongo.css`:**
- `.week-grid` — CSS grid: time column + 7 day columns
- `.day-grid` — single column with hour rows
- `.event-block` — position absolute within day column, coloured by calendar
- `.all-day-header` — row above time grid for all-day events
- `.hour-row` — border-bottom, min-height 3rem

**Done when:** Month/Week/Day views render correctly. Events positioned by time in
week/day views. View switcher navigates between views. All-day events display in header.

### 2. Recurring events (RRULE)

**Add RRULE parsing library to go.mod** (e.g., `github.com/teambition/rrule-go` or
implement basic parsing).

**Modify `internal/store/events.go`:**
- `ExpandRecurrence(event *Event, rangeStart, rangeEnd int64) ([]*Event, error)`:
  - Parse `event.Rrule` (FREQ=DAILY/WEEKLY/MONTHLY/YEARLY, INTERVAL, COUNT, UNTIL, BYDAY)
  - Generate occurrence instances within the date range
  - Each instance: same GUID + occurrence date suffix, computed start/end times
  - Respect `exdates` (exception dates — skip those occurrences)
- Modify `ListByDateRange()`:
  - For events with non-empty `rrule`, call `ExpandRecurrence()` to generate instances
  - Merge expanded instances with non-recurring events
  - Sort by start_time

**Modify event create/edit form:**
- Recurrence selector: None, Daily, Weekly, Monthly, Yearly
- Optional: repeat interval (every N days/weeks/months), end date or count
- Store as RRULE string in `event.Rrule`

**Modify CalDAV backend:**
- `QueryCalendarObjects()`: expand recurrence when `CompFilter` requests time-range
- Ensure RRULE in iCal round-trips correctly via `ical_raw`

**Done when:** Create weekly recurring event → appears on every matching day in month/week
views. Edit single occurrence → creates exception. CalDAV clients see expanded recurrences.

### 3. Drag-and-drop event rescheduling

**Add to `bongo.js`:**
- `eventDrag()` Alpine component for week/day views:
  - `mousedown` on event block → start drag, show ghost
  - `mousemove` → update ghost position, snap to hour/half-hour slots
  - `mouseup` → compute new start/end from drop position
  - POST `/calendar/event/{guid}/reschedule` with new start_time and end_time

**Add to `internal/web/handlers/calendar.go`:**
- `POST /calendar/event/{guid}/reschedule` — update start_time/end_time only,
  return HTMX partial refresh of the view

**Modify `bongo.css`:**
- `.event-block.dragging` — opacity 0.5, cursor grabbing
- `.event-ghost` — dashed border, positioned at cursor
- `.drop-slot.highlight` — background highlight on hover

**Done when:** In week/day view, drag event from 10am to 2pm → event rescheduled.
Visual feedback during drag. Snap to 30-minute increments.

### 4. Mini calendar navigator and calendar colour coding

**Add to `internal/web/templates/calendar.templ`:**
- `MiniCalendar(year, month, selectedDay int)`:
  - Compact month grid in sidebar (below calendar list)
  - Click day → navigate main view to that date
  - Highlight today, selected day
  - Prev/next month arrows (HTMX partial swap)
- `CalendarList(calendars []*Calendar)`:
  - Each calendar with colour indicator dot + name
  - Checkbox to show/hide calendar (Alpine.js toggle)
  - Click colour → colour picker popover to change

**Modify `internal/web/handlers/calendar.go`:**
- `PUT /calendar/{calendarID}` → update calendar properties (name, colour)
- Event chips and blocks use calendar colour: inline style `background-color: {color}`

**Modify sidebar:**
- Calendar section: mini calendar + calendar list replaces simple link

**Done when:** Sidebar shows mini calendar + colour-coded calendar list. Click day in mini
calendar → main view jumps to that date. Toggle calendar visibility filters events.
Change calendar colour → events update colour.

### 5. Event reminders via SSE

**Extend Notifier** (`internal/imap/notify.go` or create separate event notifier):
- Add event notification types: `NotifEventReminder`
- `EventReminder` struct: EventGUID, Title, StartTime, MinutesBefore

**Create reminder checker:**
- Background goroutine in agent: check every 60 seconds for upcoming events
- Scan events with start_time within next N minutes (5, 10, 15, 30)
- For each matching event: notify via Notifier → SSE → browser notification

**Modify `internal/web/handlers/sse.go`:**
- Listen for event reminder notifications
- Send SSE event: `event: reminder\ndata: {"title","start_time","minutes_before"}\n\n`

**Modify `bongo.js`:**
- Extend `notifications()` component: listen for `reminder` SSE events
- Show browser Notification: "Event in {minutes} minutes: {title}"

**Modify event create/edit form:**
- Reminder selector: None, 5 min, 10 min, 15 min, 30 min, 1 hour before
- Store as field on Event proto (add `int32 reminder_minutes = 14` if needed)

**Done when:** Set reminder on event → N minutes before, browser notification fires.
Notification shows event title and time. Works while any bongo tab is open.

### 6. Shared calendar display and free/busy

**Modify `internal/store/calendars.go`:**
- Add `shared_with` field to Calendar proto (repeated string of userIDs)
- `ListSharedCalendars(userID string) ([]*Calendar, error)` — scan all calendars,
  filter where `shared_with` contains userID

**Modify `internal/web/handlers/calendar.go`:**
- In month/week/day views: load own calendars + shared calendars
- Shared calendar events rendered as read-only (muted colour, no edit/delete)
- `POST /calendar/{calendarID}/share` → add userID to shared_with list
- Share UI: button on calendar list → input email/userID → share

**Add free/busy lookup:**
- `GET /calendar/freebusy?user={userID}&start={ts}&end={ts}` → returns JSON with
  busy time blocks (start/end pairs) for the specified user and range
- Used for scheduling: when creating event with attendees, show their availability
- Display in event create form as greyed-out blocks on a time picker

**Done when:** Share calendar with another user → they see events (read-only) in their
calendar view. Free/busy query returns correct busy times. Event creation shows
attendee availability.

### 7. Integration tests

**Create `test/integration/calendar_b_test.go` (build tag `integration`):**

**TestWeekDayViews:**
1. Create event at specific time
2. GET /calendar/week/{year}/{week} → verify event appears
3. GET /calendar/day/{year}/{month}/{day} → verify event appears
4. Verify view switcher links present

**TestRecurringEvents:**
1. Create weekly recurring event (every Monday)
2. GET /calendar/{year}/{month} → verify event appears on every Monday in month
3. Delete one occurrence → verify exception respected

**TestEventReschedule:**
1. Create event at 10:00
2. POST /calendar/event/{guid}/reschedule with new time 14:00
3. GET event → verify updated start_time

**TestSharedCalendar:**
1. User A creates calendar, shares with User B
2. User B: GET /calendar → verify shared calendar events visible (read-only)
3. GET /calendar/freebusy?user=A → verify busy blocks match events

**Done when:** `go test -race -tags=integration ./test/integration/calendar_b_test.go` passes.

---

## Exit Criteria

- [ ] Week view renders events positioned by time in 7-column grid
- [ ] Day view renders events in hour-by-hour timeline
- [ ] View switcher (Month/Week/Day) navigates between views
- [ ] All-day events display in header row of week/day views
- [ ] Recurring events expand correctly in date range queries
- [ ] RRULE supports DAILY, WEEKLY, MONTHLY, YEARLY frequencies
- [ ] Exception dates (exdates) skip specific occurrences
- [ ] Drag-and-drop reschedules events in week/day views
- [ ] Drag snaps to 30-minute increments
- [ ] Mini calendar in sidebar navigates main view
- [ ] Calendar colour coding applied to event chips/blocks
- [ ] Calendar visibility toggle filters events
- [ ] Event reminders fire as browser notifications via SSE
- [ ] Reminder timing configurable (5/10/15/30/60 min before)
- [ ] Shared calendars visible as read-only to shared users
- [ ] Free/busy lookup returns busy time blocks
- [ ] `go test -race ./...` passes
- [ ] `go test -race -tags=integration ./test/integration/calendar_b_test.go` passes

## What NOT to do in this sprint

- No Sieve/mail filter rules (Sprint 12)
- No admin panel (Sprint 11)
- No POP3 (Sprint 12)
- No push notifications / service workers
- No attendee invitations via email (RSVP flow — future)
- No CalDAV scheduling (VFREEBUSY protocol — web-only for now)
- No timezone conversion UI (use browser timezone)
