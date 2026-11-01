package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// attendeesFieldValue is what the event page puts in the attendees input.
func attendeesFieldValue(ev *bongov1.Event) string {
	parts := make([]string, 0, len(ev.Attendees))
	for _, a := range ev.Attendees {
		parts = append(parts, (&mail.Address{Name: a.Name, Address: a.Address}).String())
	}
	return strings.Join(parts, ", ")
}

func addrList(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("p%d@example.com", i)
	}
	return strings.Join(parts, ", ")
}

const invitedICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Client//Cal//EN\r\n" +
	"BEGIN:VEVENT\r\nUID:inv-1@example.org\r\nDTSTAMP:20300101T000000Z\r\n" +
	"DTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\nSUMMARY:Invite\r\n" +
	"SEQUENCE:5\r\nSTATUS:CONFIRMED\r\n" +
	"ORGANIZER;CN=Olive:mailto:olive@example.org\r\n" +
	"ATTENDEE;CN=Bob;CUTYPE=INDIVIDUAL;ROLE=OPT-PARTICIPANT;PARTSTAT=ACCEPTED;RSVP=TRUE;X-NUM-GUESTS=2:mailto:bob@example.org\r\n" +
	"ATTENDEE;PARTSTAT=DECLINED:mailto:carol@example.org\r\n" +
	"END:VEVENT\r\nEND:VCALENDAR\r\n"

func seedInvited(t *testing.T) (*quotaEnv, *CalendarHandler, string, *bongov1.Event) {
	t.Helper()
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	guid, ev := seedClientEvent(t, env, []byte(invitedICS), utcUnix(2030, 5, 6, 9, 0), utcUnix(2030, 5, 6, 10, 0), "")
	return env, h, guid, ev
}

func attendeeLines(c *ical.Component) []string {
	var out []string
	for _, p := range c.Props.Values(ical.PropAttendee) {
		var b bytes.Buffer
		b.WriteString(p.Value)
		for _, k := range []string{"CN", "CUTYPE", "ROLE", "PARTSTAT", "RSVP", "X-NUM-GUESTS"} {
			b.WriteString("|" + k + "=" + p.Params.Get(k))
		}
		out = append(out, b.String())
	}
	return out
}

func TestParseAttendees(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		want    []string
		wantErr error
	}{
		{"empty", "", nil, nil},
		{"blank and commas", " , ", nil, nil},
		{"bare", "a@example.com", []string{"a@example.com"}, nil},
		{"named and list", `Jane Doe <jane@example.com>, "Doe, Bob" <bob@example.com>`, []string{"jane@example.com", "bob@example.com"}, nil},
		{"trailing comma", "a@example.com,", []string{"a@example.com"}, nil},
		{"duplicates fold case", "a@example.com, A@Example.com", []string{"a@example.com"}, nil},
		{"100 accepted", addrList(100), nil, nil},
		{"101 refused", addrList(101), nil, errAttendeesTooMany},
		{"101 with a duplicate is 100", addrList(100) + ", P3@example.com", nil, nil},
		{"not an address", "nobody", nil, errAttendeeInvalid},
		{"one bad of two", "a@example.com, nobody", nil, errAttendeeInvalid},
		{"CRLF in address", "a@example.com\r\nBcc: x@example.com", nil, errAttendeeInvalid},
		{"CRLF in encoded name", "=?utf-8?q?x=0D=0AATTENDEE:mailto:e@example.com?= <a@example.com>", nil, errAttendeeInvalid},
		{"quote in encoded name", "=?utf-8?q?x=22y?= <a@example.com>", nil, errAttendeeInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAttendees(tc.in)
			if err != tc.wantErr {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if strings.HasPrefix(tc.name, "100") || strings.HasPrefix(tc.name, "101 with") {
				if len(got) != 100 {
					t.Fatalf("len = %d, want 100", len(got))
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i, a := range got {
				if a.Address != tc.want[i] || a.Role != icalevent.RoleRequired || a.Partstat != icalevent.PartStatNeedsAction || !a.Rsvp {
					t.Errorf("attendee %d = %v", i, a)
				}
			}
		})
	}
}

func TestCreateEventAttendeeLimitsAndValidation(t *testing.T) {
	base := func(attendees string) url.Values {
		return url.Values{
			"title": {"Meeting"}, "start_date": {"2030-05-06"}, "start_time": {"09:00"},
			"end_date": {"2030-05-06"}, "end_time": {"10:00"}, "attendees": {attendees},
		}
	}
	for _, tc := range []struct {
		name      string
		attendees string
		wantCode  int
		wantCount int
	}{
		{"100 accepted", addrList(100), http.StatusSeeOther, 1},
		{"101 refused", addrList(101), http.StatusBadRequest, 0},
		{"invalid address", "a@example.com, nobody", http.StatusBadRequest, 0},
		{"CRLF injected address", "a@example.com\r\nATTENDEE:mailto:evil@example.com", http.StatusBadRequest, 0},
		{"no attendees", "", http.StatusSeeOther, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newQuotaEnv(t)
			h := NewCalendarHandler(env.events, env.calendars, nil)
			w := httptest.NewRecorder()
			h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", base(tc.attendees)))
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}
			if n := env.countEvents(t); n != tc.wantCount {
				t.Errorf("events stored = %d, want %d", n, tc.wantCount)
			}
		})
	}
}

