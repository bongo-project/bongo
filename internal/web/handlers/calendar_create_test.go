package handlers

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/caldav"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func (e *taskEnv) createCalendar(user, locale string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	req := sFormReq(http.MethodPost, "/calendar", form)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req = withRoute(req, user, nil)
	if locale != "" {
		req = req.WithContext(middleware.WithLocale(req.Context(), locale))
	}
	w := httptest.NewRecorder()
	e.cal.CreateCalendar(w, req)
	return w
}

// sidebarItem returns the calendar-list-item in page whose checkbox toggles
// calendar guid, or "" if there is none.
func sidebarItem(page, guid string) string {
	panel := page[max(strings.Index(page, `class="calendar-list-panel"`), 0):]
	for _, item := range strings.Split(panel, `class="calendar-list-item"`)[1:] {
		if strings.Contains(item, `data-calendar="`+guid+`"`) {
			item, _, _ = strings.Cut(item, "</label>")
			return item
		}
	}
	return ""
}

func (e *taskEnv) settingsPage(user string) string {
	w := httptest.NewRecorder()
	e.cal.CalendarSettings(w, withRoute(httptest.NewRequest(http.MethodGet, "/calendar/settings", nil), user, nil))
	return w.Body.String()
}

func (e *taskEnv) ownedCount(t *testing.T, user string) int {
	t.Helper()
	cals, err := e.cals.List(user)
	if err != nil {
		t.Fatal(err)
	}
	return len(cals)
}

