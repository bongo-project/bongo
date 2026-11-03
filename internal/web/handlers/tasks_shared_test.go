package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	taskShareOwner  = "u-task-owner"
	taskShareColour = "#EF4444"
)

// sharedTaskEnv is a taskEnv whose viewer (taskUser) can be granted the
// owner's calendars. The owner holds the Tasks calendar and a second one.
type sharedTaskEnv struct {
	*taskEnv
	acl *store.ACLStore
}

func newSharedTaskEnv(t *testing.T) *sharedTaskEnv {
	t.Helper()
	e := newTaskEnv(t)
	s := &sharedTaskEnv{taskEnv: e, acl: store.NewACLStore(e.ps, &directApplierTags{ps: e.ps})}
	e.h.SetACL(s.acl)
	e.cal.SetACL(s.acl)
	tc := store.NewTasksCalendar()
	tc.Color = taskShareColour
	if err := e.cals.Update(taskShareOwner, store.TasksCalendarGUID, tc); err != nil {
		t.Fatal(err)
	}
	if err := e.cals.Update(taskShareOwner, "other", &bongov1.Calendar{Guid: "other", Name: "Other", Components: []string{"VTODO"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *sharedTaskEnv) grant(t *testing.T, cal, level, state string) {
	t.Helper()
	if err := s.acl.SetCalendarGrant(taskShareOwner, cal, taskUser, level, state, taskShareOwner, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
}

// ownerTask stores an open task due the day after taskNow in the owner's
// key space. cal "" stores an empty CalendarId.
func (s *sharedTaskEnv) ownerTask(t *testing.T, owner, guid, summary, cal string) {
	t.Helper()
	task := &bongov1.Task{Guid: guid, Summary: summary, CalendarId: cal, Due: taskNow.Add(24 * time.Hour).Unix()}
	raw, err := icalevent.BuildTodo(task, false, taskNow)
	if err != nil {
		t.Fatal(err)
	}
	task.IcalRaw = raw
	if _, err := s.tasks.Create(owner, task); err != nil {
		t.Fatal(err)
	}
}

// taskRow returns the <li> carrying data-task-guid=guid, up to its first
// closing tag. It fails the test when there is none.
func taskRow(t *testing.T, body, guid string) string {
	t.Helper()
	i := strings.Index(body, `data-task-guid="`+guid+`"`)
	if i < 0 {
		t.Fatalf("no row for task %s in\n%s", guid, body)
	}
	start := strings.LastIndex(body[:i], "<li")
	end := strings.Index(body[i:], "</li>")
	if start < 0 || end < 0 {
		t.Fatalf("task %s row is not an li", guid)
	}
	return body[start : i+end]
}

func TestSharedTasksDisplay(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, s *sharedTaskEnv)
		visible bool
	}{
		{"read grant", func(t *testing.T, s *sharedTaskEnv) {
			s.grant(t, store.TasksCalendarGUID, store.GrantLevelRead, store.GrantStateAccepted)
		}, true},
		{"write grant", func(t *testing.T, s *sharedTaskEnv) {
			s.grant(t, store.TasksCalendarGUID, store.GrantLevelWrite, store.GrantStateAccepted)
		}, true},
		{"freebusy grant", func(t *testing.T, s *sharedTaskEnv) {
			s.grant(t, store.TasksCalendarGUID, store.GrantLevelFreeBusy, store.GrantStateAccepted)
		}, false},
		{"pending grant", func(t *testing.T, s *sharedTaskEnv) {
			s.grant(t, store.TasksCalendarGUID, store.GrantLevelRead, store.GrantStatePending)
		}, false},
		{"revoked grant", func(t *testing.T, s *sharedTaskEnv) {
			s.grant(t, store.TasksCalendarGUID, store.GrantLevelRead, store.GrantStateAccepted)
			if err := s.acl.RevokeCalendarGrant(taskShareOwner, store.TasksCalendarGUID, taskUser); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"grant on another calendar", func(t *testing.T, s *sharedTaskEnv) {
			s.grant(t, "other", store.GrantLevelRead, store.GrantStateAccepted)
		}, false},
		{"no grant", func(t *testing.T, s *sharedTaskEnv) {}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSharedTaskEnv(t)
			s.ownerTask(t, taskShareOwner, "shared-g", "shared-task", store.TasksCalendarGUID)
			tt.setup(t, s)
			for _, path := range []string{"/tasks", "/tasks?show=open", "/calendar/agenda?from=2026-06-15"} {
				w := s.get(taskUser, path)
				if w.Code != 200 {
					t.Fatalf("%s: status %d", path, w.Code)
				}
				body := w.Body.String()
				if got := strings.Contains(body, ">shared-task<"); got != tt.visible {
					t.Fatalf("%s: shared task visible = %v, want %v", path, got, tt.visible)
				}
				if !tt.visible {
					continue
				}
				// Scoped to the task's own li: the sidebar dot of the shared
				// calendar carries the same class. The viewer's default task
				// colour (blue) differs from the share colour (red), so a row
				// without the share colour cannot pass by accident.
				if row := taskRow(t, body, "shared-g"); !strings.Contains(row, templates.CalendarColourClass(taskShareColour)) {
					t.Errorf("%s: share colour class missing from the task row:\n%s", path, row)
				}
				if strings.Contains(body, "/tasks/shared-g") {
					t.Errorf("%s: shared task is linked or has a form", path)
				}
			}
		})
	}
}

func TestSharedTasksOnlyGrantedCalendar(t *testing.T) {
	s := newSharedTaskEnv(t)
	s.ownerTask(t, taskShareOwner, "in-g", "in-granted", store.TasksCalendarGUID)
	s.ownerTask(t, taskShareOwner, "out-g", "in-other", "other")
	s.grant(t, store.TasksCalendarGUID, store.GrantLevelRead, store.GrantStateAccepted)
	for _, path := range []string{"/tasks", "/calendar/agenda?from=2026-06-15"} {
		body := s.get(taskUser, path).Body.String()
		if !strings.Contains(body, ">in-granted<") {
			t.Errorf("%s: granted calendar's task missing", path)
		}
		if strings.Contains(body, ">in-other<") {
			t.Errorf("%s: task of an ungranted calendar leaked", path)
		}
	}
}

func TestSharedTasksEmptyCalendarIDIsTasksCalendar(t *testing.T) {
	s := newSharedTaskEnv(t)
	s.ownerTask(t, taskShareOwner, "empty-g", "no-cal-id", "")
	s.grant(t, store.TasksCalendarGUID, store.GrantLevelRead, store.GrantStateAccepted)
	if !strings.Contains(s.get(taskUser, "/tasks").Body.String(), ">no-cal-id<") {
		t.Fatal("task without a calendar id missing from the shared Tasks calendar")
	}
	// A grant on "default" must not match it.
	s2 := newSharedTaskEnv(t)
	s2.ownerTask(t, taskShareOwner, "empty-g", "no-cal-id", "")
	if err := s2.cals.Update(taskShareOwner, "default", &bongov1.Calendar{Guid: "default", Name: "Default"}); err != nil {
		t.Fatal(err)
	}
	s2.grant(t, "default", store.GrantLevelRead, store.GrantStateAccepted)
	if strings.Contains(s2.get(taskUser, "/tasks").Body.String(), ">no-cal-id<") {
		t.Fatal("task without a calendar id matched the default calendar")
	}
}

func TestSharedTasksGUIDCollision(t *testing.T) {
	s := newSharedTaskEnv(t)
	s.seed(t, &bongov1.Task{Guid: "dup", Summary: "own-dup"})
	s.ownerTask(t, taskShareOwner, "dup", "shared-dup", store.TasksCalendarGUID)
	s.grant(t, store.TasksCalendarGUID, store.GrantLevelRead, store.GrantStateAccepted)
	body := s.get(taskUser, "/tasks").Body.String()
	for _, want := range []string{">own-dup<", ">shared-dup<"} {
		if !strings.Contains(body, want) {
			t.Fatalf("%s missing", want)
		}
	}
	// Only the viewer's own row has links and forms for the guid.
	if n := strings.Count(body, `action="/tasks/dup/complete"`); n != 1 {
		t.Errorf("complete forms = %d, want 1 (own row only)", n)
	}
	if n := strings.Count(body, `href="/tasks/dup"`); n != 1 {
		t.Errorf("edit links = %d, want 1 (own row only)", n)
	}
	// Completed list: shared completed tasks are read-only too.
	agenda := s.get(taskUser, "/calendar/agenda?from=2026-06-15").Body.String()
	if !strings.Contains(agenda, ">shared-dup<") {
		t.Error("shared task missing from the agenda")
	}
	// The viewer's own "dup" has no due date and is not on the agenda, so any
	// link to /tasks/dup there would be the shared row pointing at it.
	if strings.Contains(agenda, `href="/tasks/dup"`) {
		t.Error("the shared agenda row links to the viewer's own /tasks/dup")
	}
}

func TestSharedTasksCompletedListReadOnly(t *testing.T) {
	s := newSharedTaskEnv(t)
	task := &bongov1.Task{Guid: "done-g", Summary: "shared-done", CalendarId: store.TasksCalendarGUID, Status: "COMPLETED", Completed: 1}
	raw, err := icalevent.BuildTodo(task, false, taskNow)
	if err != nil {
		t.Fatal(err)
	}
	task.IcalRaw = raw
	if _, err := s.tasks.Create(taskShareOwner, task); err != nil {
		t.Fatal(err)
	}
	s.grant(t, store.TasksCalendarGUID, store.GrantLevelWrite, store.GrantStateAccepted)
	body := s.get(taskUser, "/tasks?show=completed").Body.String()
	if !strings.Contains(body, ">shared-done<") {
		t.Fatal("shared completed task missing")
	}
	if strings.Contains(body, "/tasks/done-g") {
		t.Fatal("shared completed task has a link or reopen/delete form")
	}
}

func TestSharedTasksMutationsStayInOwnKeySpace(t *testing.T) {
	s := newSharedTaskEnv(t)
	s.ownerTask(t, taskShareOwner, "owner-g", "shared-task", store.TasksCalendarGUID)
	s.grant(t, store.TasksCalendarGUID, store.GrantLevelWrite, store.GrantStateAccepted)
	for _, action := range []string{"complete", "delete"} {
		if w := s.post(taskUser, "/tasks/owner-g/"+action, nil); w.Code != 404 {
			t.Errorf("%s of a shared task: status %d, want 404", action, w.Code)
		}
	}
	got, err := s.tasks.Get(taskShareOwner, "owner-g")
	if err != nil || got.Status == "COMPLETED" {
		t.Fatalf("owner's task changed: %v %v", got, err)
	}
}

func TestSharedTasksSummaryUpcomingSkipsThem(t *testing.T) {
	s := newSharedTaskEnv(t)
	s.ownerTask(t, taskShareOwner, "owner-g", "shared-task", store.TasksCalendarGUID)
	s.grant(t, store.TasksCalendarGUID, store.GrantLevelRead, store.GrantStateAccepted)
	cs := &calendarSummary{cal: s.cal}
	req := httptest.NewRequest(http.MethodGet, "/summary", nil)
	_, rows, err := cs.Upcoming(req, taskUser, taskUser+"@example.com", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Title == "shared-task" || row.Href == "/tasks/owner-g" {
			t.Fatalf("shared task linked from the summary: %+v", row)
		}
	}
}
