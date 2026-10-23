package caldav

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// An all-day event with no ical_raw (every web-created one) is written as a
// date taken from its UTC midnight, whatever the server zone or the event's
// Timezone field.
func TestEventToCalendarObjectAllDayIsUTCDate(t *testing.T) {
	for _, zone := range []string{"America/New_York", "Asia/Tokyo", "UTC"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Fatal(err)
			}
			old := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = old })

			e := &bongov1.Event{
				Guid: "g1", Title: "T", AllDay: true, Timezone: zone,
				StartTime: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC).Unix(),
				EndTime:   time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC).Unix(),
			}
			obj, err := eventToCalendarObject("u", "default", e)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := ical.NewEncoder(&buf).Encode(obj.Data); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			for _, want := range []string{"DTSTART;VALUE=DATE:20261105", "DTEND;VALUE=DATE:20261106"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %s:\n%s", want, out)
				}
			}
		})
	}
}
