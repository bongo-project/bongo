package icalevent

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestApplyTodoEditAlignsDtstartWithDue(t *testing.T) {
	const timed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//EN\r\nBEGIN:VTODO\r\nUID:u1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"SUMMARY:S\r\nDTSTART;TZID=Europe/Berlin:20260630T090000\r\nDUE;TZID=Europe/Berlin:20260701T093015\r\nRRULE:FREQ=DAILY\r\nX-KEEP:yes\r\n" +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT5M\r\nDESCRIPTION:x\r\nEND:VALARM\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	const dated = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//EN\r\nBEGIN:VTODO\r\nUID:u1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"SUMMARY:S\r\nDTSTART;VALUE=DATE:20260630\r\nDUE;VALUE=DATE:20260701\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	day := func(d int) int64 { return time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC).Unix() }
	jun30 := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC).Unix()
	tests := []struct {
		name      string
		raw       string
		due       int64
		dueDate   bool
		want, not []string
		start     int64
	}{
		{"timed to date keeps DTSTART's own date", timed, day(2), true,
			[]string{"DTSTART;VALUE=DATE:20260630", "DUE;VALUE=DATE:20260702", "X-KEEP:yes", "BEGIN:VALARM", "RRULE:FREQ=DAILY"},
			[]string{"TZID", "T0900"}, jun30},
		{"date to timed becomes midnight UTC", dated, time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC).Unix(), false,
			[]string{"DTSTART:20260630T000000Z", "DUE:20260702T100000Z"}, []string{"VALUE=DATE"}, jun30},
		{"same type leaves DTSTART alone", timed, time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC).Unix(), false,
			[]string{"DTSTART;TZID=Europe/Berlin:20260630T090000"}, nil, 0},
		{"removing DUE leaves DTSTART alone", timed, 0, false,
			[]string{"DTSTART;TZID=Europe/Berlin:20260630T090000"}, []string{"DUE"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := todoFromRaw(t, tt.raw)
			upd := proto.Clone(existing).(*bongov1.Task)
			upd.Due = tt.due
			start := upd.Start
			raw, err := ApplyTodoEdit([]byte(tt.raw), existing, upd, tt.dueDate, todoNow)
			if err != nil {
				t.Fatal(err)
			}
			s := string(raw)
			for _, w := range tt.want {
				if !strings.Contains(s, w) {
					t.Errorf("lacks %q:\n%s", w, s)
				}
			}
			for _, w := range tt.not {
				if strings.Contains(s, w) {
					t.Errorf("contains %q:\n%s", w, s)
				}
			}
			if tt.start != 0 && upd.Start != tt.start {
				t.Errorf("Start = %d, want %d", upd.Start, tt.start)
			}
			if tt.start == 0 && upd.Start != start {
				t.Errorf("Start changed to %d", upd.Start)
			}
		})
	}
}
