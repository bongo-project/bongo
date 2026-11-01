package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Editing and deleting one occurrence of a series, or an occurrence and every
// one after it. The event form and the delete button send the occurrence (its
// RECURRENCE-ID) in "occurrence" and the choice in "scope":
//
//   - "this": an edit writes a RECURRENCE-ID override into the series'
//     ical_raw, a delete adds an EXDATE (and drops any override). One resource,
//     one Raft PUT.
//   - "following": the original series ends with an UNTIL before the chosen
//     instance and an edit starts a new series, with a new UID, from the form.
//     See editFollowing for the two writes and their compensation.
//   - "all" (the default, and the only scope without an occurrence): the
//     series-level paths in calendar.go, unchanged.
//
// Every write is a plain EventStore.Update or Create (a PUT); there is no
// composite Raft command and no proto change, so a mixed-version cluster can
// run it. Nodes older than the recurrence expansion ignore overrides and
// EXDATE kept in ical_raw, as they already do for CalDAV clients.
//
// Only a RECURRENCE-ID that store.FindOccurrence resolved (and so matched
// against its fixed format) is ever written into ical_raw, never the request
// value, so a client cannot inject properties through it.
//
// The functions take the userID that owns the event, not the caller, so a
// caller with a grant on someone else's calendar passes the owner.

const (
	scopeThis      = "this"
	scopeFollowing = "following"
	scopeAll       = "all"
)

// parseScope reads the scope of an instance edit or delete. An absent value
// is "all"; anything outside the fixed set is refused.
func parseScope(r *http.Request) (string, bool) {
	switch v := r.FormValue("scope"); v {
	case "":
		return scopeAll, true
	case scopeThis, scopeFollowing, scopeAll:
		return v, true
	}
	return "", false
}

// InstanceChange is what an instance edit or delete did, so a caller can act
// on it (the organiser sends iTIP for the one instance that changed).
type InstanceChange struct {
	Scope string
	// Guid is the series that was edited or truncated.
	Guid string
	// RecurrenceID is the canonical RECURRENCE-ID of the instance: the one
	// override written or excluded ("this"), or the first instance of the part
	// that was split off ("following").
	RecurrenceID string
	// NewGuid is the series an edit of "this and following" created.
	NewGuid string
	// Deleted reports a delete rather than an edit.
	Deleted bool
	// Before is the series as it was stored; After as it is stored now (nil
	// when the series was deleted).
	Before, After *bongov1.Event
	// NewSeries is the created series of an edit of "this and following".
	NewSeries *bongov1.Event
	// Instance is the edited occurrence after the change ("this" edit only).
	Instance *bongov1.Event
}

// instanceError carries the HTTP status an instance operation ends with.
type instanceError struct {
	status int
	msg    string
}

func (e *instanceError) Error() string { return e.msg }

func newInstanceError(status int, msg string) *instanceError {
	return &instanceError{status: status, msg: msg}
}

func writeInstanceError(w http.ResponseWriter, r *http.Request, err error) {
	var ie *instanceError
	if !errors.As(err, &ie) {
		http.Error(w, "failed to update event", http.StatusInternalServerError)
		return
	}
	if ie.status == http.StatusInsufficientStorage {
		refuseQuota(w, r)
		return
	}
	http.Error(w, ie.msg, ie.status)
}

// resolveSlot checks that rid names a real occurrence of the series and
// returns its canonical RECURRENCE-ID and the instant of its slot on the rule.
func resolveSlot(existing *bongov1.Event, rid string) (canonical string, slot time.Time, err error) {
	occ, ok := store.FindOccurrence(existing, rid)
	if !ok {
		return "", time.Time{}, newInstanceError(http.StatusNotFound, "event not found")
	}
	canonical = occ.RecurrenceId
	if len(canonical) == len("20060102") {
		slot, err = time.ParseInLocation("20060102", canonical, time.UTC)
	} else {
		slot, err = time.ParseInLocation("20060102T150405Z", canonical, time.UTC)
	}
	if err != nil {
		return "", time.Time{}, newInstanceError(http.StatusNotFound, "event not found")
	}
	return canonical, slot, nil
}

// --- the series as an iCalendar object -------------------------------------

// seriesCal is a decoded series: the calendar, its master VEVENT and the zone
// the expansion evaluates it in.
type seriesCal struct {
	cal     *ical.Calendar
	master  *ical.Component
	dtstart *ical.Prop
	loc     *time.Location
	allDay  bool
}

