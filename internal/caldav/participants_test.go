package caldav

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A web-created event has no ical_raw, so a CalDAV client sees the structured
// organiser, attendees, sequence and status that the synthesis writes.
func TestEventToCalendarObjectCarriesParticipants(t *testing.T) {
	e := &bongov1.Event{
		Guid: "g1", Title: "T", StartTime: 1900000000, EndTime: 1900003600,
		Organizer: "me@example.com", Sequence: 3, Status: "CONFIRMED",
		Attendees: []*bongov1.Attendee{{Address: "jane@example.com", Name: "Jane", Role: "REQ-PARTICIPANT", Partstat: "ACCEPTED", Rsvp: true}},
	}
	obj, err := eventToCalendarObject("u", "default", e)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(obj.Data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"ORGANIZER:mailto:me@example.com",
		"ATTENDEE;CN=Jane;PARTSTAT=ACCEPTED;ROLE=REQ-PARTICIPANT;RSVP=TRUE:mailto:jane@example.com",
		"SEQUENCE:3",
		"STATUS:CONFIRMED",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s:\n%s", want, out)
		}
	}
}

// An event without participants writes none of the four properties.
func TestEventToCalendarObjectWithoutParticipants(t *testing.T) {
	obj, err := eventToCalendarObject("u", "default", &bongov1.Event{Guid: "g1", Title: "T", StartTime: 1900000000, EndTime: 1900003600})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(obj.Data); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ORGANIZER", "ATTENDEE", "SEQUENCE", "STATUS"} {
		if strings.Contains(buf.String(), name) {
			t.Errorf("output has %s:\n%s", name, buf.String())
		}
	}
}

// CalDAV PUT fills the structured fields through icalToEvent.
func TestIcalToEventParticipants(t *testing.T) {
	cal, err := ical.NewDecoder(strings.NewReader("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VEVENT\r\nUID:u\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300301T090000Z\r\nORGANIZER:mailto:o@example.com\r\nATTENDEE;PARTSTAT=TENTATIVE:mailto:a@example.com\r\nSEQUENCE:2\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")).Decode()
	if err != nil {
		t.Fatal(err)
	}
	e, _ := icalToEvent(cal, "g", "default")
	if e.Organizer != "o@example.com" || len(e.Attendees) != 1 || e.Attendees[0].Partstat != "TENTATIVE" || e.Sequence != 2 {
		t.Errorf("event = %v", e)
	}
}
