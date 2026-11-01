package imip

import (
	"bytes"
	"io"
	"mime"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-message"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

var testNow = time.Date(2030, 4, 1, 12, 0, 0, 0, time.UTC)

const rawICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Client//Cal//EN\r\n" +
	"BEGIN:VEVENT\r\nUID:client-uid-7f3a@example.org\r\nDTSTAMP:20300101T000000Z\r\n" +
	"DTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\nSUMMARY:Raw\r\n" +
	"RRULE:FREQ=WEEKLY\r\nEXDATE:20300513T090000Z\r\n" +
	"ORGANIZER:mailto:someone@else.example\r\n" +
	"ATTENDEE;CN=Bob;PARTSTAT=ACCEPTED;X-NUM-GUESTS=2:mailto:bob@example.org\r\n" +
	"ATTENDEE:mailto:carol@example.org\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:Reminder\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\n" +
	"END:VEVENT\r\nEND:VCALENDAR\r\n"

func structured() *bongov1.Event {
	return &bongov1.Event{
		Guid: "guid-1", Title: "Planning", Location: "Room 4",
		StartTime: time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC).Unix(),
		EndTime:   time.Date(2030, 5, 6, 10, 0, 0, 0, time.UTC).Unix(),
		Organizer: "olive@example.org", Sequence: 3,
		Attendees: []*bongov1.Attendee{
			{Address: "bob@example.org", Role: "REQ-PARTICIPANT", Partstat: "NEEDS-ACTION", Rsvp: true},
			{Address: "carol@example.org", Name: "Carol"},
		},
	}
}

func vevent(t *testing.T, cal *ical.Calendar) *ical.Component {
	t.Helper()
	var found *ical.Component
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			if found != nil {
				t.Fatal("more than one VEVENT")
			}
			found = c
		}
	}
	if found == nil {
		t.Fatal("no VEVENT")
	}
	return found
}

func attendees(c *ical.Component) []string {
	var out []string
	for _, p := range c.Props.Values(ical.PropAttendee) {
		out = append(out, p.Value)
	}
	return out
}

func TestBuildCalendar(t *testing.T) {
	raw := structured()
	raw.IcalRaw = []byte(rawICS)
	raw.Guid = "not-the-uid"

	for _, tc := range []struct {
		name       string
		method     string
		event      *bongov1.Event
		recurrence string
		wantUID    string
		wantAtt    []string
		wantRID    string
		wantStatus string
		wantRRULE  bool
	}{
		{"structured request", MethodRequest, structured(), "", "guid-1",
			[]string{"mailto:bob@example.org", "mailto:carol@example.org"}, "", "", false},
		{"structured cancel", MethodCancel, structured(), "", "guid-1",
			[]string{"mailto:bob@example.org", "mailto:carol@example.org"}, "", "CANCELLED", false},
		{"raw keeps its UID and attendees", MethodRequest, raw, "", "client-uid-7f3a@example.org",
			[]string{"mailto:bob@example.org", "mailto:carol@example.org"}, "", "", true},
		{"instance request", MethodRequest, raw, "20300513T090000Z", "client-uid-7f3a@example.org",
			[]string{"mailto:bob@example.org", "mailto:carol@example.org"}, "20300513T090000Z", "", false},
		{"all-day instance", MethodCancel, structured(), "20300513", "guid-1",
			[]string{"mailto:bob@example.org", "mailto:carol@example.org"}, "20300513", "CANCELLED", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cal, err := BuildCalendar(tc.method, tc.event, tc.recurrence, testNow)
			if err != nil {
				t.Fatal(err)
			}
			if got := cal.Props.Get(ical.PropMethod).Value; got != tc.method {
				t.Errorf("METHOD = %q", got)
			}
			ve := vevent(t, cal)
			if got := ve.Props.Get(ical.PropUID).Value; got != tc.wantUID {
				t.Errorf("UID = %q, want %q", got, tc.wantUID)
			}
			if got := ve.Props.Get(ical.PropOrganizer).Value; got != "mailto:olive@example.org" {
				t.Errorf("ORGANIZER = %q", got)
			}
			if got := ve.Props.Get(ical.PropSequence).Value; got != "3" {
				t.Errorf("SEQUENCE = %q", got)
			}
			if got := ve.Props.Get(ical.PropDateTimeStamp).Value; got != "20300401T120000Z" {
				t.Errorf("DTSTAMP = %q", got)
			}
			if got := strings.Join(attendees(ve), ","); got != strings.Join(tc.wantAtt, ",") {
				t.Errorf("ATTENDEE = %q", got)
			}
			rid := ve.Props.Get(ical.PropRecurrenceID)
			switch {
			case tc.wantRID == "" && rid != nil:
				t.Errorf("RECURRENCE-ID = %q, want none", rid.Value)
			case tc.wantRID != "" && (rid == nil || rid.Value != tc.wantRID):
				t.Errorf("RECURRENCE-ID = %v, want %q", rid, tc.wantRID)
			}
			if got := ve.Props.Get(ical.PropRecurrenceRule) != nil; got != tc.wantRRULE {
				t.Errorf("RRULE present = %v, want %v", got, tc.wantRRULE)
			}
			if tc.recurrence != "" && ve.Props.Get(ical.PropExceptionDates) != nil {
				t.Error("an instance carries EXDATE")
			}
			status := ""
			if p := ve.Props.Get(ical.PropStatus); p != nil {
				status = p.Value
			}
			if status != tc.wantStatus {
				t.Errorf("STATUS = %q, want %q", status, tc.wantStatus)
			}
			for _, c := range ve.Children {
				if c.Name == ical.CompAlarm {
					t.Error("VALARM not stripped")
				}
			}
		})
	}
}