func newSeriesCal(cal *ical.Calendar, e *bongov1.Event) (*seriesCal, error) {
	master, _ := icalevent.Master(cal)
	if master == nil || master.Props.Get(ical.PropRecurrenceID) != nil {
		return nil, errors.New("series has no master VEVENT")
	}
	dt := master.Props.Get(ical.PropDateTimeStart)
	if dt == nil {
		return nil, errors.New("series master has no DTSTART")
	}
	s := &seriesCal{cal: cal, master: master, dtstart: dt, loc: time.UTC, allDay: e.AllDay}
	if !e.AllDay {
		s.loc = icalevent.ZoneOrUTC(e.Timezone)
	}
	return s, nil
}

// decodeSeries opens the stored ical_raw of e.
func decodeSeries(e *bongov1.Event) (*seriesCal, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(e.IcalRaw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errICalDecode, err)
	}
	return newSeriesCal(cal, e)
}

// seriesForEdit opens the object an override is written into: the stored
// ical_raw, or for a web-created series (or a raw that does not decode, which
// the expansion ignores too) the object CalDAV would serve for it. After the
// first override such a series is raw-backed.
func seriesForEdit(e *bongov1.Event) (*seriesCal, error) {
	if len(e.IcalRaw) > 0 {
		s, err := decodeSeries(e)
		if err == nil {
			return s, nil
		}
		slog.Warn("calendar: stored ical_raw is unusable, rebuilding it from the stored fields", "guid", e.Guid, "error", err)
	}
	return newSeriesCal(icalevent.Synthesise(e), e)
}

func (s *seriesCal) encode() ([]byte, error) {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(s.cal); err != nil {
		return nil, fmt.Errorf("encoding ical: %w", err)
	}
	return buf.Bytes(), nil
}

// key is the identity of an instance, as the expansion computes it.
func (s *seriesCal) key(t time.Time) int64 {
	if s.allDay {
		u := t.UTC()
		return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Unix()
	}
	return t.Unix()
}

// atOrAfter reports whether a value read from an EXDATE or RECURRENCE-ID lies
// at or after slot. A date on a timed series stands for the whole day, which
// is after the split only when the day itself is.
func (s *seriesCal) atOrAfter(t time.Time, dateOnly bool, slot time.Time) bool {
	if dateOnly && !s.allDay {
		return t.In(s.loc).Format("20060102") > slot.In(s.loc).Format("20060102")
	}
	return s.key(t) >= s.key(slot)
}

// overrideComponents lists the indexes of the RECURRENCE-ID VEVENTs.
func (s *seriesCal) overrideAt(slot time.Time) (int, *ical.Component) {
	for i, c := range s.cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if p := c.Props.Get(ical.PropRecurrenceID); p != nil {
			if t, _, ok := icalevent.PropInstant(p, s.loc); ok && s.key(t) == s.key(slot) {
				return i, c
			}
		}
	}
	return -1, nil
}

// dropOverrides removes the override VEVENTs for which drop is true.
func (s *seriesCal) dropOverrides(drop func(t time.Time, dateOnly bool) bool) {
	s.cal.Children = slices.DeleteFunc(s.cal.Children, func(c *ical.Component) bool {
		if c.Name != ical.CompEvent {
			return false
		}
		p := c.Props.Get(ical.PropRecurrenceID)
		if p == nil {
			return false
		}
		t, dateOnly, ok := icalevent.PropInstant(p, s.loc)
		return ok && drop(t, dateOnly)
	})
}

type exdate struct {
	t        time.Time
	dateOnly bool
}

// dropExdates removes the EXDATE values of the master for which drop is true
// and returns them. A value that cannot be read is kept.
func (s *seriesCal) dropExdates(drop func(exdate) bool) (dropped []exdate) {
	props := s.master.Props[ical.PropExceptionDates]
	if len(props) == 0 {
		return nil
	}
	var kept []ical.Prop
	for _, p := range props {
		var vals []string
		for _, v := range strings.Split(p.Value, ",") {
			if v = strings.TrimSpace(v); v == "" {
				continue
			}
			probe := ical.Prop{Name: p.Name, Params: p.Params, Value: v}
			t, dateOnly, ok := icalevent.PropInstant(&probe, s.loc)
			if ok && drop(exdate{t, dateOnly}) {
				dropped = append(dropped, exdate{t, dateOnly})
				continue
			}
			vals = append(vals, v)
		}
		if len(vals) > 0 {
			p.Value = strings.Join(vals, ",")
			kept = append(kept, p)
		}
	}
	s.master.Props.Del(ical.PropExceptionDates)
	for i := range kept {
		s.master.Props.Add(&kept[i])
	}
	return dropped
}

