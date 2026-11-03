package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestSharedRecurringTaskExpands(t *testing.T) {
	s := newSharedTaskEnv(t)
	s.grant(t, store.TasksCalendarGUID, store.GrantLevelRead, store.GrantStateAccepted)
	raw := "BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:sr\nSUMMARY:shared-weekly\nDUE;VALUE=DATE:20260614\nRRULE:FREQ=WEEKLY;COUNT=3\nEND:VTODO\nEND:VCALENDAR\n"
	task := &bongov1.Task{
		Guid: "sr", Summary: "shared-weekly", CalendarId: store.TasksCalendarGUID,
		Due:     time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC).Unix(),
		IcalRaw: []byte(strings.ReplaceAll(raw, "\n", "\r\n")),
	}
	if _, err := s.tasks.Create(taskShareOwner, task); err != nil {
		t.Fatal(err)
	}
	agenda := s.get(taskUser, "/calendar/agenda?from=2026-06-15").Body.String()
	if got := strings.Join(agendaDatesOf(agenda, "sr"), ""); got != "2026-06-21,2026-06-28," {
		t.Errorf("agenda dates = %q", got)
	}
	list := s.get(taskUser, "/tasks").Body.String()
	if !strings.Contains(list, "2026-06-21") {
		t.Error("/tasks does not show the next occurrence")
	}
	for name, body := range map[string]string{"agenda": agenda, "tasks": list} {
		if !strings.Contains(body, templates.CalendarColourClass(taskShareColour)) {
			t.Errorf("%s: share colour class missing", name)
		}
		if strings.Contains(body, "/tasks/sr") {
			t.Errorf("%s: shared occurrence is linked", name)
		}
	}
}
