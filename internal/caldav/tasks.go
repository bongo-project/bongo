package caldav

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	webdav "github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// SetTaskStore enables tasks (VTODO). Without it no calendar reports or
// accepts VTODO, so a node without a task store never stores a task as
// something else.
func (b *Backend) SetTaskStore(ts *store.TaskStore) { b.tasks = ts }

// preconditionRefusal is a 403 carrying a CALDAV precondition element. The
// library's NewPreconditionError hard-codes 409, so its error is unwrapped and
// re-wrapped with the status RFC 4791 5.3.2.1 gives these preconditions;
// go-webdav renders the XML body from the inner error.
func preconditionRefusal(p caldav.PreconditionType) error {
	return webdav.NewHTTPError(http.StatusForbidden, errors.Unwrap(caldav.NewPreconditionError(p)))
}

// putComponents lists the distinct component names at the top level of a PUT,
// other than VTIMEZONE, which travels with whatever uses it.
func putComponents(cal *ical.Calendar) []string {
	var kinds []string
	for _, c := range cal.Children {
		if c.Name == ical.CompTimezone {
			continue
		}
		seen := false
		for _, k := range kinds {
			seen = seen || k == c.Name
		}
		if !seen {
			kinds = append(kinds, c.Name)
		}
	}
	return kinds
}

// calendarOf reads the calendar a request targets. A calendar that cannot be
// read is nil, which allows VEVENT only: that is what a calendar that was
// never written has always done here.
func (b *Backend) calendarOf(t target) *bongov1.Calendar {
	cal, err := b.calendars.Get(t.keyOwner, t.calendar)
	if err != nil {
		return nil
	}
	return cal
}

// allowsTodo reports whether cal takes VTODO and this node can store it.
func (b *Backend) allowsTodo(cal *bongov1.Calendar) bool {
	return b.tasks != nil && store.CalendarAllows(cal, store.ComponentTodo)
}

// components is the set of component kinds a calendar advertises.
func (b *Backend) components(cal *bongov1.Calendar) []string {
	var out []string
	for _, c := range store.CalendarComponents(cal) {
		if c == store.ComponentTodo && b.tasks == nil {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return []string{store.ComponentEvent}
	}
	return out
}

// taskCalendarSubscribed reports whether task lives in one of owner's
// subscribed calendars; the stored calendar decides, never the path.
func (b *Backend) taskCalendarSubscribed(owner string, task *bongov1.Task) (bool, error) {
	if task == nil {
		return false, nil
	}
	subscribed, err := b.calendars.CalendarIsSubscribed(owner, task.CalendarId)
	if err != nil {
		return false, fmt.Errorf("reading calendar: %w", err)
	}
	return subscribed, nil
}

// guidIsTask reports whether owner holds a task with the guid. A read error
// other than not-found is returned: the collision check cannot be made.
func (b *Backend) guidIsTask(owner, guid string) (bool, error) {
	if b.tasks == nil {
		return false, nil
	}
	if _, err := b.tasks.Get(owner, guid); err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("reading task: %w", err)
	}
	return true, nil
}

// guidIsEvent is the event counterpart of guidIsTask.
func (b *Backend) guidIsEvent(owner, guid string) (bool, error) {
	if _, err := b.events.Get(owner, guid); err != nil {
		// EventStore.Get wraps the key-level not-found, not ErrEventNotFound.
		if errors.Is(err, store.ErrEventNotFound) || errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("reading event: %w", err)
	}
	return true, nil
}

// putTodo stores a VTODO PUT. The caller has already resolved the target,
// checked write access and found the target calendar to take VTODO.
func (b *Backend) putTodo(t target, calendar *ical.Calendar, raw []byte, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	owner, guid, calID := t.keyOwner, t.guid, t.calendar

	// One href must map to one object: a guid held by an event is refused.
	if clash, err := b.guidIsEvent(owner, guid); err != nil {
		return nil, err
	} else if clash {
		return nil, httpErr(http.StatusForbidden, "forbidden")
	}

	task := todoToTask(calendar, guid, calID)
	task.IcalRaw = raw

	existing, err := b.tasks.Get(owner, guid)
	if err != nil {
		if !errors.Is(err, store.ErrTaskNotFound) {
			return nil, fmt.Errorf("reading task: %w", err)
		}
		existing = nil
	}
	// Create with a set guid is an upsert, so a PUT through a shared
	// collection must not overwrite a task kept in another calendar. The
	// answer is the same 403 a read share gives, so it does not confirm the
	// guid exists.
	if t.shared() && existing != nil && existing.CalendarId != calID {
		return nil, httpErr(http.StatusForbidden, "forbidden")
	}
	if subscribed, err := b.taskCalendarSubscribed(owner, existing); err != nil {
		return nil, err
	} else if subscribed {
		return nil, httpErr(http.StatusForbidden, "this calendar is a read-only subscription")
	}

	var existingRaw []byte
	var existingUpdated int64
	if existing != nil {
		existingRaw, existingUpdated = existing.IcalRaw, existing.UpdatedAt
	}
	if err := checkPreconditions(opts, existing != nil, existingRaw, existingUpdated); err != nil {
		return nil, err
	}

	var addBytes int64
	if existing != nil {
		task.CreatedAt = existing.CreatedAt
		addBytes = store.TaskRecordSize(task) - store.TaskRecordSize(existing)
	} else {
		addBytes = store.TaskRecordSize(task)
	}
	if err := b.checkQuota(owner, addBytes); err != nil {
		return nil, err
	}

	if existing != nil {
		if err := b.tasks.Update(owner, guid, task); err != nil {
			return nil, fmt.Errorf("updating task: %w", err)
		}
		b.logger.Info("caldav: task updated", "user", owner, "calendar", calID, "guid", guid)
	} else {
		if _, err := b.tasks.Create(owner, task); err != nil {
			return nil, fmt.Errorf("creating task: %w", err)
		}
		b.logger.Info("caldav: task created", "user", owner, "calendar", calID, "guid", guid)
	}
	return taskToCalendarObject(t.user, t.calSeg, task)
}

