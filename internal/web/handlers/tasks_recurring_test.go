package handlers

import (
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// agendaDatesOf returns the dates of the agenda sections that hold a row for
// the task guid.
func agendaDatesOf(body, guid string) []string {
	var out []string
	parts := strings.Split(body, `data-agenda-date="`)
	for _, p := range parts[1:] {
		date := p[:10]
		if end := strings.Index(p, "</section>"); end >= 0 && strings.Contains(p[:end], `data-task-guid="`+guid+`"`) {
			out = append(out, strings.Repeat(date+",", strings.Count(p[:end], `data-task-guid="`+guid+`"`)))
		}
	}
	return out
}

func recurringRaw(extra string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:rt\nSUMMARY:Water plants\nDUE;VALUE=DATE:20261102\nRRULE:FREQ=WEEKLY;COUNT=4\n" +
		extra + "END:VTODO\nEND:VCALENDAR\n"
}

func seedRecurring(t *testing.T, e *taskEnv, extra string) {
	t.Helper()
	e.seedRaw(t, &bongov1.Task{Guid: "rt", Summary: "Water plants", Due: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC).Unix()}, recurringRaw(extra))
}

func TestAgendaRecurringTask(t *testing.T) {
	tests := []struct {
		name  string
		extra string
		path  string
		want  string
	}{
		{"three occurrences", "", "/calendar/agenda?from=2026-11-03", "2026-11-09,2026-11-16,2026-11-23,"},
		{"exdate", "EXDATE;VALUE=DATE:20261116\n", "/calendar/agenda?from=2026-11-03", "2026-11-09,2026-11-23,"},
		{"override moved", "END:VTODO\nBEGIN:VTODO\nUID:rt\nRECURRENCE-ID;VALUE=DATE:20261116\nDUE;VALUE=DATE:20261118\nSUMMARY:Moved\n", "/calendar/agenda?from=2026-11-03", "2026-11-09,2026-11-18,2026-11-23,"},
		{"override completed", "END:VTODO\nBEGIN:VTODO\nUID:rt\nRECURRENCE-ID;VALUE=DATE:20261116\nSTATUS:COMPLETED\nDUE;VALUE=DATE:20261116\n", "/calendar/agenda?from=2026-11-03", "2026-11-09,2026-11-23,"},
		// Before today: not shown, not overdue.
		{"window before today", "", "/calendar/agenda?from=2026-10-01", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newTaskEnv(t)
			e.now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
			seedRecurring(t, e, tc.extra)
			body := e.get(taskUser, tc.path).Body.String()
			if got := strings.Join(agendaDatesOf(body, "rt"), ""); got != tc.want {
				t.Errorf("dates = %q, want %q", got, tc.want)
			}
			if strings.Contains(body, ">Overdue<") {
				t.Error("a recurring series with occurrences left is labelled overdue")
			}
		})
	}
}

func TestAgendaRecurringSkipsOccurrencesBeforeToday(t *testing.T) {
	e := newTaskEnv(t)
	e.now = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	seedRecurring(t, e, "")
	body := e.get(taskUser, "/calendar/agenda?from=2026-11-03").Body.String()
	if got := strings.Join(agendaDatesOf(body, "rt"), ""); got != "2026-11-16,2026-11-23," {
		t.Errorf("dates = %q", got)
	}
}

func TestAgendaEndedSeriesIsOverdueOnToday(t *testing.T) {
	e := newTaskEnv(t)
	e.now = time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	seedRecurring(t, e, "")
	body := e.get(taskUser, "/calendar/agenda?from=2026-12-01").Body.String()
	if got := strings.Join(agendaDatesOf(body, "rt"), ""); got != "2026-12-15," {
		t.Errorf("dates = %q", got)
	}
	if !strings.Contains(body, "Overdue") {
		t.Error("no overdue label")
	}
}

func TestAgendaOverdueTask(t *testing.T) {
	dueOld := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	raw := "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:od\nSUMMARY:Late\nDUE;VALUE=DATE:20261020\nEND:VTODO\nEND:VCALENDAR\n"
	tests := []struct {
		name   string
		status string
		path   string
		want   string
		label  bool
	}{
		{"window containing today", "", "/calendar/agenda?from=2026-11-03", "2026-11-03,", true},
		{"other window", "", "/calendar/agenda?from=2026-12-01", "", false},
		// Contains today and the due date: shown once, on today.
		{"shown once", "", "/calendar/agenda?from=2026-10-15", "2026-11-03,", true},
		{"completed", "COMPLETED", "/calendar/agenda?from=2026-11-03", "", false},
		// A window in the past holding the due date, not today: due-day row.
		{"past window", "", "/calendar/agenda?from=2026-09-25", "2026-10-20,", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newTaskEnv(t)
			e.now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
			e.seedRaw(t, &bongov1.Task{Guid: "od", Summary: "Late", Due: dueOld.Unix(), Status: tc.status}, raw)
			body := e.get(taskUser, tc.path).Body.String()
			if got := strings.Join(agendaDatesOf(body, "od"), ""); got != tc.want {
				t.Errorf("dates = %q, want %q", got, tc.want)
			}
			if has := strings.Contains(body, ">Overdue<") || strings.Contains(body, "Overdue\n"); has != tc.label {
				t.Errorf("overdue label = %v, want %v", has, tc.label)
			}
		})
	}
}

func TestTasksListShowsNextOccurrence(t *testing.T) {
	e := newTaskEnv(t)
	e.now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	seedRecurring(t, e, "")
	body := e.get(taskUser, "/tasks").Body.String()
	if !strings.Contains(body, "2026-11-16") || strings.Contains(body, "2026-11-02") {
		t.Errorf("list does not show the next occurrence:\n%s", body)
	}
	if strings.Contains(body, ">Overdue<") {
		t.Error("next occurrence labelled overdue")
	}
	// The edit page keeps the stored DUE, and the store is untouched.
	if got := e.stored(t, "rt").Due; got != time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("stored due changed: %d", got)
	}
	if edit := e.get(taskUser, "/tasks/rt").Body.String(); !strings.Contains(edit, "2026-11-02") {
		t.Error("edit form lost the stored DUE")
	}
}

func TestTasksListEndedSeriesIsOverdue(t *testing.T) {
	e := newTaskEnv(t)
	e.now = time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	seedRecurring(t, e, "")
	body := e.get(taskUser, "/tasks").Body.String()
	if !strings.Contains(body, "2026-11-23") || !strings.Contains(body, "Overdue") {
		t.Errorf("ended series: %s", body)
	}
}
