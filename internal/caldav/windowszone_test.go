package caldav

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/store"
)

// An Outlook series with a Windows TZID is stored at the right instant, and
// its instances follow the Berlin wall clock across the DST change.
func TestPutWindowsZoneSeries(t *testing.T) {
	if _, err := time.LoadLocation("Europe/Berlin"); err != nil {
		t.Skip("no zone database")
	}
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	raw := strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Outlook//EN",
		"BEGIN:VEVENT", "UID:win1", "DTSTAMP:20300101T000000Z",
		"DTSTART;TZID=W. Europe Standard Time:20301014T090000",
		"DTEND;TZID=W. Europe Standard Time:20301014T100000",
		"SUMMARY:Weekly", "RRULE:FREQ=WEEKLY", "END:VEVENT", "END:VCALENDAR", "",
	}, "\r\n")
	cal, err := ical.NewDecoder(strings.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PutCalendarObject(ctx, objectPath(userID, "default", "win1"), cal, nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	ev, err := backend.events.Get(userID, "win1")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2030, 10, 14, 7, 0, 0, 0, time.UTC).Unix(); ev.StartTime != want {
		t.Errorf("StartTime = %s, want 2030-10-14T07:00Z", time.Unix(ev.StartTime, 0).UTC())
	}
	if want := time.Date(2030, 10, 14, 8, 0, 0, 0, time.UTC).Unix(); ev.EndTime != want {
		t.Errorf("EndTime = %s, want 2030-10-14T08:00Z", time.Unix(ev.EndTime, 0).UTC())
	}
	from := time.Date(2030, 11, 3, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(2030, 11, 5, 0, 0, 0, 0, time.UTC).Unix()
	occ, err := store.ExpandRecurrence(ev, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(occ) != 1 || occ[0].StartTime != time.Date(2030, 11, 4, 8, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("instances = %v, want one at 2030-11-04T08:00Z", occ)
	}
}
