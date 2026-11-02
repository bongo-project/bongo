package icalevent

import (
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

func TestToEventReadsTransparency(t *testing.T) {
	for _, tc := range []struct {
		transp string
		want   bool
	}{
		{"", false},
		{"TRANSP:OPAQUE\r\n", false},
		{"TRANSP:TRANSPARENT\r\n", true},
		{"TRANSP:transparent\r\n", true},
	} {
		raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//t//EN\r\nBEGIN:VEVENT\r\nUID:u1\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:20261110T090000Z\r\nDTEND:20261110T100000Z\r\nSUMMARY:x\r\n" + tc.transp + "END:VEVENT\r\nEND:VCALENDAR\r\n"
		cal, err := ical.NewDecoder(strings.NewReader(raw)).Decode()
		if err != nil {
			t.Fatal(err)
		}
		e, _ := ToEvent(cal)
		if e.Transparent != tc.want {
			t.Errorf("TRANSP %q: Transparent = %v, want %v", strings.TrimSpace(tc.transp), e.Transparent, tc.want)
		}
	}
}
