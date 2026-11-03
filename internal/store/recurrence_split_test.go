package store

import (
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestInstancesBefore(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Skip("Europe/Amsterdam unavailable")
	}
	start := time.Date(2030, 10, 14, 9, 0, 0, 0, ams)
	timed := &bongov1.Event{
		Guid: "s", StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
		Rrule: "FREQ=WEEKLY;COUNT=6", Timezone: "Europe/Amsterdam",
		// An excluded instance still counts towards COUNT.
		Exdates: []string{"20301021T090000"},
	}
	allDay := &bongov1.Event{
		Guid: "d", AllDay: true, StartTime: time.Date(2030, 10, 14, 0, 0, 0, 0, time.UTC).Unix(),
		EndTime: time.Date(2030, 10, 15, 0, 0, 0, 0, time.UTC).Unix(), Rrule: "FREQ=DAILY",
	}
	for _, tc := range []struct {
		name string
		e    *bongov1.Event
		slot time.Time
		want int
	}{
		{"first instance", timed, start, 0},
		{"before the first", timed, start.Add(-time.Hour), 0},
		{"second", timed, start.AddDate(0, 0, 7), 1},
		{"after DST change", timed, time.Date(2030, 11, 4, 9, 0, 0, 0, ams), 3},
		{"last of COUNT", timed, time.Date(2030, 11, 18, 9, 0, 0, 0, ams), 5},
		{"all-day", allDay, time.Date(2030, 10, 20, 0, 0, 0, 0, time.UTC), 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := InstancesBefore(tc.e, tc.slot)
			if err != nil || got != tc.want {
				t.Errorf("InstancesBefore = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
	if _, err := InstancesBefore(&bongov1.Event{}, start); err == nil {
		t.Error("an event without a rule must be an error")
	}
}

func TestVisibleInstancesBefore(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Skip("Europe/Amsterdam unavailable")
	}
	const rule = "FREQ=WEEKLY;COUNT=5"
	start := time.Date(2030, 10, 14, 9, 0, 0, 0, ams)
	slot2, slot3 := start.AddDate(0, 0, 7), start.AddDate(0, 0, 14)

	timed := func(exdates []string) *bongov1.Event {
		return &bongov1.Event{
			Guid: "s", StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
			Rrule: rule, Timezone: "Europe/Amsterdam", Exdates: exdates,
		}
	}
	// withRaw stores an iCalendar object: masterExtra lines inside the master
	// VEVENT, then each further component (an override) as given.
	withRaw := func(masterExtra string, components ...string) *bongov1.Event {
		e := timed(nil)
		lines := []string{
			"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//T//T//EN",
			"BEGIN:VEVENT", "UID:s", "DTSTAMP:20300101T000000Z",
			"DTSTART;TZID=Europe/Amsterdam:20301014T090000", "DTEND;TZID=Europe/Amsterdam:20301014T100000",
			"RRULE:" + rule,
		}
		if masterExtra != "" {
			lines = append(lines, masterExtra)
		}
		lines = append(lines, "END:VEVENT")
		lines = append(lines, components...)
		lines = append(lines, "END:VCALENDAR", "")
		e.IcalRaw = []byte(strings.Join(lines, "\r\n"))
		return e
	}
	override := func(extra ...string) string {
		lines := []string{
			"BEGIN:VEVENT", "UID:s", "DTSTAMP:20300101T000000Z",
			"RECURRENCE-ID;TZID=Europe/Amsterdam:20301014T090000",
			"DTSTART;TZID=Europe/Amsterdam:20301014T090000", "DTEND;TZID=Europe/Amsterdam:20301014T100000",
		}
		return strings.Join(append(lines, append(extra, "END:VEVENT")...), "\r\n")
	}
	moved := override("SUMMARY:Moved")
	moved = strings.Replace(moved, "DTSTART;TZID=Europe/Amsterdam:20301014T090000", "DTSTART;TZID=Europe/Amsterdam:20301014T120000", 1)
	allDay := &bongov1.Event{
		Guid: "d", AllDay: true, StartTime: time.Date(2030, 10, 14, 0, 0, 0, 0, time.UTC).Unix(),
		EndTime: time.Date(2030, 10, 15, 0, 0, 0, 0, time.UTC).Unix(), Rrule: rule,
		Exdates: []string{"20301014"},
	}

	for _, tc := range []struct {
		name string
		e    *bongov1.Event
		slot time.Time
		want int
	}{
		{"nothing excluded", timed(nil), slot2, 1},
		{"structured EXDATE on the first", timed([]string{"20301014T090000"}), slot2, 0},
		{"structured EXDATE on the second, split at the third", timed([]string{"20301021T090000"}), slot3, 1},
		{"date-valued EXDATE on a timed series", timed([]string{"20301014"}), slot2, 0},
		{"ical_raw EXDATE", withRaw("EXDATE;TZID=Europe/Amsterdam:20301014T090000"), slot2, 0},
		{"ical_raw date-valued EXDATE", withRaw("EXDATE;VALUE=DATE:20301014"), slot2, 0},
		{"cancelled override", withRaw("", override("STATUS:CANCELLED")), slot2, 0},
		{"moved override still shows", withRaw("", moved), slot2, 1},
		{"all-day series", allDay, time.Date(2030, 10, 21, 0, 0, 0, 0, time.UTC), 0},
		{"all-day series, later split", allDay, time.Date(2030, 10, 28, 0, 0, 0, 0, time.UTC), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := VisibleInstancesBefore(tc.e, tc.slot)
			if err != nil || got != tc.want {
				t.Errorf("VisibleInstancesBefore = %d, %v; want %d", got, err, tc.want)
			}
			// The bare count never moves with exclusions.
			if bare, _ := InstancesBefore(tc.e, tc.slot); bare < got {
				t.Errorf("bare count %d below visible %d", bare, got)
			}
		})
	}
	if _, err := VisibleInstancesBefore(&bongov1.Event{}, start); err == nil {
		t.Error("an event without a rule must be an error")
	}
}
