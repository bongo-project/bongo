package store

import (
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func todoICal(body string) []byte {
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n" + strings.ReplaceAll(body, "\n", "\r\n") + "\r\nEND:VCALENDAR\r\n")
}

func utcDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func dueDays(ts []*bongov1.Task) []string {
	var out []string
	for _, t := range ts {
		out = append(out, time.Unix(t.Due, 0).UTC().Format("2006-01-02"))
	}
	return out
}

const weeklyDue = `BEGIN:VTODO
UID:t1
SUMMARY:Water plants
DUE;VALUE=DATE:20261102
RRULE:FREQ=WEEKLY;COUNT=4
%EXTRA%
END:VTODO`

func weeklyTask(extra string) *bongov1.Task {
	raw := strings.Replace(weeklyDue, "%EXTRA%", extra, 1)
	return &bongov1.Task{Guid: "t1", CalendarId: "cal", Summary: "Water plants", Due: utcDay(2026, 11, 2).Unix(), IcalRaw: todoICal(raw)}
}

func TestExpandTodo(t *testing.T) {
	from := utcDay(2026, 11, 3)
	to := from.AddDate(0, 0, 28)
	tests := []struct {
		name  string
		extra string
		want  []string
		titls []string
	}{
		{"plain", "", []string{"2026-11-09", "2026-11-16", "2026-11-23"}, nil},
		{"exdate", "EXDATE;VALUE=DATE:20261116", []string{"2026-11-09", "2026-11-23"}, nil},
		{"override moved and renamed", "END:VTODO\nBEGIN:VTODO\nUID:t1\nRECURRENCE-ID;VALUE=DATE:20261116\nSUMMARY:Moved\nDUE;VALUE=DATE:20261118",
			[]string{"2026-11-09", "2026-11-18", "2026-11-23"}, []string{"Water plants", "Moved", "Water plants"}},
		{"override completed", "END:VTODO\nBEGIN:VTODO\nUID:t1\nRECURRENCE-ID;VALUE=DATE:20261116\nSTATUS:COMPLETED\nDUE;VALUE=DATE:20261116",
			[]string{"2026-11-09", "2026-11-23"}, nil},
		{"override cancelled", "END:VTODO\nBEGIN:VTODO\nUID:t1\nRECURRENCE-ID;VALUE=DATE:20261116\nSTATUS:CANCELLED\nDUE;VALUE=DATE:20261116",
			[]string{"2026-11-09", "2026-11-23"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := weeklyTask(tc.extra)
			got, err := ExpandTodo(task, from.Unix(), to.Unix())
			if err != nil {
				t.Fatal(err)
			}
			days := dueDays(got)
			if strings.Join(days, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("due days = %v, want %v", days, tc.want)
			}
			for i, w := range tc.titls {
				if got[i].Summary != w {
					t.Errorf("summary[%d] = %q, want %q", i, got[i].Summary, w)
				}
			}
			for _, g := range got {
				if g.CalendarId != "cal" || g.Guid != "t1" {
					t.Errorf("identity lost: %+v", g)
				}
			}
			if task.Due != utcDay(2026, 11, 2).Unix() {
				t.Error("stored task was mutated")
			}
		})
	}
}

func TestExpandTodoStartAndTimezone(t *testing.T) {
	// DTSTART-anchored with a 2 hour span, 09:00 Berlin across the DST end of
	// 2026-10-25.
	raw := todoICal(`BEGIN:VTODO
UID:t2
SUMMARY:Standup
DTSTART;TZID=Europe/Berlin:20261020T090000
DUE;TZID=Europe/Berlin:20261020T110000
RRULE:FREQ=WEEKLY;COUNT=3
END:VTODO`)
	berlin, _ := time.LoadLocation("Europe/Berlin")
	first := time.Date(2026, 10, 20, 9, 0, 0, 0, berlin)
	task := &bongov1.Task{Guid: "t2", Start: first.Unix(), Due: first.Add(2 * time.Hour).Unix(), IcalRaw: raw}
	got, err := ExpandTodo(task, utcDay(2026, 10, 1).Unix(), utcDay(2026, 12, 1).Unix())
	if err != nil || len(got) != 3 {
		t.Fatalf("got %d, err %v", len(got), err)
	}
	last := time.Unix(got[2].Start, 0).In(berlin)
	if last.Hour() != 9 || last.Day() != 3 {
		t.Errorf("third start = %s, want 09:00 on 3 Nov", last)
	}
	if d := time.Unix(got[2].Due, 0).In(berlin); d.Hour() != 11 {
		t.Errorf("third due = %s, want 11:00", d)
	}
}

func TestExpandTodoNonRecurringIsReturned(t *testing.T) {
	task := &bongov1.Task{Guid: "x", Due: 5, IcalRaw: todoICal("BEGIN:VTODO\nUID:x\nDUE:20260101T000000Z\nEND:VTODO")}
	got, err := ExpandTodo(task, 0, 10)
	if err != nil || len(got) != 1 || got[0] != task {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestNextTodoOccurrence(t *testing.T) {
	tests := []struct {
		name  string
		extra string
		today time.Time
		want  string
		found bool
	}{
		{"mid series", "", utcDay(2026, 11, 10), "2026-11-16", true},
		{"on the day", "", utcDay(2026, 11, 16), "2026-11-16", true},
		{"skips exdate", "EXDATE;VALUE=DATE:20261116", utcDay(2026, 11, 10), "2026-11-23", true},
		{"series ended", "", utcDay(2026, 12, 15), "2026-11-23", false},
		{"override moved earlier", "END:VTODO\nBEGIN:VTODO\nUID:t1\nRECURRENCE-ID;VALUE=DATE:20261116\nDUE;VALUE=DATE:20261112",
			utcDay(2026, 11, 10), "2026-11-12", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NextTodoOccurrence(weeklyTask(tc.extra), tc.today)
			if got == nil {
				t.Fatal("nil occurrence")
			}
			if d := time.Unix(got.Due, 0).UTC().Format("2006-01-02"); d != tc.want || ok != tc.found {
				t.Errorf("got %s, %v; want %s, %v", d, ok, tc.want, tc.found)
			}
		})
	}
}

func TestNextTodoOccurrenceTerminates(t *testing.T) {
	// An open-ended per-second rule from far back must stop at the cap.
	task := &bongov1.Task{Guid: "s", IcalRaw: todoICal("BEGIN:VTODO\nUID:s\nDUE:20000101T000000Z\nRRULE:FREQ=SECONDLY\nEND:VTODO")}
	start := time.Now()
	if got, ok := NextTodoOccurrence(task, utcDay(2026, 11, 1)); got != nil || ok {
		t.Errorf("got %v, %v", got, ok)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("search not bounded")
	}
	plain := &bongov1.Task{Guid: "n", IcalRaw: todoICal("BEGIN:VTODO\nUID:n\nDUE:20260101T000000Z\nEND:VTODO")}
	if got, ok := NextTodoOccurrence(plain, time.Now()); got != nil || ok {
		t.Error("non-recurring task has no occurrence")
	}
}
