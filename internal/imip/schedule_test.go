package imip

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const scheduledICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Client//Cal//EN\r\n" +
	"BEGIN:VEVENT\r\nUID:sched-1@example.org\r\nDTSTAMP:20300101T000000Z\r\n" +
	"DTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\nSUMMARY:Sched\r\n" +
	"ORGANIZER;SCHEDULE-AGENT=SERVER:mailto:olive@example.org\r\n" +
	"ATTENDEE;CN=Bob;SCHEDULE-STATUS=1.1;SCHEDULE-AGENT=SERVER;SCHEDULE-FORCE-SEND=REQUEST;X-KEEP=1:mailto:bob@example.org\r\n" +
	"ATTENDEE;SCHEDULE-AGENT=CLIENT:mailto:carol@example.org\r\n" +
	"END:VEVENT\r\nEND:VCALENDAR\r\n"

// RFC 6638 section 7.3: the scheduling parameters never leave the server.
func TestBuildCalendarStripsScheduleParams(t *testing.T) {
	e := &bongov1.Event{
		Guid: "g", Organizer: "olive@example.org", IcalRaw: []byte(scheduledICS),
		Attendees: []*bongov1.Attendee{{Address: "bob@example.org"}, {Address: "carol@example.org"}},
	}
	cal, err := BuildCalendar(MethodRequest, e, "", testNow)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(strings.ToUpper(out), "SCHEDULE-") {
		t.Errorf("outgoing iTIP carries a scheduling parameter:\n%s", out)
	}
	if !strings.Contains(out, "X-KEEP=1") {
		t.Errorf("an unrelated parameter was dropped:\n%s", out)
	}
}

func TestInviteScheduled(t *testing.T) {
	part := vcal("REPLY", "UID:u1@example.org\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\n"+
		"ORGANIZER:mailto:olive@example.org\r\n"+
		"ATTENDEE;PARTSTAT=ACCEPTED;SCHEDULE-STATUS=2.0:mailto:bob@example.org\r\n"+
		"ATTENDEE;PARTSTAT=DECLINED:mailto:mallory@example.org\r\n"+
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:x\r\nTRIGGER:-PT5M\r\nEND:VALARM\r\n")
	inv, err := Parse([]byte(part), "REPLY")
	if err != nil {
		t.Fatal(err)
	}
	out, err := inv.Scheduled("bob@example.org")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"METHOD:REPLY", "mailto:bob@example.org"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	for _, bad := range []string{"mallory", "VALARM", "SCHEDULE-STATUS"} {
		if strings.Contains(s, bad) {
			t.Errorf("sanitised item still holds %q:\n%s", bad, s)
		}
	}
	// The item must parse back as the same message.
	if back, err := Parse(out, "REPLY"); err != nil || back.UID != "u1@example.org" {
		t.Errorf("round trip: %v %v", back, err)
	}
}
