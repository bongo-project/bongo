package icalevent

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

var todoNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

const clientTodo = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//EN\r\nBEGIN:VTODO\r\nUID:u1\r\nDTSTAMP:20260101T000000Z\r\n" +
	"SUMMARY:Old\r\nDUE;TZID=Europe/Berlin:20260701T093015\r\nRRULE:FREQ=DAILY\r\nX-KEEP:yes\r\nSEQUENCE:4\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT5M\r\nDESCRIPTION:x\r\nEND:VALARM\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"

func TestBuildTodo(t *testing.T) {
	due := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Unix()
	tests := []struct {
		name    string
		task    *bongov1.Task
		dueDate bool
		want    []string
		not     []string
	}{
		{"date due", &bongov1.Task{Guid: "g1", Summary: "a, b; c", Due: due, Priority: 1}, true,
			[]string{"UID:g1", "SUMMARY:a\\, b\\; c", "DUE;VALUE=DATE:20260701", "PRIORITY:1", "DTSTAMP:20260615T120000Z"}, []string{"VALUE=TEXT", "COMPLETED"}},
		{"datetime due", &bongov1.Task{Guid: "g2", Summary: "x", Due: due + 3600}, false,
			[]string{"DUE:20260701T010000Z"}, []string{"VALUE=DATE"}},
		{"completed", &bongov1.Task{Guid: "g3", Summary: "x", Status: "COMPLETED", Completed: due, Percent: 100}, false,
			[]string{"STATUS:COMPLETED", "COMPLETED:20260701T000000Z", "PERCENT-COMPLETE:100"}, []string{"VALUE=TEXT"}},
		{"no due", &bongov1.Task{Guid: "g4", Summary: "x"}, true, nil, []string{"DUE"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := BuildTodo(tt.task, tt.dueDate, todoNow)
			if err != nil {
				t.Fatal(err)
			}
			s := string(raw)
			for _, w := range tt.want {
				if !strings.Contains(s, w) {
					t.Errorf("lacks %q:\n%s", w, s)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(s, n) {
					t.Errorf("holds %q:\n%s", n, s)
				}
			}
			cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
			if err != nil {
				t.Fatal(err)
			}
			if back := TodoToTask(cal, tt.task.Guid, ""); back.Due != tt.task.Due || back.Summary != tt.task.Summary {
				t.Errorf("round trip = %+v", back)
			}
		})
	}
}

func todoFromRaw(t *testing.T, raw string) *bongov1.Task {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return TodoToTask(cal, "u1", "tasks")
}

func TestApplyTodoEdit(t *testing.T) {
	existing := todoFromRaw(t, clientTodo)
	t.Run("unchanged keeps every property and bumps stamps", func(t *testing.T) {
		raw, err := ApplyTodoEdit([]byte(clientTodo), existing, existing, false, todoNow)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		for _, w := range []string{"DUE;TZID=Europe/Berlin:20260701T093015", "RRULE:FREQ=DAILY", "X-KEEP:yes", "BEGIN:VALARM", "SEQUENCE:5", "DTSTAMP:20260615T120000Z", "LAST-MODIFIED:20260615T120000Z", "SUMMARY:Old"} {
			if !strings.Contains(s, w) {
				t.Errorf("lacks %q:\n%s", w, s)
			}
		}
		if strings.Contains(s, "VALUE=TEXT") {
			t.Errorf("INTEGER property written as TEXT:\n%s", s)
		}
	})
	t.Run("changes only what differs", func(t *testing.T) {
		upd := proto.Clone(existing).(*bongov1.Task)
		upd.Summary, upd.Description, upd.Priority = "New", "d", 4
		upd.Status, upd.Completed, upd.Percent = "COMPLETED", todoNow.Unix(), 100
		raw, err := ApplyTodoEdit([]byte(clientTodo), existing, upd, false, todoNow)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		for _, w := range []string{"SUMMARY:New", "DESCRIPTION:d", "PRIORITY:4", "STATUS:COMPLETED", "COMPLETED:20260615T120000Z", "PERCENT-COMPLETE:100", "DUE;TZID=Europe/Berlin:20260701T093015"} {
			if !strings.Contains(s, w) {
				t.Errorf("lacks %q:\n%s", w, s)
			}
		}
		// And back: reopening removes COMPLETED.
		back := proto.Clone(upd).(*bongov1.Task)
		back.Status, back.Completed, back.Percent = "NEEDS-ACTION", 0, 0
		raw2, err := ApplyTodoEdit(raw, upd, back, false, todoNow)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw2), "COMPLETED:") || strings.Contains(string(raw2), "STATUS:COMPLETED") {
			t.Errorf("reopen left completion:\n%s", raw2)
		}
	})
	t.Run("due cleared and changed", func(t *testing.T) {
		upd := proto.Clone(existing).(*bongov1.Task)
		upd.Due = 0
		raw, _ := ApplyTodoEdit([]byte(clientTodo), existing, upd, false, todoNow)
		if strings.Contains(string(raw), "DUE") {
			t.Errorf("DUE not removed:\n%s", raw)
		}
		upd.Due = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).Unix()
		raw, _ = ApplyTodoEdit([]byte(clientTodo), existing, upd, true, todoNow)
		if !strings.Contains(string(raw), "DUE;VALUE=DATE:20260801") || strings.Contains(string(raw), "TZID") {
			t.Errorf("DUE not rewritten as a date:\n%s", raw)
		}
	})
	t.Run("unreadable raw", func(t *testing.T) {
		if _, err := ApplyTodoEdit([]byte("junk"), existing, existing, false, todoNow); !errors.Is(err, ErrTodoDecode) {
			t.Errorf("err = %v, want ErrTodoDecode", err)
		}
		if _, err := ApplyTodoEdit([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nEND:VCALENDAR\r\n"), existing, existing, false, todoNow); !errors.Is(err, ErrTodoDecode) {
			t.Errorf("err = %v, want ErrTodoDecode for no VTODO", err)
		}
	})
}

func TestTodoTraits(t *testing.T) {
	date := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VTODO\r\nUID:a\r\nDUE;VALUE=DATE:20260701\r\nRRULE:FREQ=DAILY\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	if d, r := TodoTraits([]byte(date)); !d || !r {
		t.Errorf("date+rrule = %v %v", d, r)
	}
	if d, r := TodoTraits([]byte(clientTodo)); d || !r {
		t.Errorf("timed+rrule = %v %v", d, r)
	}
	if d, r := TodoTraits(nil); d || r {
		t.Errorf("nil = %v %v", d, r)
	}
}
