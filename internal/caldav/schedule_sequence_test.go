package caldav

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

// sequenceOf reads the SEQUENCE of the master VEVENT in raw, or -1 when absent.
func sequenceOf(t *testing.T, raw []byte) string {
	t.Helper()
	m := masterOf(decodeRaw(raw))
	if m == nil {
		t.Fatalf("no master VEVENT in\n%s", raw)
	}
	if p := m.Props.Get(ical.PropSequence); p != nil {
		return p.Value
	}
	return "-"
}

// Each case stores a meeting, then re-PUTs it changed, and checks the SEQUENCE
// in storage, in GET and in the event the scheduler builds the REQUEST from.
func TestOrganiserPutRaisesSequence(t *testing.T) {
	const att = "ATTENDEE:mailto:bob@d.test"
	for _, tc := range []struct {
		name    string
		first   []string // extra lines of the first PUT
		change  func(ics string) string
		extra   []string // extra lines of the second PUT
		wantSeq string   // SEQUENCE in storage and in the Organise call
	}{
		{"time change with SEQUENCE 0", []string{orgLine, att},
			func(s string) string {
				return strings.Replace(s, "DTSTART:20300506T090000Z", "DTSTART:20300506T110000Z", 1)
			},
			[]string{orgLine, att, "SEQUENCE:0"}, "1"},
		{"time change without SEQUENCE", []string{orgLine, att},
			func(s string) string {
				return strings.Replace(s, "DTSTART:20300506T090000Z", "DTSTART:20300506T110000Z", 1)
			},
			[]string{orgLine, att}, "1"},
		{"client SEQUENCE above stored is kept", []string{orgLine, att},
			func(s string) string {
				return strings.Replace(s, "DTSTART:20300506T090000Z", "DTSTART:20300506T110000Z", 1)
			},
			[]string{orgLine, att, "SEQUENCE:5"}, "5"},
		{"summary only", []string{orgLine, att},
			func(s string) string { return strings.Replace(s, "SUMMARY:Planning", "SUMMARY:Renamed", 1) },
			[]string{orgLine, att}, "1"},
		{"description only", []string{orgLine, att},
			func(s string) string { return s },
			[]string{orgLine, att, "DESCRIPTION:Agenda"}, "1"},
		{"identical re-upload does not bump", []string{orgLine, att},
			func(s string) string { return s },
			[]string{orgLine, att}, "-"},
		{"echoed SCHEDULE-STATUS does not bump", []string{orgLine, att},
			func(s string) string { return s },
			[]string{orgLine, "ATTENDEE;SCHEDULE-STATUS=1.1:mailto:bob@d.test"}, "-"},
		{"alarm only does not bump", []string{orgLine, att},
			func(s string) string { return s },
			[]string{orgLine, att, "BEGIN:VALARM", "ACTION:DISPLAY", "DESCRIPTION:x", "TRIGGER:-PT10M", "END:VALARM"}, "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSchedEnv(t, true)
			if code, body, _ := e.put("s1", meeting("s1", tc.first...)); code != http.StatusCreated {
				t.Fatalf("first PUT = %d: %s", code, body)
			}
			second := tc.change(meeting("s1", tc.extra...))
			if code, body, _ := e.put("s1", second); code != http.StatusCreated {
				t.Fatalf("second PUT = %d: %s", code, body)
			}
			stored := e.stored("s1")
			if got := sequenceOf(t, stored.IcalRaw); got != tc.wantSeq {
				t.Errorf("stored SEQUENCE = %q, want %q", got, tc.wantSeq)
			}
			_, body, _ := e.do(schUser, "GET", objPath("s1"), "", nil)
			if sequenceOf(t, []byte(body)) != tc.wantSeq {
				t.Errorf("GET SEQUENCE = %q, want %q", sequenceOf(t, []byte(body)), tc.wantSeq)
			}
			if o, _ := e.sched.counts(); o != 2 {
				t.Fatalf("Organise calls = %d, want 2", o)
			}
			after := e.sched.organise[1].after
			want := tc.wantSeq
			if want == "-" {
				want = "0"
			}
			if got := itoa(after.Sequence); got != want {
				t.Errorf("event handed to the scheduler has Sequence %s, want %s", got, want)
			}
			if got := itoa(stored.Sequence); got != want {
				t.Errorf("stored Sequence field = %s, want %s", got, want)
			}
			if got := sequenceOf(t, after.IcalRaw); got != tc.wantSeq {
				t.Errorf("scheduler saw raw SEQUENCE %q, want %q", got, tc.wantSeq)
			}
		})
	}
}

func itoa(n int32) string { return strconv.Itoa(int(n)) }

// An attendee's own PUT never touches the organiser's SEQUENCE.
func TestAttendeePutDoesNotBumpSequence(t *testing.T) {
	foreign := "ORGANIZER:mailto:zed@elsewhere.test"
	e := newSchedEnv(t, true)
	e.put("a1", meeting("a1", foreign, "ATTENDEE:mailto:"+schEmail, "SEQUENCE:2"))
	moved := strings.Replace(meeting("a1", foreign, "ATTENDEE:mailto:"+schEmail, "SEQUENCE:2"),
		"DTSTART:20300506T090000Z", "DTSTART:20300506T110000Z", 1)
	if code, body, _ := e.put("a1", moved); code != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	if got := sequenceOf(t, e.stored("a1").IcalRaw); got != "2" {
		t.Errorf("SEQUENCE = %q, an attendee's PUT must keep the organiser's value", got)
	}
}

// A grantee's PUT into a shared calendar is only stored.
func TestSharedPutDoesNotBumpSequence(t *testing.T) {
	e := newShEnv(t)
	f := newFakeSched()
	e.backend.SetScheduler(f)
	ics := strings.Replace(shICS("sh-seq", "Shared"), "SUMMARY:Shared", "SUMMARY:Shared\r\nORGANIZER:mailto:alice@d.test\r\nATTENDEE:mailto:bob@d.test", 1)
	if code, body := e.do(shWrite, "PUT", sharedPath(shWrite, shCalA)+"sh-seq.ics", ics, nil); code != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	moved := strings.Replace(ics, "DTSTART:20301105T090000Z", "DTSTART:20301105T110000Z", 1)
	if code, body := e.do(shWrite, "PUT", sharedPath(shWrite, shCalA)+"sh-seq.ics", moved, nil); code != http.StatusCreated && code != http.StatusNoContent {
		t.Fatalf("second PUT = %d: %s", code, body)
	}
	ev, err := e.events.Get(shOwner, "sh-seq")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Sequence != 0 || sequenceOf(t, ev.IcalRaw) != "-" {
		t.Errorf("shared PUT changed SEQUENCE: field %d, raw %q", ev.Sequence, sequenceOf(t, ev.IcalRaw))
	}
}
