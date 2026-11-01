package store

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Recurrence expansion is the one place that turns a stored series into
// occurrences, and every view, free/busy and the reminder loop read it through
// ListByDateRange. It follows RFC 5545:
//
//   - the rule is evaluated in the event's TZID, so a 09:00 local series stays
//     at 09:00 across DST changes; an all-day series is evaluated in UTC;
//   - EXDATE and RECURRENCE-ID are matched to instances by instant, never by
//     the UTC date string;
//   - a RECURRENCE-ID override replaces its instance (title, place and times)
//     and is listed where it now falls; an override with STATUS:CANCELLED only
//     removes the instance.
//
// Limitations, deliberately not handled: RDATE, and RECURRENCE-ID with
// RANGE=THISANDFUTURE (the override applies to its own instance only).
//
// Expanded instances keep the series guid; RecurrenceId names the occurrence.

// recurrenceIDRe is the form of an occurrence identifier: a UTC date-time, or
// a date for an all-day series.
var recurrenceIDRe = regexp.MustCompile(`^(\d{8}T\d{6}Z|\d{8})$`)

// override is a RECURRENCE-ID VEVENT of the stored iCalendar.
type override struct {
	cancelled   bool
	start, end  int64
	allDay      bool
	hasEnd      bool
	title       *string
	description *string
	location    *string
}

type recurrenceSet struct {
	event      *bongov1.Event
	rule       *rrule.RRule
	loc        *time.Location
	allDay     bool
	duration   int64
	exInstants map[int64]bool
	exDays     map[string]bool // "20060102" in loc, from date-only EXDATEs on a timed series
	overrides  map[int64]*override
}

// newRecurrenceSet prepares the expansion of a recurring event.
func newRecurrenceSet(event *bongov1.Event) (*recurrenceSet, error) {
	opt, err := rrule.StrToROption(event.Rrule)
	if err != nil {
		return nil, fmt.Errorf("parsing rrule %q: %w", event.Rrule, err)
	}
	s := &recurrenceSet{
		event:      event,
		loc:        time.UTC,
		allDay:     event.AllDay,
		duration:   event.EndTime - event.StartTime,
		exInstants: map[int64]bool{},
		exDays:     map[string]bool{},
		overrides:  map[int64]*override{},
	}
	// An all-day series is a run of dates and takes no zone. A name that does
	// not load (Outlook's "W. Europe Standard Time", a client's custom TZID)
	// falls back to UTC; icalevent.Zone logs it once.
	if !event.AllDay {
		s.loc = icalevent.ZoneOrUTC(event.Timezone)
	}
	opt.Dtstart = time.Unix(event.StartTime, 0).In(s.loc)
	s.rule, err = rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("creating rrule: %w", err)
	}

	for _, ex := range event.Exdates {
		s.addExclusion(ex, "")
	}
	s.readIcal(event.IcalRaw)
	return s, nil
}

// containsFold reports whether b holds sub, ignoring ASCII case, without
// allocating. Property names are case-insensitive in iCalendar.
func containsFold(b []byte, sub string) bool {
	needle := []byte(sub)
	n := len(needle)
	for i := 0; i+n <= len(b); i++ {
		if bytes.EqualFold(b[i:i+n], needle) {
			return true
		}
	}
	return false
}

// readIcal collects EXDATEs and RECURRENCE-ID overrides from ical_raw. The
// reminder loop expands every user's series each minute, so a stored object
// with neither property is not decoded at all.
func (s *recurrenceSet) readIcal(raw []byte) {
	if len(raw) == 0 || (!containsFold(raw, "EXDATE") && !containsFold(raw, "RECURRENCE-ID")) {
		return
	}
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return
	}
	master, _ := icalevent.Master(cal)
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if c == master {
			for _, p := range c.Props[ical.PropExceptionDates] {
				for _, v := range strings.Split(p.Value, ",") {
					s.addExclusion(strings.TrimSpace(v), p.Params.Get(ical.PropTimezoneID))
				}
			}
			continue
		}
		rid := c.Props.Get(ical.PropRecurrenceID)
		if rid == nil {
			continue
		}
		at, _, ok := s.parseTime(rid)
		if !ok {
			continue
		}
		s.overrides[s.key(at)] = s.parseOverride(c)
	}
}

