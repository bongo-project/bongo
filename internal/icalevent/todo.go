package icalevent

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ErrTodoDecode marks a stored ical_raw that cannot be parsed, as opposed to a
// failure to encode the edited calendar.
var ErrTodoDecode = errors.New("decoding stored ical")

// TodoMaster returns the VTODO that describes the object: the first without
// RECURRENCE-ID, else the first VTODO.
func TodoMaster(cal *ical.Calendar) *ical.Component {
	var first *ical.Component
	for _, c := range cal.Children {
		if c.Name != ical.CompToDo {
			continue
		}
		if c.Props.Get(ical.PropRecurrenceID) == nil {
			return c
		}
		if first == nil {
			first = c
		}
	}
	return first
}

// TodoToTask copies the summary fields of the master VTODO into a Task. The
// caller sets the raw data and timestamps.
func TodoToTask(cal *ical.Calendar, guid, calendarID string) *bongov1.Task {
	task := &bongov1.Task{Guid: guid, CalendarId: calendarID}
	m := TodoMaster(cal)
	if m == nil {
		return task
	}
	if v, err := m.Props.Text(ical.PropSummary); err == nil {
		task.Summary = v
	}
	if v, err := m.Props.Text(ical.PropDescription); err == nil {
		task.Description = v
	}
	instant := func(name string) int64 {
		p := m.Props.Get(name)
		if p == nil {
			return 0
		}
		if at, _, ok := PropInstant(p, time.UTC); ok {
			return at.Unix()
		}
		return 0
	}
	task.Due = instant(ical.PropDue)
	task.Start = instant(ical.PropDateTimeStart)
	task.Completed = instant(ical.PropCompleted)
	if v, err := m.Props.Text(ical.PropStatus); err == nil {
		task.Status = strings.ToUpper(strings.TrimSpace(v))
	}
	task.Priority = boundedInt(m.Props.Get(ical.PropPriority), 0, 9)
	task.Percent = boundedInt(m.Props.Get(ical.PropPercentComplete), 0, 100)
	return task
}

// boundedInt reads an integer property, 0 when absent or malformed, clamped
// to [lo, hi].
func boundedInt(p *ical.Prop, lo, hi int32) int32 {
	if p == nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(p.Value), 10, 32)
	if err != nil {
		return 0
	}
	return min(max(int32(n), lo), hi)
}

// SynthesiseTodo builds a VTODO from the record fields.
func SynthesiseTodo(t *bongov1.Task) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")
	todo := ical.NewComponent(ical.CompToDo)
	todo.Props.SetText(ical.PropUID, t.Guid)
	todo.Props.SetDateTime(ical.PropDateTimeStamp, time.Unix(max(t.UpdatedAt, t.CreatedAt), 0).UTC())
	if t.Summary != "" {
		todo.Props.SetText(ical.PropSummary, t.Summary)
	}
	if t.Description != "" {
		todo.Props.SetText(ical.PropDescription, t.Description)
	}
	for name, at := range map[string]int64{ical.PropDue: t.Due, ical.PropDateTimeStart: t.Start, ical.PropCompleted: t.Completed} {
		if at != 0 {
			todo.Props.SetDateTime(name, time.Unix(at, 0).UTC())
		}
	}
	if t.Status != "" {
		todo.Props.SetText(ical.PropStatus, t.Status)
	}
	if t.Priority != 0 {
		setInt(todo.Props, ical.PropPriority, int(t.Priority))
	}
	if t.Percent != 0 {
		setInt(todo.Props, ical.PropPercentComplete, int(t.Percent))
	}
	cal.Children = append(cal.Children, todo)
	return cal
}

// TodoTraits reads two facts the structured Task cannot hold from the stored
// ical_raw: whether DUE is a DATE (VALUE=DATE) rather than a DATE-TIME, and
// whether the master VTODO recurs (RRULE). A task without readable raw data
// answers false for both.
func TodoTraits(raw []byte) (dueDate, recurring bool) {
	if len(raw) == 0 {
		return false, false
	}
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return false, false
	}
	m := TodoMaster(cal)
	if m == nil {
		return false, false
	}
	p := m.Props.Get(ical.PropDue)
	return p != nil && IsDateProp(p), m.Props.Get(ical.PropRecurrenceRule) != nil
}

