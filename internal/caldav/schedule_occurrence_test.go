package caldav

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

const occBob = "bob@d.test"

// weekly is a Monday 09:00Z series from 2030-10-28; its 2030-11-04 occurrence
// is the one the stories move, cancel and exclude.
func weekly(uid string, master []string, others ...string) string {
	lines := append([]string{
		"UID:" + uid, "DTSTAMP:20300101T000000Z", "DTSTART:20301028T090000Z", "DTEND:20301028T100000Z",
		"RRULE:FREQ=WEEKLY", "SUMMARY:Weekly",
	}, master...)
	return calendarOf(veventBlock(lines...), others...)
}

func veventBlock(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func calendarOf(first string, others ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Client//EN\r\n" + first + strings.Join(others, "") + "END:VCALENDAR\r\n"
}

// moved is an override of the 2030-11-04 occurrence that starts at 11:00Z.
func moved(uid string, extra ...string) string {
	return veventBlock(append([]string{
		"UID:" + uid, "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20301104T090000Z",
		"DTSTART:20301104T110000Z", "DTEND:20301104T120000Z", "SUMMARY:Weekly",
	}, extra...)...)
}

var bobLine = "ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:" + occBob

// mailed returns the messages of rec as "METHOD recipient".
func mailed(rec *recordingSender) string {
	var out []string
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, m := range rec.sent {
		out = append(out, m.method+" "+strings.Join(m.to, ","))
	}
	return strings.Join(out, "; ")
}

func (r *recordingSender) last() mailedITIP {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent[len(r.sent)-1]
}

func (r *recordingSender) reset() {
	r.mu.Lock()
	r.sent = nil
	r.mu.Unlock()
}

func mustPut(t *testing.T, e *schedEnv, uid, ics string) {
	t.Helper()
	if code, body, _ := e.put(uid, ics); code != http.StatusCreated && code != http.StatusNoContent {
		t.Fatalf("PUT %s = %d: %s", uid, code, body)
	}
}

func overrideOf(t *testing.T, raw []byte, rid string) *ical.Component {
	t.Helper()
	for _, ve := range eventsOf(decodeRaw(raw)) {
		if recurrenceKey(ve) == rid {
			return ve
		}
	}
	t.Fatalf("no override %s in\n%s", rid, raw)
	return nil
}

func TestOrganiserOverrideSendsOneOccurrenceRequest(t *testing.T) {
	e, rec := realSchedEnv(t)
	mustPut(t, e, "s1", weekly("s1", []string{orgLine, bobLine}))
	if got := mailed(rec); got != "REQUEST "+occBob {
		t.Fatalf("create sent %q, want the series REQUEST", got)
	}
	rec.reset()

	mustPut(t, e, "s1", weekly("s1", []string{orgLine, bobLine}, moved("s1", orgLine, bobLine)))
	if got := mailed(rec); got != "REQUEST "+occBob {
		t.Fatalf("override PUT sent %q, want one REQUEST", got)
	}
	m := rec.last()
	for _, want := range []string{"RECURRENCE-ID:20301104T090000Z", "UID:s1", "DTSTART:20301104T110000Z"} {
		if !strings.Contains(m.calendar, want) {
			t.Errorf("the REQUEST lacks %s:\n%s", want, m.calendar)
		}
	}
	for _, bad := range []string{"RRULE", "EXDATE"} {
		if strings.Contains(m.calendar, bad) {
			t.Errorf("the occurrence REQUEST carries %s:\n%s", bad, m.calendar)
		}
	}

	// The REQUEST carries the override's SEQUENCE, which is above the
	// series', and GET shows the same value.
	ev := e.stored("s1")
	ov := overrideOf(t, ev.IcalRaw, "20301104T090000Z")
	seq := ov.Props.Get(ical.PropSequence)
	if seq == nil || seq.Value != "1" || m.seq != "1" {
		t.Errorf("override SEQUENCE = %v, message %q, want both 1", seq, m.seq)
	}
	if ev.Sequence != 0 {
		t.Errorf("an override-only change moved the master SEQUENCE to %d", ev.Sequence)
	}
}

func TestOrganiserEchoAndAlarmOnlyOverridesSendNothing(t *testing.T) {
	e, rec := realSchedEnv(t)
	attendees := []string{orgLine, bobLine}
	mustPut(t, e, "s2", weekly("s2", attendees, moved("s2", attendees...)))
	rec.reset()

	// The client uploads what the server gave it back, status and all.
	echoed := weekly("s2", []string{orgLine, "ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE;SCHEDULE-STATUS=1.1:mailto:" + occBob},
		moved("s2", orgLine, "ATTENDEE;PARTSTAT=ACCEPTED;SCHEDULE-STATUS=1.1:mailto:"+occBob, "X-APPLE-DEFAULT-ALARM:TRUE"))
	mustPut(t, e, "s2", echoed)
	if got := mailed(rec); got != "" {
		t.Errorf("an echo sent %q", got)
	}

	// An override written only to acknowledge an alarm changes nothing.
	mustPut(t, e, "s3", weekly("s3", attendees))
	rec.reset()
	alarm := veventBlock("UID:s3", "DTSTAMP:20300102T000000Z", "RECURRENCE-ID:20301111T090000Z", "DTSTART:20301111T090000Z",
		"DTEND:20301111T100000Z", "SUMMARY:Weekly", "BEGIN:VALARM", "ACTION:DISPLAY", "TRIGGER:-PT15M", "DESCRIPTION:x", "END:VALARM")
	mustPut(t, e, "s3", weekly("s3", attendees, alarm))
	if got := mailed(rec); got != "" {
		t.Errorf("an alarm-only override sent %q", got)
	}
}

func TestOrganiserExcludedOccurrenceSendsOneCancel(t *testing.T) {
	for _, tc := range []struct{ name, exdate string }{
		{"UTC", "EXDATE:20301104T090000Z"},
		{"TZID", "EXDATE;TZID=Europe/Berlin:20301104T100000"},
		{"date", "EXDATE;VALUE=DATE:20301104"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, rec := realSchedEnv(t)
			attendees := []string{orgLine, bobLine}
			mustPut(t, e, "x1", weekly("x1", attendees))
			rec.reset()
			mustPut(t, e, "x1", weekly("x1", append(attendees, tc.exdate)))
			if got := mailed(rec); got != "CANCEL "+occBob {
				t.Fatalf("sent %q, want one CANCEL", got)
			}
			m := rec.last()
			if !strings.Contains(m.calendar, "RECURRENCE-ID:20301104T090000Z") || !strings.Contains(m.calendar, "UID:x1") ||
				strings.Contains(m.calendar, "EXDATE") || strings.Contains(m.calendar, "RRULE") {
				t.Errorf("the CANCEL is not for the one occurrence:\n%s", m.calendar)
			}

			// The same exclusion uploaded again is not news.
			rec.reset()
			mustPut(t, e, "x1", weekly("x1", append(attendees, tc.exdate)))
			if got := mailed(rec); got != "" {
				t.Errorf("a repeated EXDATE sent %q", got)
			}
		})
	}
}

