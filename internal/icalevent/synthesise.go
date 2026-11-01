package icalevent

import (
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Synthesise builds the iCalendar object for an event that has no ical_raw: a
// web-created event. CalDAV serves it for GET, and the instance editor starts
// from it when the first RECURRENCE-ID override has to be stored, so both see
// the same VEVENT. It carries the series recurrence (RRULE and EXDATE) and the
// VTIMEZONE a TZID needs.
//
// The VEVENT comes from SynthVEvent, which export and iMIP use as well; this
// adds the calendar wrapper and the VTIMEZONE for the event's own zone.
func Synthesise(e *bongov1.Event) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")

	loc := ZoneOrUTC(e.Timezone)
	vevent := SynthVEvent(e, loc, time.Now())

	// A TZID needs the VTIMEZONE that defines it.
	if tz := VTimezoneFor(e, loc); tz != nil {
		cal.Children = append(cal.Children, tz)
	}
	cal.Children = append(cal.Children, vevent.Component)
	return cal
}
