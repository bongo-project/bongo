package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const startedTodo = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//C//EN
BEGIN:VTODO
UID:started
DTSTAMP:20260101T000000Z
SUMMARY:Old
DTSTART;TZID=Europe/Berlin:20260630T090000
DUE;TZID=Europe/Berlin:20260701T093000
END:VTODO
END:VCALENDAR
`

func TestTasksEditRefusesDueBeforeStart(t *testing.T) {
	e := newTaskEnv(t)
	berlin := mustZone(t, "Europe/Berlin")
	e.h.SetZoneSource(fixedZone{berlin})
	e.seedRaw(t, &bongov1.Task{Guid: "started", Summary: "Old", Start: 1782802800, Due: 1782891000}, startedTodo)
	before := proto.Clone(e.stored(t, "started")).(*bongov1.Task)

	form := func(date, clock string) url.Values {
		return url.Values{"summary": {"Old"}, "due_date": {date}, "due_time": {clock}}
	}
	for _, tc := range []struct{ name, date, clock string }{
		{"earlier day", "2026-06-29", "10:00"},
		{"earlier on the start day", "2026-06-30", "08:59"},
		{"date before start date", "2026-06-29", ""},
	} {
		w := e.post(taskUser, "/tasks/started", form(tc.date, tc.clock))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "The due date cannot be before the start date.") {
			t.Errorf("%s: = %d %q", tc.name, w.Code, w.Body)
		}
		got := e.stored(t, "started")
		if string(got.IcalRaw) != string(before.IcalRaw) || got.Due != before.Due || got.UpdatedAt != before.UpdatedAt {
			t.Errorf("%s: refused edit changed the stored task", tc.name)
		}
	}

	// Same date as DTSTART's own date is allowed, as is a later time.
	for _, tc := range []struct{ date, clock string }{{"2026-06-30", ""}, {"2026-06-30", "09:00"}, {"2026-07-02", "10:00"}} {
		if w := e.post(taskUser, "/tasks/started", form(tc.date, tc.clock)); w.Code != http.StatusSeeOther {
			t.Errorf("due %s %s = %d %s", tc.date, tc.clock, w.Code, w.Body)
		}
	}
}

func TestTasksEditDateDueConvertsDtstart(t *testing.T) {
	e := newTaskEnv(t)
	berlin := mustZone(t, "Europe/Berlin")
	e.h.SetZoneSource(fixedZone{berlin})
	e.seedRaw(t, &bongov1.Task{Guid: "started", Summary: "Old", Start: 1782802800, Due: 1782891000}, startedTodo)
	w := e.post(taskUser, "/tasks/started", url.Values{"summary": {"Old"}, "due_date": {"2026-07-03"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d %s", w.Code, w.Body)
	}
	got := e.stored(t, "started")
	raw := string(got.IcalRaw)
	for _, want := range []string{"DTSTART;VALUE=DATE:20260630", "DUE;VALUE=DATE:20260703"} {
		if !strings.Contains(raw, want) {
			t.Errorf("raw lacks %q:\n%s", want, raw)
		}
	}
	if got.Start != 1782777600 {
		t.Errorf("Start = %d, want midnight UTC of 2026-06-30", got.Start)
	}
}
