# Sprint 10a: Calendar Foundation — CalDAV, Event CRUD & Month View

**Phase:** 1 (Foundation)
**Goal:** Add calendar support with event storage, CalDAV sync for native clients
(Apple Calendar, Thunderbird, DAVx5), basic month-view web UI with event CRUD, and
iCalendar import/export. Mirrors what Sprint 09 did for contacts.

---

## Context

Sprint 09 added contacts with CardDAV sync. The CardDAV backend in `internal/carddav/`
implements `carddav.Backend` from `emersion/go-webdav` — the CalDAV backend will follow
the exact same pattern using `caldav.Backend` from the same library (already in go.mod).

**Existing infrastructure to build on:**
- `emersion/go-webdav` v0.7.0 already in go.mod (includes caldav package)
- CardDAV backend pattern: Backend struct wrapping stores, context-based auth, path routing
- ContactStore/ContactsHandler patterns for CRUD + search + agent wiring
- `CalDAVConfig` already defined in `internal/config/config.go` with `Enabled` field
- `internal/caldav/` directory exists (currently only has CardDAV server)
- SSE handler with event types (newmail, expunge, flagchange) + 30s heartbeat
- Notifier fan-out pattern for real-time updates

**Deferred to Sprint 10b:**
- Week and day views
- Recurring events (RRULE parsing/expansion)
- Drag-and-drop event rescheduling
- Mini calendar sidebar navigator
- Event reminders/alarms via SSE
- Multiple calendar colour coding
- Shared calendar display
- Free/busy lookup

---

## Tasks

### 1. Event and Calendar protos, stores, and agent wiring

Foundation layer — data model, storage, lifecycle.

**Create `proto/bongo/v1/event.proto`:**
- `message Event`:
  - `string guid = 1`
  - `string calendar_id = 2`
  - `string title = 3`
  - `string description = 4`
  - `string location = 5`
  - `int64 start_time = 6` — Unix timestamp
  - `int64 end_time = 7` — Unix timestamp
  - `bool all_day = 8`
  - `string rrule = 9` — RRULE string (used in 10b, stored now for CalDAV round-trip)
  - `string timezone = 10` — IANA timezone name
  - `bytes ical_raw = 11` — preserves non-standard iCal properties for CalDAV round-trip
  - `int64 created_at = 12`
  - `int64 updated_at = 13`

**Create `proto/bongo/v1/calendar.proto`:**
- `message Calendar`:
  - `string guid = 1`
  - `string name = 2`
  - `string description = 3`
  - `string color = 4` — hex colour for UI (used in 10b, stored now)
  - `string timezone = 5` — default timezone
  - `int64 created_at = 6`
  - `int64 updated_at = 7`
- Run `proto/generate.go` to produce `.pb.go` files

**Create `internal/store/events.go`:**
- `EventStore` wrapping `*PebbleStore` + `RaftApplier` + `*SearchManager`
- Key: `/user/{userID}/event/{guid}` → Event proto
- Methods: `Create`, `Get`, `Update`, `Delete`
- `ListByDateRange(userID string, start, end int64, calendarID string) ([]*Event, error)` —
  PrefixScan on `/user/{userID}/event/`, filter by start_time/end_time overlap with range,
  optionally filter by calendar_id
- `ListAll(userID string, offset, limit int) ([]*Event, int, error)` — paginated list

**Create `internal/store/calendars.go`:**
- `CalendarStore` wrapping `*PebbleStore` + `RaftApplier`
- Key: `/user/{userID}/calendar/{guid}` → Calendar proto
- Methods: `Create`, `Get`, `Update`, `Delete`, `List(userID) ([]*Calendar, error)`
- On first access, ensure default calendar exists ("Personal", colour="#4A90D9")

**Create `internal/store/events_test.go`:**
- CRUD round-trip, ListByDateRange filters correctly, delete removes key

**Create `internal/store/calendars_test.go`:**
- CRUD round-trip, default calendar creation, list returns all

**Wire into `internal/agent/agent.go`:**
- Add `Events *store.EventStore` and `Calendars *store.CalendarStore` to Agent struct
- Initialize after Pebble/Raft init (same location as Contacts)

**Done when:** EventStore and CalendarStore CRUD work, unit tests pass, agent creates
both stores on startup. Default calendar auto-created.

### 2. CalDAV server (RFC 4791)

Implement CalDAV protocol for sync with native calendar clients.

**Create `internal/caldav/caldav_server.go`:**
- Implement `caldav.Backend` interface from `emersion/go-webdav/caldav`:
  - `CurrentUserPrincipal(ctx)` — return `/caldav/{userID}/`
  - `CalendarHomeSetPath(ctx)` — return `/caldav/{userID}/calendars/`
  - `ListCalendars(ctx)` — list user's calendars
  - `GetCalendar(ctx, path)` — return calendar metadata
  - `GetCalendarObject(ctx, path, req)` — load event, return iCal data
  - `ListCalendarObjects(ctx, path, req)` — list events in calendar
  - `QueryCalendarObjects(ctx, path, query)` — filter events by time range
  - `PutCalendarObject(ctx, path, calendar, opts)` — create/update event from iCal
  - `DeleteCalendarObject(ctx, path)` — delete event
