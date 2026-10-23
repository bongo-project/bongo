package icalevent

import (
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

func decode(t *testing.T, body string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\n" + body + "END:VCALENDAR\r\n")).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func ev(summary, extra string) string {
	return "BEGIN:VEVENT\r\nUID:u\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300301T090000Z\r\nSUMMARY:" + summary + "\r\n" + extra + "END:VEVENT\r\n"
}

// evAt is ev with a chosen DTSTART.
func evAt(summary, dtstart, extra string) string {
	return "BEGIN:VEVENT\r\nUID:u\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:" + dtstart + "\r\nSUMMARY:" + summary + "\r\n" + extra + "END:VEVENT\r\n"
}

func TestMaster(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		want     string
		fallback bool
	}{
		{"master first", ev("M", "") + ev("O", "RECURRENCE-ID:20300308T090000Z\r\n"), "M", false},
		{"master last", ev("O", "RECURRENCE-ID:20300308T090000Z\r\n") + ev("M", ""), "M", false},
		{"overrides only earliest", ev("Late", "RECURRENCE-ID:20300315T090000Z\r\n") + ev("Early", "RECURRENCE-ID:20300308T090000Z\r\n"), "Early", true},
		// An unparsable RECURRENCE-ID is ordered by its DTSTART instead.
		{"unparsable id earliest dtstart first", evAt("Bad", "20300302T090000Z", "RECURRENCE-ID:garbage\r\n") + evAt("Valid", "20300308T090000Z", "RECURRENCE-ID:20300308T090000Z\r\n"), "Bad", true},
		{"unparsable id earliest dtstart last", evAt("Valid", "20300308T090000Z", "RECURRENCE-ID:20300308T090000Z\r\n") + evAt("Bad", "20300302T090000Z", "RECURRENCE-ID:garbage\r\n"), "Bad", true},
		{"unparsable id later dtstart", evAt("Bad", "20300320T090000Z", "RECURRENCE-ID:garbage\r\n") + evAt("Valid", "20300308T090000Z", "RECURRENCE-ID:20300308T090000Z\r\n"), "Valid", true},
		{"no vevent", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, fb := Master(decode(t, tc.body))
			if fb != tc.fallback {
				t.Errorf("fallback = %v, want %v", fb, tc.fallback)
			}
			if tc.want == "" {
				if m != nil {
					t.Fatalf("want nil master")
				}
				return
			}
			if m == nil {
				t.Fatal("nil master")
			}
			if got, _ := m.Props.Text(ical.PropSummary); got != tc.want {
				t.Errorf("master = %q, want %q", got, tc.want)
			}
		})
	}
}
