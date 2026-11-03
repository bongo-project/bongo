package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const delGrantee = "u-del-grantee"

type calDelEnv struct {
	*pubEnv
	tasks *store.TaskStore
	acl   *store.ACLStore
}

// newCalDelEnv gives pubOwner calendars default, tasks and work, an event and
// a task in work (and ones elsewhere), a read grant on work to delGrantee, and
// work published.
func newCalDelEnv(t *testing.T) *calDelEnv {
	t.Helper()
	e := &calDelEnv{pubEnv: newPubEnv(t)}
	app := &directApplierTags{ps: e.ps}
	e.tasks = store.NewTaskStore(e.ps, app)
	e.acl = store.NewACLStore(e.ps, app)
	e.cal.SetTaskStore(e.tasks)
	if _, err := e.calendars.Create(pubOwner, store.NewTasksCalendar()); err != nil {
		t.Fatal(err)
	}
	e.addCalendar(t, "work")
	for _, ev := range []*bongov1.Event{
		{Guid: "ev-work", CalendarId: "work", Title: "W", StartTime: 1000, EndTime: 2000},
		{Guid: "ev-def", CalendarId: "default", Title: "D", StartTime: 1000, EndTime: 2000},
	} {
		if _, err := e.events.Create(pubOwner, ev); err != nil {
			t.Fatal(err)
		}
	}
	for _, tk := range []*bongov1.Task{
		{Guid: "tk-work", CalendarId: "work", Summary: "W"},
		{Guid: "tk-tasks", Summary: "T"},
	} {
		if _, err := e.tasks.Create(pubOwner, tk); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.acl.SetCalendarGrant(pubOwner, "work", delGrantee, store.GrantLevelRead, store.GrantStateAccepted, pubOwner, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *calDelEnv) deleteAs(user, calID string, htmx bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/calendar/"+calID, nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req = withChiParam(withUserContext(req, user, user+"@example.com"), "calendarID", calID)
	w := httptest.NewRecorder()
	e.cal.DeleteCalendar(w, req)
	return w
}

func (e *calDelEnv) calendarExists(t *testing.T, owner, id string) bool {
	t.Helper()
	ok, err := e.calendars.Exists(owner, id)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestDeleteCalendar(t *testing.T) {
	e := newCalDelEnv(t)
	tok := e.publish(t, "work", "full")
	if w := e.get(pubOwner, "work", tok, "", nil); w.Code != http.StatusOK {
		t.Fatalf("feed before delete = %d", w.Code)
	}
	if lvl, _ := e.acl.CalendarAccessLevel(pubOwner, "work", delGrantee); lvl != store.GrantLevelRead {
		t.Fatalf("setup: grant level %q", lvl)
	}

	w := e.deleteAs(pubOwner, "work", true)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get("HX-Trigger") != "calendarRefresh" || w.Header().Get("HX-Refresh") != "true" {
		t.Errorf("headers = %v", w.Header())
	}
	if e.calendarExists(t, pubOwner, "work") {
		t.Error("calendar still there")
	}
	if _, err := e.events.Get(pubOwner, "ev-work"); err == nil {
		t.Error("event in work survives")
	}
	if _, err := e.events.Get(pubOwner, "ev-def"); err != nil {
		t.Errorf("event in default lost: %v", err)
	}
	tasks, _ := e.tasks.ListAll(pubOwner)
	if len(tasks) != 1 || tasks[0].Guid != "tk-tasks" {
		t.Errorf("tasks = %v, want only tk-tasks", tasks)
	}
	if lvl, _ := e.acl.CalendarAccessLevel(pubOwner, "work", delGrantee); lvl != "" {
		t.Errorf("grantee still has %q", lvl)
	}
	if n, _ := e.acl.CountCalendarGrants(pubOwner, "work"); n != 0 {
		t.Errorf("grant rows = %d, want 0", n)
	}
	if held, _ := e.acl.ListCalendarGrantsHeld(delGrantee); len(held) != 0 {
		t.Errorf("grantee holds %d grants", len(held))
	}
	if w := e.get(pubOwner, "work", tok, "", nil); w.Code != http.StatusNotFound {
		t.Errorf("feed after delete = %d, want 404", w.Code)
	}
}

func TestDeleteCalendarPlainRequestRedirects(t *testing.T) {
	e := newCalDelEnv(t)
	w := e.deleteAs(pubOwner, "work", false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/calendar/settings" {
		t.Fatalf("DELETE = %d -> %q", w.Code, w.Header().Get("Location"))
	}
}

func TestDeleteCalendarRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, e *calDelEnv)
		user  string
		id    string
		want  int
	}{
		{"tasks", nil, pubOwner, "tasks", http.StatusForbidden},
		{"default", nil, pubOwner, "default", http.StatusForbidden},
		{"missing", nil, pubOwner, "nope", http.StatusNotFound},
		{"another user's calendar", nil, "u-someone-else", "work", http.StatusNotFound},
		{"subscription", func(t *testing.T, e *calDelEnv) {
			if _, err := e.calendars.Create(pubOwner, &bongov1.Calendar{Guid: "feed", Name: "Feed", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"}}); err != nil {
				t.Fatal(err)
			}
		}, pubOwner, "feed", http.StatusConflict},
		{"last event calendar", func(t *testing.T, e *calDelEnv) {
			if err := e.calendars.Delete(pubOwner, "default"); err != nil {
				t.Fatal(err)
			}
		}, pubOwner, "work", http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCalDelEnv(t)
			tok := e.publish(t, "work", "full")
			if tt.setup != nil {
				tt.setup(t, e)
			}
			if w := e.deleteAs(tt.user, tt.id, true); w.Code != tt.want {
				t.Fatalf("DELETE = %d %q, want %d", w.Code, w.Body.String(), tt.want)
			}
			if !e.calendarExists(t, pubOwner, "work") {
				t.Error("work was deleted")
			}
			if tt.id != "work" && tt.name != "missing" && tt.name != "another user's calendar" {
				if !e.calendarExists(t, pubOwner, tt.id) {
					t.Errorf("%s was deleted", tt.id)
				}
			}
			if _, err := e.events.Get(pubOwner, "ev-work"); err != nil {
				t.Errorf("event lost: %v", err)
			}
			if tasks, _ := e.tasks.ListAll(pubOwner); len(tasks) != 2 {
				t.Errorf("tasks = %d, want 2", len(tasks))
			}
			if n, _ := e.acl.CountCalendarGrants(pubOwner, "work"); n != 1 {
				t.Errorf("grant rows = %d, want 1", n)
			}
			if w := e.get(pubOwner, "work", tok, "", nil); w.Code != http.StatusOK {
				t.Errorf("feed = %d, want 200", w.Code)
			}
		})
	}
}

// The settings page offers Delete for a deletable calendar only.
func TestSettingsCardOffersDeleteWhenAllowed(t *testing.T) {
	e := newCalDelEnv(t)
	req := withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/settings", nil), pubOwner, pubOwner+"@example.com")
	w := httptest.NewRecorder()
	e.cal.CalendarSettings(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if n := strings.Count(body, `hx-delete="/calendar/work"`); n != 1 {
		t.Errorf("delete button for work: %d", n)
	}
	for _, id := range []string{"default", "tasks"} {
		if strings.Contains(body, `hx-delete="/calendar/`+id+`"`) {
			t.Errorf("delete button offered for %s", id)
		}
	}
}