func (e *taskEnv) findByName(t *testing.T, user, name string) *bongov1.Calendar {
	t.Helper()
	cals, err := e.cals.List(user)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cals {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestCreateCalendarEvents(t *testing.T) {
	e := newTaskEnv(t)
	w := e.createCalendar(taskUser, "", url.Values{"name": {"Work"}, "color": {"#aabbcc"}}, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/calendar/settings" {
		t.Fatalf("status %d location %q: %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	c := e.findByName(t, taskUser, "Work")
	if c == nil {
		t.Fatal("calendar not created")
	}
	if c.Guid == "" || c.Guid == "default" || c.Guid == store.TasksCalendarGUID || len(c.Components) != 0 || c.Color != "#aabbcc" {
		t.Errorf("calendar = %+v, want a generated GUID, VEVENT default and the colour", c)
	}
	if store.CalendarAllows(c, store.ComponentTodo) {
		t.Error("a calendar without the checkbox holds tasks")
	}

	// The month page sidebar lists it with its show/hide checkbox.
	mw := httptest.NewRecorder()
	e.cal.MonthView(mw, withRoute(httptest.NewRequest(http.MethodGet, "/calendar/2026/6", nil), taskUser, map[string]string{"year": "2026", "month": "6"}))
	if mw.Code != http.StatusOK {
		t.Fatalf("month view = %d", mw.Code)
	}
	if item := sidebarItem(mw.Body.String(), c.Guid); item == "" || !strings.Contains(item, `type="checkbox"`) || !strings.Contains(item, "Work") {
		t.Errorf("sidebar has no show/hide entry for the new calendar: %q", item)
	}
	// The CalDAV calendar home lists it as a collection.
	b := caldav.NewBackend(e.events, e.cals, slog.New(slog.NewTextHandler(io.Discard, nil)))
	home := davHomeOf(t, b, taskUser)
	_, resp, _ := strings.Cut(home, "<href>/caldav/"+taskUser+"/calendars/"+c.Guid+"/</href>")
	resp, _, _ = strings.Cut(resp, "</response>")
	if !strings.Contains(resp, ">Work</displayname>") || !strings.Contains(resp, "200 OK") {
		t.Errorf("CalDAV home does not list the new calendar:\n%s", home)
	}

	// The settings page and the event form's list.
	if page := e.settingsPage(taskUser); !strings.Contains(page, "Work") || !strings.Contains(page, "calendar-settings-"+c.Guid) {
		t.Error("settings page does not list the new calendar")
	}
	nw := httptest.NewRecorder()
	e.cal.NewEvent(nw, withRoute(httptest.NewRequest(http.MethodGet, "/calendar/event/new", nil), taskUser, nil))
	if !strings.Contains(nw.Body.String(), `value="`+c.Guid+`"`) {
		t.Error("event form does not offer the new calendar")
	}
	// It is not offered for tasks.
	if tw := e.post(taskUser, "/tasks", url.Values{"summary": {"x"}, "calendar_id": {c.Guid}}); tw.Code != http.StatusBadRequest {
		t.Errorf("task on an events-only calendar = %d, want 400", tw.Code)
	}
}

func TestCreateCalendarHoldsTasks(t *testing.T) {
	e := newTaskEnv(t)
	w := e.createCalendar(taskUser, "", url.Values{"name": {"Chores"}, "color": {"#112233"}, "holds_tasks": {"1"}}, true)
	if w.Code != http.StatusOK || w.Header().Get("HX-Refresh") != "true" || w.Header().Get("HX-Trigger") != "calendarRefresh" {
		t.Fatalf("htmx create = %d %v", w.Code, w.Header())
	}
	c := e.findByName(t, taskUser, "Chores")
	if c == nil {
		t.Fatal("calendar not created")
	}
	if got := strings.Join(c.Components, ","); got != "VEVENT,VTODO" {
		t.Errorf("components = %q", got)
	}
	if tw := e.post(taskUser, "/tasks", url.Values{"summary": {"x"}, "calendar_id": {c.Guid}}); tw.Code != http.StatusSeeOther {
		t.Errorf("task on the new calendar = %d, want 303", tw.Code)
	}
	if page := e.get(taskUser, "/tasks").Body.String(); !strings.Contains(page, c.Guid) {
		t.Error("/tasks does not offer the new calendar")
	}
}

func TestCreateCalendarRefusals(t *testing.T) {
	long := func(n int) string { return strings.Repeat("é", n) } // two bytes a rune: 255 runes is not 255 bytes
	tests := []struct {
		name string
		form url.Values
		want int
	}{
		{"empty name", url.Values{"name": {""}, "color": {"#112233"}}, 400},
		{"blank name", url.Values{"name": {"  \t "}, "color": {"#112233"}}, 400},
		{"control characters only", url.Values{"name": {"\x00\x07"}, "color": {"#112233"}}, 400},
		{"256 runes", url.Values{"name": {long(256)}, "color": {"#112233"}}, 400},
		{"255 runes", url.Values{"name": {long(255)}, "color": {"#112233"}}, 303},
		{"short colour", url.Values{"name": {"A"}, "color": {"#12345"}}, 400},
		{"named colour", url.Values{"name": {"A"}, "color": {"red"}}, 400},
		{"not hex", url.Values{"name": {"A"}, "color": {"#GGGGGG"}}, 400},
		{"missing colour", url.Values{"name": {"A"}}, 400},
		{"long colour", url.Values{"name": {"A"}, "color": {"#1234567"}}, 400},
		{"colour with trailing newline", url.Values{"name": {"A"}, "color": {"#123456\n"}}, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newTaskEnv(t)
			if _, err := e.cals.EnsureDefault(taskUser); err != nil {
				t.Fatal(err)
			}
			before := e.ownedCount(t, taskUser)
			w := e.createCalendar(taskUser, "", tc.form, false)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			want := before
			if tc.want == 303 {
				want++
			}
			if got := e.ownedCount(t, taskUser); got != want {
				t.Errorf("calendars = %d, want %d", got, want)
			}
		})
	}
}

// A client-chosen GUID is ignored: the form never names the calendar's ID.
func TestCreateCalendarIgnoresFormGUID(t *testing.T) {
	e := newTaskEnv(t)
	e.createCalendar(taskUser, "", url.Values{"name": {"X"}, "color": {"#112233"}, "guid": {"default"}, "calendar_id": {"default"}}, false)
	if c, err := e.cals.Get(taskUser, "default"); err == nil && c.Name == "X" {
		t.Error("the form chose the GUID")
	}
}

func TestCreateCalendarLimit(t *testing.T) {
	e := newTaskEnv(t)
	// Tasks (from newTaskEnv) + Personal (EnsureDefault) + three more.
	for i := range store.MaxCalendarsPerUser - 2 {
		if w := e.createCalendar(taskUser, "", url.Values{"name": {fmt.Sprintf("C%d", i)}, "color": {"#112233"}}, false); w.Code != http.StatusSeeOther {
			t.Fatalf("create %d = %d: %s", i, w.Code, w.Body.String())
		}
	}
	if got := e.ownedCount(t, taskUser); got != store.MaxCalendarsPerUser {
		t.Fatalf("calendars = %d", got)
	}
	if strings.Contains(e.settingsPage(taskUser), `id="calendar-new"`) {
		t.Error("the New calendar form is shown at the cap")
	}

	for _, tc := range []struct{ locale, want string }{
		{"en", fmt.Sprintf("at most %d calendars", store.MaxCalendarsPerUser)},
		{"de", fmt.Sprintf("höchstens %d Kalender", store.MaxCalendarsPerUser)},
	} {
		w := e.createCalendar(taskUser, tc.locale, url.Values{"name": {"Sixth"}, "color": {"#112233"}}, false)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: status %d body %q", tc.locale, w.Code, w.Body.String())
		}
	}
	if e.findByName(t, taskUser, "Sixth") != nil || e.ownedCount(t, taskUser) != store.MaxCalendarsPerUser {
		t.Error("a refused create wrote a calendar")
	}
}

// The limit message names store.MaxCalendarsPerUser in every catalogue rather
// than a number written into the text.
func TestCalendarLimitMessageNamesTheCap(t *testing.T) {
	want := strconv.Itoa(store.MaxCalendarsPerUser)
	for _, locale := range []string{"en", "de", "fr", "es"} {
		msg := calendarLimitMessage(locale)
		if !strings.Contains(msg, want) || strings.Contains(msg, "%") {
			t.Errorf("%s: %q does not name the cap %s", locale, msg, want)
		}
	}
}

func TestCreateCalendarFormShownBelowCap(t *testing.T) {
	e := newTaskEnv(t)
	page := e.settingsPage(taskUser)
	for _, want := range []string{`id="calendar-new"`, `hx-post="/calendar"`, `name="holds_tasks"`, `name="_csrf"`, `data-form-status`} {
		if !strings.Contains(page, want) {
			t.Errorf("settings page lacks %s", want)
		}
	}
}

// The route is behind CSRF outside the dev role. The production router is
// covered by TestProductionRouterCSRFGroup in the agent package.
func TestCreateCalendarRequiresCSRF(t *testing.T) {
	e := newTaskEnv(t)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), taskUser, taskUser+"@example.com")))
		})
	})
	r.Use(middleware.InjectCSRF(e.secret))
	r.Use(middleware.RequireCSRF(e.secret, true))
	r.Post("/calendar", e.cal.CreateCalendar)

	form := url.Values{"name": {"NoToken"}, "color": {"#112233"}}
	req := httptest.NewRequest(http.MethodPost, "/calendar", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("without a token = %d, want 403", w.Code)
	}
	if e.findByName(t, taskUser, "NoToken") != nil {
		t.Error("a refused request created a calendar")
	}

	form.Set("_csrf", e.token(taskUser))
	req = httptest.NewRequest(http.MethodPost, "/calendar", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Errorf("with a token = %d: %s", w.Code, w.Body.String())
	}
}

