package caldav

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Deleting an invitation the user answered declines it (RFC 6638 section
// 3.2.2.1): one REPLY, from an address the user may send as, unless the
// server must not or need not say anything.
func TestDeleteOfAnInvitationDeclinesIt(t *testing.T) {
	const zed = "ORGANIZER;CN=Zed:mailto:zed@elsewhere.test"
	me := "ATTENDEE;PARTSTAT=ACCEPTED:mailto:" + schEmail
	before := time.Date(2030, 5, 1, 8, 17, 0, 0, time.UTC)
	after := time.Date(2030, 5, 7, 12, 34, 56, 0, time.UTC)
	tests := []struct {
		name      string
		lines     []string
		now       time.Time
		replies   int
		organises int
		attendee  string
	}{
		{"accepted, future", []string{zed, me}, before, 1, 0, schEmail},
		{"needs-action, future", []string{zed, "ATTENDEE:mailto:" + schEmail}, before, 1, 0, schEmail},
		{"invited at an alias and a send-as address", []string{zed, "ATTENDEE;PARTSTAT=ACCEPTED:mailto:sales@d.test", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:alice.work@d.test"}, before, 1, 0, "alice.work@d.test"},
		{"schedule agent client", []string{"ORGANIZER;SCHEDULE-AGENT=CLIENT:mailto:zed@elsewhere.test", me}, before, 0, 0, ""},
		{"schedule agent none", []string{"ORGANIZER;SCHEDULE-AGENT=NONE:mailto:zed@elsewhere.test", me}, before, 0, 0, ""},
		{"cancelled", []string{zed, me, "STATUS:CANCELLED"}, before, 0, 0, ""},
		{"already declined", []string{zed, "ATTENDEE;PARTSTAT=DECLINED:mailto:" + schEmail}, before, 0, 0, ""},
		{"ended yesterday", []string{zed, me}, after, 0, 0, ""},
		{"open-ended series, first instance past", []string{zed, me, "RRULE:FREQ=WEEKLY"}, after, 1, 0, schEmail},
		{"bounded series, last occurrence ended", []string{zed, me, "RRULE:FREQ=DAILY;COUNT=2"}, after, 0, 0, ""},
		{"bounded series, still running", []string{zed, me, "RRULE:FREQ=DAILY;COUNT=10"}, after, 1, 0, schEmail},
		{"not invited", []string{zed, "ATTENDEE;SCHEDULE-AGENT=NONE:mailto:bob@d.test"}, before, 0, 0, ""},
		{"organiser is an alias of the user", []string{"ORGANIZER:mailto:sales@d.test", me}, before, 0, 0, ""},
		{"the user organises", []string{orgLine, "ATTENDEE:mailto:bob@d.test"}, before, 0, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSchedEnv(t, true)
			e.b.now = func() time.Time { return tt.now }
			if code, body, _ := e.put("d1", meeting("d1", tt.lines...)); code != http.StatusCreated {
				t.Fatalf("PUT = %d: %s", code, body)
			}
			o0, r0 := e.sched.counts()
			if code, _, _ := e.do(schUser, "DELETE", objPath("d1"), "", nil); code != http.StatusNoContent {
				t.Fatalf("DELETE = %d", code)
			}
			o1, r1 := e.sched.counts()
			if r1-r0 != tt.replies || o1-o0 != tt.organises {
				t.Fatalf("DELETE made %d replies and %d organise calls, want %d and %d", r1-r0, o1-o0, tt.replies, tt.organises)
			}
			if tt.replies == 1 {
				got := e.sched.replies[r1-1]
				if got.partstat != "DECLINED" || got.recurrenceID != "" || got.attendee != tt.attendee || got.user != schUser {
					t.Errorf("reply = %+v, want DECLINED from %s for the whole event", got, tt.attendee)
				}
			}
		})
	}
}

// A grantee deleting from a shared calendar never declines for the owner.
func TestSharedDeleteOfAnInvitationDeclinesNothing(t *testing.T) {
	e := newShEnv(t)
	f := newFakeSched()
	e.backend.SetScheduler(f)
	e.backend.now = func() time.Time { return time.Date(2030, 5, 1, 8, 17, 0, 0, time.UTC) }
	ics := meeting("sh-inv", "ORGANIZER:mailto:zed@elsewhere.test", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:"+schEmail)
	p := sharedPath(shWrite, shCalA) + "sh-inv.ics"
	if code, body := e.do(shWrite, "PUT", p, ics, nil); code != http.StatusCreated {
		t.Fatalf("grantee PUT = %d: %s", code, body)
	}
	if code, _ := e.do(shWrite, "DELETE", p, "", nil); code != http.StatusNoContent {
		t.Fatalf("grantee DELETE = %d", code)
	}
	if _, r := f.counts(); r != 0 {
		t.Errorf("a grantee's DELETE sent %d replies in the owner's name", r)
	}
}

// With the real scheduler the decline is a REPLY mail to the organiser from a
// send-as address; an invitation held only at a plain alias sends nothing.
func TestDeleteOfAnInvitationMailsTheDecline(t *testing.T) {
	future := func(e *schedEnv) {
		e.b.now = func() time.Time { return time.Date(2030, 5, 1, 8, 17, 0, 0, time.UTC) }
	}
	t.Run("primary address", func(t *testing.T) {
		e, rec := realSchedEnv(t)
		future(e)
		e.do(schUser, "PUT", objPath("r1"), attendeeCopy("r1", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:"+schEmail), nil)
		rec.check(t, "the answer", map[string]int{"REPLY zed@elsewhere.test": 1})
		if code, _, _ := e.do(schUser, "DELETE", objPath("r1"), "", nil); code != http.StatusNoContent {
			t.Fatalf("DELETE = %d", code)
		}
		rec.check(t, "the delete", map[string]int{"REPLY zed@elsewhere.test": 2})
		last := rec.sent[len(rec.sent)-1]
		if last.method != "REPLY" || last.from != schEmail || !strings.Contains(last.calendar, "PARTSTAT=DECLINED") {
			t.Errorf("last mail = %s from %s\n%s", last.method, last.from, last.calendar)
		}
	})
	t.Run("alias only", func(t *testing.T) {
		e, rec := realSchedEnv(t)
		future(e)
		e.do(schUser, "PUT", objPath("r2"), attendeeCopy("r2", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:sales@d.test"), nil)
		if code, _, _ := e.do(schUser, "DELETE", objPath("r2"), "", nil); code != http.StatusNoContent {
			t.Fatalf("DELETE = %d", code)
		}
		rec.check(t, "the delete", map[string]int{})
	})
}

// An all-day invitation is over at the end of its last day in the user's zone,
// not in UTC: the scheduler's zone reaches the past check.
func TestDeleteOfAllDayInvitationUsesTheUserZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Client//EN\r\nBEGIN:VEVENT\r\nUID:ad1\r\nDTSTAMP:20300101T000000Z\r\n" +
		"DTSTART;VALUE=DATE:20300509\r\nDTEND;VALUE=DATE:20300510\r\nSUMMARY:Away\r\n" +
		"ORGANIZER;CN=Zed:mailto:zed@elsewhere.test\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:" + schEmail + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	tests := []struct {
		name    string
		now     time.Time
		replies int
	}{
		// Local midnight in Berlin (UTC+2) is 22:00Z.
		{"one second before local midnight", time.Date(2030, 5, 9, 21, 59, 59, 0, time.UTC), 1},
		{"at local midnight", time.Date(2030, 5, 9, 22, 0, 0, 0, time.UTC), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSchedEnv(t, true)
			e.sched.zone = berlin
			e.b.now = func() time.Time { return tt.now }
			if code, body, _ := e.put("ad1", ics); code != http.StatusCreated {
				t.Fatalf("PUT = %d: %s", code, body)
			}
			_, r0 := e.sched.counts()
			if code, _, _ := e.do(schUser, "DELETE", objPath("ad1"), "", nil); code != http.StatusNoContent {
				t.Fatalf("DELETE = %d", code)
			}
			if _, r := e.sched.counts(); r-r0 != tt.replies {
				t.Errorf("DELETE made %d replies, want %d", r-r0, tt.replies)
			}
		})
	}
}
