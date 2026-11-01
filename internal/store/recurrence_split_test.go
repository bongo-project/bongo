package store

import (
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