func encodeCalendar(cal *ical.Calendar) ([]byte, error) {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("encoding ical: %w", err)
	}
	return buf.Bytes(), nil
}

// setInt sets an INTEGER property. SetText would add VALUE=TEXT, which is
// wrong for these properties and which clients reject.
func setInt(props ical.Props, name string, n int) {
	p := ical.NewProp(name)
	p.Value = strconv.Itoa(n)
	props.Set(p)
}

func setDue(props ical.Props, at int64, dueDate bool) {
	switch {
	case at == 0:
		props.Del(ical.PropDue)
	case dueDate:
		p := ical.NewProp(ical.PropDue)
		p.SetDate(time.Unix(at, 0).UTC())
		props.Set(p)
	default:
		props.SetDateTime(ical.PropDue, time.Unix(at, 0).UTC())
	}
}

// BuildTodo serialises a new task as a VCALENDAR holding one VTODO. dueDate
// writes DUE as VALUE=DATE.
func BuildTodo(t *bongov1.Task, dueDate bool, now time.Time) ([]byte, error) {
	cal := SynthesiseTodo(t)
	todo := TodoMaster(cal)
	todo.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	setDue(todo.Props, t.Due, dueDate)
	return encodeCalendar(cal)
}

// ApplyTodoEdit applies a web edit onto the stored iCalendar and returns the
// re-serialised bytes. A property is changed only when the structured value
// differs from the existing one, so everything the form does not model
// (VALARM, RRULE, RELATED-TO, CATEGORIES, X- properties, and a DUE that did
// not change, TZID included) is left as stored. updatedDueDate says whether a
// changed DUE is written as a DATE. DTSTAMP, LAST-MODIFIED and SEQUENCE are
// bumped. A raw that cannot be parsed returns an error wrapping ErrTodoDecode.
func ApplyTodoEdit(raw []byte, existing, updated *bongov1.Task, updatedDueDate bool, now time.Time) ([]byte, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTodoDecode, err)
	}
	master := TodoMaster(cal)
	if master == nil {
		return nil, fmt.Errorf("%w: no VTODO", ErrTodoDecode)
	}
	props := master.Props

	for _, f := range []struct{ name, old, new string }{
		{ical.PropSummary, existing.Summary, updated.Summary},
		{ical.PropDescription, existing.Description, updated.Description},
		{ical.PropStatus, existing.Status, updated.Status},
	} {
		if f.new == f.old {
			continue
		}
		if f.new == "" {
			props.Del(f.name)
		} else {
			props.SetText(f.name, f.new)
		}
	}

	oldDate := false
	if p := props.Get(ical.PropDue); p != nil {
		oldDate = IsDateProp(p)
	}
	if updated.Due != existing.Due || (updated.Due != 0 && updatedDueDate != oldDate) {
		setDue(props, updated.Due, updatedDueDate)
	}
	if updated.Completed != existing.Completed {
		if updated.Completed == 0 {
			props.Del(ical.PropCompleted)
		} else {
			props.SetDateTime(ical.PropCompleted, time.Unix(updated.Completed, 0).UTC())
		}
	}
	if updated.Priority != existing.Priority {
		if updated.Priority == 0 {
			props.Del(ical.PropPriority)
		} else {
			setInt(props, ical.PropPriority, int(updated.Priority))
		}
	}
	if updated.Percent != existing.Percent {
		setInt(props, ical.PropPercentComplete, int(updated.Percent))
	}

	seq := 0
	if p := props.Get(ical.PropSequence); p != nil {
		seq, _ = strconv.Atoi(p.Value)
	}
	setInt(props, ical.PropSequence, max(seq, 0)+1)
	props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	props.SetDateTime(ical.PropLastModified, now.UTC())

	if cal.Props.Get(ical.PropVersion) == nil {
		cal.Props.SetText(ical.PropVersion, "2.0")
	}
	if cal.Props.Get(ical.PropProductID) == nil {
		cal.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")
	}
	return encodeCalendar(cal)
}