// addExclusion records one EXDATE value. tzid is its TZID parameter, if any.
func (s *recurrenceSet) addExclusion(v, tzid string) {
	if v == "" {
		return
	}
	loc := s.loc
	if l, ok := icalevent.Zone(tzid); ok {
		loc = l
	}
	t, dateOnly, ok := icalevent.ParseExdate(v, loc)
	if !ok {
		return
	}
	switch {
	case dateOnly && s.allDay:
		s.exInstants[time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Unix()] = true
	case dateOnly:
		s.exDays[t.Format("20060102")] = true
	default:
		s.exInstants[t.Unix()] = true
	}
}

// parseTime reads a DATE or DATE-TIME property, honouring a trailing Z and the
// TZID parameter and defaulting to the series zone.
func (s *recurrenceSet) parseTime(p *ical.Prop) (t time.Time, dateOnly, ok bool) {
	loc := s.loc
	switch {
	case strings.EqualFold(p.Params.Get("VALUE"), "DATE") || len(p.Value) == len("20060102"):
		t, err := time.ParseInLocation("20060102", p.Value, time.UTC)
		if err != nil {
			return time.Time{}, false, false
		}
		if !s.allDay {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		}
		return t, true, true
	case strings.HasSuffix(p.Value, "Z"):
		t, err := time.ParseInLocation("20060102T150405Z", p.Value, time.UTC)
		return t, false, err == nil
	}
	if l, found := icalevent.Zone(p.Params.Get(ical.PropTimezoneID)); found {
		loc = l
	}
	t, err := time.ParseInLocation("20060102T150405", p.Value, loc)
	return t, false, err == nil
}

// key is the identity of an instance: its start instant, or for an all-day
// series the UTC midnight of its date.
func (s *recurrenceSet) key(t time.Time) int64 {
	if s.allDay {
		u := t.UTC()
		return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Unix()
	}
	return t.Unix()
}

func (s *recurrenceSet) parseOverride(c *ical.Component) *override {
	o := &override{}
	if st := c.Props.Get(ical.PropStatus); st != nil && strings.EqualFold(strings.TrimSpace(st.Value), "CANCELLED") {
		o.cancelled = true
		return o
	}
	o.start, o.end = s.event.StartTime, s.event.EndTime
	o.allDay = s.allDay
	if p := c.Props.Get(ical.PropDateTimeStart); p != nil {
		if t, dateOnly, ok := s.parseTime(p); ok {
			o.start, o.allDay = t.Unix(), dateOnly
			o.end = o.start + s.duration
		}
	}
	if p := c.Props.Get(ical.PropDateTimeEnd); p != nil {
		if t, _, ok := s.parseTime(p); ok {
			o.end, o.hasEnd = t.Unix(), true
		}
	} else if p := c.Props.Get(ical.PropDuration); p != nil {
		if d, err := p.Duration(); err == nil {
			o.end, o.hasEnd = o.start+int64(d/time.Second), true
		}
	}
	text := func(name string) *string {
		if c.Props.Get(name) == nil {
			return nil
		}
		v, err := c.Props.Text(name)
		if err != nil {
			return nil
		}
		return &v
	}
	o.title = text(ical.PropSummary)
	o.description = text(ical.PropDescription)
	o.location = text(ical.PropLocation)
	return o
}

func (s *recurrenceSet) excluded(occ time.Time) bool {
	if s.exInstants[s.key(occ)] {
		return true
	}
	return !s.allDay && len(s.exDays) > 0 && s.exDays[occ.In(s.loc).Format("20060102")]
}

// recurrenceID formats the identifier of the occurrence whose slot is key.
func (s *recurrenceSet) recurrenceID(key int64) string {
	t := time.Unix(key, 0).UTC()
	if s.allDay {
		return t.Format("20060102")
	}
	return t.Format("20060102T150405Z")
}

func (s *recurrenceSet) instance(key, start, end int64) *bongov1.Event {
	// Clone so every field of the master (organiser, attendees, sequence,
	// status and any added later) reaches the instance; then take the
	// occurrence's own times and identity.
	inst := proto.Clone(s.event).(*bongov1.Event)
	inst.IcalRaw = nil
	inst.Exdates = nil
	inst.StartTime = start
	inst.EndTime = end
	inst.RecurrenceId = s.recurrenceID(key)
	return inst
}