- Backend wraps `*store.EventStore` + `*store.CalendarStore`
- Auth: extract userID from context (same pattern as CardDAV)
- iCal ↔ Event proto conversion: parse VEVENT from iCal, map to proto fields,
  store raw iCal in `ical_raw` for round-trip fidelity

**Create `internal/caldav/caldav_server_test.go`:**
- Test: ListCalendarObjects returns correct count
- Test: PutCalendarObject creates event, GetCalendarObject retrieves it
- Test: DeleteCalendarObject removes event

**Wire into `internal/agent/agent.go`:**
- Create CalDAV handler in Agent.Run() (alongside existing CardDAV)
- Mount at `/caldav/` via `r.Handle("/caldav/*", caldavHandler)`
- Add `/.well-known/caldav` redirect to `/caldav/`

**Done when:** CalDAV client can discover calendars, list events, fetch individual iCal
objects, create new events, and delete events. Events created via web UI visible via
CalDAV and vice versa.

### 3. Calendar month view web UI

Basic calendar interface with month grid view.

**Create `internal/web/handlers/calendar.go`:**
- `CalendarHandler` struct with `*store.EventStore`, `*store.CalendarStore`, `*store.SearchManager`
- `GET /calendar` → redirect to current month: `/calendar/{year}/{month}`
- `GET /calendar/{year}/{month}` → render month view with events
  - Load events via `ListByDateRange(userID, monthStart, monthEnd, "")`
  - Group events by day for grid placement
  - Pass year, month, events, calendars to template
- HTMX navigation: prev/next month buttons swap month grid partial

**Create `internal/web/templates/calendar.templ`:**
- `CalendarPage(year, month int, days []DayCell, calendars []*Calendar, email, flash string)`:
  - `@LayoutWithContent("Calendar", email)` wrapper
  - Month header with prev/next arrows (HTMX: `hx-get="/calendar/{year}/{month}"`)
  - 7-column grid (Mon–Sun headers)
  - Day cells with date number, event chips (title + time, coloured by calendar)
  - Click day → create event for that day
  - Click event chip → show event detail
- `CalendarSidebar(calendars []*Calendar)` — calendar list with colour indicators
- `DayCell` struct: Date, Events, IsCurrentMonth, IsToday

**Modify `internal/web/templates/components.templ`:**
- Add "Calendar" link in sidebar navigation (between Contacts and Settings)

**Modify `bongo.css`:**
- `.calendar-grid` — CSS grid 7 columns, border styling
- `.day-cell` — min-height, padding, overflow-y for many events
- `.event-chip` — small pill with colour, truncated title, cursor pointer
- `.day-cell.today` — highlighted border/background
- `.day-cell.other-month` — muted text colour

**Register routes** in Agent.Run() protected group.

**Done when:** Navigate to /calendar → see current month grid. Events appear as coloured
chips on their days. Prev/next month navigation via HTMX without full page reload.
Calendar link in sidebar.

### 4. Event CRUD web UI

Create, view, edit, and delete events.

**Add to `internal/web/handlers/calendar.go`:**
- `GET /calendar/event/new?date={date}` → render create form pre-filled with date
- `POST /calendar/event` → create event from form, redirect to month view with flash
- `GET /calendar/event/{guid}` → render event detail/edit view
- `PUT /calendar/event/{guid}` → update event, HTMX partial swap
- `DELETE /calendar/event/{guid}` → delete event, redirect to month

