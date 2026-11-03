# PRD: Calendar fixes v0.15.1

## 1. Introduction / Overview

v0.15.0 shipped the calendar release on 2026-11-03. Its PRD
(`prds/done/prd-calendar-v0.15.md`) recorded 58 follow-up notes in
section 8.2. The operator scoped the defects and small gaps among them into
v0.15.1 on 2026-11-03 ("lets finish off 0.15's follow ups", option "All of A
as v0.15.1").

Every item was checked against the v0.15.0 tag (`ba14689`) on 2026-11-03 by
four read-only reviewers. Results that change the notes:
- **Worse than noted.** MKCALENDAR is refused with 405 by go-webdav v0.7.0
  (the newest release), so no CalDAV client can create a calendar or list.
  A Windows TZID gives the event a start time of 0 (1970), not UTC. A missing
  CalDAV object answers 500 on GET and DELETE. The ETag is quoted twice, so
  every conditional PUT answers 412.
- **Already fixed, dropped.** Week, day and index views use the user's zone
  (v0.13.0, `TestCalendarViewsShareUserZone`). `reminder_minutes` is bounded
  to the offered options (`calendar.go:1449`); US-018 adds a regression test
  only. Web single-occurrence edits already send per-occurrence iTIP; only
  CalDAV lacks it.

**The scope rule.** The in-scope list is section 3. Anything a review finds
during this release goes into section 8.2, not into TASKS.md, unless the
change under review introduced it.

| Phase | Theme | Stories |
|-------|-------|---------|
| A | CalDAV interoperability and calendars | US-001 to US-005 |
| B | Recurrence and tasks | US-006 to US-009 |
| C | Invitations | US-010 to US-016 |
| D | Sharing, reminders, mail and setup | US-017 to US-023 |

Dependencies: US-012 needs US-010. US-009 lands before US-010 (shared
revision helper). US-005 needs US-003. Others are independent.

### Size key

S: one focused session. M: two or three packages, a proto change or a new
form. L: a new subsystem or a cross-cutting change.

## 2. Goals

- Apple Calendar, Apple Reminders, Thunderbird and DAVx5 can create, list,
  update and delete calendars and objects without 405, 412 or 500 answers.
- Events from Outlook with Windows time-zone names land at the right time.
- Invitations are sent when they should be (title changes, single
  occurrences over CalDAV, attendee deletes) and not when they should not
  (events entirely in the past).
- Tasks show where users look for them: shared calendars, every occurrence
  of a recurring task, overdue tasks.
- No user-visible English date formats in reminder mail or the agenda for a
  non-English locale.

## 3. User Stories

### Phase A: CalDAV interoperability and calendars

### US-001: Create calendars and lists over CalDAV (M)
**Description:** As a user of Apple Reminders or Thunderbird, I want a new
list or calendar I create in the client to be created on the server with the
components I asked for, so that tasks and events in it sync.

Today go-webdav v0.7.0 answers MKCALENDAR with 405 (`gowebdav/internal/server.go:110`).
Extended MKCOL reaches `Backend.CreateCalendar` (`internal/caldav/server.go:238-254`),
which ignores `SupportedComponentSet` and the requested path and stores a
random GUID, VEVENT-only.

**Acceptance Criteria:**
- [ ] MKCALENDAR `/caldav/{user}/calendars/groceries/` with displayname
  "Groceries" and `supported-calendar-component-set` {VTODO} returns 201.
  PROPFIND on that path reports displayname "Groceries" and component set
  {VTODO}; a VTODO PUT to `/caldav/{user}/calendars/groceries/x.ics`
  returns 201.
- [ ] MKCALENDAR with no component set creates a VEVENT calendar; with
  {VEVENT, VTODO} it holds both. Components other than VEVENT and VTODO are
  dropped; a set with neither returns 403 `supported-calendar-component`.
- [ ] MKCALENDAR on an existing calendar path returns 405 with
  `DAV:resource-must-be-null`; on another user's path or a shared path,
  403; a segment that is not a valid GUID, 403.
- [ ] Extended MKCOL with the same body creates the same calendar at the
  requested path (Backend.CreateCalendar honours path and component set).

### US-002: Correct CalDAV status codes and ETags (S)
**Description:** As a CalDAV client, I want 404 for a missing object and an
ETag I can send back in If-Match, so that sync does not loop on errors.

**Acceptance Criteria:**
- [ ] GET and DELETE of `/caldav/{user}/calendars/default/missing.ics`
  return 404 (today 500). `EventStore.Get` returns an error matching
  `ErrEventNotFound` for a missing key, and `admin_resource.go:177` and
  `caldav/server.go:274,530` behave correctly with it.
- [ ] The ETag header and `getetag` of an existing object match
  `^"[0-9a-f]{32}"$` with no escaped quotes (events and tasks).
- [ ] PUT with `If-Match:` set to that ETag returns 2xx; with a stale ETag,
  412.

### US-003: Events go only into calendars that exist (S)
**Description:** As a user, I want events filed only under calendars I have,
and listed the same for me as for people I share with.

**Acceptance Criteria:**
- [ ] `POST /calendar/event` and `POST /calendar/import` with
  `calendar_id=nope` (not one of the user's calendars) return 404 and store
  nothing. A move on update to a missing own calendar returns 404.
- [ ] A CalDAV PUT into a non-existent own collection returns 404 (409 if
  the client expects a parent conflict; pick one and test it).
- [ ] An event stored with `CalendarId ""` appears in the owner's own
  PROPFIND Depth:1 and calendar-query on `/calendars/default/`, as it does
  for a grantee (`caldav/server.go:323-330` uses `eventCalendar` in both
  branches).

### US-004: Windows time-zone names (M)
**Description:** As a user receiving events from Outlook, I want
`TZID=W. Europe Standard Time` read as Europe/Berlin.

Operator decision: embed the CLDR `windowsZones.xml` territory "001"
mapping as a generated Go table in `internal/icalevent`; `icalevent.Zone`
consults it before the IANA shape check.

**Acceptance Criteria:**
- [ ] `icalevent.Zone("W. Europe Standard Time")` returns Europe/Berlin;
  "Eastern Standard Time" returns America/New_York; an unknown name still
  returns ok=false.
- [ ] A CalDAV PUT or import of `DTSTART;TZID=W. Europe Standard
  Time:20301014T090000` with `RRULE:FREQ=WEEKLY` stores StartTime
  2030-10-14T07:00Z (today 0), and the 2030-11-04 instance expands to
  08:00Z.
- [ ] Every site that resolves a TZID through go-ical `DateTime` or
  `time.LoadLocation` uses the resolver instead: `icalevent/icalevent.go:30,33,68,81`,
  `handlers/calendar.go:1879,1885,1901`, `handlers/calendar_publish.go:328`,
  `caldav/schedule_http.go:820-821`.
- [ ] The tests pinning the old behaviour (`icalevent/instance_test.go:86`,
  `icalevent/recurrence_test.go:24`, `store/recurrence_test.go:79`) are
  inverted. The table has a generator script and a header naming the CLDR
  version.

### US-005: Delete a calendar (M)
**Description:** As a user, I want to delete a calendar I own, in the web
and from a CalDAV client, with its events and tasks.

**Acceptance Criteria:**
- [ ] Owner with calendars "default" and "work", event E in "work", a read
  grant on "work" to B, and "work" published: `DELETE /calendar/work` (web,
  CSRF) and CalDAV `DELETE /caldav/{user}/calendars/work/` each return 2xx
  (204 for CalDAV). Afterwards the calendar, E and the work tasks are gone,
  B's grant is gone, and the feed URL returns 404.
- [ ] Deleting `tasks`, `default`, the user's last event calendar, a
  subscription (use unsubscribe), or a shared path returns 403 or 409 and
  changes nothing.
- [ ] The web calendar settings card has a Delete button with a
  confirmation step; strings in en, de, fr and es.
- [ ] Web and CalDAV share one store-level helper; partial-failure semantics
  are documented in its comment (as for UnsubscribeCalendar).

### Phase B: Recurrence and tasks

### US-006: "This and following" with excluded leading instances (S)
**Acceptance Criteria:**
- [ ] Weekly series COUNT=5 with EXDATE on instance 1: delete scope=following
  at instance 2 deletes the series (`events.Get` returns not-found).
- [ ] Same series, edit scope=following at instance 2: the original guid is
  gone and the new series has COUNT=4 and 4 visible instances.
- [ ] The COUNT carry-over still uses the bare rule (RFC 5545), unchanged.

### US-007: Tasks from shared calendars (M)
**Description:** As a user with a read or write grant on a calendar that
holds tasks, I want those tasks in `/tasks` and the agenda.

Scope: read-only display. Editing or completing a shared task in the web is
a non-goal (CalDAV already allows it for write grants).

**Acceptance Criteria:**
- [ ] B grants A read on calendar C holding an open VTODO due tomorrow. A's
  `GET /tasks` and `GET /calendar/agenda` include it, coloured as the share,
  without complete or edit controls.
- [ ] A free/busy grant shows no tasks. A revoked grant shows none.

### US-008: Recurring and overdue tasks (M)
**Acceptance Criteria:**
- [ ] A task with `DUE;VALUE=DATE:20261102` and `RRULE:FREQ=WEEKLY;COUNT=4`
  gets agenda rows on 2026-11-09, 11-16 and 11-23 for a window from
  2026-11-03 of 28 days. EXDATE and RECURRENCE-ID overrides are honoured.
- [ ] On 2026-11-10, `/tasks` shows that task due 2026-11-16 (the next open
  occurrence).
- [ ] An open task due 2026-10-20 appears, labelled overdue, in the agenda
  window containing today (2026-11-03), and not in `?from=2026-12-01`. The
  same task COMPLETED does not appear.
- [ ] Completing a single occurrence is a non-goal.

### US-009: Task edits keep DUE and DTSTART valid (S)
**Acceptance Criteria:**
- [ ] `ApplyTodoEdit` on a VTODO with DTSTART and DUE as DATE-TIME, changed to
  a date-only due, returns DTSTART and DUE both with VALUE=DATE.
- [ ] A web edit setting DUE before the stored DTSTART returns 400 with a
  translated message; the stored task is unchanged.
- [ ] One exported `icalevent.BumpRevision(props, now) int` sets SEQUENCE,
  DTSTAMP and LAST-MODIFIED and replaces the three copies
  (`icalevent/todo.go:235-241`, `handlers/calendar.go:1733-1743`,
  `handlers/calendar_instance.go:296-311`). Table test: absent → 1,
  `" 4 "` → 5, `-3` → 1, `junk` → 1.

### Phase C: Invitations

### US-010: Updates carry a new SEQUENCE and title changes are sent (S)
**Acceptance Criteria:**
- [ ] A CalDAV organiser PUT moving DTSTART but keeping SEQUENCE:0 sends a
  REQUEST with SEQUENCE:1, and GET returns SEQUENCE:1. A client SEQUENCE
  already higher (5) is kept.
- [ ] A web or CalDAV edit changing only SUMMARY or DESCRIPTION of an event
  with attendee bob sends one REQUEST with the new SUMMARY and the bumped
  SEQUENCE (`inviteRelevantChange` includes title and description).

### US-011: No invitations for events entirely in the past (S-M)
**Acceptance Criteria:**
- [ ] Creating an event with attendees that ended yesterday sends nothing
  (web and CalDAV). Deleting it sends nothing.
- [ ] Editing it to tomorrow sends one REQUEST.
- [ ] A series is past only when it has UNTIL or COUNT and its last
  occurrence has ended; an open-ended series is never past. The same check
  applies to single-occurrence notifications.

### US-012: CalDAV per-occurrence scheduling (M)
**Acceptance Criteria:**
- [ ] A CalDAV organiser re-PUT whose only change is a new override moving
  the 2030-11-04 occurrence sends one REQUEST with that RECURRENCE-ID and no
  RRULE. Adding an EXDATE, or setting an override STATUS:CANCELLED, sends one
  CANCEL with that RECURRENCE-ID.
- [ ] A series whose master has no ATTENDEE and whose 2030-11-04 override
  lists bob: bob gets one REQUEST with that RECURRENCE-ID, and the override's
  ATTENDEE gets SCHEDULE-STATUS 1.1.

### US-013: Deleting an invitation declines it (S)
**Acceptance Criteria:**
- [ ] An attendee's CalDAV DELETE of an event organised by alice@remote,
  where the attendee is ACCEPTED, sends exactly one REPLY PARTSTAT=DECLINED
  to alice, from an address the user may send as.
- [ ] Nothing is sent when the ORGANIZER has SCHEDULE-AGENT other than
  SERVER, the event is STATUS:CANCELLED, the user already DECLINED, or the
  event is entirely past (US-011).

### US-014: Replies covering several occurrences (M)
**Acceptance Criteria:**
- [ ] One REPLY from bob with master ACCEPTED and override 2030-11-04
  DECLINED leaves the stored master with bob ACCEPTED and the 2030-11-04
  override with bob DECLINED, in one store write.
- [ ] The existing `maxReplyOverrides` limit and the party-crasher check
  still apply per VEVENT.

### US-015: Invitation card accuracy (S)
**Acceptance Criteria:**
- [ ] After the user accepts, the card's attendee row for the user shows
  ACCEPTED (from the stored event; from the override for an instance
  invitation). Other attendees keep the message's values.
- [ ] Invited at a verified send-as address and at a plain alias: the REPLY
  goes from the send-as address.
- [ ] Invited only at a plain alias: the card shows a translated note that
  the answer is saved but the organiser will not be told, and answering
  sends nothing. The reply is never sent from the primary address.

### US-016: Resource bookings per occurrence; no mail kept (M)
**Acceptance Criteria:**
- [ ] A resource holding a weekly booking receives a CANCEL for 2030-11-04:
  that slot is free in its free/busy and other weeks stay booked.
- [ ] A REQUEST moving 2030-11-11 to a free slot gets one REPLY ACCEPTED with
  that RECURRENCE-ID; onto a clashing booking, DECLINED. A RID for an
  unknown series gets DECLINED.
- [ ] Ten booking REQUESTs to a resource each get a REPLY, and the
  resource's INBOX holds 0 messages with domain usage unchanged. A plain
  non-iMIP mail to the resource is still stored.

### Phase D: Sharing, reminders, mail and setup

### US-017: Pending shares expire; leaving a share (M)
**Acceptance Criteria:**
- [ ] A pending grant older than 30 days (`UpdatedAt`) is not counted
  towards the 50-share cap, is not listed on the grantee's
  `/calendar/shares`, and cannot be accepted (404). The expiry is decided
  from the command timestamp inside the FSM, so replicas agree. A re-share
  refreshes it.
- [ ] `/calendar/shares` lists accepted shares with a Leave button.
  `POST /calendar/shares/leave` (CSRF) removes the user's grant; the
  calendar disappears from their views and CalDAV. Another user's grant
  cannot be removed this way. Strings in en, de, fr and es.

### US-018: Browser reminders fire once, without a full scan (S)
**Acceptance Criteria:**
- [ ] With a fake clock, an event with a 60-minute reminder checked every
  minute for 60 minutes calls `Notify` exactly once, across the point where
  the hourly wipe used to happen. Dedupe entries are pruned once the start
  has passed.
- [ ] The checker gets its users from `ListUserIDs`, not a prefix scan of
  `/user/`.
- [ ] Regression test: the event form with `reminder_minutes` 90, -5 or abc
  is refused and nothing is stored.

### US-019: Localised dates in reminder mail and the agenda (M)
**Acceptance Criteria:**
- [ ] One locale-aware date helper in `internal/i18n` (from the logic of
  `summaryDate`, `summary.templ:128-131`) is used by the reminder mail, the
  agenda day header and the agenda title.
- [ ] For a `de` user, the reminder mail body contains the German weekday
  and month names and no English month name; the zone is shown by its IANA
  name, not an abbreviation such as MST.
- [ ] The agenda page for a `de` user contains no "Monday" or "January" text
  in `.agenda-day-head` or the title.

### US-020: Summary links and the inbox warning (S)
**Acceptance Criteria:**
- [ ] `GET /mail/to-me?unread=1` lists only unread to-me messages, and the
  parameter survives pagination and partial reloads. The summary's to-me
  link is `/mail/to-me?unread=1`.
- [ ] The summary's invitations link is labelled as opening the INBOX
  (a separate invitations view is a non-goal).
- [ ] `GET /mail/INBOX?warning=Recovery code used. You have 3 code(s)
  remaining.` renders the translated warning; `?warning=<b>x</b>` renders
  none (same allow-list as the summary page).

### US-021: Apple profiles per user (S)
**Acceptance Criteria:**
- [ ] Profiles for a@example.com and b@example.com have different outer and
  inner PayloadIdentifier values; two downloads for a@example.com have
  identical identifiers. The identifier carries a hash of the address, not
  the address.

### US-022: Address headers with quoted commas (S)
**Acceptance Criteria:**
- [ ] `store.SplitAddressHeader("\"Smith, John\" <j@x.com>, b@y.com")`
  returns 2 entries. It parses with `net/mail.ParseAddressList` and falls
  back to the comma split on a parse error (the compose helper
  `splitRecipients` moves into store and is reused).
- [ ] The IMAP ENVELOPE To for that header has exactly 2 addresses, the
  first named "Smith, John" (`imap/backend.go:1302`); the Cc backfill
  (`handlers/mail.go:2484`) uses the same helper.
- [ ] Stored MailMeta written before the fix is not rewritten (no users).

### US-023: Mail views cannot hide folders (M)
**Acceptance Criteria:**
- [ ] The header views move to their own path (`/mail/view/to-me`,
  `/mail/view/from-contacts`, `/mail/view/lists`, `/mail/view/lists/{id}`) and
  are identified internally by an argument that is never a folder name.
- [ ] A user with real folders `to-me` and `lists` sees each folder's
  messages at the URL the sidebar links to, and the To me view stays
  reachable at its own URL.
- [ ] All links, the summary page and tests use the new paths. No redirect
  from the old paths (no users).

## 4. Functional Requirements

FR-1 to FR-23 are the acceptance criteria of US-001 to US-023.

## 5. Non-Goals (Out of Scope)

- Cluster-only issues (production runs one node): follower `addresses`
  rows for `to_me` and REPLY checks, per-node booking lock, per-node report
  cap, per-node schedule-inbox cap, `CalendarStore.Mutate` per-node
  serialisation, mixed-version notes. Deferred to the remote agents and
  cluster work.
- Design decisions not taken: CalDAV Schedule-Tag; counting subscribed feed
  events against the domain quota; publishing a subscribed calendar;
  X-WR-CALNAME in feeds.
- Editing or completing shared tasks in the web; completing one occurrence
  of a recurring task; an invitations view.
- Recorded and accepted in the v0.15 PRD 8.2 (no change): the busy feed
  properties, unauthenticated feed probes, the availability timing
  difference, override-in-EXDATE ambiguity, 10-year VTIMEZONE, GrantOrphaned
  hardening, SMTPUTF8 behaviour, old-link redirects for the owner only,
  pre-v0.15 events without zones, unverified REPLY/CANCEL kept out of the
  schedule inbox.

## 6. Technical Considerations

- **Library limits.** go-webdav v0.7.0 has no MKCALENDAR and double-quotes
  ETags. US-001 handles MKCALENDAR in our own handler in front of go-webdav
  (next to `ScheduleHandler`, `internal/agent/agent.go:5184`); do not fork
  the library.
- **Raft.** US-017 expiry must be deterministic: decide it from the
  command's timestamp in `PlanACL`, never from the local clock at apply.
  US-005 and US-016 write several keys; document partial-failure semantics
  or use a composite command (CLAUDE.md guardrail).
- **Security.** US-001 and US-005 accept only the authenticated user's own
  paths. US-015 never sends from an address the attendee list does not
  contain. US-020 never echoes the raw `warning` parameter.
- **Client recordings.** During the operator's client checks, capture real
  Apple and Thunderbird traffic for MKCALENDAR, conditional PUT and DELETE,
  and add them to `internal/caldav/testdata` where they replace hand-made
  fixtures.

## 7. Success Metrics

- The operator's client checks pass with new lists and calendars created
  from Apple Reminders, Apple Calendar and Thunderbird.
- Every story has a test that is red on v0.15.0 and green after.
- `norman verify` reports no FAIL for v0.15.1.

## 8. Open Questions

### 8.1 Decisions

- 2026-11-03, operator: all of group A ships as v0.15.1; Windows zones via an
  embedded CLDR table.
- 2026-11-03, operator approved these defaults at import:
  pending shares expire after 30 days; shared tasks are read-only in
  the web; an alias-only invitee gets a note, not a reply from the primary
  address; the summary's invitations link is relabelled, not replaced by a
  view; header views move to `/mail/view/...` with no redirects; `tasks`,
  `default` and the last event calendar cannot be deleted.

### 8.2 Candidates for the next release

(empty)
