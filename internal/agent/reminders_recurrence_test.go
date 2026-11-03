package agent

import (
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func icalSeries(lines ...string) []byte {
	all := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//T//T//EN"}
	all = append(all, lines...)
	all = append(all, "END:VCALENDAR", "")
	return []byte(strings.Join(all, "\r\n"))
}

// A CalDAV client's EXDATE removes that day's reminder, and an override
// moves it, because reminders read the same expansion the views do.
func TestReminderHonoursExdateAndOverride(t *testing.T) {
	a := reminderTestAgent(t)
	series := newMailUser(t, a, "series")
	ch := a.Notifier.Subscribe(series, "INBOX")
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC) // Monday
	if _, err := a.Events.Create(series, &bongov1.Event{
		Title: "Sync", CalendarId: "default", ReminderMinutes: 15, Rrule: "FREQ=WEEKLY;BYDAY=MO",
		StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
		IcalRaw: icalSeries(
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "DTSTART:20300506T090000Z", "DTEND:20300506T100000Z",
			"RRULE:FREQ=WEEKLY;BYDAY=MO", "EXDATE:20300513T090000Z", "END:VEVENT",
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20300520T090000Z",
			"DTSTART:20300520T150000Z", "DTEND:20300520T160000Z", "SUMMARY:Moved", "END:VEVENT"),
	}); err != nil {
		t.Fatal(err)
	}
	at := func(d, h, m int) time.Time { return time.Date(2030, 5, d, h, m, 0, 0, time.UTC) }
	if !fires(t, a, ch, at(6, 8, 50)) {
		t.Error("the first occurrence should remind")
	}
	if fires(t, a, ch, at(13, 8, 50)) {
		t.Error("the excluded day reminded")
	}
	if fires(t, a, ch, at(20, 8, 50)) {
		t.Error("the moved occurrence reminded at its original time")
	}
	if !fires(t, a, ch, at(20, 14, 50)) {
		t.Error("the moved occurrence did not remind at its new time")
	}
}

// Occurrences share the series guid, so the dedupe key must include the start
// or an HOURLY series announces only its first occurrence.
func TestReminderDedupeKeyIncludesOccurrence(t *testing.T) {
	a := reminderTestAgent(t)
	hourly := newMailUser(t, a, "hourly")
	ch := a.Notifier.Subscribe(hourly, "INBOX")
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC)
	if _, err := a.Events.Create(hourly, &bongov1.Event{
		Title: "Stretch", CalendarId: "default", ReminderMinutes: 15, Rrule: "FREQ=HOURLY",
		StartTime: start.Unix(), EndTime: start.Add(10 * time.Minute).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	br := a.newBrowserReminders()
	for i, tick := range []time.Time{
		time.Date(2030, 5, 6, 10, 50, 0, 0, time.UTC),
		time.Date(2030, 5, 6, 11, 50, 0, 0, time.UTC),
	} {
		br.check(tick)
		select {
		case <-ch:
		default:
			t.Fatalf("tick %d: the occurrence was not announced", i)
		}
	}
}

// A reminder is announced once across its whole window, including when the
// ticks cross an hour boundary (the point the old hourly wipe fired), and the
// dedupe map is empty again once the event has started.
func TestBrowserReminderFiresOnceAcrossHour(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "once")
	ch := a.Notifier.Subscribe(uid, "INBOX")
	start := time.Date(2030, 5, 6, 10, 30, 0, 0, time.UTC)
	if _, err := a.Events.Create(uid, &bongov1.Event{
		Title: "Once", CalendarId: "default", ReminderMinutes: 60,
		StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	br := a.newBrowserReminders()
	count := 0
	for i := 0; i <= 60; i++ {
		br.check(start.Add(-60*time.Minute + time.Duration(i)*time.Minute))
		for drained := false; !drained; {
			select {
			case <-ch:
				count++
			default:
				drained = true
			}
		}
	}
	if count != 1 {
		t.Errorf("notifications = %d, want exactly 1", count)
	}
	if n := len(br.sent); n != 0 {
		t.Errorf("sent holds %d entries after the event started, want 0", n)
	}
}

// The checker reads users from the users table (ListUserIDs), not a scan of
// the /user/ key prefix: an owner with events but no users row is not reminded.
func TestBrowserReminderUsesUserList(t *testing.T) {
	a := reminderTestAgent(t)
	known := newMailUser(t, a, "known")
	const orphan = "orphan-no-users-row"
	start := time.Date(2030, 5, 6, 10, 30, 0, 0, time.UTC)
	chKnown := a.Notifier.Subscribe(known, "INBOX")
	chOrphan := a.Notifier.Subscribe(orphan, "INBOX")
	for _, uid := range []string{known, orphan} {
		if _, err := a.Events.Create(uid, &bongov1.Event{
			Title: "E", CalendarId: "default", ReminderMinutes: 15,
			StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !fires(t, a, chKnown, start.Add(-10*time.Minute)) {
		t.Error("listed user was not reminded")
	}
	if fires(t, a, chOrphan, start.Add(-10*time.Minute)) {
		t.Error("user without a users row was reminded")
	}
}