func TestCreateEventSetsOrganizerAndExports(t *testing.T) {
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	form := url.Values{
		"title": {"Planning"}, "start_date": {"2030-05-06"}, "start_time": {"09:00"},
		"end_date": {"2030-05-06"}, "end_time": {"10:00"},
		"attendees": {`Jane Doe <jane@example.com>, bob@example.com`},
	}
	w := httptest.NewRecorder()
	h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	events, _, err := env.events.ListAll(quotaUser, 0, 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, err %v", events, err)
	}
	ev := events[0]
	if ev.Organizer != quotaEmail || len(ev.Attendees) != 2 || ev.Attendees[0].Name != "Jane Doe" {
		t.Fatalf("stored event = %v", ev)
	}

	ics := exportICS(t, env)
	for _, want := range []string{
		"ORGANIZER:mailto:" + quotaEmail,
		"ATTENDEE;CN=Jane Doe;PARTSTAT=NEEDS-ACTION;ROLE=REQ-PARTICIPANT;RSVP=TRUE:mailto:jane@example.com",
		"mailto:bob@example.com",
	} {
		if !strings.Contains(unfold(ics), want) {
			t.Errorf("export lacks %q:\n%s", want, ics)
		}
	}
}

func unfold(s string) string { return strings.ReplaceAll(s, "\r\n ", "") }

func TestCreateEventWithoutAttendeesHasNoOrganizer(t *testing.T) {
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	form := url.Values{"title": {"Solo"}, "start_date": {"2030-05-06"}, "start_time": {"09:00"}, "end_date": {"2030-05-06"}, "end_time": {"10:00"}}
	h.CreateEvent(httptest.NewRecorder(), formReq(http.MethodPost, "/calendar/event", form))
	ics := exportICS(t, env)
	if strings.Contains(ics, "ORGANIZER") || strings.Contains(ics, "ATTENDEE") {
		t.Errorf("event without attendees exports participants:\n%s", ics)
	}
}