func (s *seriesCal) setRule(rule string) {
	if rule == "" {
		s.master.Props.Del(ical.PropRecurrenceRule)
		return
	}
	p := ical.NewProp(ical.PropRecurrenceRule)
	p.Value = rule
	s.master.Props.Set(p)
}

// clampSequence converts a SEQUENCE for the structured Sequence field.
func clampSequence(n int) int32 {
	n = max(0, min(n, 1<<30))
	return int32(n)
}

func sequenceOf(c *ical.Component) int {
	if p := c.Props.Get(ical.PropSequence); p != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(p.Value)); err == nil {
			return max(n, 0)
		}
	}
	return 0
}

// stamp sets SEQUENCE, DTSTAMP and LAST-MODIFIED on c.
func stamp(c *ical.Component, seq int, now time.Time) {
	sp := ical.NewProp(ical.PropSequence)
	sp.Value = strconv.Itoa(seq)
	c.Props.Set(sp)
	c.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	c.Props.SetDateTime(ical.PropLastModified, now.UTC())
}

func cloneComponent(c *ical.Component) *ical.Component {
	out := ical.NewComponent(c.Name)
	for _, props := range c.Props {
		for _, p := range props {
			cp := p
			cp.Params = make(ical.Params, len(p.Params))
			for k, v := range p.Params {
				cp.Params[k] = slices.Clone(v)
			}
			out.Props.Add(&cp)
		}
	}
	for _, ch := range c.Children {
		out.Children = append(out.Children, cloneComponent(ch))
	}
	return out
}

// canonicalRID formats an instant the way store.FindOccurrence names it.
func canonicalRID(t time.Time, allDay bool) string {
	if allDay {
		return t.UTC().Format("20060102")
	}
	return t.UTC().Format("20060102T150405Z")
}

// onRule reports whether the rule of e, ignoring EXDATE and overrides, has an
// instance named rid.
func onRule(e *bongov1.Event, rid string) bool {
	bare := proto.Clone(e).(*bongov1.Event)
	bare.IcalRaw, bare.Exdates = nil, nil
	_, ok := store.FindOccurrence(bare, rid)
	return ok
}

// structuredDtstart is the DTSTART a series without ical_raw is written with,
// for the value form of an UNTIL.
func structuredDtstart(e *bongov1.Event) *ical.Prop {
	p := ical.NewProp(ical.PropDateTimeStart)
	if e.AllDay {
		p.SetDate(icalevent.AllDayDate(e.StartTime))
	} else {
		p.SetDateTime(time.Unix(e.StartTime, 0).UTC())
	}
	return p
}

// --- edit "this event" ---------------------------------------------------

// instanceEdit is the input of an edit of one occurrence or of this and
// following ones.
type instanceEdit struct {
	userID, email string
	existing      *bongov1.Event // as stored; never mutated
	form          *bongov1.Event // parsed from the request
	rid           string         // canonical RECURRENCE-ID
	slot          time.Time      // the instance's slot on the rule
	zone          string         // zone name for a series without ical_raw
	now           time.Time
}

// newOverride builds the VEVENT that replaces the instance at slot. It starts
// from the instance's existing override, or from the master, and applies the
// fields the form models. The override never carries a recurrence property.
// ORGANIZER and ATTENDEE stay so that clients and the iTIP sender see the
// participants. Attendee changes in the form are series-level and not applied
// to one instance.
func newOverride(s *seriesCal, base *ical.Component, form *bongov1.Event, slot time.Time, seq int, now time.Time) *ical.Component {
	src := base
	if src == nil {
		src = s.master
	}
	ov := cloneComponent(src)
	for _, name := range []string{
		ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates, "EXRULE",
		ical.PropRecurrenceID, ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropDuration,
		ical.PropSummary, ical.PropDescription, ical.PropLocation,
	} {
		ov.Props.Del(name)
	}
	ov.Props.Set(icalevent.InSeriesForm(ical.PropRecurrenceID, s.dtstart, slot))
	ov.Props.SetText(ical.PropSummary, form.Title)
	if form.Description != "" {
		ov.Props.SetText(ical.PropDescription, form.Description)
	}
	if form.Location != "" {
		ov.Props.SetText(ical.PropLocation, form.Location)
	}

	if form.AllDay {
		ov.Props.SetDate(ical.PropDateTimeStart, icalevent.AllDayDate(form.StartTime))
		ov.Props.SetDate(ical.PropDateTimeEnd, icalevent.AllDayDate(form.EndTime))
	} else {
		like := s.dtstart
		if icalevent.IsDateProp(like) {
			// A timed override of an all-day series: the expansion reads it in
			// UTC.
			like = structuredDtstart(&bongov1.Event{StartTime: form.StartTime})
		}
		ov.Props.Set(icalevent.InSeriesForm(ical.PropDateTimeStart, like, time.Unix(form.StartTime, 0)))
		ov.Props.Set(icalevent.InSeriesForm(ical.PropDateTimeEnd, like, time.Unix(form.EndTime, 0)))
	}
	stamp(ov, seq, now)
	return ov
}

