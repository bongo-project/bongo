package templates

import (
	"encoding/json"
	"strconv"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// AvailabilityView is what the event form's availability fragment shows: one
// row per attendee and the proposed time, as unix seconds inside one day. It
// holds times and labels only, never an event, so no title can reach the page.
type AvailabilityView struct {
	DayStart, DayEnd int64
	// Proposed is the event's own time on the day, clipped to it.
	Proposed [2]int64
	// Zone is the zone the times are shown in.
	Zone *time.Location
	Rows []AvailabilityRow
}

// AvailabilityRow is one attendee. Shared is false for every attendee whose
// availability the viewer may not read, whatever the reason, so such rows are
// identical.
type AvailabilityRow struct {
	Label  string
	Shared bool
	// Blocks are busy [start, end) pairs clipped to the day.
	Blocks [][2]int64
}

// availabilityStripWidth is the width of the SVG strip in user units: one unit
// per 6 minutes of a 24 hour day.
const availabilityStripWidth = 240

// availabilityRange formats a start and end as "09:00–10:30".
func availabilityRange(v *AvailabilityView, s, e int64) string {
	f := func(ts int64) string {
		if ts >= v.DayEnd {
			return "24:00"
		}
		return time.Unix(ts, 0).In(v.Zone).Format("15:04")
	}
	return f(s) + "–" + f(e)
}

// availabilityX maps a time on the day to a strip position.
func availabilityX(v *AvailabilityView, ts int64) int {
	span := v.DayEnd - v.DayStart
	if span <= 0 {
		return 0
	}
	return int((ts - v.DayStart) * availabilityStripWidth / span)
}

// availabilityXs and availabilityWidth are the rect attributes for an interval.
func availabilityXs(v *AvailabilityView, s int64) string {
	return strconv.Itoa(availabilityX(v, s))
}

func availabilityWidth(v *AvailabilityView, s, e int64) string {
	return strconv.Itoa(max(availabilityX(v, e)-availabilityX(v, s), 1))
}

// availabilityConflicts reports whether a busy block overlaps the proposed time.
func availabilityConflicts(v *AvailabilityView, b [2]int64) bool {
	return b[0] < v.Proposed[1] && b[1] > v.Proposed[0]
}

// availabilityVals is the extra request values of the availability container:
// the guid of the event being edited, so the server can leave out the busy
// block the event itself makes.
func availabilityVals(e *bongov1.Event) string {
	if e == nil || e.Guid == "" {
		return "{}"
	}
	b, err := json.Marshal(map[string]string{"event": e.Guid})
	if err != nil {
		return "{}"
	}
	return string(b)
}

func availabilityBlockClass(v *AvailabilityView, b [2]int64) string {
	if availabilityConflicts(v, b) {
		return "availability-busy conflict"
	}
	return "availability-busy"
}