**Add to `internal/web/templates/calendar.templ`:**
- `EventForm(event *Event, calendars []*Calendar, isNew bool)`:
  - Fields: title, description, location, start date+time, end date+time, all-day toggle,
    calendar selector (dropdown of user's calendars), timezone selector
  - All-day toggle: when checked, hide time inputs (Alpine.js `x-show`)
  - Save and Cancel buttons. Delete button on edit (not create).
- `EventDetail(event *Event)`:
  - Display all fields, "Edit" button, "Delete" button with confirmation

**Register routes:**
```
r.Get("/calendar/event/new", calendarH.NewEvent)
r.Post("/calendar/event", calendarH.CreateEvent)
r.Get("/calendar/event/{guid}", calendarH.ShowEvent)
r.Put("/calendar/event/{guid}", calendarH.UpdateEvent)
r.Delete("/calendar/event/{guid}", calendarH.DeleteEvent)
```

**Done when:** Click day cell → create form with date pre-filled. Fill form → event
appears on calendar grid. Click event → detail view. Edit/delete work.

### 5. iCalendar import/export

Support .ics file upload/download.

**Add to `internal/web/handlers/calendar.go`:**
- `GET /calendar/export` → generate single .ics file with all events in VCALENDAR format.
  For each event: if ical_raw is non-empty, use it directly; else generate VEVENT from
  proto fields (SUMMARY, DTSTART, DTEND, LOCATION, DESCRIPTION, UID).
  Content-Type: text/calendar, Content-Disposition: attachment; filename="calendar.ics"
- `POST /calendar/import` → multipart file upload (.ics), parse VCALENDAR, create Event
  protos for each VEVENT entry. Flash "Imported N events".

**iCal parsing:** Use `emersion/go-ical` (add to go.mod if needed, or use go-webdav's
built-in ical support) for VCALENDAR/VEVENT parsing.

**Add to calendar.templ:**
- Export button: `<a href="/calendar/export">Export .ics</a>` in toolbar
- Import form: file input + submit, `enctype="multipart/form-data"`

**Done when:** Export downloads valid .ics. Import .ics creates events on calendar.
Round-trip: export → delete events → import → same events restored.

### 6. Event search via Bleve

Index events for search.

**Modify `internal/store/search.go`:**
- Add `EventDocument` struct: Title, Description, Location, StartTime, EndTime, CalendarID
- Add `eventIndexMapping()` — text fields for title/description/location, numeric for times
- Add `IndexEvent(userID, guid, title, description, location string, startTime, endTime int64)` —
  index into per-user Bleve (document ID prefix: "event:")
- Add `DeleteEventIndex(userID, guid string)`
- Add `SearchEvents(userID, query string, limit int) ([]EventSearchResult, error)`

**Modify `internal/store/events.go`:**
- After Create/Update → call `search.IndexEvent()`
- After Delete → call `search.DeleteEventIndex()`

**Add search UI:**
- Search box on calendar page (similar to mail search)
- `GET /calendar/search?q=...` → search events, render results list

**Done when:** Create event → searchable by title/description/location. Search results
link to event detail.

### 7. Integration tests

**Create `test/integration/calendar_test.go` (build tag `integration`):**

**TestCalendarAndEventCRUD:**
1. Start dev agent, create user, login
2. GET /calendar → verify default calendar exists, month view renders
3. POST /calendar/event → create event (title, start, end, location)
4. GET /calendar/{year}/{month} → verify event chip appears on correct day
5. PUT /calendar/event/{guid} → update title
6. GET /calendar/event/{guid} → verify updated title
7. DELETE /calendar/event/{guid} → verify removed from month view

**TestCalDAVSync:**
1. Create event via web UI POST
2. PROPFIND /caldav/{userID}/calendars/default/ → verify collection contains event
3. GET /caldav/{userID}/calendars/default/{guid}.ics → verify valid iCal with VEVENT
4. PUT new event via CalDAV (send VCALENDAR to new path)
5. GET /calendar → verify CalDAV-created event appears in web UI month view
6. DELETE via CalDAV → verify removed from web UI

**TestICalImportExport:**
1. Create 2 events via POST
2. GET /calendar/export → download .ics, verify 2 VEVENT entries
3. Delete both events
4. POST /calendar/import with the .ics → verify 2 events restored
5. GET /calendar → verify both events on grid

**Done when:** `go test -race -tags=integration ./test/integration/calendar_test.go` passes.

---

## Exit Criteria

- [ ] Event and Calendar protos created; Go code generated
- [ ] EventStore CRUD + ListByDateRange works via Raft; unit tests pass
- [ ] CalendarStore CRUD works; default calendar auto-created; unit tests pass
- [ ] CalDAV PROPFIND/GET/PUT/DELETE at /caldav/{userID}/calendars/
- [ ] /.well-known/caldav redirects correctly
- [ ] Web UI ↔ CalDAV two-way sync (events visible in both directions)
- [ ] GET /calendar/{year}/{month} renders month grid with event chips
- [ ] Prev/next month navigation via HTMX
- [ ] Calendar link in sidebar navigation
- [ ] Event create/edit/delete via web UI forms
- [ ] All-day toggle hides time inputs
- [ ] GET /calendar/export downloads valid .ics
- [ ] POST /calendar/import parses .ics and creates events
- [ ] Bleve indexes events on create/update, removes on delete
- [ ] Event search returns results by title/description/location
- [ ] `go test -race ./...` passes
- [ ] `go test -race -tags=integration ./test/integration/calendar_test.go` passes

## What NOT to do in this sprint

- No week or day views (Sprint 10b)
- No recurring events / RRULE expansion (Sprint 10b — field stored for CalDAV round-trip)
- No drag-and-drop event rescheduling (Sprint 10b)
- No mini calendar navigator in sidebar (Sprint 10b)
- No event reminders/alarms via SSE (Sprint 10b)
- No multiple calendar colour coding in UI (Sprint 10b — colour field stored)
- No shared calendar display (Sprint 10b)
- No free/busy lookup (Sprint 10b)
