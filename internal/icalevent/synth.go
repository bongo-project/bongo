package icalevent

import (
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// SynthVEvent builds the VEVENT for an event that has no stored ical_raw from
// its structured fields: calendar export and iMIP both write it, so the two
// agree on the UID (the event guid), the times and the recurrence.
//
// loc is the zone DTSTART and DTEND are written in; nil means UTC, so no TZID
// is written without a VTIMEZONE to define it. The caller adds that VTIMEZONE.
// An all-day event is written as dates and ignores loc. now becomes DTSTAMP.
func SynthVEvent(e *bongov1.Event, loc *time.Location, now time.Time) *ical.Event {
	vevent := ical.NewEvent()
	vevent.Props.SetText(ical.PropUID, e.Guid)
	vevent.Props.SetText(ical.PropSummary, e.Title)
	if e.Description != "" {
		vevent.Props.SetText(ical.PropDescription, e.Description)
	}
	if e.Location != "" {
		vevent.Props.SetText(ical.PropLocation, e.Location)
	}

	if loc == nil || e.AllDay {
		loc = time.UTC
	}
	if e.AllDay {
		// A date, not an instant: the zone plays no part.
		vevent.Props.SetDate(ical.PropDateTimeStart, AllDayDate(e.StartTime))
		vevent.Props.SetDate(ical.PropDateTimeEnd, AllDayDate(e.EndTime))
	} else {
		vevent.Props.SetDateTime(ical.PropDateTimeStart, time.Unix(e.StartTime, 0).In(loc))
		vevent.Props.SetDateTime(ical.PropDateTimeEnd, time.Unix(e.EndTime, 0).In(loc))
	}
	AddRecurrence(vevent, e, loc)
	vevent.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	SetParticipants(vevent.Props, e)
	return vevent
}
