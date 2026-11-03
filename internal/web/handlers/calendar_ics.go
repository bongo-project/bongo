package handlers

import (
	"bytes"
	"log/slog"
	"reflect"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// buildCalendarICS assembles one VCALENDAR from events: the raw VEVENTs of
// CalDAV-created events, a synthesised VEVENT for the rest, and each
// VTIMEZONE the kept events reference, once, ahead of them.
//
// transform, when non-nil, is applied to the VEVENTs of one stored object at a
// time (document order; one synthesised VEVENT for an object with no raw) and
// returns the components to write, in order. undecodable is true when the
// object has raw iCalendar that failed to decode, so the VEVENT is synthesised
// from the proto. Only the zones the transformed VEVENTs still reference are
// written. The export and
// the published feed share this path so the feed cannot drift from it.
//
// stamp supplies DTSTAMP for a synthesised VEVENT; nil means the current time.
// The feed passes a stable value so its body, and so its ETag, does not change
// between requests.
func buildCalendarICS(events []*bongov1.Event, transform func(vevents []*ical.Component, undecodable bool) []*ical.Component, stamp func(*bongov1.Event) time.Time) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")

	// The first definition of a TZID wins.
	var zones, vevents []*ical.Component
	seen := map[string]*ical.Component{}
	spans := exportZoneSpans(events)
	stampOf := func(e *bongov1.Event) time.Time {
		if stamp == nil {
			return time.Now()
		}
		return stamp(e)
	}

	for _, e := range events {
		undecodable := false
		if len(e.IcalRaw) > 0 {
			dec := ical.NewDecoder(bytes.NewReader(e.IcalRaw))
			if rawCal, err := dec.Decode(); err == nil {
				defs := map[string]*ical.Component{}
				for _, child := range rawCal.Children {
					if child.Name != ical.CompTimezone {
						continue
					}
					if p := child.Props.Get(ical.PropTimezoneID); p != nil {
						defs[p.Value] = child
					}
				}
				var objVevents []*ical.Component
				for _, child := range rawCal.Children {
					if child.Name == ical.CompEvent {
						objVevents = append(objVevents, child)
					}
				}
				if transform != nil {
					objVevents = transform(objVevents, false)
				}
				for _, child := range objVevents {
					vevents = append(vevents, child)
					for _, tzid := range icalevent.ReferencedTZIDs(child) {
						def, ok := defs[tzid]
						if !ok {
							// Some CalDAV clients omit the VTIMEZONE; there is nothing to copy.
							slog.Default().Debug("calendar export: TZID has no VTIMEZONE", "tzid", tzid)
							continue
						}
						first, dup := seen[tzid]
						switch {
						case !dup:
							seen[tzid] = def
							zones = append(zones, def)
						case !reflect.DeepEqual(first, def):
							slog.Default().Debug("calendar export: conflicting VTIMEZONE, keeping first", "tzid", tzid)
						}
					}
				}
				continue
			}
			undecodable = true
		}

		loc := time.UTC
		// A zoned series keeps its local time across DST only when it is
		// written in its zone, with the VTIMEZONE that defines the TZID.
		span, zoned := spans[e.Timezone]
		zoned = zoned && e.Rrule != "" && !e.AllDay
		if zoned {
			loc = span.loc
		}
		vevent := icalevent.SynthVEvent(e, loc, stampOf(e)).Component
		if transform != nil {
			out := transform([]*ical.Component{vevent}, undecodable)
			if len(out) == 0 {
				continue
			}
			vevent = out[0]
		}
		if zoned {
			if _, dup := seen[loc.String()]; !dup {
				tz := icalevent.VTimezone(loc, span.from, span.to)
				seen[loc.String()] = tz
				zones = append(zones, tz)
			}
		}
		vevents = append(vevents, vevent)
	}
	cal.Children = append(zones, vevents...)
	return cal
}