func TestSubscribeCalendarLimit(t *testing.T) {
	e := newSharedEnv(t)
	// Fill to the cap; EnsureDefault's calendars are exempt, so count rather
	// than assume how many the first subscription brings.
	for i := 0; ; i++ {
		if n, _ := e.cals.List(actWrite); len(n) >= store.MaxCalendarsPerUser {
			break
		}
		if i > 2*store.MaxCalendarsPerUser {
			t.Fatal("never reached the cap")
		}
		if w := subscribe(e, actWrite, fmt.Sprintf("https://feeds.example.com/%d.ics", i)); w.Code != http.StatusSeeOther {
			t.Fatalf("subscribe %d = %d: %s", i, w.Code, w.Body.String())
		}
	}
	e.app.writes = 0
	w := subscribe(e, actWrite, "https://feeds.example.com/over.ics")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "calendars") {
		t.Fatalf("over the cap = %d: %s", w.Code, w.Body.String())
	}
	if e.app.writes != 0 {
		t.Errorf("a refused subscribe wrote %d times", e.app.writes)
	}
	if cals, _ := e.cals.List(actWrite); len(cals) != store.MaxCalendarsPerUser {
		t.Errorf("calendars = %d, want %d", len(cals), store.MaxCalendarsPerUser)
	}
}
