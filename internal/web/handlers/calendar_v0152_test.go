package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// Opening a calendar page back-fills Tasks for an account with Personal only.
func TestCalendarPagesBackfillTasks(t *testing.T) {
	e := newTaskEnv(t)
	pages := map[string]func(user string){
		"month": func(user string) {
			w := httptest.NewRecorder()
			e.cal.MonthView(w, withRoute(httptest.NewRequest(http.MethodGet, "/calendar/2026/6", nil), user, map[string]string{"year": "2026", "month": "6"}))
		},
		"settings": func(user string) { e.settingsPage(user) },
	}
	for name, open := range pages {
		t.Run(name, func(t *testing.T) {
			user := "older-" + name
			if err := e.cals.Update(user, "default", &bongov1.Calendar{Guid: "default", Name: "Personal"}); err != nil {
				t.Fatal(err)
			}
			open(user)
			if _, err := e.cals.Get(user, store.TasksCalendarGUID); err != nil {
				t.Errorf("no Tasks calendar after opening %s: %v", name, err)
			}
		})
	}
}

// Where the clocks change at midnight, local midnight does not exist. The
// column must still be that date, or the header shows one day twice.
func TestWeekColumnsMidnightDST(t *testing.T) {
	tests := []struct{ zone, monday, sunday string }{
		{"America/Havana", "2026-03-02", "2026-03-08"},
		{"America/Santiago", "2026-08-31", "2026-09-06"},
		{"UTC", "2026-03-02", "2026-03-08"},
	}
	for _, tc := range tests {
		t.Run(tc.zone, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Skip(err)
			}
			m, err := time.ParseInLocation("2006-01-02", tc.monday, loc)
			if err != nil {
				t.Fatal(err)
			}
			days := buildWeekColumns(m, nil, nil, m)
			if got := days[6].DateStr; got != tc.sunday {
				t.Errorf("last column = %s, want %s", got, tc.sunday)
			}
			if got := days[6].Date.Weekday(); got != time.Sunday {
				t.Errorf("last column weekday = %v", got)
			}
		})
	}
}

// The import redirects to /calendar, which redirects again: the banner must
// survive that hop, and only allow-listed keys may.
func TestCalendarIndexCarriesFlash(t *testing.T) {
	e := newTaskEnv(t)
	tests := []struct {
		query string
		want  string
		ok    bool
	}{
		{"flash=" + flashSent.id, "flash=" + flashSent.id, true},
		{"flash=free+text", "flash=", false},
		{"", "flash=", false},
	}
	for _, tc := range tests {
		w := httptest.NewRecorder()
		e.cal.CalendarIndex(w, withRoute(httptest.NewRequest(http.MethodGet, "/calendar?"+tc.query, nil), "u", nil))
		loc := w.Header().Get("Location")
		if strings.Contains(loc, tc.want) != tc.ok {
			t.Errorf("query %q: Location = %q", tc.query, loc)
		}
	}
}
