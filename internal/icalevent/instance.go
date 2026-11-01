package icalevent

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// Helpers for editing one occurrence of a series in its iCalendar object.
// RFC 5545 requires RECURRENCE-ID and EXDATE to have the value type of the
// series' DTSTART, and UNTIL to be a DATE for a DATE series and UTC for a
// series with a TZID.

// IsDateProp reports whether p holds a DATE rather than a DATE-TIME.
func IsDateProp(p *ical.Prop) bool {
	return strings.EqualFold(p.Params.Get(ical.ParamValue), "DATE") || len(strings.TrimSpace(p.Value)) == len("20060102")
}

// PropInstant reads a DATE or DATE-TIME property the way the expansion does: a
// trailing Z is UTC, a TZID that loads is that zone, anything else is read in
// loc, and a date is midnight in loc (callers pass UTC for an all-day series).
func PropInstant(p *ical.Prop, loc *time.Location) (t time.Time, dateOnly, ok bool) {
	if loc == nil {
		loc = time.UTC
	}
	v := strings.TrimSpace(p.Value)
	switch {
	case IsDateProp(p):
		d, err := time.ParseInLocation("20060102", v, loc)
		return d, true, err == nil
	case strings.HasSuffix(v, "Z"):
		d, err := time.ParseInLocation("20060102T150405Z", v, time.UTC)
		return d, false, err == nil
	}
	if l, found := Zone(p.Params.Get(ical.PropTimezoneID)); found {
		loc = l
	}
	d, err := time.ParseInLocation("20060102T150405", v, loc)
	return d, false, err == nil
}

// InSeriesForm returns a property called name that holds t in the value form of
// dtstart, the DTSTART of the series: VALUE=DATE for a date, the TZID zone for a
// zoned series, UTC for a UTC series and for a TZID that is not an IANA name
// (the expansion reads such a series in UTC), and floating for a floating one.
func InSeriesForm(name string, dtstart *ical.Prop, t time.Time) *ical.Prop {
	p := ical.NewProp(name)
	v := strings.TrimSpace(dtstart.Value)
	switch {
	case IsDateProp(dtstart):
		p.SetDate(t.UTC())
	case strings.HasSuffix(v, "Z"):
		p.SetDateTime(t.UTC())
	default:
		tzid := dtstart.Params.Get(ical.PropTimezoneID)
		if tzid == "" {
			// Floating: the wall clock of the zone the expansion reads it in,
			// which is UTC.
			p.SetValueType(ical.ValueDateTime)
			p.Value = t.UTC().Format("20060102T150405")
			return p
		}
		if loc, ok := Zone(tzid); ok {
			p.SetDateTime(t.In(loc))
		} else {
			p.SetDateTime(t.UTC())
		}
	}
	return p
}

// UntilBefore is the UNTIL value that ends a series just before the instance
// starting at slot: the previous date for a DATE series, the same wall clock
// for a floating one, and otherwise a UTC date-time one second earlier.
func UntilBefore(dtstart *ical.Prop, slot time.Time) string {
	v := strings.TrimSpace(dtstart.Value)
	switch {
	case IsDateProp(dtstart):
		return slot.UTC().AddDate(0, 0, -1).Format("20060102")
	case !strings.HasSuffix(v, "Z") && dtstart.Params.Get(ical.PropTimezoneID) == "":
		return slot.UTC().Add(-time.Second).Format("20060102T150405")
	}
	return slot.UTC().Add(-time.Second).Format("20060102T150405Z")
}

// SetUntil returns rule with any UNTIL and COUNT removed and UNTIL=until
// appended; a rule may hold only one of the two. The edit is on the text of
// the rule, not a parse and re-render: rrule-go would normalise the rule and
// write a DATE-TIME UNTIL for a DATE series. until is a value built by
// UntilBefore, never client text.
func SetUntil(rule, until string) string {
	return setRuleParts(rule, "UNTIL="+until)
}

// SetCount returns rule with any UNTIL and COUNT removed and COUNT=n appended.
func SetCount(rule string, n int) string {
	return setRuleParts(rule, "COUNT="+strconv.Itoa(n))
}

// RuleCount returns the COUNT of a recurrence rule, or 0 when it has none.
func RuleCount(rule string) int {
	for _, part := range strings.Split(rule, ";") {
		name, value, _ := strings.Cut(part, "=")
		if strings.EqualFold(strings.TrimSpace(name), "COUNT") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return 0
			}
			return n
		}
	}
	return 0
}

func setRuleParts(rule, add string) string {
	var out []string
	for _, part := range strings.Split(rule, ";") {
		name, _, _ := strings.Cut(part, "=")
		name = strings.ToUpper(strings.TrimSpace(name))
		if part == "" || slices.Contains([]string{"UNTIL", "COUNT"}, name) {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(append(out, add), ";")
}
