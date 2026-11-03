// Package icalevent derives the summary Event fields from an iCalendar object.
// CalDAV PUT and calendar import share it so both pick the same VEVENT.
package icalevent

import (
	"errors"
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Master returns the VEVENT that describes the series: the first one without
// RECURRENCE-ID. When every VEVENT is an override (a client may send only the
// exceptions) it returns the override with the earliest RECURRENCE-ID and
// reports fallback=true. Ties and unparsable values keep document order. A nil
// result means the calendar holds no VEVENT.
func Master(cal *ical.Calendar) (master *ical.Component, fallback bool) {
	var best *ical.Component
	var bestAt time.Time
	var bestKnown bool
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		prop := c.Props.Get(ical.PropRecurrenceID)
		if prop == nil {
			return c, false
		}
		at, _, known := PropInstant(prop, time.UTC)
		if !known {
			if start, serr := EventStart(c); serr == nil {
				at, known = start, true
			}
		}
		switch {
		case best == nil:
			best, bestAt, bestKnown = c, at, known
		case known && (!bestKnown || at.Before(bestAt)):
			best, bestAt, bestKnown = c, at, true
		}
	}
	return best, best != nil
}

var errUnreadableTime = errors.New("icalevent: unreadable date or date-time")

// EventStart returns the start of the VEVENT c. It is DTSTART read the way the
// expansion reads it (PropInstant), so a Windows TZID resolves through Zone
// where go-ical would fail in time.LoadLocation. A missing DTSTART gives the
// zero time and no error, as go-ical does; an unreadable one gives an error.
func EventStart(c *ical.Component) (time.Time, error) {
	p := c.Props.Get(ical.PropDateTimeStart)
	if p == nil {
		return time.Time{}, nil
	}
	t, _, ok := PropInstant(p, time.UTC)
	if !ok {
		return time.Time{}, errUnreadableTime
	}
	return t, nil
}

// EventEnd returns the non-inclusive end of the VEVENT c: DTEND, else DTSTART
// plus DURATION, plus a day for a DATE start with neither. A missing DTSTART
// gives the zero time and no error.
func EventEnd(c *ical.Component) (time.Time, error) {
	if p := c.Props.Get(ical.PropDateTimeEnd); p != nil {
		t, _, ok := PropInstant(p, time.UTC)
		if !ok {
			return time.Time{}, errUnreadableTime
		}
		return t, nil
	}
	startProp := c.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		return time.Time{}, nil
	}
	start, dateOnly, ok := PropInstant(startProp, time.UTC)
	if !ok {
		return time.Time{}, errUnreadableTime
	}
	var dur time.Duration
	if durProp := c.Props.Get(ical.PropDuration); durProp != nil {
		d, err := durProp.Duration()
		if err != nil {
			return time.Time{}, err
		}
		dur = d
	} else if dateOnly {
		dur = 24 * time.Hour
	}
	return start.Add(dur), nil
}

// ToEvent copies the summary fields of the master VEVENT into an Event. The
// caller sets the identity (Guid, CalendarId) and the raw data. fallback is
// Master's flag. An empty Event is returned when there is no VEVENT.
func ToEvent(cal *ical.Calendar) (event *bongov1.Event, fallback bool) {
	event = &bongov1.Event{}
	m, fallback := Master(cal)
	if m == nil {
		return event, false
	}
	vevent := &ical.Event{Component: m}

	if summary, err := vevent.Props.Text(ical.PropSummary); err == nil {
		event.Title = summary
	}
	if desc, err := vevent.Props.Text(ical.PropDescription); err == nil {
		event.Description = desc
	}
	if loc, err := vevent.Props.Text(ical.PropLocation); err == nil {
		event.Location = loc
	}

	if dtstart, err := EventStart(m); err == nil {
		event.StartTime = dtstart.Unix()

		// All-day when DTSTART carries VALUE=DATE.
		if prop := vevent.Props.Get(ical.PropDateTimeStart); prop != nil {
			for _, param := range prop.Params["VALUE"] {
				if param == "DATE" {
					event.AllDay = true
				}
			}
		}
	}

	if dtend, err := EventEnd(m); err == nil {
		event.EndTime = dtend.Unix()
	}

	// Timezone from DTSTART TZID parameter.
	if prop := vevent.Props.Get(ical.PropDateTimeStart); prop != nil {
		if tzids := prop.Params["TZID"]; len(tzids) > 0 {
			event.Timezone = tzids[0]
		}
	}

	if rruleProp := vevent.Props.Get(ical.PropRecurrenceRule); rruleProp != nil {
		event.Rrule = rruleProp.Value
	}

	fillParticipants(event, vevent.Props)

	return event, fallback
}

// AllDayDate returns the calendar date (as UTC midnight) of an all-day
// event's stored time. CalDAV stores all-day events at UTC midnight, so the
// date is the UTC date, whatever zone the server runs in.
//
// It rounds to the nearest UTC midnight rather than truncating, so older
// web-created events stored at local midnight keep their date without a
// migration: local midnight in any zone from UTC-11 to UTC+11 is within twelve
// hours of the intended date's UTC midnight. Zones beyond that range can
// still be a day out for such records until they are saved again.
func AllDayDate(unix int64) time.Time {
	return time.Unix(unix, 0).UTC().Add(12 * time.Hour).Truncate(24 * time.Hour)
}
