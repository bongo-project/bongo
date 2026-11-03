package caldav

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
)

// utcZones gives the real CalDAV scheduler's handler a fixed zone, so the
// result does not depend on the machine running the tests.
type utcZones struct{}

func (utcZones) Location(string) *time.Location { return time.UTC }

const pastBob = "bob@example.org"

// pastEvent builds an organiser upload with one attendee. The handler behind
// the real scheduler uses the real clock, so the fixtures use 2020 for past
// and 2099 for future.
func pastEvent(uid, start, end string, extra ...string) string {
	lines := append([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Client//EN", "BEGIN:VEVENT",
		"UID:" + uid, "DTSTAMP:20190101T000000Z", "DTSTART:" + start, "DTEND:" + end,
		"SUMMARY:Planning", orgLine, "ATTENDEE;CN=Bob:mailto:" + pastBob,
	}, extra...)
	lines = append(lines, "END:VEVENT", "END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n"
}

func pastEnv(t *testing.T) (*schedEnv, *recordingSender) {
	t.Helper()
	return realSchedEnvWith(t, func(h *handlers.CalendarHandler, _ *store.PebbleStore, _ *grantApplier) {
		h.SetZoneSource(utcZones{})
	})
}

func storedStatus(t *testing.T, e *schedEnv, uid string) string {
	t.Helper()
	ev, err := e.events.Get(schUser, uid)
	if err != nil {
		t.Fatal(err)
	}
	return attendeeParam(t, ev.IcalRaw, pastBob, "SCHEDULE-STATUS")
}

// A real PUT and DELETE of a single past event through the Backend send
// nothing and write no SCHEDULE-STATUS. The same harness sends for a future
// event and for the past one once it is moved to the future.
func TestRealCalDAVPastEventSendsNothing(t *testing.T) {
	e, rec := pastEnv(t)

	if code, body, _ := e.put("old", pastEvent("old", "20200506T090000Z", "20200506T100000Z")); code != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	rec.check(t, "the PUT of a past event", map[string]int{})
	if got := storedStatus(t, e, "old"); got != "" {
		t.Errorf("SCHEDULE-STATUS = %q on a past event, want none", got)
	}
	if code, _, _ := e.do(schUser, "DELETE", objPath("old"), "", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	rec.check(t, "the DELETE of a past event", map[string]int{})

	// Controls: the same harness does send.
	if code, body, _ := e.put("new", pastEvent("new", "20990506T090000Z", "20990506T100000Z")); code != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	rec.check(t, "the PUT of a future event", map[string]int{"REQUEST " + pastBob: 1})
	if code, _, _ := e.do(schUser, "DELETE", objPath("new"), "", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	rec.check(t, "the DELETE of a future event", map[string]int{"REQUEST " + pastBob: 1, "CANCEL " + pastBob: 1})

	// Past to future: one REQUEST.
	rec.sent = nil
	if code, body, _ := e.put("moved", pastEvent("moved", "20200506T090000Z", "20200506T100000Z")); code != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	if code, body, _ := e.put("moved", pastEvent("moved", "20990506T090000Z", "20990506T100000Z")); code != http.StatusCreated {
		t.Fatalf("re-PUT = %d: %s", code, body)
	}
	rec.check(t, "moving the event to the future", map[string]int{"REQUEST " + pastBob: 1})
}

// An organiser's series that has ended sends nothing on create or delete,
// whether it is bounded by COUNT or UNTIL; an open-ended one does send.
func TestRealCalDAVEndedSeriesSendsNothing(t *testing.T) {
	tests := []struct {
		name  string
		rrule string
		sends bool
	}{
		{"count ended", "RRULE:FREQ=WEEKLY;COUNT=3", false},
		{"until ended", "RRULE:FREQ=WEEKLY;UNTIL=20200601T090000Z", false},
		{"open-ended since 2020", "RRULE:FREQ=WEEKLY", true},
		{"count still running", "RRULE:FREQ=WEEKLY;COUNT=100000", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, rec := pastEnv(t)
			if code, body, _ := e.put("ser", pastEvent("ser", "20200506T090000Z", "20200506T100000Z", tt.rrule)); code != http.StatusCreated {
				t.Fatalf("PUT = %d: %s", code, body)
			}
			want := map[string]int{}
			if tt.sends {
				want["REQUEST "+pastBob] = 1
			}
			rec.check(t, "the PUT", want)
			if code, _, _ := e.do(schUser, "DELETE", objPath("ser"), "", nil); code != http.StatusNoContent {
				t.Fatalf("DELETE = %d", code)
			}
			if tt.sends {
				want["CANCEL "+pastBob] = 1
			}
			rec.check(t, "the DELETE", want)
		})
	}
}

// The attendee side: deleting a past invitation declines nothing, deleting a
// future one declines once. Real scheduler, real REPLY path.
func TestRealCalDAVDeleteOfPastInvitationRepliesNothing(t *testing.T) {
	invite := func(uid, start, end string) string {
		return strings.Join([]string{
			"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Client//EN", "BEGIN:VEVENT",
			"UID:" + uid, "DTSTAMP:20190101T000000Z", "DTSTART:" + start, "DTEND:" + end, "SUMMARY:Planning",
			"ORGANIZER;CN=Zed:mailto:zed@elsewhere.test", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:" + schEmail,
			"END:VEVENT", "END:VCALENDAR",
		}, "\r\n") + "\r\n"
	}
	e, rec := pastEnv(t)
	for uid, when := range map[string][2]string{"past": {"20200506T090000Z", "20200506T100000Z"}, "future": {"20990506T090000Z", "20990506T100000Z"}} {
		if code, body, _ := e.put(uid, invite(uid, when[0], when[1])); code != http.StatusCreated {
			t.Fatalf("PUT = %d: %s", code, body)
		}
	}
	rec.sent = nil // the PUTs answer an accepted invitation too
	if code, _, _ := e.do(schUser, "DELETE", objPath("past"), "", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	rec.check(t, "the DELETE of a past invitation", map[string]int{})
	if code, _, _ := e.do(schUser, "DELETE", objPath("future"), "", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	rec.check(t, "the DELETE of a future invitation", map[string]int{"REPLY zed@elsewhere.test": 1})
}
