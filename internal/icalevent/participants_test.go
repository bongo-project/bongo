package icalevent

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestToEventParticipants(t *testing.T) {
	var many strings.Builder
	for i := range 101 {
		fmt.Fprintf(&many, "ATTENDEE:mailto:p%d@example.com\r\n", i)
	}

	for _, tc := range []struct {
		name  string
		extra string
		check func(t *testing.T, e *bongov1.Event)
	}{
		{"no props", "", func(t *testing.T, e *bongov1.Event) {
			if e.Organizer != "" || len(e.Attendees) != 0 || e.Sequence != 0 || e.Status != "" {
				t.Errorf("want empty participant fields, got %v", e)
			}
		}},
		{"organizer and params", "ORGANIZER;CN=Boss:mailto:Boss@Example.com\r\n" +
			"ATTENDEE;CN=\"Doe, Jane\";ROLE=chair;PARTSTAT=accepted;RSVP=TRUE:mailto:jane@example.com\r\n" +
			"ATTENDEE;RSVP=FALSE:mailto:bob@example.com\r\n" +
			"SEQUENCE:4\r\nSTATUS:confirmed\r\n", func(t *testing.T, e *bongov1.Event) {
			if e.Organizer != "Boss@Example.com" {
				t.Errorf("organizer = %q", e.Organizer)
			}
			if len(e.Attendees) != 2 {
				t.Fatalf("attendees = %v", e.Attendees)
			}
			want := &bongov1.Attendee{Address: "jane@example.com", Name: "Doe, Jane", Role: "CHAIR", Partstat: "ACCEPTED", Rsvp: true}
			if !proto.Equal(e.Attendees[0], want) {
				t.Errorf("attendee 0 = %v, want %v", e.Attendees[0], want)
			}
			if e.Attendees[1].Rsvp || e.Attendees[1].Role != "" {
				t.Errorf("attendee 1 = %v", e.Attendees[1])
			}
			if e.Sequence != 4 || e.Status != "CONFIRMED" {
				t.Errorf("sequence/status = %d/%q", e.Sequence, e.Status)
			}
		}},
		{"upper case MAILTO", "ORGANIZER:MAILTO:o@example.com\r\nATTENDEE:MAILTO:a@example.com\r\n", func(t *testing.T, e *bongov1.Event) {
			if e.Organizer != "o@example.com" || len(e.Attendees) != 1 || e.Attendees[0].Address != "a@example.com" {
				t.Errorf("got %v", e)
			}
		}},
		{"non mailto and invalid skipped", "ATTENDEE:urn:uuid:1234\r\nATTENDEE:http://sneaky@example.com\r\nATTENDEE:ftp://x@example.com\r\nATTENDEE:mailto:not-an-address\r\nATTENDEE:mailto:Name <x@example.com>\r\nATTENDEE:mailto:ok@example.com\r\n", func(t *testing.T, e *bongov1.Event) {
			if len(e.Attendees) != 1 || e.Attendees[0].Address != "ok@example.com" {
				t.Errorf("attendees = %v", e.Attendees)
			}
		}},
		{"duplicates keep first", "ATTENDEE;CN=First:mailto:a@example.com\r\nATTENDEE;CN=Second:mailto:A@EXAMPLE.com\r\n", func(t *testing.T, e *bongov1.Event) {
			if len(e.Attendees) != 1 || e.Attendees[0].Name != "First" {
				t.Errorf("attendees = %v", e.Attendees)
			}
		}},
		{"cap at 100 with 101 sent", many.String(), func(t *testing.T, e *bongov1.Event) {
			if len(e.Attendees) != MaxAttendees {
				t.Fatalf("len = %d, want %d", len(e.Attendees), MaxAttendees)
			}
			if e.Attendees[99].Address != "p99@example.com" {
				t.Errorf("last kept = %s", e.Attendees[99].Address)
			}
		}},
		{"sequence garbage", "SEQUENCE:abc\r\n", func(t *testing.T, e *bongov1.Event) {
			if e.Sequence != 0 {
				t.Errorf("sequence = %d", e.Sequence)
			}
		}},
		{"sequence negative", "SEQUENCE:-3\r\n", func(t *testing.T, e *bongov1.Event) {
			if e.Sequence != 0 {
				t.Errorf("sequence = %d", e.Sequence)
			}
		}},
		{"unknown status dropped", "STATUS:bogus\r\n", func(t *testing.T, e *bongov1.Event) {
			if e.Status != "" {
				t.Errorf("status = %q", e.Status)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := ToEvent(decode(t, ev("S", tc.extra)))
			tc.check(t, e)
		})
	}
}

func TestToEventKeepsAllAttendeesInRaw(t *testing.T) {
	// The cap is on the structured copy only; the calendar the caller stores
	// is untouched by ToEvent.
	var many strings.Builder
	for i := range 101 {
		fmt.Fprintf(&many, "ATTENDEE:mailto:p%d@example.com\r\n", i)
	}
	cal := decode(t, ev("S", many.String()))
	ToEvent(cal)
	m, _ := Master(cal)
	if n := len(m.Props.Values(ical.PropAttendee)); n != 101 {
		t.Errorf("raw attendees = %d, want 101", n)
	}
}

func TestValidAddress(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"a@example.com", true},
		{"A.B+tag@Example.COM", true},
		{"", false},
		{"plain", false},
		{"Name <a@example.com>", false},
		{"a@example.com\r\nBCC:x@example.com", false},
		{"a@example.com\n", false},
		{"\"a\"@example.com", false},
		{"a b@example.com", false},
		{"a@example.com, b@example.com", false},
	} {
		if got := ValidAddress(tc.in); got != tc.want {
			t.Errorf("ValidAddress(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSetParticipantsRoundTrip(t *testing.T) {
	src := &bongov1.Event{
		Organizer: "boss@example.com",
		Sequence:  3,
		Status:    "CONFIRMED",
		Attendees: []*bongov1.Attendee{
			{Address: "jane@example.com", Name: "Doe, Jane", Role: "REQ-PARTICIPANT", Partstat: "NEEDS-ACTION", Rsvp: true},
			{Address: "bob@example.com"},
			{Address: "bad\r\naddress@example.com"},
			{Address: "x@example.com", Name: "Quote\"Injection\r\nATTENDEE:mailto:evil@example.com"},
		},
	}
	vevent := ical.NewEvent()
	vevent.Props.SetText(ical.PropUID, "u")
	vevent.Props.SetText(ical.PropSummary, "S")
	vevent.Props.SetDateTime(ical.PropDateTimeStamp, mustTime())
	vevent.Props.SetDateTime(ical.PropDateTimeStart, mustTime())
	SetParticipants(vevent.Props, src)

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//T//EN")
	cal.Children = append(cal.Children, vevent.Component)
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "evil@example.com\r\n") && strings.Count(buf.String(), "ATTENDEE") != 3 {
		t.Fatalf("injection reached output:\n%s", buf.String())
	}

	back, err := ical.NewDecoder(&buf).Decode()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := ToEvent(back)
	if got.Organizer != src.Organizer || got.Sequence != 3 || got.Status != "CONFIRMED" {
		t.Errorf("scalars = %q %d %q", got.Organizer, got.Sequence, got.Status)
	}
	if len(got.Attendees) != 3 {
		t.Fatalf("attendees = %v", got.Attendees)
	}
	if !proto.Equal(got.Attendees[0], src.Attendees[0]) {
		t.Errorf("attendee 0 = %v, want %v", got.Attendees[0], src.Attendees[0])
	}
	if got.Attendees[2].Address != "x@example.com" || strings.ContainsAny(got.Attendees[2].Name, "\"\r\n") {
		t.Errorf("attendee 2 = %v", got.Attendees[2])
	}
}

func TestSetParticipantsEmpty(t *testing.T) {
	vevent := ical.NewEvent()
	SetParticipants(vevent.Props, &bongov1.Event{})
	for _, n := range []string{ical.PropOrganizer, ical.PropAttendee, ical.PropSequence, ical.PropStatus} {
		if len(vevent.Props.Values(n)) != 0 {
			t.Errorf("%s written for an empty event", n)
		}
	}
}

func TestEventWithoutParticipantsUnmarshals(t *testing.T) {
	// A record stored before these fields existed: only the old fields set.
	old := &bongov1.Event{Guid: "g", Title: "Old"}
	b, err := proto.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	var e bongov1.Event
	if err := proto.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	if e.Organizer != "" || e.Attendees != nil || e.Sequence != 0 || e.Status != "" {
		t.Errorf("new fields not empty: %v", &e)
	}
}

func mustTime() time.Time { return time.Date(2030, 3, 1, 9, 0, 0, 0, time.UTC) }
