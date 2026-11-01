package icalevent

import (
	"sort"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ParseExdate reads one entry of Event.Exdates. The canonical forms are an
// RFC 5545 local date-time ("20300513T090000", read in loc) and a date
// ("20300513"); "20300513T090000Z" is an absolute instant and "2006-01-02" is
// the older date spelling. A date excludes every instance that starts on that
// day in loc, which the caller decides by comparing dates.
func ParseExdate(s string, loc *time.Location) (t time.Time, dateOnly, ok bool) {
	if loc == nil {
		loc = time.UTC
	}
	for _, f := range []struct {
		layout string
		date   bool
		utc    bool
	}{
		{"20060102T150405Z", false, true},
		{"20060102T150405", false, false},
		{"20060102", true, false},
		{"2006-01-02", true, false},
	} {
		in := loc
		if f.utc {
			in = time.UTC
		}
		if t, err := time.ParseInLocation(f.layout, s, in); err == nil {
			return t, f.date, true
		}
	}
	return time.Time{}, false, false
}

// validRrule reports whether rule is a well-formed RRULE value. Writers call
// it so a stored value is never copied into an iCalendar object unchecked
// (CR/LF would end the content line).
func validRrule(rule string) bool {
	for _, r := range rule {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	_, err := rrule.StrToROption(rule)
	return err == nil
}

// seriesRule builds the recurrence rule of a web-created series in loc.
func seriesRule(e *bongov1.Event, loc *time.Location) (*rrule.RRule, bool) {
	opt, err := rrule.StrToROption(e.Rrule)
	if err != nil {
		return nil, false
	}
	opt.Dtstart = time.Unix(e.StartTime, 0).In(loc)
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, false
	}
	return r, true
}

// AddRecurrence writes the RRULE and EXDATE properties of a web-created
// series onto vevent. loc is the zone DTSTART is written in (UTC for none);
// an all-day series ignores it. An event without a usable rule is left as it
// is. This covers the structured fallback only: an event with ical_raw
// already carries its own recurrence properties.
func AddRecurrence(vevent *ical.Event, e *bongov1.Event, loc *time.Location) {
	if e.Rrule == "" || !validRrule(e.Rrule) {
		return
	}
	if loc == nil || e.AllDay {
		loc = time.UTC
	}
	rrp := ical.NewProp(ical.PropRecurrenceRule)
	rrp.Value = e.Rrule
	vevent.Props.Set(rrp)

	rule, ok := seriesRule(e, loc)
	if !ok {
		return
	}
	// Collect the excluded instants first: a date-only entry stands for each
	// instance starting that day, so it is resolved against the rule.
	seen := map[int64]bool{}
	var instants []time.Time
	add := func(t time.Time) {
		if !seen[t.Unix()] {
			seen[t.Unix()] = true
			instants = append(instants, t)
		}
	}
	for _, ex := range e.Exdates {
		t, dateOnly, ok := ParseExdate(ex, loc)
		if !ok {
			continue
		}
		if !dateOnly {
			add(t)
			continue
		}
		dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		for _, occ := range rule.Between(dayStart, dayStart.AddDate(0, 0, 1).Add(-time.Second), true) {
			add(occ)
		}
	}
	if len(instants) == 0 {
		return
	}
	sort.Slice(instants, func(i, j int) bool { return instants[i].Before(instants[j]) })
	for _, t := range instants {
		p := ical.NewProp(ical.PropExceptionDates)
		if e.AllDay {
			p.SetDate(t)
		} else {
			p.SetDateTime(t.In(loc))
		}
		vevent.Props.Add(p)
	}
}

// seriesSpan is how far a VTIMEZONE for a series must reach: from a year
// before DTSTART (so the offset in force at DTSTART is defined) to the end of
// the series, or ten years past DTSTART for an open-ended one. A far-off UNTIL
// is capped at maxSeriesSpanYears: sampling is linear in the span, and no client
// reads observances that far ahead. Observances are
// written explicitly, so an open-ended series outlives them; clients then keep
// the last offset. That limitation is accepted over writing a rule.
const maxSeriesSpanYears = 50

func seriesSpan(e *bongov1.Event) (from, to time.Time) {
	start := time.Unix(e.StartTime, 0).UTC()
	from = start.AddDate(-1, 0, 0)
	to = start.AddDate(10, 0, 0)
	if opt, err := rrule.StrToROption(e.Rrule); err == nil && !opt.Until.IsZero() && opt.Until.After(to) {
		to = opt.Until.AddDate(0, 0, 1)
	}
	if limit := start.AddDate(maxSeriesSpanYears, 0, 0); to.After(limit) {
		to = limit
	}
	return from, to
}

// SeriesZone reports the zone a structured event is written in and the span a
// VTIMEZONE for it must cover. ok is false for all-day events, events with no
// IANA zone and UTC: those need no VTIMEZONE.
func SeriesZone(e *bongov1.Event) (loc *time.Location, from, to time.Time, ok bool) {
	if e.AllDay {
		return nil, from, to, false
	}
	l, found := Zone(e.Timezone)
	if !found || l == time.UTC {
		return nil, from, to, false
	}
	from, to = seriesSpan(e)
	return l, from, to, true
}

// VTimezoneFor returns the VTIMEZONE for a structured event written in loc,
// or nil when none is needed (all-day, UTC).
func VTimezoneFor(e *bongov1.Event, loc *time.Location) *ical.Component {
	if e.AllDay || loc == nil || loc == time.UTC {
		return nil
	}
	from, to := seriesSpan(e)
	return VTimezone(loc, from, to)
}

type transition struct {
	at       time.Time
	from, to int
	name     string
	dst      bool
}

// transitions finds every offset change of loc in [from, to). Go exposes no
// transition table, so the offset is sampled every six hours and each change
// is narrowed to the second by bisection.
func transitions(loc *time.Location, from, to time.Time) []transition {
	var out []transition
	step := 6 * time.Hour
	prev := from
	_, prevOff := prev.In(loc).Zone()
	for t := from.Add(step); !t.After(to.Add(step)); t = t.Add(step) {
		_, off := t.In(loc).Zone()
		if off == prevOff {
			prev = t
			continue
		}
		lo, hi := prev, t // offset(lo) == prevOff, offset(hi) != prevOff
		for hi.Sub(lo) > time.Second {
			mid := lo.Add(hi.Sub(lo) / 2)
			if _, o := mid.In(loc).Zone(); o == prevOff {
				lo = mid
			} else {
				hi = mid
			}
		}
		name, newOff := hi.In(loc).Zone()
		out = append(out, transition{at: hi, from: prevOff, to: newOff, name: name, dst: hi.In(loc).IsDST()})
		prev, prevOff = t, off
	}
	return out
}

// VTimezone builds a VTIMEZONE for loc that covers [from, to]. Each offset
// change becomes one STANDARD or DAYLIGHT observance with an explicit DTSTART;
// a zone with no change in the span gets a single STANDARD observance.
func VTimezone(loc *time.Location, from, to time.Time) *ical.Component {
	tz := ical.NewComponent(ical.CompTimezone)
	tz.Props.SetText(ical.PropTimezoneID, loc.String())

	trs := transitions(loc, from, to)
	if len(trs) == 0 {
		name, off := from.In(loc).Zone()
		obs := ical.NewComponent(ical.CompTimezoneStandard)
		obs.Props.SetDateTime(ical.PropDateTimeStart, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC))
		obs.Props.Set(textProp(ical.PropTimezoneOffsetFrom, formatOffset(off)))
		obs.Props.Set(textProp(ical.PropTimezoneOffsetTo, formatOffset(off)))
		obs.Props.Set(textProp(ical.PropTimezoneName, name))
		tz.Children = append(tz.Children, obs)
		return tz
	}
	for _, tr := range trs {
		kind := ical.CompTimezoneStandard
		if tr.dst {
			kind = ical.CompTimezoneDaylight
		}
		obs := ical.NewComponent(kind)
		// The observance starts at the change, in the local time in force
		// just before it (TZOFFSETFROM).
		local := tr.at.UTC().Add(time.Duration(tr.from) * time.Second)
		p := ical.NewProp(ical.PropDateTimeStart)
		p.Value = local.Format("20060102T150405")
		obs.Props.Set(p)
		obs.Props.Set(textProp(ical.PropTimezoneOffsetFrom, formatOffset(tr.from)))
		obs.Props.Set(textProp(ical.PropTimezoneOffsetTo, formatOffset(tr.to)))
		obs.Props.Set(textProp(ical.PropTimezoneName, tr.name))
		tz.Children = append(tz.Children, obs)
	}
	return tz
}

// textProp builds a property whose value is written as is. Offsets and zone
// abbreviations come from the zone database, never from a client.
func textProp(name, value string) *ical.Prop {
	p := ical.NewProp(name)
	p.Value = value
	return p
}