func TestOrganiserCancelledOverrideSendsOneCancel(t *testing.T) {
	e, rec := realSchedEnv(t)
	attendees := []string{orgLine, bobLine}
	mustPut(t, e, "c1", weekly("c1", attendees))
	rec.reset()
	cancelled := veventBlock("UID:c1", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20301104T090000Z", "DTSTART:20301104T090000Z",
		"DTEND:20301104T100000Z", "SUMMARY:Weekly", "STATUS:CANCELLED")
	// Excluded as well: still one CANCEL for the occurrence.
	mustPut(t, e, "c1", weekly("c1", append(attendees, "EXDATE:20301104T090000Z"), cancelled))
	if got := mailed(rec); got != "CANCEL "+occBob {
		t.Errorf("sent %q, want exactly one CANCEL", got)
	}
}

func TestOrganiserOverrideOnlyAttendee(t *testing.T) {
	// The master names nobody; the 2030-11-04 override lists bob.
	for _, tc := range []struct {
		name  string
		first string // PUT before the override, "" for a create with it
	}{
		{"create", ""},
		{"re-PUT", "series"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, rec := realSchedEnv(t)
			withBob := weekly("o1", []string{orgLine}, moved("o1", orgLine, bobLine))
			if tc.first != "" {
				mustPut(t, e, "o1", weekly("o1", []string{orgLine}))
				if got := mailed(rec); got != "" {
					t.Fatalf("a series nobody is invited to sent %q", got)
				}
			}
			mustPut(t, e, "o1", withBob)
			if got := mailed(rec); got != "REQUEST "+occBob {
				t.Fatalf("sent %q, want one REQUEST to bob", got)
			}
			if m := rec.last(); !strings.Contains(m.calendar, "RECURRENCE-ID:20301104T090000Z") || strings.Contains(m.calendar, "RRULE") {
				t.Errorf("not an occurrence REQUEST:\n%s", m.calendar)
			}
			ov := overrideOf(t, e.stored("o1").IcalRaw, "20301104T090000Z")
			for _, a := range scheduleAttendees(ov) {
				if a.address == occBob {
					if got := ov.Props.Values(ical.PropAttendee)[a.prop].Params.Get("SCHEDULE-STATUS"); got != "1.1" {
						t.Errorf("override ATTENDEE SCHEDULE-STATUS = %q, want 1.1", got)
					}
					return
				}
			}
			t.Error("the override lost its ATTENDEE")
		})
	}
}

