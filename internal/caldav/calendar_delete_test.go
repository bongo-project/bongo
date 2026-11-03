package caldav

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-webdav/caldav"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type delEnv struct {
	t      *testing.T
	cals   *store.CalendarStore
	events *store.EventStore
	tasks  *store.TaskStore
	acl    *store.ACLStore
	srv    *httptest.Server
}

// newDelEnv gives mkUser calendars default, tasks and work with an event and a
// task in work, a read grant on work to mkOther, and work published.
func newDelEnv(t *testing.T) *delEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &grantApplier{ps: ps}
	e := &delEnv{
		t: t, cals: store.NewCalendarStore(ps, app), events: store.NewEventStore(ps, app),
		tasks: store.NewTaskStore(ps, app), acl: store.NewACLStore(ps, app),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := NewBackend(e.events, e.cals, logger)
	b.SetTaskStore(e.tasks)
	h := MkcalendarHandler(PrivilegeFilter(&caldav.Handler{Backend: b, Prefix: "/caldav"}, b.ReadOnlyShare, logger), b, logger)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), r.Header.Get("X-Test-User"))))
	}))
	t.Cleanup(e.srv.Close)

	for _, c := range []*bongov1.Calendar{
		{Guid: "default", Name: "Personal"},
		store.NewTasksCalendar(),
		{Guid: "work", Name: "Work", Published: true, PublishToken: make([]byte, 32)},
	} {
		if _, err := e.cals.Create(mkUser, c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.events.Create(mkUser, &bongov1.Event{Guid: "e1", CalendarId: "work", Title: "x", StartTime: 1000, EndTime: 2000}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.events.Create(mkUser, &bongov1.Event{Guid: "e2", CalendarId: "default", Title: "y", StartTime: 1000, EndTime: 2000}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Create(mkUser, &bongov1.Task{Guid: "t1", CalendarId: "work", Summary: "z"}); err != nil {
		t.Fatal(err)
	}
	if err := e.acl.SetCalendarGrant(mkUser, "work", mkOther, store.GrantLevelRead, store.GrantStateAccepted, mkUser, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *delEnv) del(user, path string) int {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodDelete, e.srv.URL+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("X-Test-User", user)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func (e *delEnv) calendarExists(owner, id string) bool {
	e.t.Helper()
	ok, err := e.cals.Exists(owner, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return ok
}

// feedStatus fetches work's public feed through the real feed handler (the
// env publishes it with an all-zero token).
func (e *delEnv) feedStatus() int {
	e.t.Helper()
	r := chi.NewRouter()
	r.Get("/pub/calendar/{owner}/{calendar}/{token}.ics", handlers.NewCalendarPublishHandler(e.events, e.cals).ServeFeed)
	tok := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/pub/calendar/"+mkUser+"/work/"+tok+".ics", nil))
	return w.Code
}

func TestCollectionDeleteRemovesEverything(t *testing.T) {
	for _, path := range []string{mkPath("work"), strings.TrimSuffix(mkPath("work"), "/")} {
		t.Run(path, func(t *testing.T) {
			e := newDelEnv(t)
			if code := e.feedStatus(); code != http.StatusOK {
				t.Fatalf("feed before delete = %d, want 200", code)
			}
			if code := e.del(mkUser, path); code != http.StatusNoContent {
				t.Fatalf("DELETE = %d, want 204", code)
			}
			if code := e.feedStatus(); code != http.StatusNotFound {
				t.Errorf("feed after delete = %d, want 404", code)
			}
			if e.calendarExists(mkUser, "work") {
				t.Error("calendar still there")
			}
			if _, err := e.events.Get(mkUser, "e1"); err == nil {
				t.Error("event of work survives")
			}
			if _, err := e.events.Get(mkUser, "e2"); err != nil {
				t.Errorf("event of default lost: %v", err)
			}
			if tasks, _ := e.tasks.ListAll(mkUser); len(tasks) != 0 {
				t.Errorf("tasks = %d, want 0", len(tasks))
			}
			if lvl, _ := e.acl.CalendarAccessLevel(mkUser, "work", mkOther); lvl != "" {
				t.Errorf("grant survives: %q", lvl)
			}
			if n, _ := e.acl.CountCalendarGrants(mkUser, "work"); n != 0 {
				t.Errorf("grants = %d", n)
			}
			if code := e.del(mkUser, path); code != http.StatusNotFound {
				t.Errorf("second DELETE = %d, want 404", code)
			}
		})
	}
}

func TestCollectionDeleteRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(e *delEnv)
		user  string
		path  string
		want  int
		keep  []string // calendars of mkUser that must still exist
	}{
		{"tasks", nil, mkUser, mkPath("tasks"), 403, []string{"tasks"}},
		{"default", nil, mkUser, mkPath("default"), 403, []string{"default"}},
		{"shared path", nil, mkUser, mkPath("shared-" + mkOther + "-work"), 403, []string{"work"}},
		{"foreign user", nil, mkOther, mkPath("work"), 403, []string{"work"}},
		{"foreign user missing calendar", nil, mkOther, mkPath("nope"), 403, nil},
		{"invalid segment", nil, mkUser, mkPath("a%20b"), 403, nil},
		{"missing", nil, mkUser, mkPath("nope"), 404, []string{"work"}},
		{"subscription", func(e *delEnv) {
			if _, err := e.cals.Create(mkUser, &bongov1.Calendar{Guid: "feed", Name: "Feed", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"}}); err != nil {
				e.t.Fatal(err)
			}
		}, mkUser, mkPath("feed"), 409, []string{"feed"}},
		{"last event calendar", func(e *delEnv) {
			if err := e.cals.Delete(mkUser, "default"); err != nil {
				e.t.Fatal(err)
			}
		}, mkUser, mkPath("work"), 409, []string{"work"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newDelEnv(t)
			if tt.setup != nil {
				tt.setup(e)
			}
			if code := e.del(tt.user, tt.path); code != tt.want {
				t.Fatalf("DELETE = %d, want %d", code, tt.want)
			}
			for _, id := range tt.keep {
				if !e.calendarExists(mkUser, id) {
					t.Errorf("calendar %q was deleted", id)
				}
			}
			if _, err := e.events.Get(mkUser, "e1"); err != nil {
				t.Errorf("event e1 lost: %v", err)
			}
			if tasks, _ := e.tasks.ListAll(mkUser); len(tasks) != 1 {
				t.Errorf("tasks = %d, want 1", len(tasks))
			}
			if lvl, err := e.acl.CalendarAccessLevel(mkUser, "work", mkOther); lvl == "" {
				t.Errorf("grant lost: %v", err)
			}
		})
	}
}

// An object path is still the object delete, not a collection delete.
func TestCollectionDeleteLeavesObjectsAlone(t *testing.T) {
	e := newDelEnv(t)
	if code := e.del(mkUser, mkPath("work")+"e1.ics"); code != http.StatusNoContent {
		t.Fatalf("object DELETE = %d, want 204", code)
	}
	if !e.calendarExists(mkUser, "work") {
		t.Error("deleting an object deleted the calendar")
	}
}
