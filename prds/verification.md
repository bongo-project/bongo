# Verification: Calendar v0.15.0

**Date:** 2026-11-03
**Commit verified:** `07666b3` (main after Phase 30)
**PRD:** [done/prd-calendar-v0.15.md](done/prd-calendar-v0.15.md)
**Rigor:** adversarial. Each story had two read-only verifiers: one traced the implementation and its edge cases, the other judged whether the tests would go red if the guarantee broke. A completeness critic then reviewed the whole result. Operator decisions in PRD 8.1 override the story text where they differ.
**Scale:** 41 agents, 1,132 tool calls, 13 minutes.

**Previous report:** the v0.14 verification of 2026-11-01. Its gaps were fixed in Phase 29, and the report is still in git history.

## Summary

PASS 2, PARTIAL 17, FAIL 1. Most PARTIAL verdicts come from test gaps or from limits already recorded in 8.2. The defects below are the ones that change behaviour.

| Requirement | Verdict | Main reason |
|-------------|---------|-------------|
| US-001 recurrence | PASS | |
| US-002 occurrence edits | PARTIAL | "This and following" on a shared calendar stores the new series under the calendar ID `shared:{owner}:{cal}`, a calendar that does not exist. |
| US-003 grant key space | PARTIAL | The leader sweep of orphaned grants is untested where it is wired. The 50-grant cap is tested only at 50 to 51. |
| US-004 shared calendars | PARTIAL | No scheduling assistant view exists and no busy block is titled "Busy". Nothing in the web UI reads `/calendar/freebusy`. CSRF on share and unshare is untested through the production router. |
| US-005 share invitations | PARTIAL | Logging of a failed invitation is untested. |
| US-006 attendees | PARTIAL | The attendee autocomplete is covered only by source greps. The contacts merge in `/api/gal` is untested. |
| US-007 iMIP sending | PARTIAL | Test gaps only (ordering of save then send). |
| US-008 receive invitations | PARTIAL | Accept with no calendar chosen can file the event in an arbitrary calendar. A read-only subscribed calendar can be chosen, and the next refresh deletes the event. A malformed or oversized inline calendar part with no filename shows neither a card nor an attachment. The card's time zone is untested. |
| US-009 apply replies | PARTIAL | Test gaps (failure logging, COUNTER and REFRESH shown as mail). On a follower, missing address rows leave a valid REPLY unapplied. |
| US-020 CalDAV scheduling | PARTIAL | An attendee invited through a plain alias cannot answer: the REPLY is dropped. Attendees listed only in an override are not scheduled. The 100-attendee limit counts the organiser's own ATTENDEE line. Every test replaces delivery with a recorder. |
| US-010 published feeds | PARTIAL | In full mode, an override of a private series publishes that occurrence's details. In busy mode, a cancelled or transparent override re-appears as busy from the master's rule. The settings forms are not asserted in the rendered page. |
| US-011 subscriptions | PARTIAL | A feed that is emptied legitimately is refused as "holds no events", so its old events are never deleted. The 30-second timeout and the 5 MiB value are not asserted. |
| US-012 email reminders | PARTIAL | The sweep of sent records is untested where it is wired. No CalDAV test shows that a VALARM creates no server reminder. |
| US-013 tasks over CalDAV | FAIL | The manual client check (Apple Reminders, Thunderbird, DAVx5 with tasks.org) is not recorded. `docs/caldav-carddav.md` says the Tasks calendar is created for new accounts; the code creates it only when a user has no calendars, and not when the first CalDAV action is MKCALENDAR. |
| US-014 tasks in the web | PARTIAL | `/tasks` CSRF is checked by a source grep only. A recurring task shows only at its first due date. An overdue task due before the window is not in the agenda. |
| US-015 resources | PARTIAL | A series whose override moves onto a booked slot is accepted (double booking); an excluded occurrence that would collide declines the whole series. CalDAV free-busy requests for a room are refused even from its own domain. The admin bookings wiring is untested. |
| US-016 agenda | PASS | |
| US-017 summary page | PARTIAL | Known: no invitations view, and the to-me link lists read mail too. The benchmark asserts no limit. |
| US-018 DSN relay | PARTIAL | go-smtp sends `SMTPUTF8` only when asked, and the relay never asks, so a message to a non-ASCII address can be refused permanently. net/smtp added it automatically. |
| US-019 header views | PARTIAL | IMAP APPEND and web import store no List-Id or Cc, so imported list mail (bongo-import uses APPEND) never appears in Mailing lists. A grouped vCard email (`item1.EMAIL`, as Apple Contacts writes) is not matched in From my contacts. |
| Critic | | `SECURITY.md` does not mention the unauthenticated `/pub/calendar/...` route or the per-calendar feed lockout (PRD 8.2 asked for it). |

## Behaviour defects

