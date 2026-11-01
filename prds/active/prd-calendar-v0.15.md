# PRD: Calendar v0.15.0

## 1. Introduction / Overview

The feature-parity audit of the original Bongo
(`prds/research/parity-audit-original-bongo.md`, rows cited as #N) shows the
calendar as the largest gap after mail delivery. Meeting invitations,
calendar sharing that grants real access, publishing and subscribing,
editing one occurrence of a series, email reminders, tasks, resource
calendars and a summary page were all features (or stated requirements) of
the original. This PRD fixes the scope of v0.15.0 "Calendar".

Every story was checked against `main` at `6725448` on 2026-10-26. Where the
code disagrees with the audit, the story says so; section 8.2 lists the
corrections together. Two findings matter most:
- Calendar sharing today grants **free/busy only**, not read access as the
  audit says, and there is no share control in the web UI at all.
- Recurring events have correctness gaps under the audit's "PARTIAL": no
  override or EXDATE handling in expansion, and expanded instances carry a
  GUID that the event routes probably cannot find.

**The scope rule.** The in-scope list is section 3. Anything a review finds
during this release goes into section 8.2, not into TASKS.md, unless the
change under review introduced it.

| Phase | Theme | Stories |
|-------|-------|---------|
| A | Recurrence and sharing foundations | US-001 to US-005 |
| B | Meeting invitations (iTIP/iMIP) | US-006 to US-009, US-020 |
| C | Publishing and subscriptions | US-010 to US-011 |
| D | Reminders, tasks and resources | US-012 to US-015 |
| E | Views | US-016 to US-017 |
| F | Mail stories moved from v0.14 | US-018, US-019 |

TASKS rows are assigned by `norman import`. Dependencies: US-002 needs
US-001; US-004 and US-005 need US-003; Phase B needs US-001 (overrides) and
US-006 first; US-015 needs Phase B; US-017 needs US-008, US-014 and
US-019; US-020 needs US-007 to US-009.

### Size key

S: one focused session. M: two or three packages, a proto change or a new
form. L: a new subsystem or a cross-cutting change.

## 2. Goals

- A recurring series displays its exceptions and exclusions correctly, and a
  user can change or delete one occurrence, or this and all following.
- A user can share a calendar with another local user at free/busy, read or
  write level; the recipient sees it in the web UI and in CalDAV clients.
- A user can invite people to an event by email, and can accept, decline or
  tentatively accept an invitation they receive; updates and cancellations
  are applied.
- A user can publish a calendar as a read-only feed and subscribe to an
  external ICS feed, without the server becoming a tool for reaching its own
  internal network.
- Reminders can arrive by email; tasks sync over CalDAV and can be managed in
  the web UI; rooms and equipment can be booked.
- A summary page brings mail to me, starred mail, pending invitations and
  upcoming events together.

## 3. User Stories

### Phase A: Recurrence and sharing foundations

Release note: recurring events show their exceptions, and single
occurrences can be edited or deleted. Calendars can be shared at free/busy,
read or write level, and shared calendars appear in CalDAV clients.

### US-001: Recurrence correctness
**Source:** audit #61 (corrected). **Size:** M.

**What exists:** more is missing than the audit's "PARTIAL" suggests.
`store.ExpandRecurrence` (`internal/store/events.go:148-213`, rrule-go
v1.8.2):
- forces DTSTART to UTC, so a weekly 09:00 Europe/Amsterdam meeting moves by
  an hour across a DST change;
- does not apply RECURRENCE-ID overrides held in `ical_raw`, so every
  instance shows the master's time and title;
- matches `Event.exdates` by date, but nothing fills that field from a
  CalDAV client's EXDATE (`icalevent.ToEvent` does not read it), so
  client-side deletions of one occurrence reappear in the web, in free/busy
  and in reminders.

Expanded instances get GUID `{guid}_{YYYY-MM-DD}` (`events.go:196`), and the
templates link `/calendar/event/{that GUID}` (`calendar.templ:95`), which
`ShowEvent` looks up as an exact key (`calendar.go:518`). Opening or
rescheduling a recurring instance therefore probably returns 404. This was
found by reading the code, not by running it; the story's first test
confirms or disproves it.

Events created in the web have no `ical_raw`, and the CalDAV and export
fallbacks (`internal/caldav/server.go:389-424`, `calendar.go:977-996`) write
no RRULE, EXDATE or VALARM, so CalDAV clients see a web-created series as
one event.

**Acceptance Criteria:**
- [ ] Expansion evaluates the rule in the event's TZID: a weekly 09:00
      Europe/Amsterdam series stays at 09:00 local on both sides of the
      October and March DST changes.
- [ ] EXDATEs from `ical_raw` are honoured by web views, free/busy and
      reminders (a CalDAV fixture with one EXDATE: that day has no instance).
- [ ] RECURRENCE-ID overrides replace their instance: an override moving
      one instance to 15:00 with another title shows at 15:00 with that
      title, and the original slot is empty.
- [ ] An instance's link resolves: `GET /calendar/event/{guid}?occurrence=
      {RECURRENCE-ID}` shows that instance. The instance GUID format is
      replaced by an explicit occurrence parameter; old links of the form
      `{guid}_{date}` redirect.
- [ ] A web-created recurring event exported or fetched over CalDAV carries
      its RRULE and EXDATEs, and its VTIMEZONE when zoned.

### US-002: Edit or delete one occurrence, or this and following
**Source:** audit #61. **Size:** M. **Needs:** US-001.

**What exists:** series-level RRULE presets only
(`eventRrulePresets`, `calendar.go:1011-1020`); `DeleteEvent` deletes the
whole series (`calendar.go:594`).

**Acceptance Criteria:**
- [ ] Editing an instance of a series asks "This event", "This and following"
      or "All events" (translated), as the delete action does.
- [ ] "This event" writes a RECURRENCE-ID override into the series'
      `ical_raw` (one resource, one Raft PUT); CalDAV GET returns the master
      and the override in one object.
- [ ] "This and following" ends the original series with an UNTIL before
      the chosen instance and creates a new series from it, with a new UID;
      both writes are separate PUTs. If the second fails, the first is
      rolled back by restoring the previous `ical_raw`, and the request
      fails; the comment documents this compensation.
- [ ] Deleting "this event" adds an EXDATE (and removes any override for
      that instance); "this and following" sets UNTIL.
- [ ] An instance edit on an event with attendees (Phase B) sends the
      matching iTIP message for that instance only.

### US-003: The grant key space and calendar permission levels
**Source:** audit #64 (corrected). **Size:** M.

**What exists:** the audit says sharing is read-only and web-only. Both are
wrong. `Calendar.shared_with` (field 8) holds user IDs and its only consumer
is `CalendarStore.IsSharedWith` (`internal/store/calendars.go:167`), whose
only caller is free/busy (`calendar.go:427, 468`). The recipient never sees
the shared events. The share and unshare routes exist
(`POST /calendar/{id}/share`, `/unshare`, `agent.go:1408-1409`) with a
50-share cap and a uniform refusal, but `calendar.templ` has no control that
calls them.

There is no generic ACL key space today (CLAUDE.md). This story creates one,
designed for the mailbox and address-book sharing of v0.16 as well (section
6.2 has the full design).

**Acceptance Criteria:**
- [ ] The keys `/acl/calendar/{owner_id}/{calendar_guid}/u:{grantee_id}`
      (a `Grant` protobuf) and the reverse marker
      `/aclrev/u:{grantee_id}/calendar/{owner_id}/{calendar_guid}` are only
      ever written together, by one composite Raft command (`/__acl__/`)
      applied in a single Pebble batch. A test applies set and revoke and
      asserts both keys change or neither does.
- [ ] Levels are `freebusy`, `read` and `write`. Any other value is refused.
- [ ] A one-time leader migration turns every `shared_with` entry into a
      `freebusy` grant in state `accepted`, then clears `shared_with`. It is
      idempotent and guarded by `/system/migrations/calendar-grants-v1`,
      written last. Free/busy behaves exactly as before for migrated shares.
- [ ] `IsSharedWith` is replaced by a grant lookup; free/busy needs at least
      `freebusy` on any of the owner's calendars, as today.
- [ ] Deleting a calendar revokes its grants; deleting a user revokes
      grants they own and grants they hold, in the same request. A leader
      sweep removes any grant whose owner, calendar or grantee is gone.
- [ ] The existing 50-grants-per-calendar cap, the per-user share rate
      limit and the uniform refusal message are kept.
- [ ] CLAUDE.md's key table and its "no generic ACL" paragraph are updated
      in the same change.

**Cluster:** a node on the old binary applies `/__acl__/…` as a literal
key, so it would hold no grants. Grants must not be created until every node
runs v0.15 (release note). Production runs one node.

### US-004: Shared calendars in the web UI and CalDAV, with write access
**Source:** audit #64. **Size:** L. **Needs:** US-003.

**Acceptance Criteria:**
- [ ] The calendar settings card has a sharing section: add a local user by
      address with a level, change the level, remove. All POSTs carry CSRF;
      a failed grant write fails the request.
- [ ] Calendars shared with the user at `read` or `write` appear in the
      month, week, day and agenda views under "Shared with me", in the
      owner's colour, with a per-calendar visibility toggle.
- [ ] `freebusy` grants show the owner's busy blocks only, titled "Busy",
      and only in the free/busy lookup and the scheduling assistant view,
      never in the calendar views.
- [ ] With `write`, the recipient can create, edit and delete events in the
      shared calendar; with `read`, every write route answers 403 (a test
      per route: create, update, reschedule, delete, import).
- [ ] CalDAV: shared calendars appear in the recipient's calendar home as
      `/caldav/{recipient}/calendars/shared-{owner}-{guid}/`; PROPFIND
      reports `read` or `read-write` privileges (`current-user-privilege-set`);
      PUT and DELETE on a `read` share answer 403.
- [ ] Every CalDAV and web handler checks the grant on each request (no
      caching of authorisation across requests), and the user ID in a
      CalDAV path is checked against the authenticated user or a grant,
      never trusted as is.
- [ ] Writes by the recipient are stored under the owner's key space and
      count against the owner's quota.

**Security:** this is the first time one user can write into another's
data. Each route gets a negative test with a valid session for a user who
holds no grant, a `freebusy` grant and a `read` grant.

### US-005: Invite to a shared calendar by email, with acceptance
**Source:** audit #65. **Size:** M. **Needs:** US-003, US-004.

**Acceptance Criteria:**
- [ ] Sharing with a local user creates the grant in state `pending` and
      sends the recipient a translated email ("{0} has shared the calendar
      {1} with you") with a link to `/calendar/shares`.
- [ ] `/calendar/shares` lists pending shares with Accept and Decline (POST,
      CSRF). Only accepted shares appear in views and CalDAV.
- [ ] Declining deletes the grant; the owner sees it disappear from the
      sharing list.
- [ ] An address that is not a local account is refused with the same
      uniform message as today. Sharing with an external address is
      publishing (US-010), not a grant: if the calendar is published, the
      form offers "Send the public link" instead.
- [ ] The invitation email is sent off the request path and is best-effort
      (logged on failure; the grant stands).

### Phase B: Meeting invitations (iTIP/iMIP)

Release note: invitations can be sent from the web calendar, and received
invitations can be accepted, declined or tentatively accepted from the
message view.

### US-006: Organiser and attendees in the event model and form
**Source:** audit #68. **Size:** M.

**What exists:** nothing; the audit is correct. `Event`
(`proto/bongo/v1/event.proto`) has no organiser, attendee, status or
sequence fields; ATTENDEE and ORGANIZER from CalDAV clients survive only in
`ical_raw`.

**Acceptance Criteria:**
- [ ] `Event` gains additive fields: `organizer` (address), `attendees`
      (repeated message: address, name, role, partstat, rsvp), `sequence`
      and `status`. `icalevent.ToEvent` fills them from `ical_raw`, and the
      web writer emits them.
- [ ] The event form has an attendees field with address autocomplete from
      contacts and the GAL; each attendee shows a status badge.
- [ ] At most 100 attendees per event; addresses are validated.
- [ ] Existing events read with empty fields; nothing is migrated.

### US-007: Send invitations, updates and cancellations (iMIP)
**Source:** audit #68. **Size:** M. **Needs:** US-006.

**Design:** server-side sending from the web UI. CalDAV PUTs do not send
mail in this story: until the server advertises CalDAV scheduling, clients
such as Thunderbird send iMIP themselves, and a server copy would duplicate
it. US-020 adds implicit scheduling for CalDAV clients and reuses this send
path.

**Acceptance Criteria:**
- [ ] Saving an event with attendees from the web sends each attendee an
      iMIP message: `multipart/alternative` with a text part and
      `text/calendar; method=REQUEST` carrying the VEVENT with ORGANIZER set
      to the user's address, plus an `.ics` attachment.
- [ ] Changing time, place or attendees increments SEQUENCE and sends
      REQUEST to all attendees; removing an attendee sends them CANCEL;
      deleting the event sends CANCEL to all.
- [ ] Local attendees receive through the local pipeline, external ones
      through the outbound queue, as ordinary mail from the user (DKIM,
      send-as and recipient rate limits apply).
- [ ] Sending happens after the event is saved; a send failure leaves the
      event saved and shows a translated warning naming the attendees not
      reached.

### US-008: Receive and answer invitations
**Source:** audit #68. **Size:** M. **Needs:** US-006.

**Acceptance Criteria:**
- [ ] The message view detects a `text/calendar` part with
      `method=REQUEST` and shows an invitation card: title, time in the
      user's zone, organiser, attendees, conflicts with the user's
      calendars, and Accept, Tentative and Decline buttons (POST, CSRF).
- [ ] Answering adds the event to the user's default calendar (or the one
      chosen) with PARTSTAT set, or for Decline marks it declined and
      hidden, and sends `method=REPLY` to the organiser.
- [ ] A REQUEST with a higher SEQUENCE for an event already in the calendar
      updates it, keeping the user's PARTSTAT where the time is unchanged
      and resetting it to NEEDS-ACTION where it changed.
- [ ] A CANCEL for a known event marks it cancelled (shown struck through,
      removable).
- [ ] Nothing is added to a calendar without the user's action.
- [ ] Malformed or oversized (over 256 KiB) calendar parts show as ordinary
      attachments.

**Security:** an invitation is untrusted input. The card renders text only,
escaped; URLs in DESCRIPTION are not linked; the organiser's address is
shown with a warning when the message failed DMARC.

### US-009: Organiser side: apply replies
**Source:** audit #68. **Size:** S. **Needs:** US-007.

**Acceptance Criteria:**
- [ ] A `method=REPLY` delivered to the organiser updates that attendee's
      PARTSTAT on the organiser's event when the UID matches an event the
      organiser owns and the replying address is an attendee.
- [ ] A REPLY is applied only when the message passed DMARC for the
      attendee's domain or was submitted by an authenticated local user who
      owns that address; otherwise it is left as ordinary mail.
- [ ] Applying a REPLY is one PUT of the event; a failure is logged and the
      mail stays in the INBOX.
- [ ] COUNTER and REFRESH are shown as ordinary mail (non-goal).

### US-020: CalDAV scheduling (RFC 6638) for CalDAV clients
**Source:** audit #68, 8.1 decision 3. **Size:** L. **Needs:** US-007,
US-008, US-009.

**What exists:** nothing. go-webdav v0.7.0 has no scheduling support (no
inbox or outbox collection, no scheduling properties), so Apple Calendar and
other CalDAV clients cannot send or answer invitations through Bongo.

**Design:** implemented in Bongo's CalDAV HTTP wrapper and backend, not by
forking go-webdav: the wrapper answers the scheduling properties and the
outbox POST, and the backend's object write runs implicit scheduling with
the same iMIP send and receive paths as US-007 to US-009.

**Acceptance Criteria:**
- [ ] The DAV header advertises `calendar-auto-schedule`. The principal
      reports `schedule-inbox-URL`, `schedule-outbox-URL` and
      `calendar-user-address-set` (the user's primary address and verified
      send-as identities only).
- [ ] Implicit scheduling as organiser: a PUT or DELETE of an event whose
      ORGANIZER is one of the user's addresses sends REQUEST or CANCEL to
      the attendees through the US-007 path (SEQUENCE rules, local and
      external delivery, rate limits, DKIM), and sets SCHEDULE-STATUS on
      each ATTENDEE. An attendee with `SCHEDULE-AGENT=CLIENT` or `NONE` is
      not sent anything.
- [ ] Implicit scheduling as attendee: a PUT that changes the user's own
      PARTSTAT on an event they were invited to sends REPLY to the
      organiser through the US-008 path.
- [ ] Incoming REQUEST, REPLY and CANCEL for a local user are placed in
      their schedule inbox as well as delivered as mail, so CalDAV clients
      show them; nothing is added to a calendar without the user's action
      (as US-008). A REPLY is applied to the organiser's event under the
      US-009 rules (DMARC or authenticated local sender).
- [ ] A free-busy POST to the outbox answers for local users with the
      busy times their sharing level permits (US-003), and for others with
      `3.7;Invalid calendar user`.
- [ ] No duplicate mail: a client that sees `calendar-auto-schedule` stops
      sending iMIP itself; a test with Apple Calendar and Thunderbird
      request recordings shows one REQUEST per attendee.
- [ ] Limits as US-006 and US-007 (100 attendees, recipient budget); the
      schedule inbox keeps at most 500 items per user, oldest dropped first.

**Security:** the organiser address must be one the user may send as; an
ORGANIZER the user does not own is refused with 403 and nothing is sent.

### Phase C: Publishing and subscriptions

Release note: calendars can be published as a read-only feed, optionally
password-protected, and users can subscribe to external ICS feeds.

### US-010: Publish a calendar as a tokenised read-only feed
**Source:** audit #66. **Size:** M.

**What exists:** nothing; there is no unauthenticated token route today.

**Design (8.1 decision 7):** `Calendar` gains `published` (9),
`publish_token` (10, 32 random bytes from crypto/rand, base64url in the
link), `publish_detail` (11, `full` or `busy`) and `publish_password_hash`
(12, bcrypt). The token is stored, so the link can be shown again, is
independent of `web.session_secret` (rotating the secret leaves published
links working), and is revoked or regenerated per calendar by writing a new
token. The route carries the owner and calendar IDs, so no lookup index is
needed. The token is a capability: it is never logged, and a backup or
Pebble export contains it.

**Acceptance Criteria:**
- [ ] `GET /pub/calendar/{owner_id}/{calendar_guid}/{token}.ics` on the
      public router, outside the authenticated groups, serves the calendar as
      iCalendar with `ETag` and `Cache-Control: private, max-age=300`.
      A wrong token, an unpublished calendar or an unknown calendar all
      answer the same 404, compared in constant time.
- [ ] `busy` detail serves only DTSTART, DTEND and "Busy" per event. Both
      modes strip ATTENDEE, ORGANIZER, VALARM and private events'
      details.
- [ ] With a password set, the feed requires HTTP Basic auth (any user
      name); failures are throttled per IP and per calendar with the
      existing `authlimit`.
- [ ] Settings show the `https://` and `webcal://` links, a Regenerate
      link button (writes a new token; the old link answers 404 at once), an
      Unpublish button (clears the token), and the password field. All
      mutations carry CSRF.
- [ ] The route is GET-only and sets no cookie.

### US-011: Subscribe to an external ICS feed
**Source:** audit #67. **Size:** M.

**What exists:** nothing. No outbound HTTP client in the code has a
private-address guard: the MTA-STS fetcher (`internal/smtp/mtasts.go:199-222`)
has timeouts, no redirects and a size limit, but no address check.

**Acceptance Criteria:**
- [ ] A new `internal/netx` safe HTTP client: the dialer's `Control` hook
      refuses, after DNS resolution and for every connection including
      redirects, loopback, private (RFC 1918, `fc00::/7`), link-local,
      CGNAT (`100.64.0.0/10`), multicast, unspecified, `0.0.0.0/8`,
      benchmarking and documentation ranges, and IPv4-mapped forms of all
      of them. A table test covers each range and a DNS name resolving to
      `127.0.0.1`.
- [ ] Schemes `https` and `http` (and `webcal`, fetched as https); ports 80,
      443 and 8443 only; at most 3 redirects; 30-second timeout; 5 MiB body
      limit; URLs with user information are refused.
- [ ] `calendar.subscriptions.allow_private_networks` (default false)
      exists for operators who need an internal feed; it is a restart key.
- [ ] A subscribed calendar stores `source_url` and refresh state; it is
      read-only in the web and over CalDAV (PUT answers 403).
- [ ] A leader-only worker refreshes each subscription every 6 hours
      (minimum 1 hour), with conditional GET. Events are replaced by UID:
      new ones written, changed ones updated, missing ones deleted, each a
      separate PUT or DELETE. A failure part-way leaves a mix of old and
      new events that the next refresh corrects; the worker's comment
      documents this.
- [ ] At most 20 subscriptions per user and 5000 events per subscription;
      beyond the event cap the refresh stops and records the error.
- [ ] The URL is treated as a secret: logs show its host only.

### Phase D: Reminders, tasks and resources

Release note: reminders can arrive by email; tasks sync over CalDAV and
appear in the web UI; rooms and equipment can be booked as resource
calendars.

### US-012: Email reminders
**Source:** audit #70. **Size:** S.

**What exists:** browser reminders over SSE (`internal/agent/reminders.go`),
with an in-memory dedupe that is lost on restart and wiped hourly, and the
checker runs on every node (correct for SSE, which is node-local). SMS is
dropped by the operator.

**Acceptance Criteria:**
- [ ] `Event` gains `reminder_email` (additive). The form offers "In the
      browser", "By email" and "Both".
- [ ] A leader-only worker sends due email reminders to the owner's primary
      address through the local pipeline, so forwarding and rules apply.
- [ ] Each sent reminder is recorded at
      `/user/{id}/remindersent/{event_guid}/{occurrence_unix}` before the
      mail is enqueued; a write failure skips the send (no duplicates). The
      leader's sweep deletes records older than 2 days.
- [ ] A restart or leader change does not resend a reminder (a test runs two
      checker passes with a restart between them).
- [ ] VALARM from CalDAV clients still does not create server reminders
      (non-goal, as today).

### US-013: Tasks (VTODO) over CalDAV
**Source:** audit #72 (corrected). **Size:** M.

**What exists:** the audit says CalDAV advertises VEVENT only, which is
true, but misses a bug: `PutCalendarObject` (`internal/caldav/server.go:267`)
does not check the component, so a VTODO PUT returns 201 and is stored as an
event with an empty title at 1970-01-01 that shows in the web, the export
and free/busy.

**Acceptance Criteria:**
- [ ] A PUT whose only component is not allowed by the target calendar is
      refused with 403 and the `CALDAV:supported-calendar-component`
      precondition. The existing blank events created by VTODO PUTs are
      listed by a one-time check in the startup log (not deleted).
- [ ] `Calendar` gains `components` (VEVENT, VTODO or both; empty means
      VEVENT). Each new user gets a "Tasks" calendar for VTODO.
- [ ] Tasks are stored at `/user/{id}/task/{guid}` as a `Task` protobuf
      (summary, description, due, start, completed, status, priority,
      percent, `ical_raw` inline); the new message is classified in
      `knownBlobBearingFields` (it carries no blob).
- [ ] CalDAV PROPFIND reports the supported component set per calendar. If
      go-webdav cannot report it (its PROPFIND has a TODO for this
      property), the story patches or wraps the library and records which.
- [ ] Apple Reminders, Thunderbird and DAVx5 with tasks.org can create,
      complete and delete a task (manual check, recorded in the PR).
- [ ] Tasks count towards the owner's quota in the usage recompute.

### US-014: Tasks in the web UI
**Source:** audit #72. **Size:** M. **Needs:** US-013.

**Acceptance Criteria:**
- [ ] `/tasks` lists open tasks by due date, with overdue first and a
      "Completed" filter; creating, editing, completing and deleting a task
      use POST with CSRF and fail when the write fails.
- [ ] A completed task records `COMPLETED` and `STATUS:COMPLETED`; reopening
      clears both.
- [ ] Tasks with a due date appear in the agenda view (US-016).
- [ ] All text is translated (en, de, es, fr).

### US-015: Resource calendars
**Source:** audit #75. **Size:** M. **Needs:** US-008.

**What exists:** nothing; `UserProfile` has no account kind.

**Acceptance Criteria:**
- [ ] `UserProfile` gains `kind` (12): empty for a person, `resource` for a
      room or equipment. Global and domain admins create resources in
      `/admin/users` with a name, address and capacity.
- [ ] A resource cannot log in on any protocol (authentication refuses it
      with the same response as a wrong password).
- [ ] A REQUEST to a resource is answered automatically: ACCEPTED when the
      slot is free, DECLINED when it overlaps an accepted booking; the
      booking is written to the resource's calendar. Only senders on the
      resource's own domain may book (others get DECLINED).
- [ ] Resources appear in the attendee autocomplete marked as rooms or
      equipment, and their free/busy is readable by every user of the
      domain.
- [ ] Domain admins can see and cancel bookings.

### Phase E: Views

### US-016: The upcoming (agenda) view
**Source:** audit #60. **Size:** S.

**What exists:** the Agenda view shipped in v0.13 (TASKS 27.9,
`/calendar/agenda`, next 30 days grouped by day).

**Acceptance Criteria:**
- [ ] The agenda includes accepted shared calendars (US-004), subscriptions
      (US-011) and tasks with a due date (US-014), each marked by its
      calendar colour.

### US-017: Summary page
**Source:** audit #88. **Size:** M. **Needs:** US-008, US-016, US-019
(for "to me").

**What exists:** `/` redirects to `/mail/INBOX` (`agent.go:1120-1122`). A
starred view exists (`store.VirtualMailboxes`), contrary to what the
audit's silence suggests, so the "starred" section reuses it.

**Acceptance Criteria:**
- [ ] `/summary` shows four sections, each with at most 10 items and a link
      to its full view: unread mail to me (the US-019 "to me" view; unread
      INBOX if that has not shipped), starred mail, invitations awaiting an
      answer (REQUESTs from US-008 with PARTSTAT NEEDS-ACTION), and events
      and due tasks in the next 7 days.
- [ ] A preference `start_page` (`summary` or `inbox`, default `summary`,
      8.1 decision 4) decides where `/` and login redirect.
- [ ] Each section renders its own empty state; one failing section shows
      an error in place and does not fail the page.
- [ ] The page loads in under 300 ms with 10,000 messages in INBOX (a
      benchmark in the handler tests).

### Phase F: Mail stories moved from v0.14

Release note: DSN parameters are passed to the next server, and the "To
me", "From my contacts" and "Mailing lists" views.

### US-018: DSN parameters relayed onward (from v0.14 US-011)
**Source:** audit #6. **Size:** M. **Needs:** v0.14 US-010.

**What exists:** `Relay.Deliver` (`internal/smtp/relay.go:90`) uses the
standard library's `net/smtp`, whose `Mail` and `Rcpt` cannot carry DSN or
SIZE parameters.

**Acceptance Criteria:**
- [ ] The relay moves to the go-smtp client. STARTTLS, MTA-STS, DANE,
      timeouts and error mapping are unchanged (the existing relay tests
      pass unmodified).
- [ ] When the remote advertises `DSN`, RET, ENVID, NOTIFY and ORCPT are
      passed on; when it does not and NOTIFY includes SUCCESS, an
      `Action: relayed` report is sent.
- [ ] `SIZE=` is sent when the remote advertises SIZE.
- [ ] Bounces and DSN reports for a sender outside the hosted domains are
      relayed to that sender instead of being dropped by the deliver stage
      (v0.14 PRD 8.2), under the existing backscatter rules: null senders
      and unauthenticated forwards still get nothing, and relayed reports
      are capped per recipient and globally, the way reversed SRS bounces
      already are (`srsAddrLimiter`, `srsGlobalLimiter` in the deliver
      stage).

### US-019: "To me", "From my contacts" and "Mailing lists" views (from v0.14 US-016)
**Source:** audit #89. **Size:** M. **Needs:** v0.14 US-015.

**What exists:** none of the three views. The SQLite `maildocument` table
has a `list_id` column (`internal/store/metadata.go:106`) that no insert
fills. The existing virtual views (`unread`, `starred`, `all-mail`,
`store.VirtualMailboxes`, `mailbox.go:898-958`) are Pebble scans over every
mailbox, which does not scale to header-based views.

**Acceptance Criteria:**
- [ ] The derived index fills `list_id` and gains `to_me` (any of the user's
      addresses in To or Cc at delivery) and the normalised sender address;
      existing indexes are filled by the existing rebuild path, resumable
      and rate-limited.
- [ ] Views `/mail/to-me`, `/mail/from-contacts`, `/mail/lists` and
      `/mail/lists/{list-id}` list messages from INBOX and user folders,
      excluding Junk, Trash, Sent and Drafts, newest first, paginated like a
      mailbox.
- [ ] "From my contacts" matches the normalised sender against the user's
      contact addresses; adding a contact changes the view without a
      re-index.
- [ ] `/mail/lists` groups by List-Id with the list name and count; each
      group links to its list view.
- [ ] Actions in a view act on the underlying message in its real mailbox.
- [ ] A user can only see their own messages (a test with two users sharing
      a list).

## 4. Functional Requirements

FR-1 to FR-20 are the acceptance criteria of US-001 to US-020. In every
story:

- Every cookie-authenticated mutation is behind CSRF and same-origin checks.
- A persistence error on a user-visible write fails the request, unless the
  code says the write is best-effort and logs the failure.
- Authorisation for shared data is checked on every request, in the handler
  that reads or writes, with a negative test per route.
- New text is translated in en, de, es and fr, with a second grep pass.
- New config keys have defaults, validation and a class in `config/diff.go`.

## 5. Non-Goals (Out of Scope)

- SMS reminders (#70): dropped by the operator.
- Natural-language event entry and mail-to-calendar commands (#71): dropped
  by the operator.
- CalDAV scheduling (RFC 6638, schedule inbox and outbox); see open
  question 2.
- iTIP COUNTER, REFRESH and DECLINECOUNTER.
- Publishing one event on its own, and a public search of published
  calendars (part of #66).
- CalDAV free-busy-query REPORT (go-webdav answers 400); free/busy stays a
  web route.
- Server reminders from client VALARMs.
- Sharing with people outside the server other than by publishing.

## 6. Technical Considerations

### 6.1 Security

The server is internet-facing.
- US-010 adds the only unauthenticated data route in this release. It is
  GET-only, sets no cookie, answers one 404 for every failure, compares
  tokens in constant time and throttles password attempts.
- US-011 is an SSRF surface. The address check sits in the dialer, so it
  applies after DNS resolution and on every redirect, which also defeats
  DNS rebinding between check and connect.
- US-004 lets one user write into another's data; see its security note.
- iMIP input (US-008, US-009) is untrusted. Nothing is added to a calendar
  without a user action, except REPLYs that pass DMARC and resource bookings
  from the resource's own domain.
- No new route goes under `/internal/*`. Every new `http.Server` (none are
  planned) would need explicit timeouts. Tokens and feed URLs are not
  logged.

### 6.2 The grant key space

Introduced by US-003 for calendars and extended by v0.16
(`prd-sharing-contacts-v0.16.md`) to mailboxes, address books and groups.

```
/acl/{kind}/{owner_id}/{resource_id}/{grantee}      -> Grant protobuf
/aclrev/{grantee}/{kind}/{owner_id}/{resource_id}   -> empty marker
```

- `kind`: `calendar` in v0.15; `mailbox` and `addressbook` in v0.16.
- `resource_id`: the calendar GUID. Every segment is escaped with
  `url.PathEscape`, so no segment contains `/`.
- `grantee`: `u:{user_id}`; v0.16 adds `g:{group_id}`. There is no
  "anyone" or anonymous grantee; public access is publishing, not a grant.
- `Grant`: `level` (calendar: `freebusy`, `read`, `write`), `rights`
  (reserved for v0.16 mailbox rights, RFC 4314 letters), `state`
  (`pending`, `accepted`), `granted_by`, `created_at`, `updated_at`.
- Writes go through one composite Raft command, `/__acl__/`, with a JSON
  payload (`set`, `revoke`, `revoke_resource`, `revoke_owner`,
  `revoke_grantee`). The FSM applies it in one Pebble batch, like the queue
  and Bayes commands (`commitSyncBatch`), so the forward and reverse keys
  never disagree.
- "What have I shared" is a prefix scan of `/acl/{kind}/{owner}/`; "what is
  shared with me" is a prefix scan of `/aclrev/u:{me}/{kind}/`.
- Cleanup: deleting a resource or a user issues the matching revoke command
  in the same request; a leader sweep removes grants whose owner, resource
  or grantee no longer exists.

### 6.3 Other data

- Additive fields on `Event`, `Calendar` and `UserProfile`; new keys
  `/user/{id}/task/{guid}`, `/user/{id}/remindersent/…`,
  `/system/migrations/calendar-grants-v1`. Tasks carry no blobs.
- Multi-key writes and their semantics: US-002 (compensation), US-011
  (convergent refresh), US-003 (composite command). Every other write is a
  single PUT.
- The CLAUDE.md key table is updated by each story that adds a key.

### 6.4 Mixed-version clusters

Nodes do not publish their version, and new command types are not gated
(`prd-hardening-v0.9.md` 8.2, "Version-aware rolling upgrades"). This
release adds one composite command (`/__acl__/`), so it is **not** rollable
while grants are being created: a node on v0.14 would store the command as a
literal key and hold no grants. Upgrade every node before anyone shares a
calendar. The migration in US-003 runs on the leader; run it after the
upgrade completes. Production runs one node.

### 6.5 Dependencies on other releases

US-017 uses US-019 ("to me"). US-016 depends on v0.13 27.9. v0.16
builds on the grant key space from US-003.

## 7. Success Metrics

- Every story done, each with a test that fails on `6725448`.
- An invitation sent from Bongo to a Gmail and an Outlook.com account shows
  as an invitation there, and their replies update PARTSTAT (manual check).
- The SSRF table test covers every refused range, and gosec medium findings
  do not increase.
- `make test-all` stays green.

## 8. Open Questions

None open. Questions 1 to 5 are answered in 8.1 (decisions 3, 4, 6, 7
and 8).

### 8.1 Decisions

1. (operator, 2026-10-26) Scope as listed in section 3.
2. (operator, 2026-10-26) SMS reminders and natural-language entry with
   mail-to-calendar commands (#70 SMS part, #71) are dropped.
3. (operator, 2026-10-27) CalDAV server-side scheduling (RFC 6638) is
   wanted, so Apple Calendar and other CalDAV clients can send and answer
   invitations through Bongo. Add it as a story after iTIP/iMIP (US for
   #68); its release (v0.15.0 or v0.15.1) is settled at import.
4. (operator, 2026-10-27) The Summary page becomes the default start page
   after login, with a user preference to start in the inbox instead.
5. (operator, 2026-10-27) The mail views (v0.14 US-016) and DSN relay
   (v0.14 US-011) move into this release, per the v0.14 split. They are
   US-019 and US-018 here.
6. (operator, 2026-11-01) Open question 1: no split. All 20 stories,
   US-001 to US-020, ship in v0.15.0. RFC 6638 is US-020 (decision 3).
7. (operator, 2026-11-01) Open question 3: published feed tokens are
   random and stored per calendar, not derived from the session secret
   (US-010).
8. (operator, 2026-11-01) Open question 4: resources accept bookings only
   from accounts on their own domain (US-015, as written).

### 8.2 Candidates for the next release

Audit corrections found while writing this PRD (2026-10-26):

- #64: a calendar share grants free/busy only, not read access, and the web
  UI has no share control.
- #61: expansion ignores TZID, overrides and client EXDATEs; instance links
  probably 404 (unconfirmed, see US-001).
- #72: a VTODO PUT is stored as a blank 1970 event.
- #88: a starred view already exists (`/mail/starred`).
- The reminder checker's dedupe is wiped hourly, so a 60-minute reminder can
  fire twice, and it scans every key under `/user/` every minute.

Release size (2026-10-26, before US-020 was added): 17 stories (3 S,
13 M, 1 L). That is above the ~15-story guide and heavy in M stories.
Proposed split (not taken: 8.1 decision 6 ships all 20 stories in
v0.15.0):

| Release | Stories | Why |
|---------|---------|-----|
| v0.15.0 | Phases A, B, C (US-001 to US-011) | Recurrence, sharing, invitations, publish and subscribe: the core calendar gap. |
| v0.15.1 or v0.16 | Phases D, E (US-012 to US-017) | Reminders, tasks, resources and views build on A to C and can ship separately. |

Other candidates:

- Bound `reminder_minutes` to the offered options (already noted in the
  v0.12 PRD 8.2) and make the SSE reminder dedupe survive the hourly wipe.
- Week, day and the calendar index place timed events in the server zone,
  not the user's preference zone (v0.12 PRD 8.2).

Found while building Phase A, batch 1 (2026-11-01):

- `CalendarStore.Delete` revokes the calendar's grants, but no route deletes
  a calendar (web or CalDAV collection DELETE). A story that adds calendar
  deletion must call it; until then the leader sweep covers orphans.
- An override whose RECURRENCE-ID is also listed in EXDATE is still shown.
  RFC 5545 leaves this ambiguous; Google and Apple differ.
- `GrantOrphaned` probes `/user/{id}/profile` with unescaped IDs. User IDs
  never contain `/`, so this is hardening only.
- Web events saved before v0.15 carry no zone and keep expanding in UTC; a
  series created before the upgrade can still shift by an hour across DST.

Found while building batch 2 (2026-11-01):

- "This and following" counts instances with the bare rule, so splitting at
  the second instance of a series whose first instance is excluded leaves
  an original series with no visible instance.
- An `ical_raw` that does not decode is replaced by a synthesised object on
  the first single-occurrence edit (expansion already ignores it).
- Changing the level of a pending grant re-posts to `/share` and writes it
  as accepted; US-005 must keep the state.
- `resolveCalendarTarget` accepts a calendar ID the caller owns without
  checking that it exists, so create and import can file events under a
  missing calendar of the caller's own.
- Events with an empty `calendar_id` show in a shared default calendar's
  CalDAV listing but not in the owner's own CalDAV listing of it.
- A title-only edit bumps SEQUENCE but sends no REQUEST (the criterion
  covers time, place and attendees). US-009 and US-020 must tolerate the gap
  when matching replies.
- The "CalDAV PUT sends nothing" guard is an import check; US-020 replaces
  it with a behavioural test.
- An edit by a delegate with write access sends no invitations unless the
  delegate may send as the organiser.

Found while building batch 3 (2026-11-01):

- Accepted as built (US-010): the `busy` feed keeps UID, DTSTAMP, DURATION,
  RRULE, RDATE, EXDATE and RECURRENCE-ID as well as DTSTART, DTEND and
  "Busy"; without them a recurring series shows only its first instance.
- Accepted as built (US-010): a feed request with no Basic credentials is not
  counted against the password limiter, because clients probe for the
  challenge before sending credentials.
- US-010: the per-calendar lockout (10 failures in 15 minutes from any IP)
  lets anyone holding the link but not the password keep the feed answering
  429 for its subscribers. The PRD asked for the throttle; document it in
  SECURITY.md.
- US-010: `CalendarStore.Mutate` serialises per node only. A follower can
  serve a revoked token until replication catches up. The feed has no
  X-WR-CALNAME. Whether a subscribed calendar may be published is undecided.
- Accepted as built (US-011): `POST /calendar/{id}/unsubscribe` (CSRF,
  owner only) was added; a subscription needs a way to end it.
- US-011: feed events are not counted against the domain quota. Each pass
  lists all of a user's events once per due subscription.
- US-008: a known event is matched by the hash of its UID or the UID as the
  key only; an event a CalDAV client stored under another object name is
  treated as new, and accepting creates a duplicate.
- US-018: go-smtp's command (5 minutes) and submission (12 minutes)
  deadlines now apply to outbound relay, where net/smtp had none after the
  30-second dial. The STARTTLS step is written over textproto because
  go-smtp v0.24 exports no client StartTLS, so a plaintext remote sees two
  EHLOs. Reports to external senders are capped per node (5 per address,
  200 per hour by default); a cluster of N nodes allows N times that.
- Found in the browser check: the event form accepts an end before the
  start (the end date keeps its default when only the start date changes,
  and `parseEventForm` has never checked the order). Since US-007 such an
  event is also sent to attendees as an invitation. Predates v0.15.
- US-008: after answering, the card's attendee list still shows the user's
  own status as in the received invitation ("no answer"); the "You accepted"
  line is correct.
