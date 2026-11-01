package agent

import (
	"strings"
	"sync"
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
	ch := a.Notifier.Subscribe("series", "INBOX")
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC) // Monday
	if _, err := a.Events.Create("series", &bongov1.Event{
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
	ch := a.Notifier.Subscribe("hourly", "INBOX")
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC)
	if _, err := a.Events.Create("hourly", &bongov1.Event{
		Title: "Stretch", CalendarId: "default", ReminderMinutes: 15, Rrule: "FREQ=HOURLY",
		StartTime: start.Unix(), EndTime: start.Add(10 * time.Minute).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	sent := map[string]bool{}
	for i, tick := range []time.Time{
		time.Date(2030, 5, 6, 10, 50, 0, 0, time.UTC),
		time.Date(2030, 5, 6, 11, 50, 0, 0, time.UTC),
	} {
		a.checkReminders(tick, &mu, sent)
		select {
		case <-ch:
		default:
			t.Fatalf("tick %d: the occurrence was not announced", i)
		}
	}
}