func TestUpdateEventAttendees(t *testing.T) {
	t.Run("unchanged set keeps ATTENDEE lines, PARTSTAT and organiser", func(t *testing.T) {
		env, h, guid, ev := seedInvited(t)
		_, before := decodeStored(t, env, guid)
		w := putEdit(t, h, guid, browserForm(ev, map[string]string{"title": "Renamed"}))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		got, cal := decodeStored(t, env, guid)
		beforeVEvent, _ := icalevent.Master(before)
		master, _ := icalevent.Master(cal)
		if a, b := attendeeLines(beforeVEvent), attendeeLines(master); strings.Join(a, "\n") != strings.Join(b, "\n") {
			t.Errorf("ATTENDEE changed:\nbefore %v\nafter  %v", a, b)
		}
		if got.Organizer != "olive@example.org" || got.Status != "CONFIRMED" {
			t.Errorf("organizer/status = %q/%q", got.Organizer, got.Status)
		}
		if got.Sequence != 6 || propValue(master, ical.PropSequence) != "6" {
			t.Errorf("sequence proto %d raw %q, want 6", got.Sequence, propValue(master, ical.PropSequence))
		}
		if len(got.Attendees) != 2 || got.Attendees[0].Partstat != "ACCEPTED" || got.Attendees[0].Role != "OPT-PARTICIPANT" || got.Attendees[1].Partstat != "DECLINED" {
			t.Errorf("attendees = %v", got.Attendees)
		}
	})

	t.Run("adding keeps the others and defaults the new one", func(t *testing.T) {
		env, h, guid, ev := seedInvited(t)
		field := attendeesFieldValue(ev) + ", dave@example.org"
		if w := putEdit(t, h, guid, browserForm(ev, map[string]string{"attendees": field})); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		got, cal := decodeStored(t, env, guid)
		master, _ := icalevent.Master(cal)
		lines := attendeeLines(master)
		if len(lines) != 3 || !strings.Contains(lines[0], "PARTSTAT=ACCEPTED") || !strings.Contains(lines[0], "X-NUM-GUESTS=2") || !strings.Contains(lines[0], "CUTYPE=INDIVIDUAL") {
			t.Fatalf("lines = %v", lines)
		}
		if !strings.Contains(lines[2], "dave@example.org") || !strings.Contains(lines[2], "PARTSTAT=NEEDS-ACTION") || !strings.Contains(lines[2], "ROLE=REQ-PARTICIPANT") {
			t.Errorf("new attendee line = %q", lines[2])
		}
		if len(got.Attendees) != 3 || got.Attendees[0].Partstat != "ACCEPTED" {
			t.Errorf("attendees = %v", got.Attendees)
		}
	})

	t.Run("removing drops only that attendee", func(t *testing.T) {
		env, h, guid, ev := seedInvited(t)
		field := `Bob <bob@example.org>`
		if w := putEdit(t, h, guid, browserForm(ev, map[string]string{"attendees": field})); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		got, cal := decodeStored(t, env, guid)
		master, _ := icalevent.Master(cal)
		lines := attendeeLines(master)
		if len(lines) != 1 || !strings.Contains(lines[0], "bob@example.org") || !strings.Contains(lines[0], "PARTSTAT=ACCEPTED") {
			t.Errorf("lines = %v", lines)
		}
		if len(got.Attendees) != 1 || got.Attendees[0].Address != "bob@example.org" {
			t.Errorf("attendees = %v", got.Attendees)
		}
	})

	t.Run("case-only change of an address is not a change", func(t *testing.T) {
		env, h, guid, ev := seedInvited(t)
		_, before := decodeStored(t, env, guid)
		field := "BOB@Example.org, carol@example.org"
		if w := putEdit(t, h, guid, browserForm(ev, map[string]string{"attendees": field})); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", w.Code)
		}
		_, cal := decodeStored(t, env, guid)
		a, _ := icalevent.Master(before)
		b, _ := icalevent.Master(cal)
		if strings.Join(attendeeLines(a), "\n") != strings.Join(attendeeLines(b), "\n") {
			t.Errorf("ATTENDEE lines changed: %v vs %v", attendeeLines(a), attendeeLines(b))
		}
	})

	t.Run("an existing organiser is never overwritten", func(t *testing.T) {
		env, h, guid, ev := seedInvited(t)
		field := attendeesFieldValue(ev) + ", dave@example.org"
		putEdit(t, h, guid, browserForm(ev, map[string]string{"attendees": field}))
		got, cal := decodeStored(t, env, guid)
		master, _ := icalevent.Master(cal)
		if v := propValue(master, ical.PropOrganizer); v != "mailto:olive@example.org" {
			t.Errorf("ORGANIZER = %q", v)
		}
		if got.Organizer != "olive@example.org" {
			t.Errorf("organizer = %q", got.Organizer)
		}
	})

	t.Run("101 attendees on update is refused and nothing changes", func(t *testing.T) {
		env, h, guid, ev := seedInvited(t)
		w := putEdit(t, h, guid, browserForm(ev, map[string]string{"attendees": addrList(101)}))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", w.Code)
		}
		got, _ := decodeStored(t, env, guid)
		if len(got.Attendees) != 2 {
			t.Errorf("attendees = %v", got.Attendees)
		}
	})

	t.Run("raw with more than 100 attendees keeps the rest on an unrelated edit", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//EN\r\nBEGIN:VEVENT\r\nUID:big@x\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\nSUMMARY:Big\r\n")
		for i := range 151 {
			fmt.Fprintf(&b, "ATTENDEE:mailto:p%d@example.com\r\n", i)
		}
		b.WriteString("END:VEVENT\r\nEND:VCALENDAR\r\n")
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		guid, ev := seedClientEvent(t, env, []byte(b.String()), utcUnix(2030, 5, 6, 9, 0), utcUnix(2030, 5, 6, 10, 0), "")
		if len(ev.Attendees) != 100 {
			t.Fatalf("structured attendees = %d", len(ev.Attendees))
		}
		if w := putEdit(t, h, guid, browserForm(ev, map[string]string{"title": "Big 2", "rrule": ""})); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		_, cal := decodeStored(t, env, guid)
		master, _ := icalevent.Master(cal)
		if n := len(master.Props.Values(ical.PropAttendee)); n != 151 {
			t.Errorf("raw attendees = %d, want 151", n)
		}
	})
}

