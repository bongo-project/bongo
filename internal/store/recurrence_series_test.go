package store

import (
	"slices"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func seriesWith(extra string) *bongov1.Event {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VEVENT\r\nUID:s\r\nDTSTART:20301028T090000Z\r\n" +
		"RRULE:FREQ=WEEKLY\r\nATTENDEE:mailto:bob@d.test\r\n" + extra + "END:VCALENDAR\r\n"
	return &bongov1.Event{
		Guid: "s", StartTime: 1919408400, EndTime: 1919412000, Rrule: "FREQ=WEEKLY", Sequence: 2,
		Attendees: []*bongov1.Attendee{{Address: "bob@d.test"}}, IcalRaw: []byte(raw),
	}
}

func TestSeriesExceptions(t *testing.T) {
	override := func(rid, status string) string {
		return "BEGIN:VEVENT\r\nUID:s\r\nRECURRENCE-ID" + rid + "\r\nDTSTART:20301104T110000Z\r\n" + status + "END:VEVENT\r\n"
	}
	tests := []struct {
		name, extra             string
		wantCancelled, wantOver []string
	}{
		{"none", "END:VEVENT\r\n", nil, nil},
		{"EXDATE in UTC", "EXDATE:20301104T090000Z\r\nEND:VEVENT\r\n", []string{"20301104T090000Z"}, nil},
		{"EXDATE with a TZID", "EXDATE;TZID=Europe/Berlin:20301104T100000\r\nEND:VEVENT\r\n", []string{"20301104T090000Z"}, nil},
		{"EXDATE as a date", "EXDATE;VALUE=DATE:20301104\r\nEND:VEVENT\r\n", []string{"20301104T090000Z"}, nil},
		{"EXDATE off the rule", "EXDATE:20301105T090000Z\r\nEND:VEVENT\r\n", nil, nil},
		{"override", "END:VEVENT\r\n" + override(":20301104T090000Z", ""), nil, []string{"20301104T090000Z"}},
		{"override with a TZID", "END:VEVENT\r\n" + override(";TZID=Europe/Berlin:20301104T100000", ""), nil, []string{"20301104T090000Z"}},
		{"cancelled override", "END:VEVENT\r\n" + override(":20301104T090000Z", "STATUS:CANCELLED\r\n"), []string{"20301104T090000Z"}, nil},
		{"EXDATE and cancelled override once", "EXDATE:20301104T090000Z\r\nEND:VEVENT\r\n" + override(":20301104T090000Z", "STATUS:CANCELLED\r\n"),
			[]string{"20301104T090000Z"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := OpenSeries(seriesWith(tc.extra))
			if !ok {
				t.Fatal("series did not open")
			}
			c, o := s.Exceptions()
			if !slices.Equal(c, tc.wantCancelled) || !slices.Equal(o, tc.wantOver) {
				t.Errorf("Exceptions = %v, %v; want %v, %v", c, o, tc.wantCancelled, tc.wantOver)
			}
		})
	}
	if _, ok := OpenSeries(&bongov1.Event{Guid: "x"}); ok {
		t.Error("a single event opened as a series")
	}
}

func TestSeriesOccurrenceUsesTheOverridesAttendees(t *testing.T) {
	ov := "END:VEVENT\r\nBEGIN:VEVENT\r\nUID:s\r\nRECURRENCE-ID:20301104T090000Z\r\nDTSTART:20301104T110000Z\r\nSEQUENCE:5\r\n" +
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:carol@d.test\r\nEND:VEVENT\r\n"
	ev := seriesWith(ov)
	s, _ := OpenSeries(ev)

	plain, eff := s.Occurrence("20301104T090000Z")
	if plain == nil || eff == nil {
		t.Fatalf("plain %v effective %v", plain, eff)
	}
	if len(eff.Attendees) != 1 || eff.Attendees[0].Address != "carol@d.test" || eff.Sequence != 5 {
		t.Errorf("effective = %v seq %d, want carol and the override's SEQUENCE", eff.Attendees, eff.Sequence)
	}
	if len(plain.Attendees) != 1 || plain.Attendees[0].Address != "bob@d.test" || plain.StartTime != 1919408400+7*86400 {
		t.Errorf("plain = %v at %d", plain.Attendees, plain.StartTime)
	}
	// FindOccurrence keeps deciding from the master's list.
	if got, _ := FindOccurrence(ev, "20301104T090000Z"); got == nil || got.Attendees[0].Address != "bob@d.test" {
		t.Errorf("FindOccurrence attendees changed: %v", got)
	}
	if p, e := s.Occurrence("20301105T090000Z"); p != nil || e != nil {
		t.Error("an off-rule slot is an occurrence")
	}
}
