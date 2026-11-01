package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-message"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// sentInvite is one message the calendar handed to the sender.
type sentInvite struct {
	userID, sessionEmail, from string
	to                         []string
	method                     string
	cal                        *ical.Calendar
	subject                    string
}

// fakeInviteSender records invitations and can refuse chosen recipients.
type fakeInviteSender struct {
	mu   sync.Mutex
	sent []sentInvite
	// refuse lists addresses reported as not reached.
	refuse map[string]bool
	// err is returned with no recipient named.
	err error
}

func (f *fakeInviteSender) DeliverGenerated(userID, sessionEmail, from string, rcpts []string, raw []byte) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv := sentInvite{userID: userID, sessionEmail: sessionEmail, from: from, to: append([]string(nil), rcpts...)}
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		return rcpts, err
	}
	inv.subject = entity.Header.Get("Subject")
	mr := entity.MultipartReader()
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		if mt, _, _ := part.Header.ContentType(); mt == "multipart/alternative" {
			ar := part.MultipartReader()
			for {
				sub, err := ar.NextPart()
				if err != nil {
					break
				}
				if mt, params, _ := sub.Header.ContentType(); mt == "text/calendar" {
					body, _ := io.ReadAll(sub.Body)
					inv.cal, _ = ical.NewDecoder(bytes.NewReader(body)).Decode()
					inv.method = params["method"]
				}
			}
		}
	}
	f.sent = append(f.sent, inv)
	if f.err != nil {
		return nil, f.err
	}
	var missed []string
	for _, r := range rcpts {
		if f.refuse[r] {
			missed = append(missed, r)
		}
	}
	return missed, nil
}

func (f *fakeInviteSender) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = nil
}

func (f *fakeInviteSender) messages() []sentInvite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentInvite(nil), f.sent...)
}

func (i sentInvite) vevent(t *testing.T) *ical.Component {
	t.Helper()
	if i.cal == nil {
		t.Fatal("message carried no calendar")
	}
	for _, c := range i.cal.Children {
		if c.Name == ical.CompEvent {
			return c
		}
	}
	t.Fatal("no VEVENT")
	return nil
}

func (i sentInvite) seq(t *testing.T) string { return i.vevent(t).Props.Get(ical.PropSequence).Value }

func addrsOf(i sentInvite) string { return strings.Join(sorted(i.to), ",") }

func newInviteHandler(t *testing.T) (*quotaEnv, *CalendarHandler, *fakeInviteSender) {
	t.Helper()
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	fs := &fakeInviteSender{}
	h.SetInviteSender(fs)
	return env, h, fs
}

func createMeeting(t *testing.T, h *CalendarHandler, env *quotaEnv, attendees string) (string, *bongov1.Event) {
	t.Helper()
	form := url.Values{
		"title": {"Planning"}, "start_date": {"2030-05-06"}, "start_time": {"09:00"},
		"end_date": {"2030-05-06"}, "end_time": {"10:00"}, "location": {"Room 4"},
		"attendees": {attendees},
	}
	w := httptest.NewRecorder()
	h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d: %s", w.Code, w.Body)
	}
	events, _, err := env.events.ListAll(quotaUser, 0, 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, err %v", events, err)
	}
	return events[0].Guid, events[0]
}

func TestInviteOnCreate(t *testing.T) {
	env, h, fs := newInviteHandler(t)
	_, ev := createMeeting(t, h, env, "bob@example.org, Carol <carol@example.org>, "+quotaEmail)

	msgs := fs.messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.method != "REQUEST" || m.userID != quotaUser || m.sessionEmail != quotaEmail || m.from != quotaEmail {
		t.Errorf("message = %+v", m)
	}
	if got := addrsOf(m); got != "bob@example.org,carol@example.org" {
		t.Errorf("recipients = %q (the organiser's own address must not be written to)", got)
	}
	ve := m.vevent(t)
	if got := ve.Props.Get(ical.PropOrganizer).Value; got != "mailto:"+quotaEmail {
		t.Errorf("ORGANIZER = %q", got)
	}
	if got := ve.Props.Get(ical.PropUID).Value; got != ev.Guid {
		t.Errorf("UID = %q", got)
	}
	if !strings.Contains(m.subject, "Planning") {
		t.Errorf("subject = %q", m.subject)
	}
}

func TestInviteNothingToSend(t *testing.T) {
	env, h, fs := newInviteHandler(t)
	createMeeting(t, h, env, "")
	if n := len(fs.messages()); n != 0 {
		t.Errorf("messages = %d for an event without attendees", n)
	}
}

