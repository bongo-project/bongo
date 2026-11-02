package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	taskUser  = "u-task"
	taskEmail = "task@example.com"
	taskOther = "u-task-other"
)

// switchApplier writes through until failing is set, then refuses every write.
type switchApplier struct {
	inner   *directApplierTags
	failing atomic.Bool
	writes  atomic.Int32
}

func (a *switchApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if a.failing.Load() {
		return errors.New("injected write failure")
	}
	a.writes.Add(1)
	return a.inner.ApplyCommand(cmd, d)
}

type taskEnv struct {
	h       *TasksHandler
	cal     *CalendarHandler
	tasks   *store.TaskStore
	cals    *store.CalendarStore
	events  *store.EventStore
	applier *switchApplier
	secret  []byte
	now     time.Time
}

// taskNow is the fixed clock: 2026-06-15 12:00 UTC.
var taskNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func newTaskEnv(t *testing.T) *taskEnv {
	t.Helper()
	q := newQuotaEnv(t)
	a := &switchApplier{inner: &directApplierTags{ps: q.ps}}
	e := &taskEnv{
		tasks:   store.NewTaskStore(q.ps, a),
		cals:    store.NewCalendarStore(q.ps, a),
		events:  store.NewEventStore(q.ps, a),
		applier: a,
		secret:  []byte("tasks-csrf-test-secret-0123456789"),
		now:     taskNow,
	}
	e.h = NewTasksHandler(e.tasks, e.cals)
	e.h.now = func() time.Time { return e.now }
	e.h.SetZoneSource(fixedZone{time.UTC})
	e.cal = NewCalendarHandler(e.events, e.cals, nil)
	e.cal.now = e.h.now
	e.cal.SetZoneSource(fixedZone{time.UTC})
	e.cal.SetTaskStore(e.tasks)
	if _, err := e.cals.Create(taskUser, store.NewTasksCalendar()); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *taskEnv) router(userID string) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), userID, userID+"@example.com")))
		})
	})
	r.Use(middleware.InjectCSRF(e.secret))
	r.Use(middleware.RequireCSRF(e.secret, true))
	r.Get("/tasks", e.h.List)
	r.Post("/tasks", e.h.Create)
	r.Get("/tasks/{guid}", e.h.Edit)
	r.Post("/tasks/{guid}", e.h.Update)
	r.Post("/tasks/{guid}/complete", e.h.Complete)
	r.Post("/tasks/{guid}/reopen", e.h.Reopen)
	r.Post("/tasks/{guid}/delete", e.h.Delete)
	r.Get("/calendar/agenda", e.cal.AgendaView)
	return r
}

