package caldav

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// A user owns at most store.MaxCalendarsPerUser calendars: the 6th over
// MKCALENDAR or extended MKCOL is 507 with a DAV:error body, an existing path
// is still 405, and nothing is written.
func TestMkcalendarCalendarLimit(t *testing.T) {
	e := newMkEnv(t, true)
	// Personal and Tasks arrive with the first create; fill the rest.
	for i := range store.MaxCalendarsPerUser - 2 {
		if code, body, _ := e.do(mkUser, "MKCALENDAR", mkPath(fmt.Sprintf("fill%d", i)), mkcalBody("Fill")); code != 201 {
			t.Fatalf("fill %d = %d: %s", i, code, body)
		}
	}
	tests := []struct {
		name, method, seg, body string
		want                    int
	}{
		{"MKCALENDAR over the cap", "MKCALENDAR", "extra", mkcalBody("Extra"), http.StatusInsufficientStorage},
		{"MKCOL over the cap", "MKCOL", "extra", mkcolBody("Extra", "VEVENT"), http.StatusInsufficientStorage},
		{"existing path is still 405", "MKCALENDAR", "fill0", mkcalBody("Again"), http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, body, hdr := e.do(mkUser, tc.method, mkPath(tc.seg), tc.body)
			if code != tc.want {
				t.Fatalf("status = %d, want %d: %s", code, tc.want, body)
			}
			if code == http.StatusInsufficientStorage {
				if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
					t.Errorf("Content-Type = %q", ct)
				}
				if !strings.Contains(body, "error") || !strings.Contains(body, "quota-not-exceeded") {
					t.Errorf("body = %s", body)
				}
			}
		})
	}
	if _, err := e.cals.Get(mkUser, "extra"); err == nil {
		t.Error("a refused create wrote the calendar")
	}
	if cals, _ := e.cals.List(mkUser); len(cals) != store.MaxCalendarsPerUser {
		t.Errorf("calendars = %d, want %d", len(cals), store.MaxCalendarsPerUser)
	}
}
