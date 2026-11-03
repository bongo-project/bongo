package caldav

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

// attendeeCopy is an invitation from a foreign organiser as a client uploads
// it after the user answered it.
func attendeeCopy(uid string, lines ...string) string {
	return meeting(uid, append([]string{"ORGANIZER;CN=Zed:mailto:zed@elsewhere.test"}, lines...)...)
}

func organiserParam(t *testing.T, raw []byte, key, param string) string {
	t.Helper()
	for _, ve := range decodeRaw(raw).Children {
		if ve.Name == ical.CompEvent && recurrenceKey(ve) == key {
			return ve.Props.Get(ical.PropOrganizer).Params.Get(param)
		}
	}
	t.Fatalf("no VEVENT %q in\n%s", key, raw)
	return ""
}

func (e *schedEnv) storedRaw(uid string) []byte {
	e.t.Helper()
	ev, err := e.events.Get(schUser, uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return ev.IcalRaw
}

// An answer is reported where the client looks: SCHEDULE-STATUS on the
// ORGANIZER of the answered VEVENT in the attendee's copy. An answer given at
// a plain alias is never sent in the alias's name and says so with 3.8; the
// ETag of the response is the stored object's.
func TestAttendeeAnswerReportsScheduleStatus(t *testing.T) {
	const alias = "sales@d.test"
	tests := []struct {
		name       string
		attendee   string
		wantStatus string
		wantMail   map[string]int
	}{
		{"alias cannot be sent as", alias, "3.8", map[string]int{}},
		{"primary address", schEmail, "1.1", map[string]int{"REPLY zed@elsewhere.test": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, rec := realSchedEnv(t)
			ics := attendeeCopy("obj", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:"+tt.attendee)
			code, body, hdr := e.do(schUser, "PUT", objPath("obj"), ics, nil)
			if code != http.StatusCreated {
				t.Fatalf("PUT = %d: %s", code, body)
			}
			rec.check(t, "the answer", tt.wantMail)
			stored := e.storedRaw("obj")
			if got := organiserParam(t, stored, "", "SCHEDULE-STATUS"); got != tt.wantStatus {
				t.Errorf("ORGANIZER SCHEDULE-STATUS = %q, want %q", got, tt.wantStatus)
			}
			if got := attendeeParam(t, stored, tt.attendee, "SCHEDULE-STATUS"); got != "" {
				t.Errorf("ATTENDEE carries SCHEDULE-STATUS %q", got)
			}
			_, data, got := e.do(schUser, "GET", objPath("obj"), "", nil)
			if hdr.Get("ETag") == "" || hdr.Get("ETag") != got.Get("ETag") {
				t.Errorf("PUT ETag %q, stored object ETag %q", hdr.Get("ETag"), got.Get("ETag"))
			}

			// The client uploads what it now holds, status included: nothing is
			// sent and the only write is the PUT itself.
			before := e.app.writes
			if code, body, _ := e.do(schUser, "PUT", objPath("obj"), data, nil); code >= 300 {
				t.Fatalf("re-PUT = %d: %s", code, body)
			}
			rec.check(t, "the re-upload", tt.wantMail)
			if d := e.app.writes - before; d != 1 {
				t.Errorf("the re-upload made %d writes, want 1", d)
			}
			if got := organiserParam(t, e.storedRaw("obj"), "", "SCHEDULE-STATUS"); got != tt.wantStatus {
				t.Errorf("after the re-upload SCHEDULE-STATUS = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

// An answer for one occurrence is reported on that occurrence's ORGANIZER.
func TestAttendeeOverrideAnswerReportsOnOverride(t *testing.T) {
	e, rec := realSchedEnv(t)
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Client//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:obj\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Planning\r\n" +
		"ORGANIZER:mailto:zed@elsewhere.test\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:sales@d.test\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:obj\r\nDTSTAMP:20300101T000000Z\r\nRECURRENCE-ID:20300513T090000Z\r\nDTSTART:20300513T090000Z\r\nDTEND:20300513T100000Z\r\nSUMMARY:Planning\r\n" +
		"ORGANIZER:mailto:zed@elsewhere.test\r\nATTENDEE;PARTSTAT=DECLINED:mailto:sales@d.test\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if code, body, _ := e.do(schUser, "PUT", objPath("obj"), ics, nil); code != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	rec.check(t, "the answer", map[string]int{})
	raw := e.storedRaw("obj")
	if got := organiserParam(t, raw, "20300513T090000Z", "SCHEDULE-STATUS"); got != "3.8" {
		t.Errorf("override ORGANIZER SCHEDULE-STATUS = %q, want 3.8", got)
	}
	if got := organiserParam(t, raw, "", "SCHEDULE-STATUS"); got != "" {
		t.Errorf("master ORGANIZER SCHEDULE-STATUS = %q, want none", got)
	}
}

// The limit counts the attendees the server would invite. Clients list the
// organiser among the attendees and mark attendees they schedule for
// themselves CLIENT; neither takes up the limit, and all 100 invitees are sent
// a REQUEST and given a status.
func TestAttendeeLimitCountsInvitedOnly(t *testing.T) {
	invitees := func(n int) []string {
		var out []string
		for i := range n {
			out = append(out, fmt.Sprintf("ATTENDEE:mailto:p%d@d.test", i))
		}
		return out
	}
	tests := []struct {
		name   string
		lines  []string
		wantOK bool
	}{
		{"organiser line, a CLIENT attendee and 100 invitees",
			append([]string{"ATTENDEE;PARTSTAT=ACCEPTED:mailto:" + schEmail, "ATTENDEE;SCHEDULE-AGENT=CLIENT:mailto:client@d.test"}, invitees(100)...), true},
		{"101 invitees", invitees(101), false},
		{"101 invitees and an organiser line", append([]string{"ATTENDEE:mailto:" + schEmail}, invitees(101)...), false},
		{"duplicate lines count once", append(invitees(100), invitees(100)...), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, rec := realSchedEnv(t)
			lines := append([]string{orgLine}, tt.lines...)
			code, body, _ := e.do(schUser, "PUT", objPath("big"), meeting("big", lines...), nil)
			if !tt.wantOK {
				if code != http.StatusForbidden || !strings.Contains(body, "max-attendees-per-instance") {
					t.Fatalf("PUT = %d %s, want the max-attendees precondition", code, body)
				}
				rec.check(t, "the refusal", map[string]int{})
				return
			}
			if code != http.StatusCreated {
				t.Fatalf("PUT = %d %s", code, body)
			}
			if got := len(rec.tally()); got != 100 {
				t.Fatalf("%d recipients mailed, want 100", got)
			}
			raw := e.storedRaw("big")
			for i := range 100 {
				a := fmt.Sprintf("p%d@d.test", i)
				if n := rec.tally()["REQUEST "+a]; n != 1 {
					t.Errorf("%s got %d REQUESTs, want 1", a, n)
				}
				if got := attendeeParam(t, raw, a, "SCHEDULE-STATUS"); got != "1.1" {
					t.Errorf("%s SCHEDULE-STATUS = %q, want 1.1", a, got)
				}
			}
		})
	}
}