func TestUpdateEventAttendeesWithoutRaw(t *testing.T) {
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	guid, err := env.events.Create(quotaUser, &bongov1.Event{
		Title: "Web", CalendarId: "default", StartTime: utcUnix(2030, 5, 6, 9, 0), EndTime: utcUnix(2030, 5, 6, 10, 0),
		Organizer: quotaEmail, Sequence: 2, Status: "TENTATIVE",
		Attendees: []*bongov1.Attendee{{Address: "bob@example.com", Name: "Bob", Role: "REQ-PARTICIPANT", Partstat: "ACCEPTED", Rsvp: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := env.events.Get(quotaUser, guid)
	if w := putEdit(t, h, guid, browserForm(ev, map[string]string{"title": "Web 2"})); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	got, _ := env.events.Get(quotaUser, guid)
	if got.Attendees[0].Partstat != "ACCEPTED" || got.Attendees[0].Name != "Bob" || got.Organizer != quotaEmail || got.Status != "TENTATIVE" {
		t.Errorf("participants lost on edit: %v", got)
	}
	if got.Sequence != 3 {
		t.Errorf("sequence = %d, want 3", got.Sequence)
	}
	ics := unfold(exportICS(t, env))
	for _, want := range []string{"PARTSTAT=ACCEPTED", "ORGANIZER:mailto:" + quotaEmail, "SEQUENCE:3", "STATUS:TENTATIVE"} {
		if !strings.Contains(ics, want) {
			t.Errorf("export lacks %q:\n%s", want, ics)
		}
	}
}

func TestApplyEventEditToICalAttendees(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	parse := func(raw []byte) *bongov1.Event {
		cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
		if err != nil {
			t.Fatal(err)
		}
		e, _ := icalevent.ToEvent(cal)
		return e
	}
	raw := []byte(invitedICS)

	t.Run("unchanged attendees are byte-identical", func(t *testing.T) {
		existing := parse(raw)
		updated := parse(raw)
		out, err := applyEventEditToICal(raw, existing, updated, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range []string{
			"ATTENDEE;CN=Bob;CUTYPE=INDIVIDUAL;PARTSTAT=ACCEPTED;ROLE=OPT-PARTICIPANT;RSVP=TRUE;X-NUM-GUESTS=2:mailto:bob@example.org",
			"ATTENDEE;PARTSTAT=DECLINED:mailto:carol@example.org",
		} {
			if !strings.Contains(string(out), line+"\r\n") {
				t.Errorf("missing %q in:\n%s", line, out)
			}
		}
		if updated.Sequence != 6 {
			t.Errorf("updated.Sequence = %d, want 6", updated.Sequence)
		}
	})

	t.Run("organiser added only when absent", func(t *testing.T) {
		noOrg := []byte(strings.Replace(invitedICS, "ORGANIZER;CN=Olive:mailto:olive@example.org\r\n", "", 1))
		existing := parse(noOrg)
		updated := parse(noOrg)
		updated.Organizer = "me@example.com"
		out, err := applyEventEditToICal(noOrg, existing, updated, now)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "ORGANIZER:mailto:me@example.com") {
			t.Errorf("organiser not added:\n%s", out)
		}

		existing = parse(raw)
		updated = parse(raw)
		updated.Organizer = "me@example.com"
		out, err = applyEventEditToICal(raw, existing, updated, now)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "me@example.com") || !strings.Contains(string(out), "mailto:olive@example.org") {
			t.Errorf("organiser overwritten:\n%s", out)
		}
	})
}

func TestEventFormShowsAttendees(t *testing.T) {
	env, h, guid, _ := seedInvited(t)
	req := withGUID(withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/event/"+guid, nil), quotaUser, quotaEmail), guid)
	w := httptest.NewRecorder()
	h.ShowEvent(w, req)
	body := w.Body.String()
	for _, want := range []string{`name="attendees"`, `x-data="galAutocomplete"`, "bob@example.org", "attendee-badge accepted", "attendee-badge declined", "olive@example.org"} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %q", want)
		}
	}
	_ = env
}

func TestEventFormRendersOldEventWithoutParticipants(t *testing.T) {
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	guid, err := env.events.Create(quotaUser, &bongov1.Event{Title: "Old", CalendarId: "default", StartTime: utcUnix(2030, 5, 6, 9, 0), EndTime: utcUnix(2030, 5, 6, 10, 0)})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ShowEvent(w, withGUID(withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/event/"+guid, nil), quotaUser, quotaEmail), guid))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `name="attendees"`) || strings.Contains(w.Body.String(), "attendee-badge") {
		t.Fatalf("status %d, body:\n%s", w.Code, w.Body)
	}
	if ics := exportICS(t, env); strings.Contains(ics, "ATTENDEE") || strings.Contains(ics, "ORGANIZER") {
		t.Errorf("old event exports participants:\n%s", ics)
	}
}

func parseICSEvent(t *testing.T, raw []byte) *bongov1.Event {
	t.Helper()
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	e, _ := icalevent.ToEvent(cal)
	return e
}

// An address beyond the structured cap is in ical_raw but not in the
// structured list. Typing it into the field must not write a second ATTENDEE
// line for it.
func TestApplyEventEditToICalDoesNotDuplicateAttendeeBeyondCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//C//EN\r\nBEGIN:VEVENT\r\nUID:big\r\nDTSTAMP:20300101T000000Z\r\n" +
		"DTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\nSUMMARY:Big\r\nORGANIZER:mailto:o@example.org\r\n")
	for i := 0; i <= icalevent.MaxAttendees; i++ {
		fmt.Fprintf(&b, "ATTENDEE;PARTSTAT=ACCEPTED:mailto:p%d@example.org\r\n", i)
	}
	b.WriteString("END:VEVENT\r\nEND:VCALENDAR\r\n")
	raw := []byte(b.String())

	existing := parseICSEvent(t, raw)
	if len(existing.Attendees) != icalevent.MaxAttendees {
		t.Fatalf("structured attendees = %d, want the cap", len(existing.Attendees))
	}
	updated := parseICSEvent(t, raw)
	beyond := fmt.Sprintf("p%d@example.org", icalevent.MaxAttendees)
	// Remove p0 and add the address that exists only in the raw.
	updated.Attendees = append(updated.Attendees[1:], &bongov1.Attendee{Address: beyond, Role: icalevent.RoleRequired, Partstat: icalevent.PartStatNeedsAction, Rsvp: true})

	out, err := applyEventEditToICal(raw, existing, updated, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.ToLower(string(out)), "mailto:"+beyond); n != 1 {
		t.Errorf("%d ATTENDEE lines for %s, want 1", n, beyond)
	}
	if strings.Contains(string(out), "mailto:p0@example.org") {
		t.Error("the removed attendee is still present")
	}
	if !strings.Contains(string(out), "ATTENDEE;PARTSTAT=ACCEPTED:mailto:"+beyond) {
		t.Error("the raw line of the address beyond the cap was replaced")
	}
}

// A display name changed in the form reaches the ATTENDEE CN, so the raw and
// the structured copy agree; nothing else on the property moves.
func TestApplyEventEditToICalChangesKeptAttendeeName(t *testing.T) {
	raw := []byte(invitedICS)
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

	t.Run("rename sets CN and keeps other parameters", func(t *testing.T) {
		existing, updated := parseICSEvent(t, raw), parseICSEvent(t, raw)
		updated.Attendees[0].Name = "Robert"
		out, err := applyEventEditToICal(raw, existing, updated, now)
		if err != nil {
			t.Fatal(err)
		}
		want := "ATTENDEE;CN=Robert;CUTYPE=INDIVIDUAL;PARTSTAT=ACCEPTED;ROLE=OPT-PARTICIPANT;RSVP=TRUE;X-NUM-GUESTS=2:mailto:bob@example.org"
		if !strings.Contains(string(out), want+"\r\n") {
			t.Errorf("missing %q in:\n%s", want, out)
		}
		if !strings.Contains(string(out), "ATTENDEE;PARTSTAT=DECLINED:mailto:carol@example.org\r\n") {
			t.Errorf("an untouched attendee changed:\n%s", out)
		}
	})

	t.Run("name added to an attendee that had none", func(t *testing.T) {
		existing, updated := parseICSEvent(t, raw), parseICSEvent(t, raw)
		updated.Attendees[1].Name = "Carol C"
		out, err := applyEventEditToICal(raw, existing, updated, now)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "ATTENDEE;CN=\"Carol C\";PARTSTAT=DECLINED:mailto:carol@example.org") &&
			!strings.Contains(string(out), "ATTENDEE;CN=Carol C;PARTSTAT=DECLINED:mailto:carol@example.org") {
			t.Errorf("CN not set:\n%s", out)
		}
	})

	t.Run("name removed drops only CN", func(t *testing.T) {
		existing, updated := parseICSEvent(t, raw), parseICSEvent(t, raw)
		updated.Attendees[0].Name = ""
		out, err := applyEventEditToICal(raw, existing, updated, now)
		if err != nil {
			t.Fatal(err)
		}
		want := "ATTENDEE;CUTYPE=INDIVIDUAL;PARTSTAT=ACCEPTED;ROLE=OPT-PARTICIPANT;RSVP=TRUE;X-NUM-GUESTS=2:mailto:bob@example.org"
		if !strings.Contains(string(out), want+"\r\n") {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	})
}

// Through the handler: renaming in the form reaches both copies.
func TestUpdateEventRenamesKeptAttendee(t *testing.T) {
	env, h, guid, ev := seedInvited(t)
	field := "Robert <bob@example.org>, carol@example.org"
	if w := putEdit(t, h, guid, browserForm(ev, map[string]string{"attendees": field})); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	got, cal := decodeStored(t, env, guid)
	master, _ := icalevent.Master(cal)
	lines := attendeeLines(master)
	if len(lines) != 2 || !strings.Contains(lines[0], "|CN=Robert|") || !strings.Contains(lines[0], "PARTSTAT=ACCEPTED") {
		t.Errorf("lines = %v", lines)
	}
	if got.Attendees[0].Name != "Robert" {
		t.Errorf("proto name = %q", got.Attendees[0].Name)
	}
}
