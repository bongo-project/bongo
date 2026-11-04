package caldav

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Apple Reminders sends DEFAULT_TASK_CALENDAR_NAME for its default list; it is
// stored as "Tasks". Any other name is untouched.
func TestMkcalendarAppleDefaultTaskName(t *testing.T) {
	tests := []struct{ seg, display, want string }{
		{"reminders", "DEFAULT_TASK_CALENDAR_NAME", "Tasks"},
		{"other", "Default_Task_Calendar_Name", "Default_Task_Calendar_Name"},
		{"groceries", "Groceries", "Groceries"},
	}
	for _, tc := range tests {
		t.Run(tc.seg, func(t *testing.T) {
			e := newMkEnv(t, true)
			if code, _, _ := e.do(mkUser, "MKCALENDAR", mkPath(tc.seg), mkcalBody(tc.display, "VTODO")); code != 201 {
				t.Fatal(code)
			}
			if name, _, _ := e.stored(tc.seg); name != tc.want {
				t.Errorf("name = %q, want %q", name, tc.want)
			}
		})
	}
}

// A user with only Personal sees a Tasks collection in ListCalendars.
func TestListCalendarsBackfillsTasks(t *testing.T) {
	e := newMkEnv(t, true)
	if err := e.cals.Update(mkUser, "default", &bongov1.Calendar{Guid: "default", Name: "Personal"}); err != nil {
		t.Fatal(err)
	}
	b := NewBackend(nil, e.cals, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.SetTaskStore(e.tasks)
	cals, err := b.ListCalendars(WithUserID(context.Background(), mkUser))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range cals {
		if strings.HasSuffix(c.Path, "/calendars/tasks/") {
			found = slices.Contains(c.SupportedComponentSet, "VTODO")
		}
	}
	if !found {
		t.Errorf("no VTODO tasks collection in %+v", cals)
	}
}