// editOccurrence stores a RECURRENCE-ID override for one occurrence: one PUT of
// the series. The structured fields stay those of the master. A web-created
// series gets an ical_raw (the object CalDAV served for it) to hold the
// override. An override that already exists for the instance is replaced.
//
// Web edits carry no If-Match, so this is last-write-wins against a CalDAV PUT
// that lands between the read and the write, as UpdateEvent is.
func (h *CalendarHandler) editOccurrence(in instanceEdit) (*InstanceChange, error) {
	s, err := seriesForEdit(in.existing)
	if err != nil {
		slog.Error("calendar: series has no usable master", "user", in.userID, "guid", in.existing.Guid, "error", err)
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	idx, base := s.overrideAt(in.slot)
	seq := sequenceOf(s.master)
	if base != nil {
		seq = max(seq, sequenceOf(base))
	}
	ov := newOverride(s, base, in.form, in.slot, seq+1, in.now)
	if idx >= 0 {
		s.cal.Children[idx] = ov
	} else {
		s.cal.Children = append(s.cal.Children, ov)
	}
	raw, err := s.encode()
	if err != nil {
		slog.Error("calendar: encoding the override failed", "user", in.userID, "guid", in.existing.Guid, "error", err)
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}

	updated := proto.Clone(in.existing).(*bongov1.Event)
	updated.IcalRaw = raw
	if growth := int64(proto.Size(updated) - proto.Size(in.existing)); growth > 0 && quotaExceeded(h.quota, nil, in.userID, growth) {
		return nil, newInstanceError(http.StatusInsufficientStorage, "quota exceeded")
	}
	if err := h.events.Update(in.userID, in.existing.Guid, updated); err != nil {
		slog.Error("calendar: storing the override failed", "user", in.userID, "guid", in.existing.Guid, "error", err)
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	change := &InstanceChange{
		Scope: scopeThis, Guid: in.existing.Guid, RecurrenceID: in.rid,
		Before: in.existing, After: updated,
	}
	change.Instance, _ = store.FindOccurrence(updated, in.rid)
	if change.Instance != nil {
		// The override's own SEQUENCE, which the iTIP message carries.
		change.Instance.Sequence = clampSequence(seq + 1)
	}
	return change, nil
}

// --- delete "this event" -------------------------------------------------

// deleteOccurrence excludes one occurrence: an EXDATE, and the removal of its
// override, in one PUT. A series without ical_raw gets the instance in its
// structured Exdates, which is where AddRecurrence reads them for CalDAV.
//
// Deleting never refuses on quota: an EXDATE grows the record slightly, and a
// user at quota must still be able to remove an occurrence to make room.
func (h *CalendarHandler) deleteOccurrence(userID string, existing *bongov1.Event, rid string, slot time.Time, now time.Time) (*InstanceChange, error) {
	updated := proto.Clone(existing).(*bongov1.Event)
	var s *seriesCal
	if len(existing.IcalRaw) > 0 {
		var err error
		if s, err = decodeSeries(existing); err != nil {
			slog.Warn("calendar: stored ical_raw does not decode, excluding the instance in the stored fields", "user", userID, "guid", existing.Guid, "error", err)
			s = nil
		}
	}
	if s != nil {
		s.dropOverrides(func(t time.Time, _ bool) bool { return s.key(t) == s.key(slot) })
		s.master.Props.Add(icalevent.InSeriesForm(ical.PropExceptionDates, s.dtstart, slot))
		seq := sequenceOf(s.master) + 1
		stamp(s.master, seq, now)
		raw, err := s.encode()
		if err != nil {
			slog.Error("calendar: encoding the exclusion failed", "user", userID, "guid", existing.Guid, "error", err)
			return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
		}
		updated.IcalRaw = raw
		updated.Sequence = clampSequence(seq)
	} else {
		updated.Exdates = append(updated.Exdates, rid)
		if len(updated.Attendees) > 0 || existing.Sequence > 0 {
			updated.Sequence = existing.Sequence + 1
		}
	}
	if err := h.events.Update(userID, existing.Guid, updated); err != nil {
		slog.Error("calendar: storing the exclusion failed", "user", userID, "guid", existing.Guid, "error", err)
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	return &InstanceChange{
		Scope: scopeThis, Guid: existing.Guid, RecurrenceID: rid, Deleted: true,
		Before: existing, After: updated,
	}, nil
}

// --- "this and following" --------------------------------------------------

// truncateSeries returns a copy of the series that ends just before slot: the
// rule gets an UNTIL (replacing COUNT) in both the structured Rrule, which
// the expansion reads, and the master's RRULE in ical_raw, and the EXDATEs and
// overrides at or after slot go. The original is not mutated.
func truncateSeries(existing *bongov1.Event, slot, now time.Time) (*bongov1.Event, error) {
	trunc := proto.Clone(existing).(*bongov1.Event)

	var s *seriesCal
	if len(existing.IcalRaw) > 0 {
		var err error
		if s, err = decodeSeries(existing); err != nil {
			slog.Warn("calendar: stored ical_raw does not decode, truncating the stored fields only", "guid", existing.Guid, "error", err)
			s = nil
		}
	}

	dtstart := structuredDtstart(existing)
	if s != nil {
		dtstart = s.dtstart
	}
	until := icalevent.UntilBefore(dtstart, slot)
	trunc.Rrule = icalevent.SetUntil(existing.Rrule, until)

	loc := time.UTC
	if !existing.AllDay {
		loc = icalevent.ZoneOrUTC(existing.Timezone)
	}
	probe := &seriesCal{loc: loc, allDay: existing.AllDay}
	trunc.Exdates = nil
	for _, ex := range existing.Exdates {
		t, dateOnly, ok := icalevent.ParseExdate(ex, loc)
		if ok && probe.atOrAfter(t, dateOnly, slot) {
			continue
		}
		trunc.Exdates = append(trunc.Exdates, ex)
	}

	if s != nil {
		if p := s.master.Props.Get(ical.PropRecurrenceRule); p != nil {
			s.setRule(icalevent.SetUntil(p.Value, until))
		}
		after := func(t time.Time, dateOnly bool) bool { return s.atOrAfter(t, dateOnly, slot) }
		s.dropOverrides(after)
		s.dropExdates(func(ex exdate) bool { return after(ex.t, ex.dateOnly) })
		seq := sequenceOf(s.master) + 1
		stamp(s.master, seq, now)
		raw, err := s.encode()
		if err != nil {
			return nil, fmt.Errorf("encoding ical: %w", err)
		}
		trunc.IcalRaw = raw
		trunc.Sequence = clampSequence(seq)
	} else if len(trunc.Attendees) > 0 || existing.Sequence > 0 {
		trunc.Sequence = existing.Sequence + 1
	}
	return trunc, nil
}

// deleteFollowing ends the series before the chosen instance: one PUT. When
// the chosen instance is the first, there is nothing to keep and the whole
// series is deleted.
func (h *CalendarHandler) deleteFollowing(userID string, existing *bongov1.Event, rid string, slot, now time.Time) (*InstanceChange, error) {
	before, err := store.InstancesBefore(existing, slot)
	if err != nil {
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	change := &InstanceChange{Scope: scopeFollowing, Guid: existing.Guid, RecurrenceID: rid, Deleted: true, Before: existing}
	if before == 0 {
		if err := h.events.Delete(userID, existing.Guid); err != nil {
			slog.Error("calendar: deleting the series failed", "user", userID, "guid", existing.Guid, "error", err)
			return nil, newInstanceError(http.StatusInternalServerError, "failed to delete event")
		}
		return change, nil
	}
	trunc, err := truncateSeries(existing, slot, now)
	if err != nil {
		slog.Error("calendar: truncating the series failed", "user", userID, "guid", existing.Guid, "error", err)
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	// A truncation only shrinks the record, so it is not checked against quota.
	if err := h.events.Update(userID, existing.Guid, trunc); err != nil {
		slog.Error("calendar: storing the truncated series failed", "user", userID, "guid", existing.Guid, "error", err)
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	change.After = trunc
	return change, nil
}

// newSeriesFrom builds the series that continues from slot with the edit of
// the form. It is a copy of the original (so every field the form does not
// model, and any added later, carries over) with a new GUID, which is also its
// UID, the form's fields and times, and the submitted rule.
//
// The rule: when the form returns the original rule, a COUNT is reduced by the
// instances before slot (RFC 5545 COUNT includes excluded instances, so they
// are counted on the bare rule) and an UNTIL stays; a rule the user changed is
// used as submitted. An EXDATE or override from slot onwards is carried over
// only if its instance is still on the new rule; any other is dropped, since
// it would name an instance the new series does not have.
func newSeriesFrom(in instanceEdit, before int) (*bongov1.Event, error) {
	ex, form := in.existing, in.form

	rule := form.Rrule
	if rule == ex.Rrule {
		if n := icalevent.RuleCount(rule); n > 0 {
			rule = icalevent.SetCount(rule, max(n-before, 1))
		}
	}

	ns := proto.Clone(ex).(*bongov1.Event)
	ns.Guid = store.NewEventGUID()
	ns.CreatedAt, ns.UpdatedAt = 0, 0
	ns.RecurrenceId = ""
	ns.Title, ns.Description, ns.Location = form.Title, form.Description, form.Location
	ns.CalendarId, ns.AllDay, ns.ReminderMinutes = form.CalendarId, form.AllDay, form.ReminderMinutes
	ns.StartTime, ns.EndTime = form.StartTime, form.EndTime
	ns.Attendees = form.Attendees
	ns.Rrule = rule
	if len(ex.IcalRaw) == 0 && in.zone != "" {
		ns.Timezone = in.zone
	}
	carryOverParticipants(ns, ex, in.email)
	ns.Sequence = 0

	// Carry EXDATE and override entries only when the series keeps its shape.
	carry := rule != "" && form.AllDay == ex.AllDay
	loc := time.UTC
	if !ex.AllDay {
		loc = icalevent.ZoneOrUTC(ex.Timezone)
	}
	probe := &seriesCal{loc: loc, allDay: ex.AllDay}
	keep := func(t time.Time, dateOnly bool) bool {
		if !carry || !probe.atOrAfter(t, dateOnly, in.slot) {
			return false
		}
		if dateOnly && !ex.AllDay {
			// A date excludes whatever starts on that day.
			return true
		}
		return onRule(ns, canonicalRID(t, ex.AllDay))
	}
	ns.Exdates = nil
	for _, e := range ex.Exdates {
		if t, dateOnly, ok := icalevent.ParseExdate(e, loc); ok && keep(t, dateOnly) {
			ns.Exdates = append(ns.Exdates, e)
		}
	}
	ns.IcalRaw = nil

	if len(ex.IcalRaw) == 0 {
		return ns, nil
	}
	s, err := decodeSeries(ex)
	if err != nil {
		slog.Warn("calendar: stored ical_raw does not decode, the new series is kept in the stored fields only", "guid", ex.Guid, "error", err)
		return ns, nil
	}
	s.dropOverrides(func(t time.Time, dateOnly bool) bool { return !keep(t, dateOnly) })
	s.dropExdates(func(e exdate) bool { return !keep(e.t, e.dateOnly) })
	for _, c := range s.cal.Children {
		if c.Name == ical.CompEvent {
			c.Props.SetText(ical.PropUID, ns.Guid)
		}
	}
	s.setRule(rule)
	raw, err := s.encode()
	if err != nil {
		return nil, err
	}
	// The usual edit path applies the form: title, place and times, attendee
	// changes (other ATTENDEE parameters stay) and the DTSTAMP. It bumps
	// SEQUENCE, which a new series starts without.
	raw, err = applyEventEditToICal(raw, ex, ns, in.now)
	if err != nil {
		return nil, err
	}
	fresh, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errICalDecode, err)
	}
	if m, _ := icalevent.Master(fresh); m != nil {
		m.Props.Del(ical.PropSequence)
		sp := ical.NewProp(ical.PropSequence)
		sp.Value = "0"
		m.Props.Set(sp)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(fresh); err != nil {
		return nil, fmt.Errorf("encoding ical: %w", err)
	}
	ns.IcalRaw = buf.Bytes()
	ns.Sequence = 0
	return ns, nil
}

// editFollowing ends the original series before the chosen instance and starts
// a new series from the form: two separate PUTs, the original first.
//
// The two writes are not atomic. If creating the new series fails, the
// original is put back as it was before the edit (the whole record, since the
// structured Rrule changed as well as ical_raw) and the request fails. The
// restore is a third write, made only if the stored record is still the one
// the first write produced: a CalDAV PUT that landed in between is not
// overwritten. What is left when it cannot run:
//   - the restore itself fails: the original keeps its UNTIL and no new series
//     exists; both GUIDs are logged at Error for the operator;
//   - the process crashes between the two writes: the same state, and nothing
//     repairs it. The series can be extended again by editing its rule.
//
// The growth of both records is checked against quota before the first write,
// so the second cannot fail on quota after the first has succeeded.
func (h *CalendarHandler) editFollowing(in instanceEdit, before int) (*InstanceChange, error) {
	trunc, err := truncateSeries(in.existing, in.slot, in.now)
	if err != nil {
		slog.Error("calendar: truncating the series failed", "user", in.userID, "guid", in.existing.Guid, "error", err)
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	ns, err := newSeriesFrom(in, before)
	if err != nil {
		slog.Error("calendar: building the new series failed", "user", in.userID, "guid", in.existing.Guid, "error", err)
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	growth := int64(proto.Size(trunc)-proto.Size(in.existing)) + store.EventRecordSize(ns)
	if growth > 0 && quotaExceeded(h.quota, nil, in.userID, growth) {
		return nil, newInstanceError(http.StatusInsufficientStorage, "quota exceeded")
	}

	original := proto.Clone(in.existing).(*bongov1.Event)
	if err := h.events.Update(in.userID, in.existing.Guid, trunc); err != nil {
		slog.Error("calendar: storing the truncated series failed", "user", in.userID, "guid", in.existing.Guid, "error", err)
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	if _, err := h.events.Create(in.userID, ns); err != nil {
		slog.Error("calendar: creating the new series failed, restoring the original", "user", in.userID, "guid", in.existing.Guid, "new_guid", ns.Guid, "error", err)
		if rerr := h.restoreSeries(in.userID, trunc, original); rerr != nil {
			slog.Error("calendar: restoring the original series failed, it keeps its UNTIL and has no continuation",
				"user", in.userID, "guid", in.existing.Guid, "new_guid", ns.Guid, "error", rerr)
		}
		return nil, newInstanceError(http.StatusInternalServerError, "failed to update event")
	}
	return &InstanceChange{
		Scope: scopeFollowing, Guid: in.existing.Guid, RecurrenceID: in.rid, NewGuid: ns.Guid,
		Before: in.existing, After: trunc, NewSeries: ns,
	}, nil
}

// restoreSeries puts original back over written, the record the first write
// stored, unless the stored record has changed since (or is gone): then it
// leaves the record alone and says why.
func (h *CalendarHandler) restoreSeries(userID string, written, original *bongov1.Event) error {
	cur, err := h.events.Get(userID, original.Guid)
	if err != nil {
		return fmt.Errorf("re-reading the series: %w", err)
	}
	if !proto.Equal(cur, written) {
		return errors.New("the series changed since the first write, leaving it as it is")
	}
	if err := h.events.Update(userID, original.Guid, original); err != nil {
		return fmt.Errorf("writing the original back: %w", err)
	}
	return nil
}

// --- request handlers ------------------------------------------------------

// updateInstance serves the "this event" and "this and following" scopes of
// UpdateEvent. It reports whether it answered; false leaves the request to the
// series-level path (no occurrence, scope "all", and an edit from the first
// instance on, which is the whole series). existing is the event as loaded by
// UpdateEvent. owner holds the data (the caller, or the owner of a calendar
// shared with write access); userID is the caller, whose zone the form is read in.
func (h *CalendarHandler) updateInstance(w http.ResponseWriter, r *http.Request, userID, owner, email string, existing *bongov1.Event) bool {
	rid := r.FormValue("occurrence")
	if rid == "" {
		return false
	}
	scope, ok := parseScope(r)
	if !ok {
		http.Error(w, "unknown scope", http.StatusBadRequest)
		return true
	}
	if scope == scopeAll {
		return false
	}
	canonical, slot, err := resolveSlot(existing, rid)
	if err != nil {
		writeInstanceError(w, r, err)
		return true
	}
	loc := h.userLocation(userID)
	form, err := parseEventForm(r, loc)
	if err != nil {
		eventFormError(w, r, err)
		return true
	}
	in := instanceEdit{
		userID: owner, email: email, existing: existing, form: form,
		rid: canonical, slot: slot, zone: h.eventZoneName(userID, form.AllDay), now: time.Now(),
	}

	var change *InstanceChange
	switch scope {
	case scopeThis:
		// An override never carries a recurrence rule.
		form.Rrule = existing.Rrule
		change, err = h.editOccurrence(in)
	case scopeFollowing:
		if !allowedRrule(form.Rrule, existing.Rrule) {
			http.Error(w, "invalid form data: unsupported repeat rule", http.StatusBadRequest)
			return true
		}
		before, cerr := store.InstancesBefore(existing, slot)
		if cerr != nil {
			slog.Error("calendar: counting instances failed", "user", userID, "guid", existing.Guid, "error", cerr)
			http.Error(w, "failed to update event", http.StatusInternalServerError)
			return true
		}
		if before == 0 {
			// From the first instance on is the whole series.
			return false
		}
		change, err = h.editFollowing(in, before)
	}
	if err != nil {
		writeInstanceError(w, r, err)
		return true
	}

	notReached := h.notifyInstance(r, userID, email, change)
	t := eventMonthTime(form, loc)
	http.Redirect(w, r, withInviteWarning(fmt.Sprintf("/calendar/%d/%d?flash=Event+updated", t.Year(), int(t.Month())), notReached), http.StatusSeeOther) // #nosec G710 -- fixed same-origin calendar path
	return true
}

// bindOccurrenceForm handles the "all events" scope of an edit made from an
// occurrence. The form then shows the occurrence's times, and applying its
// date to the series would move the series start. The series keeps its own
// date: from the form it takes the time of day and the duration, and when the
// times are those of the occurrence untouched it keeps the series' times
// exactly (the occurrence can differ from the master across DST when the
// viewer's zone is not the series' zone). It reports false after answering.
func (h *CalendarHandler) bindOccurrenceForm(w http.ResponseWriter, r *http.Request, userID string, existing, updated *bongov1.Event) bool {
	rid := r.FormValue("occurrence")
	if rid == "" {
		return true
	}
	if scope, ok := parseScope(r); !ok || scope != scopeAll {
		// Refused or handled by updateInstance; an edit from the first
		// instance on arrives here with the form as it is.
		return true
	}
	occ, ok := store.FindOccurrence(existing, rid)
	if !ok {
		http.Error(w, "event not found", http.StatusNotFound)
		return false
	}
	rebindToSeries(updated, existing, occ, h.userLocation(userID))
	return true
}

// rebindToSeries rewrites the times parsed from an occurrence form so that
// they apply to the series: the series' date, the form's time of day and
// duration.
func rebindToSeries(updated, existing, occ *bongov1.Event, loc *time.Location) {
	if updated.AllDay == occ.AllDay && updated.StartTime/60 == occ.StartTime/60 && updated.EndTime/60 == occ.EndTime/60 {
		updated.StartTime, updated.EndTime = existing.StartTime, existing.EndTime
		return
	}
	var y, d int
	var m time.Month
	if existing.AllDay {
		y, m, d = icalevent.AllDayDate(existing.StartTime).Date()
	} else {
		y, m, d = time.Unix(existing.StartTime, 0).In(loc).Date()
	}
	if updated.AllDay {
		start := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		updated.EndTime = start.Unix() + (updated.EndTime - updated.StartTime)
		updated.StartTime = start.Unix()
		return
	}
	sf, ef := time.Unix(updated.StartTime, 0).In(loc), time.Unix(updated.EndTime, 0).In(loc)
	days := int(time.Date(ef.Year(), ef.Month(), ef.Day(), 0, 0, 0, 0, time.UTC).Sub(
		time.Date(sf.Year(), sf.Month(), sf.Day(), 0, 0, 0, 0, time.UTC)).Hours() / 24)
	updated.StartTime = time.Date(y, m, d, sf.Hour(), sf.Minute(), 0, 0, loc).Unix()
	updated.EndTime = time.Date(y, m, d+days, ef.Hour(), ef.Minute(), 0, 0, loc).Unix()
}

// deleteInstance serves the "this event" and "this and following" scopes of
// DeleteEvent. event is nil when the load failed; owner holds the data and
// userID is the caller. It reports whether it answered.
func (h *CalendarHandler) deleteInstance(w http.ResponseWriter, r *http.Request, userID, owner, email string, event *bongov1.Event) bool {
	rid := r.FormValue("occurrence")
	if rid == "" {
		return false
	}
	scope, ok := parseScope(r)
	if !ok {
		http.Error(w, "unknown scope", http.StatusBadRequest)
		return true
	}
	if scope == scopeAll {
		return false
	}
	if event == nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return true
	}
	canonical, slot, err := resolveSlot(event, rid)
	if err != nil {
		writeInstanceError(w, r, err)
		return true
	}
	now := time.Now()
	var change *InstanceChange
	if scope == scopeThis {
		change, err = h.deleteOccurrence(owner, event, canonical, slot, now)
	} else {
		change, err = h.deleteFollowing(owner, event, canonical, slot, now)
	}
	if err != nil {
		writeInstanceError(w, r, err)
		return true
	}

	notReached := h.notifyInstance(r, userID, email, change)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", withInviteWarning("/calendar", notReached))
		w.WriteHeader(http.StatusOK)
		return true
	}
	t := eventMonthTime(event, h.userLocation(userID))
	http.Redirect(w, r, withInviteWarning(fmt.Sprintf("/calendar/%d/%d?flash=Event+deleted", t.Year(), int(t.Month())), notReached), http.StatusSeeOther) // #nosec G710 -- fixed same-origin calendar path
	return true
}