func TestInviteOnUpdate(t *testing.T) {
	for _, tc := range []struct {
		name string
		over map[string]string
		// want lists "METHOD seq to,to" per message, in order.
		want []string
	}{
		{"time change", map[string]string{"start_time": "11:00", "end_time": "12:00"},
			[]string{"REQUEST 1 bob@example.org,carol@example.org"}},
		{"place change", map[string]string{"location": "Room 9"},
			[]string{"REQUEST 1 bob@example.org,carol@example.org"}},
		{"attendee added", map[string]string{"attendees": "bob@example.org, carol@example.org, dave@example.org"},
			[]string{"REQUEST 1 bob@example.org,carol@example.org,dave@example.org"}},
		{"attendee removed", map[string]string{"attendees": "bob@example.org"},
			[]string{"REQUEST 1 bob@example.org", "CANCEL 1 carol@example.org"}},
		{"everyone removed", map[string]string{"attendees": ""},
			[]string{"CANCEL 1 bob@example.org,carol@example.org"}},
		{"title only", map[string]string{"title": "Renamed"}, nil},
		{"no change", nil, nil},
		{"case-only change of an address", map[string]string{"attendees": "BOB@example.org, carol@example.org"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, h, fs := newInviteHandler(t)
			guid, ev := createMeeting(t, h, env, "bob@example.org, carol@example.org")
			fs.reset()

			if w := putEdit(t, h, guid, browserForm(ev, tc.over)); w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}
			var got []string
			for _, m := range fs.messages() {
				got = append(got, m.method+" "+m.seq(t)+" "+addrsOf(m))
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("messages = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInviteCancelListsOnlyRemoved(t *testing.T) {
	env, h, fs := newInviteHandler(t)
	guid, ev := createMeeting(t, h, env, "bob@example.org, carol@example.org")
	fs.reset()
	putEdit(t, h, guid, browserForm(ev, map[string]string{"attendees": "bob@example.org"}))
	msgs := fs.messages()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d", len(msgs))
	}
	cancel := msgs[1].vevent(t)
	if got := attendeesOf(cancel); got != "mailto:carol@example.org" {
		t.Errorf("CANCEL lists %q", got)
	}
	if got := cancel.Props.Get(ical.PropStatus).Value; got != "CANCELLED" {
		t.Errorf("STATUS = %q", got)
	}
	if got := attendeesOf(msgs[0].vevent(t)); got != "mailto:bob@example.org" {
		t.Errorf("REQUEST lists %q", got)
	}
}

func attendeesOf(c *ical.Component) string {
	var out []string
	for _, p := range c.Props.Values(ical.PropAttendee) {
		out = append(out, p.Value)
	}
	return strings.Join(out, ",")
}

func TestInviteOnDelete(t *testing.T) {
	for _, hx := range []bool{false, true} {
		env, h, fs := newInviteHandler(t)
		guid, _ := createMeeting(t, h, env, "bob@example.org, carol@example.org")
		fs.reset()

		req := withGUID(withUserContext(httptest.NewRequest(http.MethodDelete, "/calendar/event/"+guid, nil), quotaUser, quotaEmail), guid)
		if hx {
			req.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		h.DeleteEvent(w, req)
		if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		msgs := fs.messages()
		if len(msgs) != 1 || msgs[0].method != "CANCEL" || addrsOf(msgs[0]) != "bob@example.org,carol@example.org" {
			t.Fatalf("hx=%v messages = %+v", hx, msgs)
		}
		if got := msgs[0].seq(t); got != "1" {
			t.Errorf("SEQUENCE = %q, want the stored 0 plus one", got)
		}
		if _, err := env.events.Get(quotaUser, guid); err == nil {
			t.Error("event not deleted")
		}
	}
}

func TestInviteOnReschedule(t *testing.T) {
	env, h, fs := newInviteHandler(t)
	guid, _ := createMeeting(t, h, env, "bob@example.org")
	fs.reset()

	form := url.Values{"start_time": {"1904000000"}, "end_time": {"1904003777"}}
	w := httptest.NewRecorder()
	h.RescheduleEvent(w, withGUID(formReq(http.MethodPost, "/calendar/event/"+guid+"/reschedule", form), guid))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	msgs := fs.messages()
	if len(msgs) != 1 || msgs[0].method != "REQUEST" || addrsOf(msgs[0]) != "bob@example.org" {
		t.Fatalf("messages = %+v", msgs)
	}
	if got := msgs[0].seq(t); got != "1" {
		t.Errorf("SEQUENCE = %q, a move must raise it", got)
	}
	if stored, _ := env.events.Get(quotaUser, guid); stored.Sequence != 1 {
		t.Errorf("stored sequence = %d", stored.Sequence)
	}
}

// The event is organised by someone else, as a CalDAV import where the user is
// an attendee would be. Sending would speak in the organiser's name.
func TestInviteRefusedForForeignOrganiser(t *testing.T) {
	post := func(t *testing.T, h *CalendarHandler, guid string, ev *bongov1.Event) {
		t.Helper()
		if w := putEdit(t, h, guid, browserForm(ev, map[string]string{"location": "Room 9"})); w.Code != http.StatusSeeOther {
			t.Fatalf("update status = %d", w.Code)
		}
		w := httptest.NewRecorder()
		h.RescheduleEvent(w, withGUID(formReq(http.MethodPost, "/x", url.Values{"start_time": {"1904000000"}, "end_time": {"1904003777"}}), guid))
		if w.Code != http.StatusOK {
			t.Fatalf("reschedule status = %d", w.Code)
		}
		req := withGUID(withUserContext(httptest.NewRequest(http.MethodDelete, "/x", nil), quotaUser, quotaEmail), guid)
		h.DeleteEvent(httptest.NewRecorder(), req)
	}

	t.Run("raw event", func(t *testing.T) {
		env, h, guid, ev := seedInvited(t)
		fs := &fakeInviteSender{}
		h.SetInviteSender(fs)
		// A series cannot be dragged; drop the rule so reschedule is exercised.
		ev.Rrule = ""
		if err := env.events.Update(quotaUser, guid, ev); err != nil {
			t.Fatal(err)
		}
		post(t, h, guid, ev)
		if n := len(fs.messages()); n != 0 {
			t.Errorf("sent %d messages as someone else", n)
		}
	})
	t.Run("structured event", func(t *testing.T) {
		env, h, fs := newInviteHandler(t)
		guid, _ := createMeeting(t, h, env, "bob@example.org")
		stored, _ := env.events.Get(quotaUser, guid)
		stored.Organizer = "olive@example.org"
		if err := env.events.Update(quotaUser, guid, stored); err != nil {
			t.Fatal(err)
		}
		fs.reset()
		post(t, h, guid, stored)
		if n := len(fs.messages()); n != 0 {
			t.Errorf("sent %d messages as someone else", n)
		}
	})
}

func TestInviteKeepsStoredUIDAndDropsAlarms(t *testing.T) {
	env, h, fs := newInviteHandler(t)
	raw := strings.Replace(string(clientICS("DTSTART:20300506T090000Z", "DTEND:20300506T100000Z")),
		"ATTENDEE;CN=Bob:mailto:bob@example.org", "ORGANIZER:mailto:"+quotaEmail+"\r\nATTENDEE;CN=Bob:mailto:bob@example.org", 1)
	guid, ev := seedClientEvent(t, env, []byte(raw), utcUnix(2030, 5, 6, 9, 0), utcUnix(2030, 5, 6, 10, 0), "")
	if ev.Organizer != quotaEmail {
		t.Fatalf("organiser = %q", ev.Organizer)
	}
	if w := putEdit(t, h, guid, browserForm(ev, map[string]string{"location": "Room 9"})); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	msgs := fs.messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
	ve := msgs[0].vevent(t)
	if got := ve.Props.Get(ical.PropUID).Value; got != clientUID {
		t.Errorf("UID = %q, want the client's %q", got, clientUID)
	}
	for _, c := range ve.Children {
		if c.Name == ical.CompAlarm {
			t.Error("VALARM sent to attendees")
		}
	}
	if got := ve.Props.Get(ical.PropSequence).Value; got != "3" {
		t.Errorf("SEQUENCE = %q, want 3", got)
	}
}

func TestInviteFailureLeavesEventSavedAndWarns(t *testing.T) {
	env, h, fs := newInviteHandler(t)
	fs.refuse = map[string]bool{"bob@example.org": true}
	form := url.Values{
		"title": {"Planning"}, "start_date": {"2030-05-06"}, "start_time": {"09:00"},
		"end_date": {"2030-05-06"}, "end_time": {"10:00"},
		"attendees": {"bob@example.org, carol@example.org"},
	}
	w := httptest.NewRecorder()
	h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if n := env.countEvents(t); n != 1 {
		t.Fatalf("events stored = %d, the failed send must not undo the save", n)
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil || u.Query().Get("nr") != "bob@example.org" {
		t.Fatalf("Location = %q", loc)
	}

	page := renderMonth(t, h, "en", u.RawQuery)
	if !strings.Contains(page, "flash-warning") || !strings.Contains(page, "bob@example.org") {
		t.Errorf("warning missing or does not name bob")
	}
	if strings.Contains(page, "carol@example.org") {
		t.Error("warning names an attendee who was reached")
	}
	de := renderMonth(t, h, "de", u.RawQuery)
	if !strings.Contains(de, "Einladungsnachrichten") {
		t.Error("warning not translated")
	}
	if clean := renderMonth(t, h, "en", ""); strings.Contains(clean, "flash-warning") {
		t.Error("warning shown without a failure")
	}
}

func TestInviteSenderErrorNamesEveryone(t *testing.T) {
	env, h, fs := newInviteHandler(t)
	fs.err = errors.New("boom")
	form := url.Values{
		"title": {"Planning"}, "start_date": {"2030-05-06"}, "start_time": {"09:00"},
		"end_date": {"2030-05-06"}, "end_time": {"10:00"}, "attendees": {"bob@example.org, carol@example.org"},
	}
	w := httptest.NewRecorder()
	h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
	u, _ := url.Parse(w.Header().Get("Location"))
	if got := u.Query().Get("nr"); got != "bob@example.org,carol@example.org" {
		t.Errorf("nr = %q", got)
	}
	if env.countEvents(t) != 1 {
		t.Error("event not saved")
	}
}

func TestInviteWarningChannels(t *testing.T) {
	t.Run("delete over HTMX carries it in HX-Redirect and the index forwards it", func(t *testing.T) {
		env, h, fs := newInviteHandler(t)
		guid, _ := createMeeting(t, h, env, "bob@example.org")
		fs.refuse = map[string]bool{"bob@example.org": true}
		req := withGUID(withUserContext(httptest.NewRequest(http.MethodDelete, "/x", nil), quotaUser, quotaEmail), guid)
		req.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		h.DeleteEvent(w, req)
		redirect := w.Header().Get("HX-Redirect")
		if !strings.HasPrefix(redirect, "/calendar?nr=bob%40example.org") {
			t.Fatalf("HX-Redirect = %q", redirect)
		}
		u, _ := url.Parse(redirect)
		iw := httptest.NewRecorder()
		h.CalendarIndex(iw, withUserContext(httptest.NewRequest(http.MethodGet, redirect, nil), quotaUser, quotaEmail))
		if loc := iw.Header().Get("Location"); !strings.Contains(loc, "nr=bob%40example.org") || !strings.HasPrefix(loc, "/calendar/20") {
			t.Errorf("index redirect = %q (query %q)", loc, u.RawQuery)
		}
	})
	t.Run("reschedule sets a header for the drag script", func(t *testing.T) {
		env, h, fs := newInviteHandler(t)
		guid, _ := createMeeting(t, h, env, "bob@example.org")
		fs.refuse = map[string]bool{"bob@example.org": true}
		form := url.Values{"start_time": {"1904000000"}, "end_time": {"1904003777"}}
		w := httptest.NewRecorder()
		h.RescheduleEvent(w, withGUID(formReq(http.MethodPost, "/x", form), guid))
		if got := w.Header().Get("X-Invites-Query"); got != "nr=bob%40example.org" {
			t.Errorf("X-Invites-Query = %q", got)
		}
	})
}

func TestInviteWarningParams(t *testing.T) {
	many := make([]string, 8)
	for i := range many {
		many[i] = "p" + string(rune('a'+i)) + "@example.org"
	}
	q, _ := url.ParseQuery(inviteWarningParams(many))
	shown, more := parseInviteWarning(q)
	if len(shown) != 5 || more != 3 {
		t.Errorf("shown %v more %d, want 5 and 3", shown, more)
	}
	if inviteWarningParams(nil) != "" {
		t.Error("an empty list must add no parameters")
	}

	// A crafted link can only make the page name valid addresses.
	for _, raw := range []string{
		"nr=" + url.QueryEscape("<script>alert(1)</script>"),
		"nr=" + url.QueryEscape("a@b.example\r\nBcc: x@y.example"),
		"nr=&nrmore=5",
		"nrmore=5",
	} {
		q, _ := url.ParseQuery(raw)
		if shown, more := parseInviteWarning(q); shown != nil || more != 0 {
			t.Errorf("%q gave %v, %d", raw, shown, more)
		}
	}
	q, _ = url.ParseQuery("nr=a%40b.example&nrmore=99999999")
	if _, more := parseInviteWarning(q); more != 1000 {
		t.Errorf("more = %d, want it capped at 1000", more)
	}
}

func renderMonth(t *testing.T, h *CalendarHandler, locale, rawQuery string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/calendar/2030/5?"+rawQuery, nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("year", "2030")
	rc.URLParams.Add("month", "5")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = middleware.WithLocale(ctx, locale)
	req = withUserContext(req.WithContext(ctx), quotaUser, quotaEmail)
	w := httptest.NewRecorder()
	h.MonthView(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("month status = %d: %s", w.Code, w.Body)
	}
	return w.Body.String()
}

// Without a sender wired the handler still saves and nothing panics.
func TestInviteWithoutSender(t *testing.T) {
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	createMeeting(t, h, env, "bob@example.org")
}