func TestOrganiserPastOccurrencesSendNothing(t *testing.T) {
	e, rec := realSchedEnv(t)
	// Weekly Mondays from 2020-10-26: the 2020-11-02 occurrence has ended.
	past := func(extra ...string) string {
		lines := append([]string{"UID:p1", "DTSTAMP:20200101T000000Z", "DTSTART:20201026T090000Z", "DTEND:20201026T100000Z",
			"RRULE:FREQ=WEEKLY", "SUMMARY:Weekly", orgLine, bobLine}, extra...)
		return lines[0] + "\r\n" + strings.Join(lines[1:], "\r\n")
	}
	master := func(extra ...string) string { return veventBlock(strings.Split(past(extra...), "\r\n")...) }
	rec.reset()
	mustPut(t, e, "p1", calendarOf(master()))
	rec.reset()

	mustPut(t, e, "p1", calendarOf(master("EXDATE:20201102T090000Z")))
	if got := mailed(rec); got != "" {
		t.Errorf("excluding a past occurrence sent %q", got)
	}
	movedPast := veventBlock("UID:p1", "DTSTAMP:20200101T000000Z", "RECURRENCE-ID:20201109T090000Z", "DTSTART:20201109T110000Z",
		"DTEND:20201109T120000Z", "SUMMARY:Weekly", orgLine, bobLine)
	mustPut(t, e, "p1", calendarOf(master("EXDATE:20201102T090000Z"), movedPast))
	if got := mailed(rec); got != "" {
		t.Errorf("moving a past occurrence sent %q", got)
	}
}

func TestOrganiserOccurrenceMessagesAreCapped(t *testing.T) {
	e, rec := realSchedEnv(t)
	daily := func(overrides ...string) string {
		return calendarOf(veventBlock("UID:d1", "DTSTAMP:20300101T000000Z", "DTSTART:20301028T090000Z", "DTEND:20301028T100000Z",
			"RRULE:FREQ=DAILY", "SUMMARY:Daily", orgLine, bobLine), overrides...)
	}
	mustPut(t, e, "d1", daily())
	rec.reset()
	var overrides []string
	for i := range 30 {
		day := 29 + i // 2030-10-29 onwards, rolling into November
		d := fmt.Sprintf("2030%02d%02d", 10+(day-1)/31, (day-1)%31+1)
		overrides = append(overrides, veventBlock("UID:d1", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:"+d+"T090000Z",
			"DTSTART:"+d+"T110000Z", "DTEND:"+d+"T120000Z", "SUMMARY:Daily", orgLine, bobLine))
	}
	mustPut(t, e, "d1", daily(overrides...))
	if n := rec.tally()["REQUEST "+occBob]; n != maxOccurrenceMessagesPerPut {
		t.Errorf("%d occurrence REQUESTs for 30 changed overrides, want the cap of %d", n, maxOccurrenceMessagesPerPut)
	}
}

func TestOrganiserSeriesChangeInheritedByOverrideSendsOnlyTheSeries(t *testing.T) {
	e, rec := realSchedEnv(t)
	attendees := []string{orgLine, bobLine}
	plainOverride := func() string {
		return veventBlock("UID:i1", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20301104T090000Z", "DTSTART:20301104T110000Z",
			"DTEND:20301104T120000Z", orgLine, bobLine)
	}
	mustPut(t, e, "i1", weekly("i1", attendees, plainOverride()))
	rec.reset()
	// The master is renamed; the override has no SUMMARY of its own, so it
	// inherits the new title and says nothing new about itself.
	renamed := strings.Replace(weekly("i1", attendees, plainOverride()), "SUMMARY:Weekly", "SUMMARY:Renamed", 1)
	mustPut(t, e, "i1", renamed)
	if got := mailed(rec); got != "REQUEST "+occBob {
		t.Errorf("sent %q, want only the series REQUEST", got)
	}
	if m := rec.last(); strings.Contains(m.calendar, "RECURRENCE-ID") {
		t.Errorf("the series change was sent as an occurrence:\n%s", m.calendar)
	}
}

func TestOrganiserMaxAttendeesPerVEvent(t *testing.T) {
	e, _ := realSchedEnv(t)
	lines := []string{orgLine}
	for i := range 101 {
		lines = append(lines, fmt.Sprintf("ATTENDEE:mailto:guest%d@d.test", i))
	}
	code, body, _ := e.put("m1", weekly("m1", []string{orgLine}, moved("m1", lines...)))
	if code != http.StatusForbidden || !strings.Contains(body, "max-attendees-per-instance") {
		t.Errorf("PUT = %d %s, want the max-attendees refusal for an override", code, body)
	}
}

func TestOrganiseOccurrenceReachesTheScheduler(t *testing.T) {
	e := newSchedEnv(t, true)
	attendees := []string{orgLine, bobLine}
	mustPut(t, e, "f1", weekly("f1", attendees))
	mustPut(t, e, "f1", weekly("f1", attendees, moved("f1", attendees...)))
	calls := e.sched.occurrences
	if len(calls) != 1 || calls[0].rid != "20301104T090000Z" || calls[0].instance == nil || calls[0].original == nil {
		t.Fatalf("OrganiseOccurrence calls = %+v, want one REQUEST for the moved occurrence", calls)
	}
	if calls[0].instance.StartTime != calls[0].original.StartTime+2*3600 {
		t.Errorf("instance starts %d, original %d: want two hours later", calls[0].instance.StartTime, calls[0].original.StartTime)
	}
}