func (e *taskEnv) token(userID string) string {
	var tok string
	probe := middleware.InjectCSRF(e.secret)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		tok = middleware.CSRFTokenFromContext(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	probe.ServeHTTP(httptest.NewRecorder(), r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com")))
	return tok
}

func (e *taskEnv) get(userID, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.router(userID).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// post sends a form with a valid CSRF token for userID.
func (e *taskEnv) post(userID, path string, form url.Values) *httptest.ResponseRecorder {
	return e.postToken(userID, path, form, e.token(userID))
}

func (e *taskEnv) postToken(userID, path string, form url.Values, token string) *httptest.ResponseRecorder {
	if form == nil {
		form = url.Values{}
	}
	if token != "" {
		form.Set("_csrf", token)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.router(userID).ServeHTTP(w, req)
	return w
}

// seed stores a task with ical_raw built the way the web builds it.
func (e *taskEnv) seed(t *testing.T, task *bongov1.Task) *bongov1.Task {
	t.Helper()
	if task.Guid == "" {
		task.Guid = store.NewTaskGUID()
	}
	if task.CalendarId == "" {
		task.CalendarId = store.TasksCalendarGUID
	}
	raw, err := icalevent.BuildTodo(task, false, e.now)
	if err != nil {
		t.Fatal(err)
	}
	task.IcalRaw = raw
	if _, err := e.tasks.Create(taskUser, task); err != nil {
		t.Fatal(err)
	}
	return task
}

func (e *taskEnv) seedRaw(t *testing.T, task *bongov1.Task, raw string) {
	t.Helper()
	task.IcalRaw = []byte(strings.ReplaceAll(raw, "\n", "\r\n"))
	if task.CalendarId == "" {
		task.CalendarId = store.TasksCalendarGUID
	}
	if _, err := e.tasks.Create(taskUser, task); err != nil {
		t.Fatal(err)
	}
}

func (e *taskEnv) stored(t *testing.T, guid string) *bongov1.Task {
	t.Helper()
	task, err := e.tasks.Get(taskUser, guid)
	if err != nil {
		t.Fatalf("Get(%s): %v", guid, err)
	}
	return task
}

func order(t *testing.T, body string, names ...string) {
	t.Helper()
	last := -1
	for _, n := range names {
		i := strings.Index(body, ">"+n+"<")
		if i < 0 {
			t.Fatalf("%q not in page", n)
		}
		if i < last {
			t.Fatalf("%q is out of order in %v", n, names)
		}
		last = i
	}
}

func TestTasksListOrder(t *testing.T) {
	e := newTaskEnv(t)
	day := func(d int) int64 { return time.Date(2026, 6, d, 0, 0, 0, 0, time.UTC).Unix() }
	dateRaw := func(guid, due string) string {
		return "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//T//EN\nBEGIN:VTODO\nUID:" + guid + "\nDTSTAMP:20260101T000000Z\nSUMMARY:x\nDUE;VALUE=DATE:" + due + "\nEND:VTODO\nEND:VCALENDAR\n"
	}
	e.seed(t, &bongov1.Task{Summary: "e-undated"})
	e.seed(t, &bongov1.Task{Summary: "d-upcoming", Due: taskNow.Add(48 * time.Hour).Unix()})
	e.seed(t, &bongov1.Task{Summary: "b-overdue-late", Due: taskNow.Add(-time.Hour).Unix()})
	e.seed(t, &bongov1.Task{Summary: "a-overdue-early", Due: day(10) + 9*3600})
	e.seed(t, &bongov1.Task{Summary: "c1-same-prio-low", Due: taskNow.Add(24 * time.Hour).Unix(), Priority: 9})
	e.seed(t, &bongov1.Task{Summary: "c0-same-prio-high", Due: taskNow.Add(24 * time.Hour).Unix(), Priority: 1})
	e.seed(t, &bongov1.Task{Summary: "c2-same-prio-none", Due: taskNow.Add(24 * time.Hour).Unix()})
	// Due today as a DATE: not overdue until tomorrow. Yesterday as a DATE: overdue.
	e.seedRaw(t, &bongov1.Task{Guid: "today", Summary: "c-date-today", Due: day(15)}, dateRaw("today", "20260615"))
	e.seedRaw(t, &bongov1.Task{Guid: "yday", Summary: "a2-date-yesterday", Due: day(14)}, dateRaw("yday", "20260614"))
	e.seed(t, &bongov1.Task{Summary: "z-done", Status: "COMPLETED", Completed: day(1)})
	e.seed(t, &bongov1.Task{Summary: "y-cancelled", Status: "CANCELLED"})

	body := e.get(taskUser, "/tasks").Body.String()
	order(t, body, "a-overdue-early", "a2-date-yesterday", "b-overdue-late", "c-date-today")
	order(t, body, "c-date-today", "c0-same-prio-high", "c1-same-prio-low", "c2-same-prio-none", "d-upcoming", "e-undated")
	for _, hidden := range []string{"z-done", "y-cancelled"} {
		if strings.Contains(body, ">"+hidden+"<") {
			t.Errorf("%s shown among open tasks", hidden)
		}
	}
	// c-date-today must not carry the overdue marker; a2-date-yesterday must.
	row := func(guid string) string {
		i := strings.Index(body, `data-task-guid="`+guid+`"`)
		j := strings.Index(body[i:], "</li>")
		return body[i : i+j]
	}
	if strings.Contains(row("today"), "Overdue") {
		t.Error("a task due today as a DATE is marked overdue")
	}
	if !strings.Contains(row("yday"), "Overdue") {
		t.Error("a task due yesterday as a DATE is not marked overdue")
	}

	done := e.get(taskUser, "/tasks?show=completed").Body.String()
	order(t, done, "z-done", "y-cancelled")
	if strings.Contains(done, ">e-undated<") {
		t.Error("open task shown under Completed")
	}
}

func TestTasksListOverdueUsesUserZone(t *testing.T) {
	e := newTaskEnv(t)
	la := mustZone(t, "America/Los_Angeles")
	e.h.SetZoneSource(fixedZone{la})
	// 2026-06-15 12:00 UTC is 05:00 on the 15th in Los Angeles. A DATE of the
	// 15th is due today there; a server in UTC reading "now" would agree, but
	// a date shifted by the zone (the 14th) would be overdue.
	e.seedRaw(t, &bongov1.Task{Guid: "d15", Summary: "date-15", Due: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC).Unix()},
		"BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//T//EN\nBEGIN:VTODO\nUID:d15\nDTSTAMP:20260101T000000Z\nDUE;VALUE=DATE:20260615\nEND:VTODO\nEND:VCALENDAR\n")
	body := e.get(taskUser, "/tasks").Body.String()
	if !strings.Contains(body, "2026-06-15") || strings.Contains(body, "Overdue") {
		t.Errorf("date-only task shifted or overdue in a negative zone:\n%s", body)
	}
}

func TestTasksCreate(t *testing.T) {
	e := newTaskEnv(t)
	w := e.post(taskUser, "/tasks", url.Values{"summary": {" Pay rent\x07 "}, "description": {"line1\r\nline2"}, "priority": {"3"}, "due_date": {"2026-07-01"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/tasks" {
		t.Fatalf("create = %d %q", w.Code, w.Header().Get("Location"))
	}
	all, _ := e.tasks.ListAll(taskUser)
	if len(all) != 1 {
		t.Fatalf("want 1 task, got %d", len(all))
	}
	task := all[0]
	if task.Summary != "Pay rent" || task.Description != "line1\nline2" || task.Priority != 3 || task.CalendarId != store.TasksCalendarGUID {
		t.Errorf("task = %+v", task)
	}
	if task.Due != time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("date-only due = %d", task.Due)
	}
	raw := string(task.IcalRaw)
	for _, want := range []string{"BEGIN:VTODO", "UID:" + task.Guid, "DUE;VALUE=DATE:20260701", "STATUS:NEEDS-ACTION", "PRIORITY:3", "DTSTAMP:20260615T120000Z"} {
		if !strings.Contains(raw, want) {
			t.Errorf("ical_raw lacks %q:\n%s", want, raw)
		}
	}
	// ical_raw and the structured fields agree: CalDAV reads the former.
	cal, err := ical.NewDecoder(bytes.NewReader(task.IcalRaw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	back := icalevent.TodoToTask(cal, task.Guid, task.CalendarId)
	if back.Summary != task.Summary || back.Due != task.Due || back.Priority != task.Priority {
		t.Errorf("raw and fields differ: %+v vs %+v", back, task)
	}
}

func TestTasksCreateTimedDueReadInUserZone(t *testing.T) {
	e := newTaskEnv(t)
	la := mustZone(t, "America/Los_Angeles")
	e.h.SetZoneSource(fixedZone{la})
	w := e.post(taskUser, "/tasks", url.Values{"summary": {"call"}, "due_date": {"2026-07-01"}, "due_time": {"09:30"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", w.Code)
	}
	all, _ := e.tasks.ListAll(taskUser)
	if want := time.Date(2026, 7, 1, 16, 30, 0, 0, time.UTC).Unix(); all[0].Due != want {
		t.Errorf("due = %d, want %d", all[0].Due, want)
	}
	if !strings.Contains(string(all[0].IcalRaw), "DUE:20260701T163000Z") {
		t.Errorf("raw = %s", all[0].IcalRaw)
	}
}

func TestTasksCreateCreatesTasksCalendarAndRejectsBadCalendar(t *testing.T) {
	e := newTaskEnv(t)
	// A user with no calendars at all (Tasks deleted): the POST creates it.
	other := "u-nocal"
	req := func(form url.Values) *httptest.ResponseRecorder {
		return e.post(other, "/tasks", form)
	}
	if w := req(url.Values{"summary": {"first"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", w.Code)
	}
	cal, err := e.cals.Get(other, store.TasksCalendarGUID)
	if err != nil || !store.CalendarAllows(cal, store.ComponentTodo) {
		t.Fatalf("tasks calendar not created: %v %+v", err, cal)
	}
	// An events-only calendar, another user's calendar and a made-up ID are
	// all refused, and nothing is stored.
	if _, err := e.cals.Create(other, &bongov1.Calendar{Guid: "events", Name: "Events"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.cals.Create(taskUser, &bongov1.Calendar{Guid: "mine-only", Name: "Mine", Components: []string{store.ComponentTodo}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"events", "mine-only", "nope"} {
		if w := req(url.Values{"summary": {"bad"}, "calendar_id": {id}}); w.Code != http.StatusBadRequest {
			t.Errorf("calendar_id %q = %d, want 400", id, w.Code)
		}
	}
	if all, _ := e.tasks.ListAll(other); len(all) != 1 {
		t.Errorf("want only the first task, got %d", len(all))
	}
	// A user's own VTODO calendar is accepted.
	if w := e.post(taskUser, "/tasks", url.Values{"summary": {"ok"}, "calendar_id": {"mine-only"}}); w.Code != http.StatusSeeOther {
		t.Errorf("own VTODO calendar = %d", w.Code)
	}
}

func TestTasksFormValidation(t *testing.T) {
	e := newTaskEnv(t)
	long := func(n int) string { return strings.Repeat("é", n) }
	tests := []struct {
		name string
		form url.Values
		want int
	}{
		{"summary required", url.Values{"summary": {"   "}}, 400},
		{"summary only control chars", url.Values{"summary": {"\x01\x02"}}, 400},
		{"summary at cap", url.Values{"summary": {long(maxTaskSummary)}}, 303},
		{"summary over cap", url.Values{"summary": {long(maxTaskSummary + 1)}}, 400},
		{"description at cap", url.Values{"summary": {"s"}, "description": {strings.Repeat("a", maxTaskDescription)}}, 303},
		{"description over cap", url.Values{"summary": {"s"}, "description": {strings.Repeat("a", maxTaskDescription+1)}}, 400},
		{"priority 9", url.Values{"summary": {"s"}, "priority": {"9"}}, 303},
		{"priority 10", url.Values{"summary": {"s"}, "priority": {"10"}}, 400},
		{"priority text", url.Values{"summary": {"s"}, "priority": {"high"}}, 400},
		{"bad date", url.Values{"summary": {"s"}, "due_date": {"2026-02-30"}}, 400},
		{"time without date", url.Values{"summary": {"s"}, "due_time": {"09:00"}}, 400},
		{"bad time", url.Values{"summary": {"s"}, "due_date": {"2026-02-01"}, "due_time": {"25:00"}}, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, _ := e.tasks.ListAll(taskUser)
			w := e.post(taskUser, "/tasks", tt.form)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.want, w.Body)
			}
			after, _ := e.tasks.ListAll(taskUser)
			if wrote := len(after) - len(before); (tt.want == 303) != (wrote == 1) {
				t.Errorf("stored %d new tasks for status %d", wrote, w.Code)
			}
			if tt.want == 400 && strings.Contains(w.Body.String(), "ééé") {
				t.Error("error body echoes input")
			}
		})
	}
}

func TestTasksCompleteReopenRoundTrip(t *testing.T) {
	e := newTaskEnv(t)
	task := e.seed(t, &bongov1.Task{Summary: "ship", Due: taskNow.Add(time.Hour).Unix()})
	path := "/tasks/" + task.Guid

	if w := e.post(taskUser, path+"/complete", nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/tasks" {
		t.Fatalf("complete = %d", w.Code)
	}
	got := e.stored(t, task.Guid)
	if got.Status != "COMPLETED" || got.Completed != taskNow.Unix() || got.Percent != 100 {
		t.Errorf("structured fields = %+v", got)
	}
	raw := string(got.IcalRaw)
	for _, want := range []string{"STATUS:COMPLETED", "COMPLETED:20260615T120000Z", "PERCENT-COMPLETE:100"} {
		if !strings.Contains(raw, want) {
			t.Errorf("after complete, raw lacks %q:\n%s", want, raw)
		}
	}
	if !strings.Contains(e.get(taskUser, "/tasks?show=completed").Body.String(), ">ship<") {
		t.Error("completed task not under Completed")
	}

	if w := e.post(taskUser, path+"/reopen", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("reopen = %d", w.Code)
	}
	got = e.stored(t, task.Guid)
	if got.Completed != 0 || got.Status == "COMPLETED" || got.Percent != 0 {
		t.Errorf("after reopen = %+v", got)
	}
	raw = string(got.IcalRaw)
	if strings.Contains(raw, "COMPLETED") || strings.Contains(raw, "PERCENT-COMPLETE:100") {
		t.Errorf("after reopen, raw still holds completion:\n%s", raw)
	}
	if !strings.Contains(raw, "STATUS:NEEDS-ACTION") {
		t.Errorf("reopened raw lacks NEEDS-ACTION:\n%s", raw)
	}
}

func TestTasksCompleteReopenIdempotent(t *testing.T) {
	e := newTaskEnv(t)
	open := e.seed(t, &bongov1.Task{Summary: "open"})
	done := e.seed(t, &bongov1.Task{Summary: "done", Status: "COMPLETED", Completed: 1000, Percent: 100})
	e.applier.failing.Store(true)
	// With every write failing, a no-op must still succeed and a real change
	// must not.
	if w := e.post(taskUser, "/tasks/"+open.Guid+"/reopen", nil); w.Code != http.StatusSeeOther {
		t.Errorf("reopen of an open task = %d, want a no-op 303", w.Code)
	}
	if w := e.post(taskUser, "/tasks/"+done.Guid+"/complete", nil); w.Code != http.StatusSeeOther {
		t.Errorf("complete of a completed task = %d, want a no-op 303", w.Code)
	}
	if w := e.post(taskUser, "/tasks/"+open.Guid+"/complete", nil); w.Code != http.StatusInternalServerError {
		t.Errorf("complete with failing writes = %d, want 500", w.Code)
	}
	e.applier.failing.Store(false)
	if got := e.stored(t, done.Guid); got.Completed != 1000 {
		t.Errorf("completed time rewritten: %d", got.Completed)
	}
}

func TestTasksMutationsFailWhenWriteFails(t *testing.T) {
	tests := []struct {
		name string
		path func(guid string) string
		form url.Values
		seed *bongov1.Task
	}{
		{"create", func(string) string { return "/tasks" }, url.Values{"summary": {"new"}}, nil},
		{"edit", func(g string) string { return "/tasks/" + g }, url.Values{"summary": {"changed"}}, &bongov1.Task{Summary: "orig"}},
		{"complete", func(g string) string { return "/tasks/" + g + "/complete" }, nil, &bongov1.Task{Summary: "orig"}},
		{"reopen", func(g string) string { return "/tasks/" + g + "/reopen" }, nil, &bongov1.Task{Summary: "orig", Status: "COMPLETED", Completed: 5, Percent: 100}},
		{"delete", func(g string) string { return "/tasks/" + g + "/delete" }, nil, &bongov1.Task{Summary: "orig"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTaskEnv(t)
			guid := ""
			var before *bongov1.Task
			if tt.seed != nil {
				before = e.seed(t, tt.seed)
				guid = before.Guid
			}
			e.applier.failing.Store(true)
			w := e.post(taskUser, tt.path(guid), tt.form)
			if w.Code < 500 || w.Header().Get("Location") != "" {
				t.Fatalf("status = %d, Location = %q; want a 5xx and no redirect", w.Code, w.Header().Get("Location"))
			}
			if body := w.Body.String(); strings.Contains(body, "injected") || !strings.Contains(body, "could not be saved") {
				t.Errorf("body = %q, want the translated message only", body)
			}
			e.applier.failing.Store(false)
			all, _ := e.tasks.ListAll(taskUser)
			switch {
			case before == nil && len(all) != 0:
				t.Error("a task was stored although the write failed")
			case before != nil && (len(all) != 1 || all[0].Summary != "orig" || all[0].Status != before.Status):
				t.Errorf("task changed although the write failed: %+v", all)
			}
		})
	}
}

func TestTasksDeleteAndNotFound(t *testing.T) {
	e := newTaskEnv(t)
	mine := e.seed(t, &bongov1.Task{Summary: "mine"})
	if w := e.post(taskUser, "/tasks/"+mine.Guid+"/delete", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", w.Code)
	}
	if _, err := e.tasks.Get(taskUser, mine.Guid); !errors.Is(err, store.ErrTaskNotFound) {
		t.Errorf("task still stored: %v", err)
	}
	if w := e.post(taskUser, "/tasks/"+mine.Guid+"/delete", nil); w.Code != http.StatusNotFound {
		t.Errorf("delete of a missing task = %d, want 404", w.Code)
	}
}

func TestTasksOtherUsersTaskIs404(t *testing.T) {
	e := newTaskEnv(t)
	theirs := e.seed(t, &bongov1.Task{Summary: "private", Due: taskNow.Unix() + 60})
	if w := e.get(taskOther, "/tasks/"+theirs.Guid); w.Code != http.StatusNotFound {
		t.Errorf("GET edit = %d, want 404", w.Code)
	}
	for _, p := range []struct {
		path string
		form url.Values
	}{
		{"/tasks/" + theirs.Guid, url.Values{"summary": {"hijacked"}}},
		{"/tasks/" + theirs.Guid + "/complete", nil},
		{"/tasks/" + theirs.Guid + "/reopen", nil},
		{"/tasks/" + theirs.Guid + "/delete", nil},
	} {
		if w := e.post(taskOther, p.path, p.form); w.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p.path, w.Code)
		}
	}
	if got := e.stored(t, theirs.Guid); got.Summary != "private" || got.Status == "COMPLETED" {
		t.Errorf("task modified by another user: %+v", got)
	}
	if body := e.get(taskOther, "/tasks").Body.String(); strings.Contains(body, "private") {
		t.Error("another user's task listed")
	}
	// An owner parameter in the form or query changes nothing.
	if w := e.post(taskOther, "/tasks/"+theirs.Guid+"/delete?user="+taskUser, url.Values{"user": {taskUser}, "owner": {taskUser}}); w.Code != http.StatusNotFound {
		t.Errorf("owner parameter = %d, want 404", w.Code)
	}
}

func TestTasksRequireCSRF(t *testing.T) {
	e := newTaskEnv(t)
	task := e.seed(t, &bongov1.Task{Summary: "guarded"})
	paths := []string{"/tasks", "/tasks/" + task.Guid, "/tasks/" + task.Guid + "/complete", "/tasks/" + task.Guid + "/reopen", "/tasks/" + task.Guid + "/delete"}
	for _, p := range paths {
		for name, tok := range map[string]string{"missing": "", "invalid": "not-a-token"} {
			w := e.postToken(taskUser, p, url.Values{"summary": {"x"}}, tok)
			if w.Code != http.StatusForbidden || !strings.Contains(strings.ToLower(w.Body.String()), "csrf") {
				t.Errorf("POST %s with %s token = %d %q", p, name, w.Code, w.Body)
			}
		}
	}
	if got := e.stored(t, task.Guid); got.Summary != "guarded" || got.Status != "" {
		t.Errorf("task changed behind CSRF: %+v", got)
	}
	if all, _ := e.tasks.ListAll(taskUser); len(all) != 1 {
		t.Errorf("want 1 task, got %d", len(all))
	}
}

const richTodo = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Client//EN
BEGIN:VTODO
UID:rich
DTSTAMP:20260101T000000Z
SUMMARY:Old
DUE;TZID=Europe/Berlin:20260701T093015
PRIORITY:2
RRULE:FREQ=WEEKLY
CATEGORIES:home,money
RELATED-TO:parent-1
X-APPLE-SORT-ORDER:42
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT15M
DESCRIPTION:Reminder
END:VALARM
END:VTODO
END:VCALENDAR
`

func TestTasksEditPreservesUnmodelledProperties(t *testing.T) {
	e := newTaskEnv(t)
	berlin := mustZone(t, "Europe/Berlin")
	e.h.SetZoneSource(fixedZone{berlin})
	due := time.Date(2026, 7, 1, 9, 30, 15, 0, berlin).Unix()
	e.seedRaw(t, &bongov1.Task{Guid: "rich", Summary: "Old", Due: due, Priority: 2}, richTodo)

	// The form shows 09:30 (no seconds). Resubmitting that value must leave
	// DUE exactly as the client wrote it, seconds and TZID included.
	w := e.post(taskUser, "/tasks/rich", url.Values{"summary": {"New"}, "description": {"notes"}, "priority": {"2"}, "due_date": {"2026-07-01"}, "due_time": {"09:30"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d %s", w.Code, w.Body)
	}
	got := e.stored(t, "rich")
	raw := strings.ReplaceAll(string(got.IcalRaw), "\r\n", "\n")
	for _, want := range []string{
		"SUMMARY:New", "DESCRIPTION:notes", "DUE;TZID=Europe/Berlin:20260701T093015", "PRIORITY:2", "RRULE:FREQ=WEEKLY",
		"CATEGORIES:home,money", "RELATED-TO:parent-1", "X-APPLE-SORT-ORDER:42", "BEGIN:VALARM", "TRIGGER:-PT15M", "SEQUENCE:1",
		"LAST-MODIFIED:20260615T120000Z", "DTSTAMP:20260615T120000Z",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("edited raw lacks %q:\n%s", want, raw)
		}
	}
	if got.Due != due || got.Summary != "New" || got.Description != "notes" {
		t.Errorf("structured fields = %+v", got)
	}

	// Completing keeps them too.
	if w := e.post(taskUser, "/tasks/rich/complete", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("complete = %d", w.Code)
	}
	raw = string(e.stored(t, "rich").IcalRaw)
	for _, want := range []string{"BEGIN:VALARM", "X-APPLE-SORT-ORDER:42", "RRULE:FREQ=WEEKLY", "STATUS:COMPLETED"} {
		if !strings.Contains(raw, want) {
			t.Errorf("completed raw lacks %q", want)
		}
	}

	// A changed due date is written in UTC.
	if w := e.post(taskUser, "/tasks/rich", url.Values{"summary": {"New"}, "description": {"notes"}, "priority": {"2"}, "due_date": {"2026-07-02"}, "due_time": {"10:00"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d", w.Code)
	}
	if raw := string(e.stored(t, "rich").IcalRaw); !strings.Contains(raw, "DUE:20260702T080000Z") {
		t.Errorf("changed due not written in UTC:\n%s", raw)
	}
}

func TestTasksEditUnreadableRawFallsBackToFields(t *testing.T) {
	e := newTaskEnv(t)
	e.seedRaw(t, &bongov1.Task{Guid: "broken", Summary: "Old"}, "this is not icalendar")
	if w := e.post(taskUser, "/tasks/broken", url.Values{"summary": {"New"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d", w.Code)
	}
	got := e.stored(t, "broken")
	if got.Summary != "New" || got.IcalRaw != nil {
		t.Errorf("task = %+v, want new summary and no raw", got)
	}
}

func TestTasksSubscribedCalendarIsReadOnly(t *testing.T) {
	e := newTaskEnv(t)
	if _, err := e.cals.Create(taskUser, &bongov1.Calendar{
		Guid: "feed", Name: "Feed", Components: []string{store.ComponentTodo},
		Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://feed.example/x.ics"},
	}); err != nil {
		t.Fatal(err)
	}
	task := e.seed(t, &bongov1.Task{Summary: "feed task", CalendarId: "feed"})
	for _, p := range []string{"", "/complete", "/reopen", "/delete"} {
		if w := e.post(taskUser, "/tasks/"+task.Guid+p, url.Values{"summary": {"x"}}); w.Code != http.StatusForbidden {
			t.Errorf("POST %q = %d, want 403", p, w.Code)
		}
	}
	if got := e.stored(t, task.Guid); got.Summary != "feed task" || got.Status != "" {
		t.Errorf("subscribed task changed: %+v", got)
	}
}

type fixedQuota struct{ over bool }

func (q fixedQuota) RecipientWouldExceedQuota(string, int64) (bool, error) { return q.over, nil }

func TestTasksQuota(t *testing.T) {
	e := newTaskEnv(t)
	task := e.seed(t, &bongov1.Task{Summary: "a fairly long summary to shrink", Description: strings.Repeat("d", 200)})
	e.h.SetQuotaChecker(fixedQuota{over: true})

	if w := e.post(taskUser, "/tasks", url.Values{"summary": {"new"}}); w.Code != http.StatusInsufficientStorage {
		t.Errorf("create over quota = %d, want 507", w.Code)
	}
	if w := e.post(taskUser, "/tasks/"+task.Guid, url.Values{"summary": {"a fairly long summary to shrink"}, "description": {strings.Repeat("d", 4000)}}); w.Code != http.StatusInsufficientStorage {
		t.Errorf("growing edit over quota = %d, want 507", w.Code)
	}
	if w := e.post(taskUser, "/tasks/"+task.Guid, url.Values{"summary": {"short"}}); w.Code != http.StatusSeeOther {
		t.Errorf("shrinking edit over quota = %d, want 303", w.Code)
	}
	if w := e.post(taskUser, "/tasks/"+task.Guid+"/delete", nil); w.Code != http.StatusSeeOther {
		t.Errorf("delete over quota = %d, want 303", w.Code)
	}
	if all, _ := e.tasks.ListAll(taskUser); len(all) != 0 {
		t.Errorf("want only the delete to land, got %d tasks", len(all))
	}
}

func TestAgendaShowsDueTasks(t *testing.T) {
	e := newTaskEnv(t)
	e.seed(t, &bongov1.Task{Summary: "due-timed", Due: time.Date(2026, 6, 20, 14, 0, 0, 0, time.UTC).Unix()})
	e.seedRaw(t, &bongov1.Task{Guid: "d", Summary: "due-date", Due: time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC).Unix()},
		"BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//T//EN\nBEGIN:VTODO\nUID:d\nDTSTAMP:20260101T000000Z\nDUE;VALUE=DATE:20260621\nEND:VTODO\nEND:VCALENDAR\n")
	e.seed(t, &bongov1.Task{Summary: "no-due"})
	e.seed(t, &bongov1.Task{Summary: "finished", Due: time.Date(2026, 6, 20, 15, 0, 0, 0, time.UTC).Unix(), Status: "COMPLETED", Completed: 1})
	e.seed(t, &bongov1.Task{Summary: "beyond-range", Due: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix()})

	body := e.get(taskUser, "/calendar/agenda?from=2026-06-15").Body.String()
	for _, want := range []string{">due-timed<", ">due-date<"} {
		if !strings.Contains(body, want) {
			t.Errorf("agenda lacks %s", want)
		}
	}
	for _, not := range []string{">no-due<", ">finished<", ">beyond-range<"} {
		if strings.Contains(body, not) {
			t.Errorf("agenda holds %s", not)
		}
	}
	if !strings.Contains(body, `data-agenda-date="2026-06-21"`) {
		t.Error("date-only task not on its own date")
	}
	if strings.Contains(e.get(taskOther, "/calendar/agenda?from=2026-06-15").Body.String(), "due-timed") {
		t.Error("another user's task in the agenda")
	}
}

func TestAgendaDateOnlyTaskKeepsItsDateWestOfUTC(t *testing.T) {
	la := mustZone(t, "America/Los_Angeles")
	day := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	task := &bongov1.Task{
		Guid: "d", Summary: "dated", Due: day.Unix(),
		IcalRaw: []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\nUID:d\r\nDUE;VALUE=DATE:20260621\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"),
	}
	days := buildAgenda(time.Date(2026, 6, 15, 0, 0, 0, 0, la), 30, nil, []*bongov1.Task{task}, nil, la)
	if len(days) != 1 || days[0].Date.Format("2006-01-02") != "2026-06-21" || days[0].Items[0].TimeLabel != "" {
		t.Fatalf("days = %+v", days)
	}
}

func TestTasksPagesCarryCSRFOnEveryForm(t *testing.T) {
	e := newTaskEnv(t)
	e.seed(t, &bongov1.Task{Guid: "open1", Summary: "open"})
	e.seed(t, &bongov1.Task{Guid: "done1", Summary: "done", Status: "COMPLETED", Completed: 1})
	for _, path := range []string{"/tasks", "/tasks?show=completed", "/tasks/open1"} {
		body := e.get(taskUser, path).Body.String()
		forms := strings.Count(body, `<form method="post" action="/tasks`)
		if forms == 0 || strings.Count(body, `name="_csrf"`) < forms {
			t.Errorf("%s: %d forms, %d csrf fields", path, forms, strings.Count(body, `name="_csrf"`))
		}
	}
	if body := e.get(taskUser, "/tasks").Body.String(); !strings.Contains(body, `href="/tasks?show=completed"`) {
		t.Error("list lacks the Completed filter link")
	}
}

func TestTasksEditPageEscapesGuid(t *testing.T) {
	e := newTaskEnv(t)
	e.seedRaw(t, &bongov1.Task{Guid: "a b&c", Summary: "odd guid"}, "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//T//EN\nBEGIN:VTODO\nUID:a b&c\nDTSTAMP:20260101T000000Z\nEND:VTODO\nEND:VCALENDAR\n")
	body := e.get(taskUser, "/tasks").Body.String()
	if !strings.Contains(body, "/tasks/a%20b&amp;c/complete") && !strings.Contains(body, "/tasks/a%20b&c/complete") {
		t.Errorf("guid not path-escaped:\n%s", body)
	}
}

// A flash parameter is not rendered on the task list: no task handler
// redirects with one, so the text would be arbitrary.
func TestTasksListIgnoresFlashParameter(t *testing.T) {
	e := newTaskEnv(t)
	const probe = "Your account has been suspended call 555-0100"
	body := e.get(taskUser, "/tasks?flash="+url.QueryEscape(probe)).Body.String()
	if strings.Contains(body, "suspended") {
		t.Errorf("flash text rendered on /tasks:\n%s", body)
	}
}

// A task created over CalDAV can have a summary past the web form's cap. A
// change that leaves the summary alone must still save; a longer or different
// over-long summary must not.
func TestTasksUpdateCapAppliesOnlyWhenSummaryChanges(t *testing.T) {
	e := newTaskEnv(t)
	long := strings.Repeat("é", maxTaskSummary+45)
	task := e.seed(t, &bongov1.Task{Summary: long})

	due := url.Values{"summary": {long}, "due_date": {"2026-07-01"}}
	if w := e.post(taskUser, "/tasks/"+task.Guid, due); w.Code != http.StatusSeeOther {
		t.Fatalf("due-date-only edit of an over-long summary = %d: %s", w.Code, w.Body)
	}
	got := e.stored(t, task.Guid)
	if got.Summary != long || got.Due == 0 {
		t.Errorf("stored summary kept = %v, due = %d", got.Summary == long, got.Due)
	}

	changed := url.Values{"summary": {long + "x"}, "due_date": {"2026-07-01"}}
	if w := e.post(taskUser, "/tasks/"+task.Guid, changed); w.Code != http.StatusBadRequest {
		t.Errorf("changing an over-long summary = %d, want 400", w.Code)
	}
	if got := e.stored(t, task.Guid); got.Summary != long {
		t.Error("a refused edit changed the summary")
	}
}

// Tasks in a subscribed (read-only) calendar answer 403 to Complete, Reopen
// and Delete, so the list does not offer them.
func TestTasksListHidesActionsForReadOnlyCalendar(t *testing.T) {
	e := newTaskEnv(t)
	if _, err := e.cals.Create(taskUser, &bongov1.Calendar{
		Guid: "feed", Name: "Feed", Components: []string{store.ComponentTodo},
		Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://feed.example/x.ics"},
	}); err != nil {
		t.Fatal(err)
	}
	ro := e.seed(t, &bongov1.Task{Summary: "feed task", CalendarId: "feed"})
	rw := e.seed(t, &bongov1.Task{Summary: "mine"})
	done := e.seed(t, &bongov1.Task{Summary: "feed done", CalendarId: "feed", Status: "COMPLETED", Completed: 1})

	open := e.get(taskUser, "/tasks").Body.String()
	for _, a := range []string{"complete", "delete"} {
		if strings.Contains(open, "/tasks/"+ro.Guid+"/"+a) {
			t.Errorf("read-only task offers %s", a)
		}
		if !strings.Contains(open, "/tasks/"+rw.Guid+"/"+a) {
			t.Errorf("writable task lacks %s", a)
		}
	}
	if !strings.Contains(open, ">feed task<") {
		t.Error("read-only task not listed")
	}
	completed := e.get(taskUser, "/tasks?show=completed").Body.String()
	for _, a := range []string{"reopen", "delete"} {
		if strings.Contains(completed, "/tasks/"+done.Guid+"/"+a) {
			t.Errorf("read-only completed task offers %s", a)
		}
	}
	if !strings.Contains(completed, ">feed done<") {
		t.Error("read-only completed task not listed")
	}
}
