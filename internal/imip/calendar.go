// Package imip builds iTIP calendar objects and the iMIP mail messages that
// carry them (RFC 5546 and RFC 6047). It has no store or handler dependencies:
// callers decide who may send and how the message is delivered.
//
// The web UI sends REQUEST and CANCEL through it, and answers a received
// invitation with a REPLY. CalDAV implicit scheduling builds on the same
// functions.
package imip

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The iTIP methods this package can build.
const (
	MethodRequest = "REQUEST"
	MethodCancel  = "CANCEL"
	MethodReply   = "REPLY"
)

const prodID = "-//Bongo//Calendar//EN"

// recurrenceIDPattern is the RECURRENCE-ID the expander produces: a UTC basic
// date-time or, for an all-day series, a date.
var recurrenceIDPattern = regexp.MustCompile(`^\d{8}(T\d{6}Z)?$`)

// BuildCalendar returns the VCALENDAR for one iTIP message about e.
//
// ORGANIZER is e.Organizer, which must be a valid address. ATTENDEE lists
// e.Attendees, so a caller can address a CANCEL to a subset by passing a copy
// of the event with only those attendees. SEQUENCE is e.Sequence and DTSTAMP
// is now. CANCEL also writes STATUS:CANCELLED.
//
// An event with ical_raw keeps its stored UID, which is what CalDAV and
// export emit and what a reply is matched on; its VALARMs are dropped because
// an alarm is the sender's own. An event without it is written from its
// structured fields under its guid.
//
// A non-empty recurrenceID makes the VEVENT a single instance: it carries
// RECURRENCE-ID and no RRULE, RDATE or EXDATE. The value is a UTC basic
// date-time ("20300513T090000Z") or a date ("20300513").
func BuildCalendar(method string, e *bongov1.Event, recurrenceID string, now time.Time) (*ical.Calendar, error) {
	if method != MethodRequest && method != MethodCancel && method != MethodReply {
		return nil, fmt.Errorf("imip: unsupported method %q", method)
	}
	if e == nil {
		return nil, errors.New("imip: no event")
	}
	if !icalevent.ValidAddress(e.Organizer) {
		return nil, errors.New("imip: event has no valid organiser")
	}
	if recurrenceID != "" && !recurrenceIDPattern.MatchString(recurrenceID) {
		return nil, fmt.Errorf("imip: invalid recurrence id %q", recurrenceID)
	}

	var vevent *ical.Component
	var zones []*ical.Component
	if len(e.IcalRaw) > 0 {
		raw, err := ical.NewDecoder(bytes.NewReader(e.IcalRaw)).Decode()
		if err != nil {
			return nil, fmt.Errorf("imip: decoding stored event: %w", err)
		}
		vevent, _ = icalevent.Master(raw)
		if vevent == nil {
			return nil, errors.New("imip: stored event has no VEVENT")
		}
		for _, c := range raw.Children {
			if c.Name == ical.CompTimezone {
				zones = append(zones, c)
			}
		}
		prepareRaw(vevent, e)
	} else {
		if e.Guid == "" {
			return nil, errors.New("imip: event has no identity")
		}
		var loc *time.Location
		if l, _, _, ok := icalevent.SeriesZone(e); ok {
			loc = l
			if tz := icalevent.VTimezoneFor(e, l); tz != nil {
				zones = append(zones, tz)
			}
		}
		vevent = icalevent.SynthVEvent(e, loc, now).Component
	}

	props := vevent.Props
	props.Set(icalevent.OrganizerProp(e.Organizer))
	seq := ical.NewProp(ical.PropSequence)
	seq.Value = strconv.Itoa(int(max(e.Sequence, 0)))
	props.Set(seq)
	props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	if method == MethodCancel {
		props.SetText(ical.PropStatus, "CANCELLED")
	}
	if recurrenceID != "" {
		setRecurrenceID(props, recurrenceID)
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, prodID)
	cal.Props.SetText(ical.PropMethod, method)
	cal.Children = append(cal.Children, zones...)
	cal.Children = append(cal.Children, vevent)
	return cal, nil
}

// prepareRaw adjusts a stored master VEVENT in place for sending: alarms go
// and the ATTENDEE list is brought to e.Attendees. Every attendee property
// that stays keeps its parameters.
func prepareRaw(vevent *ical.Component, e *bongov1.Event) {
	StripAlarms(vevent)

	props := vevent.Props
	want := make(map[string]*bongov1.Attendee, len(e.Attendees))
	for i, a := range e.Attendees {
		if i >= icalevent.MaxAttendees || !icalevent.ValidAddress(a.Address) {
			continue
		}
		want[strings.ToLower(a.Address)] = a
	}
	var out []ical.Prop
	for _, p := range props[ical.PropAttendee] {
		addr, ok := icalevent.AddressFromCalAddress(p.Value)
		if !ok {
			continue
		}
		key := strings.ToLower(addr)
		if _, listed := want[key]; listed {
			out = append(out, p)
			delete(want, key)
		}
	}
	// Whatever is still wanted was not in the stored VEVENT.
	for _, a := range e.Attendees {
		if left, ok := want[strings.ToLower(a.Address)]; ok && left == a {
			out = append(out, *icalevent.AttendeeProp(a))
		}
	}
	if len(out) == 0 {
		props.Del(ical.PropAttendee)
	} else {
		props[ical.PropAttendee] = out
	}
}

// setRecurrenceID turns a VEVENT into the single instance at id.
func setRecurrenceID(props ical.Props, id string) {
	for _, name := range []string{ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates} {
		props.Del(name)
	}
	p := ical.NewProp(ical.PropRecurrenceID)
	p.Value = id
	if !strings.Contains(id, "T") {
		p.Params.Set(ical.ParamValue, "DATE")
	}
	props.Set(p)
}