// getTask serves a task through the collection t names. found is false when
// the guid is not a task this collection covers.
func (b *Backend) getTask(t target) (obj *caldav.CalendarObject, found bool, err error) {
	task, err := b.tasks.Get(t.keyOwner, t.guid)
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading task: %w", err)
	}
	// A grant covers one calendar: a task kept in another is not reachable
	// through this one, and answers as if it did not exist.
	if t.shared() && task.CalendarId != t.calendar {
		return nil, false, nil
	}
	obj, err = taskToCalendarObject(t.user, t.calSeg, task)
	return obj, err == nil, err
}

// deleteTask removes a task through the collection t names. handled is false
// when the guid is not a task this collection covers.
func (b *Backend) deleteTask(t target) (handled bool, err error) {
	task, err := b.tasks.Get(t.keyOwner, t.guid)
	if err != nil {
		if errors.Is(err, store.ErrTaskNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("reading task: %w", err)
	}
	if t.shared() && task.CalendarId != t.calendar {
		return false, nil
	}
	if subscribed, err := b.taskCalendarSubscribed(t.keyOwner, task); err != nil {
		return false, err
	} else if subscribed {
		return false, httpErr(http.StatusForbidden, "this calendar is a read-only subscription")
	}
	if err := b.tasks.Delete(t.keyOwner, t.guid); err != nil {
		b.logger.Warn("caldav: delete task failed", "user", t.keyOwner, "guid", t.guid, "error", err)
		return false, err
	}
	b.logger.Info("caldav: task deleted", "user", t.keyOwner, "guid", t.guid)
	return true, nil
}

// listTasks returns the tasks of the collection t names.
func (b *Backend) listTasks(t target) ([]caldav.CalendarObject, error) {
	tasks, err := b.tasks.ListAll(t.keyOwner)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	var objects []caldav.CalendarObject
	for _, task := range tasks {
		if task.CalendarId != t.calendar {
			continue
		}
		obj, err := taskToCalendarObject(t.user, t.calSeg, task)
		if err != nil {
			continue
		}
		objects = append(objects, *obj)
	}
	return objects, nil
}

// todoMaster returns the VTODO that describes the object: the first without
// RECURRENCE-ID, else the first VTODO.
func todoMaster(cal *ical.Calendar) *ical.Component {
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

// todoToTask copies the summary fields of the master VTODO into a Task. The
// caller sets the raw data and timestamps.
func todoToTask(cal *ical.Calendar, guid, calendarID string) *bongov1.Task {
	task := &bongov1.Task{Guid: guid, CalendarId: calendarID}
	m := todoMaster(cal)
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
		if at, _, ok := icalevent.PropInstant(p, time.UTC); ok {
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

// taskToCalendarObject converts a Task to a CalDAV object. The client's own
// bytes are served when stored; a record without them is synthesised.
func taskToCalendarObject(userID, calID string, t *bongov1.Task) (*caldav.CalendarObject, error) {
	var cal *ical.Calendar
	if len(t.IcalRaw) > 0 {
		if c, err := ical.NewDecoder(bytes.NewReader(t.IcalRaw)).Decode(); err == nil {
			cal = c
		}
	}
	if cal == nil {
		cal = synthesiseTask(t)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("encoding ical: %w", err)
	}
	if calID == "" {
		calID = t.CalendarId
	}
	modTime := time.Unix(t.UpdatedAt, 0)
	if t.UpdatedAt == 0 {
		modTime = time.Unix(t.CreatedAt, 0)
	}
	return &caldav.CalendarObject{
		Path:          objectPath(userID, calID, t.Guid),
		ModTime:       modTime,
		ContentLength: int64(buf.Len()),
		ETag:          store.ContentETag(t.IcalRaw, t.UpdatedAt),
		Data:          cal,
	}, nil
}

// synthesiseTask builds a VTODO from the record fields.
func synthesiseTask(t *bongov1.Task) *ical.Calendar {
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
		todo.Props.SetText(ical.PropPriority, strconv.Itoa(int(t.Priority)))
	}
	if t.Percent != 0 {
		todo.Props.SetText(ical.PropPercentComplete, strconv.Itoa(int(t.Percent)))
	}
	cal.Children = append(cal.Children, todo)
	return cal
}

// relaxTodoTimeRanges returns f with the time range removed from every VTODO
// filter, and reports whether it removed any. go-webdav's time-range match
// knows only VEVENT: a VTODO filter with a time range matches nothing, so the
// range is dropped here and applied by todoInRange.
func relaxTodoTimeRanges(f caldav.CompFilter) (caldav.CompFilter, bool) {
	relaxed := false
	if f.Name == ical.CompToDo && (!f.Start.IsZero() || !f.End.IsZero()) {
		f.Start, f.End = time.Time{}, time.Time{}
		relaxed = true
	}
	if len(f.Comps) > 0 {
		comps := make([]caldav.CompFilter, len(f.Comps))
		for i, c := range f.Comps {
			var r bool
			comps[i], r = relaxTodoTimeRanges(c)
			relaxed = relaxed || r
		}
		f.Comps = comps
	}
	return f, relaxed
}

// todoRanges collects the time ranges of the VTODO filters in f.
func todoRanges(f caldav.CompFilter) [][2]time.Time {
	var out [][2]time.Time
	if f.Name == ical.CompToDo && (!f.Start.IsZero() || !f.End.IsZero()) {
		out = append(out, [2]time.Time{f.Start, f.End})
	}
	for _, c := range f.Comps {
		out = append(out, todoRanges(c)...)
	}
	return out
}

// filterTodoObjects applies the original query to objects: the library
// matches everything but VTODO time ranges, which are applied here. An object
// without a VTODO is not affected by a VTODO range.
func filterTodoObjects(query *caldav.CalendarQuery, objects []caldav.CalendarObject) ([]caldav.CalendarObject, error) {
	relaxed, any := relaxTodoTimeRanges(query.CompFilter)
	if !any {
		return caldav.Filter(query, objects)
	}
	q := *query
	q.CompFilter = relaxed
	matched, err := caldav.Filter(&q, objects)
	if err != nil {
		return nil, err
	}
	ranges := todoRanges(query.CompFilter)
	var out []caldav.CalendarObject
	for _, o := range matched {
		if todoObjectInRanges(o.Data, ranges) {
			out = append(out, o)
		}
	}
	return out, nil
}

func todoObjectInRanges(cal *ical.Calendar, ranges [][2]time.Time) bool {
	if cal == nil {
		return false
	}
	hasTodo := false
	for _, c := range cal.Children {
		if c.Name != ical.CompToDo {
			continue
		}
		hasTodo = true
		inAll := true
		for _, r := range ranges {
			inAll = inAll && todoInRange(c, r[0], r[1])
		}
		if inAll {
			return true
		}
	}
	return !hasTodo
}

// todoInRange is the RFC 4791 9.9 time-range test for a VTODO. A zero end is
// unbounded. A recurring VTODO, and one whose times cannot be read, is
// included: a client filters further, while a task left out is invisible.
func todoInRange(c *ical.Component, start, end time.Time) bool {
	if c.Props.Get(ical.PropRecurrenceRule) != nil || c.Props.Get(ical.PropRecurrenceDates) != nil {
		return true
	}
	read := func(name string) (t time.Time, set, ok bool) {
		p := c.Props.Get(name)
		if p == nil {
			return time.Time{}, false, true
		}
		at, _, ok := icalevent.PropInstant(p, start.Location())
		return at, true, ok
	}
	dtstart, hasStart, ok1 := read(ical.PropDateTimeStart)
	due, hasDue, ok2 := read(ical.PropDue)
	completed, hasDone, ok3 := read(ical.PropCompleted)
	created, hasCreated, ok4 := read(ical.PropCreated)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return true
	}

	// A zero end stands for "no end".
	endAfter := func(t time.Time) bool { return end.IsZero() || end.After(t) }
	endAtOrAfter := func(t time.Time) bool { return end.IsZero() || !end.Before(t) }
	startAtOrBefore := func(t time.Time) bool { return !start.After(t) }

	switch {
	case hasStart && hasDue:
		return (start.Before(due) || startAtOrBefore(dtstart)) && (endAfter(dtstart) || endAtOrAfter(due))
	case hasStart:
		dur := time.Duration(0)
		if p := c.Props.Get(ical.PropDuration); p != nil {
			if d, err := p.Duration(); err == nil {
				dur = d
			}
		}
		if dur > 0 {
			return startAtOrBefore(dtstart.Add(dur)) && (endAfter(dtstart) || endAtOrAfter(dtstart.Add(dur)))
		}
		return startAtOrBefore(dtstart) && endAfter(dtstart)
	case hasDue:
		return start.Before(due) && endAtOrAfter(due)
	case hasDone && hasCreated:
		return (startAtOrBefore(created) || startAtOrBefore(completed)) && (endAtOrAfter(created) || endAtOrAfter(completed))
	case hasDone:
		return startAtOrBefore(completed) && endAtOrAfter(completed)
	case hasCreated:
		return endAfter(created)
	}
	return true
}
