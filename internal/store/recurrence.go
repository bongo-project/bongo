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
	// partstats maps a normalised attendee address to the PARTSTAT the
	// override's ATTENDEE lines carry, so an answer to one occurrence reaches
	// the expanded instance.
	partstats map[string]string
	// attendees is the override's own ATTENDEE list and sequence its own
	// SEQUENCE. Only Series reads them; the expansion keeps the master's list.
	attendees []*bongov1.Attendee
	sequence  int
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
	// todo marks a series built from a VTODO (see todoSet): ical_raw is read
	// for VTODO components, and an override with STATUS:COMPLETED drops its
	// occurrence like a cancelled one.
	todo bool
	// dueAnchor is set when the series has no DTSTART and recurs on DUE.
	dueAnchor bool
}

// newRecurrenceSet prepares the expansion of a recurring event.
func newRecurrenceSet(event *bongov1.Event) (*recurrenceSet, error) {
	return newRecurrenceSetFor(event, false, false)
}

// newRecurrenceSetFor is newRecurrenceSet for an event or, with todo set, the
// shim of a VTODO (see todoSet); dueAnchor says the series recurs on DUE.
func newRecurrenceSetFor(event *bongov1.Event, todo, dueAnchor bool) (*recurrenceSet, error) {
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
		todo:       todo,
		dueAnchor:  dueAnchor,
	}
	// An all-day series is a run of dates and takes no zone. A name that does
	// not load (a client's custom TZID) falls back to UTC; icalevent.Zone logs
	// it once. A Windows name such as "W. Europe Standard Time" loads through
	// Zone's CLDR table.
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
	name, master := ical.CompEvent, (*ical.Component)(nil)
	if s.todo {
		name, master = ical.CompToDo, icalevent.TodoMaster(cal)
	} else {
		master, _ = icalevent.Master(cal)
	}
	for _, c := range cal.Children {
		if c.Name != name {
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
		s.overrides[s.key(at)] = s.parseOverride(c, s.key(at))
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

// parseOverride reads one RECURRENCE-ID component; slot is the key of the
// instance it replaces.
func (s *recurrenceSet) parseOverride(c *ical.Component, slot int64) *override {
	o := &override{}
	if st := c.Props.Get(ical.PropStatus); st != nil {
		v := strings.TrimSpace(st.Value)
		// Clients write COMPLETED on a VTODO override when one occurrence is
		// ticked off; it leaves the open views.
		if strings.EqualFold(v, "CANCELLED") || (s.todo && strings.EqualFold(v, "COMPLETED")) {
			o.cancelled = true
			return o
		}
	}
	o.attendees = icalevent.ParseAttendees(c.Props)
	o.sequence = icalevent.Sequence(c.Props)
	o.start, o.end = s.event.StartTime, s.event.EndTime
	if s.todo {
		// An override without a date stays in its own slot.
		o.start = slot
		o.end = slot + s.duration
	}
	o.allDay = s.allDay
	if p := c.Props.Get(ical.PropDateTimeStart); p != nil {
		if t, dateOnly, ok := s.parseTime(p); ok {
			o.start, o.allDay = t.Unix(), dateOnly
			o.end = o.start + s.duration
		}
	}
	if p := c.Props.Get(ical.PropDue); s.todo && p != nil {
		if t, dateOnly, ok := s.parseTime(p); ok {
			if s.dueAnchor {
				o.start, o.allDay = t.Unix(), dateOnly
				o.end = o.start
			}
			o.end, o.hasEnd = t.Unix(), true
		}
	} else if p := c.Props.Get(ical.PropDateTimeEnd); p != nil {
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
	for _, a := range c.Props.Values(ical.PropAttendee) {
		ps := a.Params.Get(ical.ParamParticipationStatus)
		addr := strings.TrimSpace(a.Value)
		if len(addr) >= 7 && strings.EqualFold(addr[:7], "mailto:") {
			addr = addr[7:]
		}
		addr = NormaliseAddress(addr)
		if ps == "" || addr == "" {
			continue
		}
		if o.partstats == nil {
			o.partstats = map[string]string{}
		}
		o.partstats[addr] = strings.ToUpper(ps)
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
	// The master's attendees carry the series answer; an override that holds
	// its own PARTSTAT for an attendee replaces it for this occurrence only.
	for _, a := range inst.Attendees {
		if ps, ok := o.partstats[NormaliseAddress(a.Address)]; ok {
			a.Partstat = ps
		}
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
	t, ok := s.parseRecurrenceID(rid)
	if !ok {
		return nil, false
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

// parseRecurrenceID reads a canonical RECURRENCE-ID value: a UTC date-time for
// a timed series, a date for an all-day one.
func (s *recurrenceSet) parseRecurrenceID(rid string) (time.Time, bool) {
	if !recurrenceIDRe.MatchString(rid) {
		return time.Time{}, false
	}
	if len(rid) == len("20060102") {
		if !s.allDay {
			return time.Time{}, false
		}
		d, err := time.ParseInLocation("20060102", rid, time.UTC)
		return d, err == nil
	}
	if s.allDay {
		return time.Time{}, false
	}
	d, err := time.ParseInLocation("20060102T150405Z", rid, time.UTC)
	return d, err == nil
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

// SeriesEndsBy reports whether a recurring event is bounded (UNTIL or COUNT)
// and nothing of it ends after t. It answers false on any doubt: no rule, an
// open-ended rule, a rule that does not parse, or an override that is not
// cancelled and ends after t. EXDATEs are ignored, which errs towards
// "not past".
func SeriesEndsBy(event *bongov1.Event, t time.Time) bool {
	if event == nil || event.Rrule == "" {
		return false
	}
	opt, err := rrule.StrToROption(event.Rrule)
	if err != nil || (opt.Until.IsZero() && opt.Count == 0) {
		return false
	}
	s, err := newRecurrenceSet(event)
	if err != nil {
		return false
	}
	if !s.rule.After(t.Add(-time.Duration(s.duration)*time.Second), false).IsZero() {
		return false
	}
	for _, o := range s.overrides {
		if o.cancelled {
			continue
		}
		end := o.end
		if !o.hasEnd {
			end = o.start + s.duration
		}
		if end > t.Unix() {
			return false
		}
	}
	return true
}

// EventEntirelyPast reports whether nothing of event ends after now: a single
// event or an expanded occurrence whose end (its start when it has none) is
// before now, or a bounded series by SeriesEndsBy. An open-ended series never
// is, and nor is a nil event.
//
// All-day events are stored as UTC-midnight dates with an exclusive end, so
// they are judged by the wall clock of loc (nil means UTC): one dated
// yesterday in loc is past from local midnight. Timed events are instants and
// ignore loc. RDATE is not modelled (see the top of this file) and is not
// looked at.
func EventEntirelyPast(event *bongov1.Event, now time.Time, loc *time.Location) bool {
	if event == nil {
		return false
	}
	if !event.AllDay {
		// An expanded occurrence keeps the master's RRULE but is one slot.
		if event.Rrule != "" && event.RecurrenceId == "" {
			return SeriesEndsBy(event, now)
		}
		return max(event.EndTime, event.StartTime) < now.Unix()
	}
	if loc == nil {
		loc = time.UTC
	}
	w := now.In(loc)
	floating := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, time.UTC)
	if event.Rrule != "" && event.RecurrenceId == "" {
		return SeriesEndsBy(event, floating)
	}
	end := event.EndTime
	if end <= event.StartTime {
		end = event.StartTime + 86400
	}
	// The end is exclusive: the event is past at the midnight that ends it.
	return end <= floating.Unix()
}

// todoSeriesCap bounds the slots NextTodoOccurrence walks from DTSTART, so a
// rule that recurs every second (a shared task is written by another user)
// ends the search instead of spinning.
const todoSeriesCap = 200000

// todoSet holds a recurring VTODO as a recurrence set. The set expands a
// shim Event whose times are the task's anchor (DTSTART, else DUE) and whose
// duration is DUE minus the anchor; mapBack turns an instance into a Task.
type todoSet struct {
	*recurrenceSet
	task   *bongov1.Task
	hasDue bool
}

// newTodoSet returns nil for a task that does not recur, has no usable
// anchor, or whose rule does not parse.
func newTodoSet(task *bongov1.Task) *todoSet {
	if task == nil || len(task.IcalRaw) == 0 || !containsFold(task.IcalRaw, "RRULE") {
		return nil
	}
	cal, err := ical.NewDecoder(bytes.NewReader(task.IcalRaw)).Decode()
	if err != nil {
		return nil
	}
	m := icalevent.TodoMaster(cal)
	if m == nil {
		return nil
	}
	rr := m.Props.Get(ical.PropRecurrenceRule)
	if rr == nil {
		return nil
	}
	dtstart, due := m.Props.Get(ical.PropDateTimeStart), m.Props.Get(ical.PropDue)
	anchor := dtstart
	if anchor == nil {
		anchor = due
	}
	if anchor == nil {
		return nil
	}
	at, dateOnly, ok := icalevent.PropInstant(anchor, time.UTC)
	if !ok {
		return nil
	}
	ev := &bongov1.Event{
		Guid: task.Guid, CalendarId: task.CalendarId, Title: task.Summary,
		Description: task.Description,
		StartTime:   at.Unix(), EndTime: at.Unix(),
		AllDay: dateOnly, Rrule: strings.TrimSpace(rr.Value),
		Timezone: anchor.Params.Get(ical.PropTimezoneID),
		IcalRaw:  task.IcalRaw,
	}
	hasDue := due != nil
	if hasDue && anchor != due {
		if d, _, ok := icalevent.PropInstant(due, time.UTC); ok && d.Unix() > ev.StartTime {
			ev.EndTime = d.Unix()
		}
	}
	s, err := newRecurrenceSetFor(ev, true, anchor == due)
	if err != nil {
		return nil
	}
	return &todoSet{recurrenceSet: s, task: task, hasDue: hasDue}
}

// mapBack turns an instance of the shim event into a copy of the task. The
// stored task is never touched.
func (t *todoSet) mapBack(inst *bongov1.Event) *bongov1.Task {
	out := proto.Clone(t.task).(*bongov1.Task)
	out.Summary = inst.Title
	out.Description = inst.Description
	if t.hasDue {
		out.Due = inst.EndTime
	}
	if !t.dueAnchor && t.task.Start != 0 {
		out.Start = inst.StartTime
	}
	return out
}

// ExpandTodo lists the occurrences of a recurring VTODO whose due instant lies
// in [rangeStart, rangeEnd), as copies of the task with Due (and Start, when
// the task has one) moved to the occurrence and an override's SUMMARY applied.
// EXDATE, RECURRENCE-ID overrides and a COMPLETED or CANCELLED override are
// honoured. A task that does not recur is returned as is. RDATE and
// THISANDFUTURE are not handled.
func ExpandTodo(task *bongov1.Task, rangeStart, rangeEnd int64) ([]*bongov1.Task, error) {
	s := newTodoSet(task)
	if s == nil {
		return []*bongov1.Task{task}, nil
	}
	// A zero-duration instance at rangeStart would otherwise be dropped.
	insts := s.instances(rangeStart-1, rangeEnd)
	out := make([]*bongov1.Task, 0, len(insts))
	for _, inst := range insts {
		if inst.EndTime < rangeStart || inst.EndTime >= rangeEnd {
			continue
		}
		out = append(out, s.mapBack(inst))
	}
	return out, nil
}

// NextTodoOccurrence finds the first occurrence of a recurring VTODO whose due
// instant is not before notBefore, and reports true. When the series has none
// (it ended), it returns the last occurrence and false. It returns nil and
// false for a task that does not recur, or whose series cannot be walked
// within todoSeriesCap slots.
func NextTodoOccurrence(task *bongov1.Task, notBefore time.Time) (*bongov1.Task, bool) {
	s := newTodoSet(task)
	if s == nil {
		return nil, false
	}
	nb := notBefore.Unix()
	var next, last *bongov1.Event
	iter := s.rule.Iterator()
	walked := 0
	for {
		occ, ok := iter()
		if !ok {
			break
		}
		if walked++; walked > todoSeriesCap {
			return nil, false
		}
		start := occ.Unix()
		end := start + s.duration
		if s.excluded(occ) {
			continue
		}
		key := s.key(occ)
		if _, moved := s.overrides[key]; moved {
			continue
		}
		if end < nb {
			last = s.instance(key, start, end)
			continue
		}
		next = s.instance(key, start, end)
		break
	}
	for key, o := range s.overrides {
		if o.cancelled {
			continue
		}
		inst := s.overrideInstance(key, o)
		switch {
		case inst.EndTime >= nb && (next == nil || inst.EndTime < next.EndTime):
			next = inst
		case inst.EndTime < nb && (last == nil || inst.EndTime > last.EndTime):
			last = inst
		}
	}
	switch {
	case next != nil:
		return s.mapBack(next), true
	case last != nil:
		return s.mapBack(last), false
	}
	return nil, false
}
