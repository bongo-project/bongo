package imip

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/icalevent"
)

func vcal(method string, events ...string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//X//EN\r\n")
	if method != "" {
		b.WriteString("METHOD:" + method + "\r\n")
	}
	for _, e := range events {
		b.WriteString("BEGIN:VEVENT\r\n" + e + "END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

const baseEvent = "UID:u1@example.org\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\n" +
	"SUMMARY:Planning\r\nORGANIZER;CN=Olive:mailto:olive@example.org\r\nSEQUENCE:2\r\n" +
	"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:bob@example.org\r\n"

func TestParse(t *testing.T) {
	zone := "BEGIN:VTIMEZONE\r\nTZID:Europe/Amsterdam\r\nBEGIN:STANDARD\r\nDTSTART:19701025T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n"
	tzCal := strings.Replace(vcal("REQUEST", strings.Replace(baseEvent, "DTSTART:20300506T090000Z", "DTSTART;TZID=Europe/Amsterdam:20300506T090000", 1)), "METHOD:REQUEST\r\n", "METHOD:REQUEST\r\n"+zone, 1)
	allDay := strings.NewReplacer("DTSTART:20300506T090000Z", "DTSTART;VALUE=DATE:20300506", "DTEND:20300506T100000Z", "DTEND;VALUE=DATE:20300507").Replace(baseEvent)
	override := strings.Replace(baseEvent, "DTSTART:", "RECURRENCE-ID:20300513T090000Z\r\nDTSTART:", 1)
	master := baseEvent + "RRULE:FREQ=WEEKLY\r\n"
	twoUID := strings.Replace(baseEvent, "u1@", "u2@", 1)
	noStart := strings.Replace(baseEvent, "DTSTART:20300506T090000Z\r\n", "", 1)
	noUID := strings.Replace(baseEvent, "UID:u1@example.org\r\n", "", 1)
	badOrg := strings.Replace(baseEvent, "mailto:olive@example.org", "mailto:not an address", 1)

	big := vcal("REQUEST", baseEvent+"DESCRIPTION:"+strings.Repeat("a", MaxPartBytes)+"\r\n")
	// Exactly one byte over the limit, not a round boundary.
	over := big[:MaxPartBytes+1]

	tests := []struct {
		name     string
		part     string
		ctMethod string
		wantErr  error
		check    func(t *testing.T, inv *Invite)
	}{
		{name: "request with zone", part: tzCal, ctMethod: "REQUEST", check: func(t *testing.T, inv *Invite) {
			if inv.Method != MethodRequest || inv.UID != "u1@example.org" || inv.Sequence != 2 || inv.Organizer != "olive@example.org" {
				t.Errorf("got %+v", inv)
			}
			if len(inv.Zones) != 1 || inv.Event.Timezone != "Europe/Amsterdam" || inv.Event.StartTime == 0 {
				t.Errorf("zones %d tz %q start %d", len(inv.Zones), inv.Event.Timezone, inv.Event.StartTime)
			}
		}},
		{name: "no content type method", part: vcal("REQUEST", baseEvent), check: func(t *testing.T, inv *Invite) {
			if inv.Method != MethodRequest {
				t.Errorf("method %q", inv.Method)
			}
		}},
		{name: "lower case method", part: vcal("request", baseEvent), ctMethod: "Request", check: func(t *testing.T, inv *Invite) {
			if inv.Method != MethodRequest {
				t.Errorf("method %q", inv.Method)
			}
		}},
		{name: "all day", part: vcal("REQUEST", allDay), ctMethod: "REQUEST", check: func(t *testing.T, inv *Invite) {
			if !inv.Event.AllDay {
				t.Error("not all-day")
			}
		}},
		{name: "master with override", part: vcal("REQUEST", master, override), ctMethod: "REQUEST", check: func(t *testing.T, inv *Invite) {
			if len(inv.Events) != 2 || inv.RecurrenceID != "" || inv.Event.Rrule == "" {
				t.Errorf("events %d rid %q rrule %q", len(inv.Events), inv.RecurrenceID, inv.Event.Rrule)
			}
		}},
		{name: "instance only", part: vcal("REQUEST", override), ctMethod: "REQUEST", check: func(t *testing.T, inv *Invite) {
			if inv.RecurrenceID != "20300513T090000Z" {
				t.Errorf("rid %q", inv.RecurrenceID)
			}
		}},
		{name: "cancel", part: vcal("CANCEL", baseEvent+"STATUS:CANCELLED\r\n"), ctMethod: "CANCEL", check: func(t *testing.T, inv *Invite) {
			if inv.Method != MethodCancel || inv.Event.Status != "CANCELLED" {
				t.Errorf("got %q %q", inv.Method, inv.Event.Status)
			}
		}},
		{name: "mismatched method", part: vcal("REQUEST", baseEvent), ctMethod: "CANCEL", wantErr: ErrMethodMismatch},
		{name: "no method", part: vcal("", baseEvent), ctMethod: "REQUEST", wantErr: ErrMethodMismatch},
		{name: "oversize", part: over, ctMethod: "REQUEST", wantErr: ErrTooLarge},
		{name: "garbage", part: "this is not a calendar", ctMethod: "REQUEST", wantErr: ErrMalformed},
		{name: "empty", part: "", wantErr: ErrMalformed},
		{name: "no vevent", part: vcal("REQUEST"), ctMethod: "REQUEST", wantErr: ErrNoEvent},
		{name: "two uids", part: vcal("REQUEST", baseEvent, twoUID), ctMethod: "REQUEST", wantErr: ErrMultipleUIDs},
		{name: "no dtstart", part: vcal("REQUEST", noStart), ctMethod: "REQUEST", wantErr: ErrMalformed},
		{name: "no uid", part: vcal("REQUEST", noUID), ctMethod: "REQUEST", wantErr: ErrMalformed},
		{name: "bad organiser", part: vcal("REQUEST", badOrg), ctMethod: "REQUEST", wantErr: ErrOrganiser},
		{name: "too many events", part: vcal("REQUEST", repeatOverrides(MaxEvents+1)...), ctMethod: "REQUEST", wantErr: ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv, err := Parse([]byte(tt.part), tt.ctMethod)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, inv)
		})
	}
}

// The limit applies to the part as received: exactly MaxPartBytes is still read.
func TestParseSizeBoundary(t *testing.T) {
	pad := MaxPartBytes - len(vcal("REQUEST", baseEvent+"DESCRIPTION:\r\n"))
	part := vcal("REQUEST", baseEvent+"DESCRIPTION:"+strings.Repeat("a", pad)+"\r\n")
	if len(part) != MaxPartBytes {
		t.Fatalf("test part is %d bytes", len(part))
	}
	if _, err := Parse([]byte(part), "REQUEST"); err != nil {
		t.Fatalf("part at the limit: %v", err)
	}
	if _, err := Parse([]byte(part+" "), "REQUEST"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("part over the limit: %v", err)
	}
}

func repeatOverrides(n int) []string {
	var out []string
	for i := range n {
		id := "2030" + string(rune('0'+(i/28+1)%10)) + "1" + string(rune('0'+i%10)) + "T090000Z"
		out = append(out, strings.Replace(baseEvent, "DTSTART:", "RECURRENCE-ID:"+id+"\r\nDTSTART:", 1))
	}
	return out
}

func TestStoredCalendar(t *testing.T) {
	part := vcal("REQUEST", baseEvent+"BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:x\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\n")
	inv, err := Parse([]byte(part), "REQUEST")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := inv.Stored("BOB@example.org", "ACCEPTED")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, bad := range []string{"METHOD", "VALARM"} {
		if strings.Contains(s, bad) {
			t.Errorf("stored form keeps %s:\n%s", bad, s)
		}
	}
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	ve, _ := icalevent.Master(cal)
	if got := ve.Props.Get(ical.PropAttendee).Params.Get(ical.ParamParticipationStatus); got != "ACCEPTED" {
		t.Errorf("PARTSTAT = %q", got)
	}
	if got := ve.Props.Get(ical.PropUID).Value; got != "u1@example.org" {
		t.Errorf("UID = %q", got)
	}
}

func TestRecurrenceKey(t *testing.T) {
	tests := []struct {
		value, tzid, want string
	}{
		{"20300513T090000Z", "", "20300513T090000Z"},
		{"20300513", "", "20300513"},
		{"20300513T110000", "Europe/Amsterdam", "20300513T090000Z"},
	}
	for _, tt := range tests {
		p := ical.NewProp(ical.PropRecurrenceID)
		p.Value = tt.value
		if tt.tzid != "" {
			p.Params.Set("TZID", tt.tzid)
		}
		if got, ok := RecurrenceKey(p); !ok || got != tt.want {
			t.Errorf("%q %q = %q, %v", tt.value, tt.tzid, got, ok)
		}
	}
}

func TestDMARCResult(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		id     string
		want   string
	}{
		{"fail", []string{"mx.example.org; spf=pass; dkim=pass header.d=a.example; dmarc=fail"}, "mx.example.org", "fail"},
		{"pass", []string{"mx.example.org; dmarc=pass"}, "MX.example.org", "pass"},
		{"missing token", []string{"mx.example.org; spf=pass"}, "mx.example.org", ""},
		{"foreign authserv-id ignored", []string{"evil.example; dmarc=pass", "mx.example.org; dmarc=fail"}, "mx.example.org", "fail"},
		{"only foreign", []string{"evil.example; dmarc=fail"}, "mx.example.org", ""},
		{"no hostname configured", []string{"mx.example.org; dmarc=fail"}, "", ""},
		{"version suffix", []string{"mx.example.org 1; dmarc=fail"}, "mx.example.org", "fail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DMARCResult(tt.values, tt.id); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