func (s *recurrenceSet) overrideInstance(key int64, o *override) *bongov1.Event {
	end := o.end
	if !o.hasEnd {
		end = o.start + s.duration
	}
	inst := s.instance(key, o.start, end)
	inst.AllDay = o.allDay
	if o.title != nil {
		inst.Title = *o.title
	}
	if o.description != nil {
		inst.Description = *o.description
	}
	if o.location != nil {
		inst.Location = *o.location
	}
	return inst
}

// instances lists the occurrences that overlap [rangeStart, rangeEnd).
func (s *recurrenceSet) instances(rangeStart, rangeEnd int64) []*bongov1.Event {
	from := time.Unix(rangeStart-s.duration, 0).In(s.loc)
	to := time.Unix(rangeEnd, 0).In(s.loc)

	var out []*bongov1.Event
	for _, occ := range s.rule.Between(from, to, true) {
		start := occ.Unix()
		end := start + s.duration
		if start >= rangeEnd || end <= rangeStart {
			continue
		}
		key := s.key(occ)
		if s.excluded(occ) {
			continue
		}
		// The original slot stays empty whether or not the override lands in
		// range; overrides are placed below by their own times.
		if _, moved := s.overrides[key]; moved {
			continue
		}
		out = append(out, s.instance(key, start, end))
	}
	for key, o := range s.overrides {
		if o.cancelled {
			continue
		}
		inst := s.overrideInstance(key, o)
		if inst.StartTime < rangeEnd && inst.EndTime > rangeStart {
			out = append(out, inst)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartTime != out[j].StartTime {
			return out[i].StartTime < out[j].StartTime
		}
		return out[i].RecurrenceId < out[j].RecurrenceId
	})
	return out
}

// find returns the occurrence named by a RECURRENCE-ID value, or false when
// the series has no such occurrence (not on the rule, excluded or cancelled).
func (s *recurrenceSet) find(rid string) (*bongov1.Event, bool) {
	if !recurrenceIDRe.MatchString(rid) {
		return nil, false
	}
	var t time.Time
	if len(rid) == len("20060102") {
		if !s.allDay {
			return nil, false
		}
		d, err := time.ParseInLocation("20060102", rid, time.UTC)
		if err != nil {
			return nil, false
		}
		t = d
	} else {
		if s.allDay {
			return nil, false
		}
		d, err := time.ParseInLocation("20060102T150405Z", rid, time.UTC)
		if err != nil {
			return nil, false
		}
		t = d
	}
	key := s.key(t)
	if o, ok := s.overrides[key]; ok {
		if o.cancelled {
			return nil, false
		}
		return s.overrideInstance(key, o), true
	}
	at := t.In(s.loc)
	if s.excluded(at) || len(s.rule.Between(at, at, true)) == 0 {
		return nil, false
	}
	return s.instance(key, t.Unix(), t.Unix()+s.duration), true
}

// ExpandRecurrence generates the occurrences of a recurring event that
// overlap [rangeStart, rangeEnd). An event without a rule is returned as is.
// Instances keep the series guid and carry their RECURRENCE-ID in
// RecurrenceId.
func ExpandRecurrence(event *bongov1.Event, rangeStart, rangeEnd int64) ([]*bongov1.Event, error) {
	if event.Rrule == "" {
		return []*bongov1.Event{event}, nil
	}
	s, err := newRecurrenceSet(event)
	if err != nil {
		return nil, err
	}
	return s.instances(rangeStart, rangeEnd), nil
}

// FindOccurrence returns the occurrence of a recurring event identified by
// rid (its RECURRENCE-ID: "20300513T090000Z", or "20300513" for an all-day
// series). It reports false for a value that is not an occurrence of the
// series, so a caller can refuse anything else.
func FindOccurrence(event *bongov1.Event, rid string) (*bongov1.Event, bool) {
	if event.Rrule == "" {
		return nil, false
	}
	s, err := newRecurrenceSet(event)
	if err != nil {
		return nil, false
	}
	return s.find(rid)
}