func TestBuildCalendarRawKeepsAttendeeParameters(t *testing.T) {
	e := structured()
	e.IcalRaw = []byte(rawICS)
	// Only Bob is addressed (a CANCEL to one removed attendee), plus a new one.
	e.Attendees = []*bongov1.Attendee{{Address: "bob@example.org"}, {Address: "dave@example.org"}}
	cal, err := BuildCalendar(MethodCancel, e, "", testNow)
	if err != nil {
		t.Fatal(err)
	}
	ve := vevent(t, cal)
	if got := strings.Join(attendees(ve), ","); got != "mailto:bob@example.org,mailto:dave@example.org" {
		t.Fatalf("ATTENDEE = %q", got)
	}
	bob := ve.Props.Values(ical.PropAttendee)[0]
	if bob.Params.Get("X-NUM-GUESTS") != "2" || bob.Params.Get("PARTSTAT") != "ACCEPTED" {
		t.Errorf("Bob's parameters lost: %v", bob.Params)
	}
}

func TestBuildCalendarRefusals(t *testing.T) {
	noOrganiser := structured()
	noOrganiser.Organizer = ""
	badOrganiser := structured()
	badOrganiser.Organizer = "x@y.example\r\nBCC:z@y.example"
	noGuid := structured()
	noGuid.Guid = ""
	for _, tc := range []struct {
		name       string
		method     string
		event      *bongov1.Event
		recurrence string
	}{
		{"reply is not built here", "REPLY", structured(), ""},
		{"no organiser", MethodRequest, noOrganiser, ""},
		{"organiser with control characters", MethodRequest, badOrganiser, ""},
		{"no identity", MethodRequest, noGuid, ""},
		{"bad recurrence id", MethodRequest, structured(), "2030-05-13"},
		{"nil event", MethodRequest, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildCalendar(tc.method, tc.event, tc.recurrence, testNow); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func buildTestMessage(t *testing.T, method, subject string) []byte {
	t.Helper()
	cal, err := BuildCalendar(method, structured(), "", testNow)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := BuildMessage(Params{
		From: "olive@example.org", To: []string{"bob@example.org", "carol@example.org"},
		Subject: subject, TextBody: "Hello \u00e9\r\n", Cal: cal,
		MessageID: "<abc@example.org>", Date: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBuildMessageStructure(t *testing.T) {
	raw := buildTestMessage(t, MethodRequest, "Invitation: Planning \u00fc")

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Invitation: Planning \u00fc" {
		t.Errorf("Subject = %q, %v", subject, err)
	}
	if got := msg.Header.Get("Message-Id"); got != "<abc@example.org>" {
		t.Errorf("Message-ID = %q", got)
	}
	if got := msg.Header.Get("From"); !strings.Contains(got, "olive@example.org") {
		t.Errorf("From = %q", got)
	}
	if got := msg.Header.Get("To"); !strings.Contains(got, "bob@example.org") || !strings.Contains(got, "carol@example.org") {
		t.Errorf("To = %q", got)
	}

	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mt, _, _ := entity.Header.ContentType()
	if mt != "multipart/mixed" {
		t.Fatalf("top level = %q", mt)
	}
	mr := entity.MultipartReader()
	alt, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if mt, _, _ := alt.Header.ContentType(); mt != "multipart/alternative" {
		t.Fatalf("first part = %q", mt)
	}
	ar := alt.MultipartReader()
	text, err := ar.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if mt, _, _ := text.Header.ContentType(); mt != "text/plain" {
		t.Errorf("text part = %q", mt)
	}
	body, _ := io.ReadAll(text.Body)
	if !strings.Contains(string(body), "Hello \u00e9") {
		t.Errorf("text body = %q", body)
	}
	cal, err := ar.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	mt, params, _ := cal.Header.ContentType()
	if mt != "text/calendar" || params["method"] != MethodRequest {
		t.Errorf("calendar part = %q %v", mt, params)
	}
	inline, _ := io.ReadAll(cal.Body)
	parsed, err := ical.NewDecoder(bytes.NewReader(inline)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Props.Get(ical.PropMethod).Value; got != params["method"] {
		t.Errorf("METHOD %q differs from the content-type method %q", got, params["method"])
	}

	att, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	_, ap, _ := att.Header.ContentType()
	disp, dp, _ := att.Header.ContentDisposition()
	if disp != "attachment" || dp["filename"] != "invite.ics" || ap["name"] != "invite.ics" {
		t.Errorf("attachment = %q %v %v", disp, dp, ap)
	}
	attBody, _ := io.ReadAll(att.Body)
	if !bytes.Contains(attBody, []byte("METHOD:REQUEST")) {
		t.Error("attachment does not carry the calendar")
	}
}

func TestBuildMessageCancelMethod(t *testing.T) {
	raw := buildTestMessage(t, MethodCancel, "Cancelled: Planning")
	if !bytes.Contains(raw, []byte("method=CANCEL")) {
		t.Errorf("content-type method missing:\n%s", raw)
	}
	if bytes.Contains(raw, []byte("method=REQUEST")) {
		t.Error("CANCEL message advertises REQUEST")
	}
}

func TestBuildMessageHeaderInjection(t *testing.T) {
	// 255 bytes of three-byte runes would hide a byte cut; this title mixes
	// widths and ends in a line break pair.
	title := "Plan \u00e9\r\nBcc: attacker@evil.example\r\nX-Evil: 1\r\n\u20ac"
	raw := buildTestMessage(t, MethodRequest, "Invitation: "+title)
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"Bcc", "X-Evil"} {
		if msg.Header.Get(h) != "" {
			t.Errorf("header %s injected", h)
		}
	}
	if len(msg.Header["Subject"]) != 1 {
		t.Errorf("Subject headers = %d", len(msg.Header["Subject"]))
	}
}

func TestBuildMessageRefusals(t *testing.T) {
	cal, err := BuildCalendar(MethodRequest, structured(), "", testNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		p    Params
	}{
		{"method mismatch", Params{Method: MethodCancel, From: "olive@example.org", To: []string{"bob@example.org"}, Cal: cal}},
		{"no calendar", Params{From: "olive@example.org", To: []string{"bob@example.org"}}},
		{"no recipients", Params{From: "olive@example.org", Cal: cal}},
		{"bad sender", Params{From: "olive@example.org\r\nBcc: x@y.example", To: []string{"bob@example.org"}, Cal: cal}},
		{"bad recipient", Params{From: "olive@example.org", To: []string{"bob@example.org>, x@y.example"}, Cal: cal}},
		{"bad message id", Params{From: "olive@example.org", To: []string{"bob@example.org"}, Cal: cal, MessageID: "a b\r\nX: y"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildMessage(tc.p); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestBuildMessageGeneratesMessageID(t *testing.T) {
	cal, _ := BuildCalendar(MethodRequest, structured(), "", testNow)
	raw, err := BuildMessage(Params{From: "olive@example.org", To: []string{"bob@example.org"}, Cal: cal, Subject: "s"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if id := msg.Header.Get("Message-Id"); !strings.HasSuffix(id, "@example.org>") {
		t.Errorf("Message-ID = %q", id)
	}
}

func TestDescribe(t *testing.T) {
	tr := func(id string) string {
		return map[string]string{
			"imip.subjectRequest": "Invitation: %s", "imip.subjectUpdate": "Updated invitation: %s",
			"imip.subjectCancel": "Cancelled: %s", "imip.introRequest": "%s invites you.",
			"imip.introUpdate": "%s updated it.", "imip.introCancel": "%s cancelled it.",
			"imip.when": "When", "imip.where": "Where", "imip.organiser": "Organiser",
		}[id]
	}
	e := structured()
	for _, tc := range []struct {
		method  string
		updated bool
		subject string
		intro   string
	}{
		{MethodRequest, false, "Invitation: Planning", "olive@example.org invites you."},
		{MethodRequest, true, "Updated invitation: Planning", "olive@example.org updated it."},
		{MethodCancel, false, "Cancelled: Planning", "olive@example.org cancelled it."},
	} {
		subject, body := Describe(tc.method, tc.updated, e, time.UTC, tr)
		if subject != tc.subject {
			t.Errorf("subject = %q, want %q", subject, tc.subject)
		}
		for _, want := range []string{tc.intro, "When: 2030-05-06 09:00 - 10:00 UTC", "Where: Room 4", "Organiser: olive@example.org"} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %q:\n%s", want, body)
			}
		}
	}

	allDay := structured()
	allDay.AllDay = true
	allDay.StartTime = time.Date(2030, 5, 6, 0, 0, 0, 0, time.UTC).Unix()
	allDay.EndTime = time.Date(2030, 5, 8, 0, 0, 0, 0, time.UTC).Unix()
	_, body := Describe(MethodRequest, false, allDay, nil, tr)
	if !strings.Contains(body, "When: 2030-05-06 - 2030-05-07\r\n") {
		t.Errorf("all-day span wrong:\n%s", body)
	}
}