1. **US-002 shared split.** `editFollowing` takes the calendar ID from the form, which on a shared calendar is `shared:{owner}:{cal}`, and stores the new series under the owner with that ID. Reproduced by a verifier with an overlay test. The existing tests use the bare calendar ID.
2. **US-008 default calendar.** When no calendar is chosen, `ownCalendar` does not prefer the user's default calendar, so Accept can file the event in another calendar.
3. **US-008 subscribed target.** `ownCalendar` and the card's calendar list check only the component, not `Subscription`. An invitation accepted into a subscribed calendar is deleted by the next refresh.
4. **US-008 inline part.** `servableParts` lists only parts with a filename. Outlook and Google put `text/calendar` inline in `multipart/alternative` with no filename, so a malformed or oversized one disappears from the message view.
5. **US-010 private override.** `publishTransform` judges CLASS per VEVENT, so an override without CLASS of a PRIVATE master is published in full.
6. **US-010 cancelled override.** In busy mode a cancelled or transparent override is dropped, and the master's RRULE then produces that occurrence as busy.
7. **US-011 empty feed.** `ParseFeed` refuses a VCALENDAR with no events, so a calendar emptied at the source keeps its old events and shows a refresh error.
8. **US-015 booking conflicts.** The conflict check expands the series without its overrides and EXDATEs, so a moved occurrence can double-book and an excluded one can decline the series.
9. **US-015 CalDAV free/busy.** `CalDAVScheduler.FreeBusy` checks self and grants but not `sharedResource`, so CalDAV clients cannot see a room's availability.
10. **US-018 SMTPUTF8.** The relay never sets `MailOptions.UTF8`, so non-ASCII addresses go out without `SMTPUTF8` even when the remote advertises it.
11. **US-019 imported mail.** IMAP APPEND and web import build `MailMeta` without List-Id and Cc, and the backfill does not revisit those rows.
12. **US-019 grouped vCard email.** `ContactStore.Addresses` matches lines starting with `EMAIL` only.
13. **US-020 alias attendee.** An attendee invited at a plain alias is put in attendee mode, and the REPLY is then refused because the user may not send as the alias. The answer is lost with a log line.

## Missing feature

- **US-004 scheduling assistant.** The story says free/busy grants show busy blocks titled "Busy" in the free/busy lookup and the scheduling assistant view. The lookup exists as JSON; no view shows it, and the event form does not show attendees' availability.

## Test gaps worth closing

- Production-router CSRF checks for `/calendar/{id}/share`, `/unshare`, the invitation POST and the `/tasks` routes.
- Wiring tests for the orphaned-grant sweep, the sent-reminder sweep and `adminH.SetResourceBookings`.
- The invitation card in a non-UTC user zone.
- A CalDAV PUT with a VALARM sets no server reminder.
- A COUNTER or REFRESH message shows no card.
- The 50-grant cap at 49 to 50.

## Documentation

- `SECURITY.md`: the unauthenticated feed route, its constant-time token check, and the per-calendar lockout that lets a link holder keep the feed answering 429.
- `docs/caldav-carddav.md`: when the Tasks calendar is created.

## Known or accepted (already in PRD 8.2, or recorded there now)

Non-IANA TZIDs fall back to UTC. The generated VTIMEZONE covers 10 years for an open series. An override also listed in EXDATE is shown. Old `{guid}_{date}` links redirect for the owner only. A higher-SEQUENCE REQUEST for one occurrence offers only an answer. An event stored by a CalDAV client under another object name is not recognised by the invitation card. Override-only edits send no per-occurrence iTIP. The server does not bump SEQUENCE. The schedule-inbox cap is per node. Recurring tasks show at their first due date only. Resource bookings list the first 500 events in UTC. The client recordings in `internal/caldav/testdata` are hand-made approximations of Apple and Thunderbird traffic.

## Phase 31 and the mutation sweep (2026-11-03)

