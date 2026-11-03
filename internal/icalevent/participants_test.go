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

// attProps builds ORGANIZER and ATTENDEE lines: n server-scheduled invitees
// (p0..), the organiser's own line when withOrganiserLine is set, then agents
// client-scheduled ones (c0..) with the given SCHEDULE-AGENT.
func attProps(organiser string, n int, withOrganiserLine bool, agent string, agents int) ical.Props {
	props := ical.Props{}
	if organiser != "" {
		props.Add(OrganizerProp(organiser))
	}
	add := func(addr, sa string) {
		p := AttendeeProp(&bongov1.Attendee{Address: addr})
		if sa != "" {
			p.Params.Set("SCHEDULE-AGENT", sa)
		}
		props.Add(p)
	}
	if withOrganiserLine {
		add(organiser, "")
	}
	for i := range n {
		add(fmt.Sprintf("p%d@example.com", i), "")
	}
	for i := range agents {
		add(fmt.Sprintf("c%d@example.com", i), agent)
	}
	return props
}

func TestFillParticipantsCap(t *testing.T) {
	const org = "boss@example.com"
	for _, tc := range []struct {
		name      string
		props     ical.Props
		wantTotal int
		wantSrv   int // invited by the server among those stored
		wantOrg   bool
	}{
		{"100 invitees all kept", attProps(org, 100, false, "", 0), 100, 100, false},
		{"101 invitees: the 101st is dropped", attProps(org, 101, false, "", 0), 100, 100, false},
		{"organiser line plus 100 invitees all kept", attProps(org, 100, true, "", 0), 101, 100, true},
		{"organiser line plus 101 invitees drops one invitee", attProps(org, 101, true, "", 0), 101, 100, true},
		{"organiser line case-insensitive", attProps("Boss@Example.com", 100, true, "", 0), 101, 100, true},
		{"100 invitees plus 50 CLIENT", attProps(org, 100, false, "CLIENT", 50), 150, 100, false},
		{"100 invitees plus 50 NONE", attProps(org, 100, false, "NONE", 50), 150, 100, false},
		{"100 invitees plus 50 server-agent lines beyond the cap", attProps(org, 100, false, "SERVER", 50), 100, 100, false},
		{"organiser, 100 invitees and 99 CLIENT is 200", attProps(org, 100, true, "CLIENT", 99), 200, 100, true},
		{"hard bound 200: 100 invitees and 100 CLIENT", attProps(org, 100, false, "CLIENT", 100), 200, 100, false},
		{"hard bound 200: 201st line is dropped", attProps(org, 100, false, "CLIENT", 101), 200, 100, false},
		{"hard bound applies to organiser line too", attProps(org, 100, true, "CLIENT", 100), 200, 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &bongov1.Event{}
			fillParticipants(e, tc.props)
			if len(e.Attendees) != tc.wantTotal {
				t.Errorf("stored %d attendees, want %d", len(e.Attendees), tc.wantTotal)
			}
			if len(e.Attendees) > MaxStoredAttendees {
				t.Errorf("stored %d, over the bound %d", len(e.Attendees), MaxStoredAttendees)
			}
			srv, hasOrg := 0, false
			for _, a := range e.Attendees {
				switch {
				case strings.EqualFold(a.Address, e.Organizer):
					hasOrg = true
				case strings.HasPrefix(a.Address, "p"):
					srv++
				}
			}
			if srv != tc.wantSrv {
				t.Errorf("%d server-invited attendees, want %d", srv, tc.wantSrv)
			}
			if hasOrg != tc.wantOrg {
				t.Errorf("organiser line stored = %v, want %v", hasOrg, tc.wantOrg)
			}
		})
	}
}

func TestMaxStoredAttendeesIsTwiceMaxAttendees(t *testing.T) {
	if MaxAttendees != 100 || MaxStoredAttendees != 200 {
		t.Errorf("limits = %d/%d, the documented rule is 100 invited, 200 stored", MaxAttendees, MaxStoredAttendees)
	}
}

func TestServerSchedules(t *testing.T) {
	for _, tc := range []struct {
		agent string
		set   bool
		want  bool
	}{
		{"", false, true},
		{"SERVER", true, true},
		{"server", true, true},
		{" SERVER ", true, true},
		{"", true, true},
		{"CLIENT", true, false},
		{"client", true, false},
		{"NONE", true, false},
		{"other", true, false},
	} {
		t.Run(fmt.Sprintf("%q set=%v", tc.agent, tc.set), func(t *testing.T) {
			p := ical.NewProp(ical.PropAttendee)
			p.Value = "mailto:a@example.com"
			if tc.set {
				p.Params.Set("SCHEDULE-AGENT", tc.agent)
			}
			if got := serverSchedules(p); got != tc.want {
				t.Errorf("serverSchedules = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClientScheduledAndCountInvited(t *testing.T) {
	const org = "boss@example.com"
	props := attProps(org, 3, true, "CLIENT", 2)
	skip := ClientScheduled(props)
	if len(skip) != 2 || !skip["c0@example.com"] || !skip["c1@example.com"] {
		t.Fatalf("ClientScheduled = %v", skip)
	}
	e := &bongov1.Event{}
	fillParticipants(e, props)
	if n := CountInvited(e.Attendees, org, skip, nil); n != 3 {
		t.Errorf("CountInvited = %d, want 3 (organiser and CLIENT lines are exempt)", n)
	}
	own := func(a string) bool { return a == "p0@example.com" }
	if n := CountInvited(e.Attendees, org, skip, own); n != 2 {
		t.Errorf("CountInvited with an own address = %d, want 2", n)
	}
	dup := append(e.Attendees, &bongov1.Attendee{Address: "P1@example.com"})
	if n := CountInvited(dup, org, skip, nil); n != 3 {
		t.Errorf("CountInvited with a duplicate = %d, want 3", n)
	}
}