All 13 defects, the missing availability view, the test gaps and the doc gaps were fixed in Phase 31 (PRs #148 and #149), each defect with a test that was red on main.

The invariant sweep then ran against `eefd2e9`. Five read-only agents enumerated 61 falsifiable v0.15 guarantees in five areas: sharing, recurrence and tasks, iTIP and scheduling, publish and subscribe, and mail. After deduplication, 59 remained. Each was broken with the smallest compiling edit through `go test -overlay`, so the working tree was never touched.

- **58 guarded:** a named test went red for each.
- **1 unguarded, confirmed by an adversarial verifier (MM-1, medium).** A domain whose MTA-STS or DANE policy requires TLS was protected against a remote that offers STARTTLS and then refuses it with 454, but no test exercised that path. An injected 454 is a downgrade attack. Fixed by `TestRelayRequireTLSRefusedSTARTTLSNeverFallsBackToPlaintext`, which is red under the break and green on main.

## Not yet done
- **Manual client checks (operator):** CalDAV scheduling with Apple Calendar and Thunderbird; tasks with Apple Reminders, Thunderbird and DAVx5 with tasks.org; Apple account setup after the discovery fix (PR #146).

# Verification: Calendar fixes v0.15.1

**Date:** 2026-11-03
**Commit verified:** `ba7a047` (main after Phase 32)
**PRD:** [done/prd-calendar-fixes-v0.15.1.md](done/prd-calendar-fixes-v0.15.1.md)
**Rigor:** adversarial. Two read-only verifiers per story (implementation and edge cases; whether the tests would go red), then a completeness critic.
**Scale:** 53 agents, 811 tool calls, 8 minutes.

## Summary

PASS 13 (US-001, 003, 006, 008, 009, 013, 014, 015, 017, 020, 021, 023, 024), PARTIAL 13, FAIL 0. Most PARTIAL verdicts are tests that would not go red; five are behaviour defects.

## Behaviour defects

1. **US-002 weak ETag for web events.** Events created or edited in the web have no `ical_raw`, so CalDAV serves `ETag: "t<unix>"`, which fails the criterion and changes only once a second.
2. **US-011 all-day slack.** `store.EventEntirelyPast` gives all-day events a fixed 14 hours of slack instead of using the user's zone, so an all-day event dated yesterday still sends invitations until 14:00 UTC.
3. **US-005 silent refusal.** The settings card's Delete button has no status target, so a refused or failed delete shows nothing; its error bodies are English only.
4. **US-022 encoded names.** enmime decodes RFC 2047 words before `SplitAddressHeader`, so an encoded "Last, First" display name with a comma arrives unquoted and splits. `parseAddress` also keeps backslash escapes in quoted names.
5. **Docs (critic).** `docs/caldav-carddav.md` still says MKCALENDAR ignores the component set and that a first MKCALENDAR never gets Tasks; it says a subscribed calendar's DELETE answers 403 (collection DELETE answers 409); it omits collection DELETE, extended MKCOL and the 5-calendar cap (507).

## Test gaps

- US-004: no Windows-TZID test for the whole-series web edit (`setEventTimes`, `eventTimesChanged`), `exdateFor` in published feeds, `freeBusyInstant` (VFREEBUSY POST), or web ICS import.
- US-005: the confirmation step (`hx-confirm`) is unasserted; no agent-level CalDAV collection DELETE wiring test; the CalDAV delete test does not check the feed.
- US-007: the agenda colour assertion is satisfied by the sidebar dot, so the row colour is unguarded; no wiring test.
- US-010: no CalDAV summary-only or description-only PUT through the real scheduler checking the mailed SUMMARY and SEQUENCE; the web description-only case does not check DESCRIPTION.
- US-011: no real CalDAV PUT or DELETE of a past event; no organiser-level test for an ended COUNT/UNTIL series.
- US-012: no test of an override set to STATUS:CANCELLED without an EXDATE, checking RECURRENCE-ID and no RRULE.
- US-018: no test drives `runReminderChecker`, where the hourly wipe lived.
- US-019: the agent does not prove the reminder mailer gets the real preference store (locale).
- US-022: no IMAP ENVELOPE (buildEnvelope) test; no Cc backfill test.
- US-025: no assertion of the `user-created` and `booking-cancelled` redirect Locations.
- US-026: no de/fr/es test of the new form strings or the subscribe 409; no locale-parity test.

## Recorded, no change

- US-016's "domain usage unchanged" means no mail is stored; accepted bookings add event records, which domain usage counts. Criterion wording corrected in 8.1.
- A web DELETE of another user's calendar or a `shared:` key answers 404 (nothing changes, existence not revealed); accepted in 8.1.
- An attendee removed from one override's attendee list gets no CANCEL for that occurrence (same class as the US-012 note in 8.2).

All of the above fixed in Phase 33 at the operator's request (2026-11-03).

## v0.15.2 web UI fixes (2026-11-04)

Verified against `main` at `0989607` (PR #161): one combined lens per story, two for US-005, and a completeness critic.

| Story | Result | Gaps |
|-------|--------|------|
| US-001 settings page | PARTIAL | Share rows misaligned, overlap at 390 px in de and fr; markup untested; sidebar classes not scanned |
| US-002 sidebar | PARTIAL | Import not submittable by mouse without JS; no result shown after import |
| US-003 day header | PARTIAL | Wrong date in midnight-DST zones; undefined token for today's tint |
| US-004 admin | PARTIAL | Implementation correct; tests check only that the wordmark colour differs from the header, and that the rules exist. Recorded, not changed. |
| US-005 Tasks list | PARTIAL | Web wiring untested; docs overclaimed |
| US-006 seed | PARTIAL | Dates in host zone, not UTC; a login redirect counted as created |
| Critic | FR-1 false | Event form and Accept kept browser buttons; new `.share-item` grid broke `/calendar/shares` at 390 px |

All fixed in Phase 35, except the items in section 8.2 of the PRD.

