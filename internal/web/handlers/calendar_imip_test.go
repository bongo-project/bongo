package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	imipUID       = "uid-1@example.org"
	imipOrganiser = "olive@example.org"
	otherUser     = "u-other"
	otherEmail    = "other@example.com"
)

type imipEnv struct {
	q      *quotaEnv
	cal    *CalendarHandler
	mail   *MailHandler
	fs     *fakeInviteSender
	secret []byte
	n      int
}

func newImipEnv(t *testing.T) *imipEnv {
	t.Helper()
	q := newQuotaEnv(t)
	for _, u := range []string{quotaUser, otherUser} {
		if err := q.mailboxes.CreateMailbox(u, "INBOX"); err != nil {
			t.Fatal(err)
		}
	}
	cal := NewCalendarHandler(q.events, q.calendars, nil)
	fs := &fakeInviteSender{}
	cal.SetInviteSender(fs)
	cal.SetAuthservID("mx.test")
	cal.SetZoneSource(fixedZone{time.UTC})
	mail := NewMailHandler(q.ps, q.blobs, q.mailboxes, discardLogger())
	mail.SetInvitations(cal)
	return &imipEnv{q: q, cal: cal, mail: mail, fs: fs, secret: []byte("imip-csrf-test-secret-0123456789")}
}

// router serves the message view and the answer route as userID, behind the
// same CSRF middleware the agent mounts them under.
func (e *imipEnv) router(userID, email string) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), userID, email)))
		})
	})
	r.Use(middleware.InjectCSRF(e.secret))
	r.Use(middleware.RequireCSRF(e.secret, true))
	r.Get("/mail/{mailbox}/{uid}", e.mail.ShowMessage)
	r.Post("/mail/{mailbox}/{uid}/invitation", e.mail.AnswerInvitation)
	return r
}

func (e *imipEnv) token(userID, email string) string {
	var tok string
	probe := middleware.InjectCSRF(e.secret)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		tok = middleware.CSRFTokenFromContext(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	probe.ServeHTTP(httptest.NewRecorder(), r.WithContext(middleware.WithUserContext(r.Context(), userID, email)))
	return tok
}

type icsOpts struct {
	uid, organiser, attendee string
	seq                      int
	start, end               string
	summary, description     string
	rid, rrule, status       string
	alarm                    bool
}

func (o icsOpts) build(method string) string {
	if o.uid == "" {
		o.uid = imipUID
	}
	if o.organiser == "" {
		o.organiser = imipOrganiser
	}
	if o.attendee == "" {
		o.attendee = quotaEmail
	}
	if o.start == "" {
		o.start, o.end = "20300506T090000Z", "20300506T100000Z"
	}
	if o.summary == "" {
		o.summary = "Planning"
	}
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//EN", "METHOD:" + method, "BEGIN:VEVENT",
		"UID:" + o.uid, "DTSTAMP:20300101T000000Z", "DTSTART:" + o.start, "DTEND:" + o.end,
		"SUMMARY:" + o.summary, "SEQUENCE:" + fmt.Sprint(o.seq),
		"ORGANIZER;CN=Olive:mailto:" + o.organiser,
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:" + o.attendee,
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:carol@example.org",
	}
	if o.description != "" {
		lines = append(lines, "DESCRIPTION:"+o.description)
	}
	if o.rid != "" {
		lines = append(lines, "RECURRENCE-ID:"+o.rid)
	}
	if o.rrule != "" {
		lines = append(lines, "RRULE:"+o.rrule)
	}
	if o.status != "" {
		lines = append(lines, "STATUS:"+o.status)
	}
	if o.alarm {
		lines = append(lines, "BEGIN:VALARM", "ACTION:DISPLAY", "DESCRIPTION:x", "TRIGGER:-PT15M", "END:VALARM")
	}
	lines = append(lines, "END:VEVENT", "END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n"
}

// inviteMail wraps a calendar part the way a mail client does: inline in
// multipart/alternative, with the method in the Content-Type.
func inviteMail(ctMethod, ics, extraHeaders string) string {
	return "From: Olive <" + imipOrganiser + ">\r\nTo: " + quotaEmail + "\r\nSubject: Invitation\r\n" + extraHeaders +
		"MIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=BB\r\n\r\n" +
		"--BB\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nYou are invited.\r\n" +
		"--BB\r\nContent-Type: text/calendar; method=" + ctMethod + "; charset=utf-8\r\n\r\n" + ics + "\r\n--BB--\r\n"
}

func (e *imipEnv) deliver(t *testing.T, userID, raw string) uint32 {
	t.Helper()
	e.n++
	hash, err := e.q.blobs.Put([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	meta := &bongov1.MailMeta{Guid: fmt.Sprintf("m-%s-%d", userID, e.n), Subject: "Invitation", From: imipOrganiser, RawMessage: &bongov1.BlobRef{Hash: hash}}
	uid, err := e.q.mailboxes.DeliverMessage(userID, "INBOX", meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	return uid
}

func (e *imipEnv) invite(t *testing.T, o icsOpts) uint32 {
	t.Helper()
	return e.deliver(t, quotaUser, inviteMail("REQUEST", o.build("REQUEST"), "Authentication-Results: mx.test; spf=pass; dmarc=pass\r\n"))
}

func (e *imipEnv) cancelMsg(t *testing.T, o icsOpts) uint32 {
	t.Helper()
	return e.deliver(t, quotaUser, inviteMail("CANCEL", o.build("CANCEL"), ""))
}

func (e *imipEnv) view(t *testing.T, uid uint32) string {
	t.Helper()
	w := httptest.NewRecorder()
	e.router(quotaUser, quotaEmail).ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d?pane=1", uid), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("view status = %d: %s", w.Code, w.Body)
	}
	return w.Body.String()
}

func (e *imipEnv) post(userID, email string, uid uint32, form url.Values, withToken bool) *httptest.ResponseRecorder {
	if withToken {
		form.Set("_csrf", e.token(userID, email))
	}
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/mail/INBOX/%d/invitation", uid), strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.router(userID, email).ServeHTTP(w, r)
	return w
}

func (e *imipEnv) answer(t *testing.T, uid uint32, partstat string) *httptest.ResponseRecorder {
	t.Helper()
	return e.post(quotaUser, quotaEmail, uid, url.Values{"action": {"answer"}, "partstat": {partstat}}, true)
}

func (e *imipEnv) act(uid uint32, action string) *httptest.ResponseRecorder {
	return e.post(quotaUser, quotaEmail, uid, url.Values{"action": {action}}, true)
}

func (e *imipEnv) stored(t *testing.T, uid string) *bongov1.Event {
	t.Helper()
	ev, err := e.q.events.Get(quotaUser, uidGuid(uid))
	if err != nil {
		t.Fatalf("event for %s: %v", uid, err)
	}
	return ev
}

func (e *imipEnv) noEvent(t *testing.T) {
	t.Helper()
	if n := e.q.countEvents(t); n != 0 {
		t.Fatalf("%d events in the calendar, want none", n)
	}
}

func partstatOf(ev *bongov1.Event) string { return myPartstat(ev, map[string]bool{quotaEmail: true}) }

func rawPartstat(t *testing.T, raw []byte) string {
	t.Helper()
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		for _, p := range c.Props.Values(ical.PropAttendee) {
			if strings.EqualFold(p.Value, "mailto:"+quotaEmail) {
				return p.Params.Get(ical.ParamParticipationStatus)
			}
		}
	}
	return ""
}

func TestInvitationCardRendersAndWritesNothing(t *testing.T) {
	e := newImipEnv(t)
	uid := e.invite(t, icsOpts{description: "See <script>alert(1)</script> http://evil.example/x", summary: "Plan <b>x</b>"})
	body := e.view(t, uid)
	for _, want := range []string{
		"invite-card", "Planning" /* never: summary is the escaped one below */, "olive@example.org", "carol@example.org",
		`name="partstat" value="ACCEPTED"`, `name="partstat" value="TENTATIVE"`, `name="partstat" value="DECLINED"`,
		`name="_csrf"`, fmt.Sprintf(`action="/mail/INBOX/%d/invitation"`, uid), "2030-05-06 09:00",
	} {
		if want == "Planning" {
			want = "Plan &lt;b&gt;x&lt;/b&gt;"
		}
		if !strings.Contains(body, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, "<b>x</b>") {
		t.Error("the card rendered markup from the invitation")
	}
	if strings.Contains(body, `href="http://evil.example`) {
		t.Error("a URL in DESCRIPTION was linked")
	}
	if strings.Contains(body, "failed sender authentication") {
		t.Error("DMARC warning shown for a message that passed")
	}
	e.noEvent(t)
	if cals, _ := e.q.calendars.List(quotaUser); len(cals) != 0 {
		t.Errorf("rendering the card created %d calendars", len(cals))
	}
	if n := len(e.fs.messages()); n != 0 {
		t.Errorf("rendering the card sent %d messages", n)
	}
}

func TestInvitationCardDMARC(t *testing.T) {
	tests := []struct {
		name, header string
		want         bool
	}{
		{"fail", "Authentication-Results: mx.test; dmarc=fail\r\n", true},
		{"pass", "Authentication-Results: mx.test; dmarc=pass\r\n", false},
		{"no verdict", "", false},
		{"foreign server cannot raise or hide", "Authentication-Results: elsewhere.test; dmarc=fail\r\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newImipEnv(t)
			uid := e.deliver(t, quotaUser, inviteMail("REQUEST", icsOpts{}.build("REQUEST"), tt.header))
			if got := strings.Contains(e.view(t, uid), "failed sender authentication"); got != tt.want {
				t.Errorf("warning shown = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInvitationCardSenderDiffersFromOrganiser(t *testing.T) {
	e := newImipEnv(t)
	uid := e.invite(t, icsOpts{organiser: "boss@example.org"})
	if !strings.Contains(e.view(t, uid), "The organiser is not the sender of this message") {
		t.Error("no warning when ORGANIZER differs from From")
	}
	uid = e.invite(t, icsOpts{})
	if strings.Contains(e.view(t, uid), "The organiser is not the sender of this message") {
		t.Error("warning shown when ORGANIZER is the sender")
	}
}

func TestInvitationCardConflicts(t *testing.T) {
	e := newImipEnv(t)
	calID, _ := e.q.calendars.EnsureDefault(quotaUser)
	mk := func(title string, status string, partstat string) {
		ev := &bongov1.Event{CalendarId: calID, Title: title, Status: status,
			StartTime: time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC).Unix(), EndTime: time.Date(2030, 5, 6, 10, 0, 0, 0, time.UTC).Unix()}
		if partstat != "" {
			ev.Attendees = []*bongov1.Attendee{{Address: quotaEmail, Partstat: partstat}}
		}
		if _, err := e.q.events.Create(quotaUser, ev); err != nil {
			t.Fatal(err)
		}
	}
	mk("Clashing lunch", "", "")
	mk("Cancelled clash", "CANCELLED", "")
	mk("Declined clash", "", "DECLINED")
	body := e.view(t, e.invite(t, icsOpts{}))
	if !strings.Contains(body, "Clashing lunch") {
		t.Error("conflict not listed")
	}
	for _, not := range []string{"Cancelled clash", "Declined clash"} {
		if strings.Contains(body, not) {
			t.Errorf("%q listed as a conflict", not)
		}
	}
	if strings.Contains(body, "Only the first occurrence") {
		t.Error("first-instance note on a single event")
	}
	body = e.view(t, e.invite(t, icsOpts{uid: "series@example.org", rrule: "FREQ=WEEKLY"}))
	if !strings.Contains(body, "Only the first occurrence is checked") {
		t.Error("a series does not say that only its first occurrence is checked")
	}
}

func TestAnswerAddsEventAndRepliesToOrganiser(t *testing.T) {
	for _, partstat := range []string{"ACCEPTED", "TENTATIVE", "DECLINED"} {
		t.Run(partstat, func(t *testing.T) {
			e := newImipEnv(t)
			uid := e.invite(t, icsOpts{alarm: true})
			w := e.answer(t, uid, partstat)
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != fmt.Sprintf("/mail/INBOX/%d", uid) {
				t.Fatalf("status %d location %q: %s", w.Code, w.Header().Get("Location"), w.Body)
			}
			ev := e.stored(t, imipUID)
			if partstatOf(ev) != partstat || rawPartstat(t, ev.IcalRaw) != partstat {
				t.Errorf("structured %q raw %q, want %s in both", partstatOf(ev), rawPartstat(t, ev.IcalRaw), partstat)
			}
			if strings.Contains(string(ev.IcalRaw), "METHOD") || strings.Contains(string(ev.IcalRaw), "VALARM") {
				t.Errorf("stored object keeps METHOD or the organiser's alarm:\n%s", ev.IcalRaw)
			}
			if !strings.Contains(string(ev.IcalRaw), "UID:"+imipUID) {
				t.Errorf("stored object lost the UID:\n%s", ev.IcalRaw)
			}
			cals, _ := e.q.calendars.List(quotaUser)
			if len(cals) != 1 || ev.CalendarId != cals[0].Guid {
				t.Errorf("event in %q, calendars %v: want the default calendar", ev.CalendarId, cals)
			}
			if ev.Organizer != imipOrganiser || ev.Title != "Planning" {
				t.Errorf("event = %+v", ev)
			}

			msgs := e.fs.messages()
			if len(msgs) != 1 {
				t.Fatalf("messages = %d, want 1 REPLY", len(msgs))
			}
			m := msgs[0]
			if m.method != "REPLY" || m.from != quotaEmail || addrsOf(m) != imipOrganiser || m.userID != quotaUser {
				t.Errorf("message = %+v", m)
			}
			ve := m.vevent(t)
			atts := ve.Props.Values(ical.PropAttendee)
			if len(atts) != 1 || !strings.EqualFold(atts[0].Value, "mailto:"+quotaEmail) || atts[0].Params.Get(ical.ParamParticipationStatus) != partstat {
				t.Errorf("REPLY attendees = %+v", atts)
			}
			if ve.Props.Get(ical.PropUID).Value != imipUID || ve.Props.Get(ical.PropSequence).Value != "0" {
				t.Errorf("REPLY UID/SEQUENCE = %q/%q", ve.Props.Get(ical.PropUID).Value, ve.Props.Get(ical.PropSequence).Value)
			}
			if !strings.Contains(m.subject, "Planning") {
				t.Errorf("subject = %q", m.subject)
			}
		})
	}
}

func TestAnswerIsIdempotentAndAnswerCanChange(t *testing.T) {
	e := newImipEnv(t)
	uid := e.invite(t, icsOpts{})
	e.answer(t, uid, "ACCEPTED")
	e.answer(t, uid, "ACCEPTED")
	if n := e.q.countEvents(t); n != 1 {
		t.Fatalf("%d events after a double submit, want 1", n)
	}
	if w := e.answer(t, uid, "DECLINED"); w.Code != http.StatusSeeOther {
		t.Fatalf("changing the answer: %d %s", w.Code, w.Body)
	}
	ev := e.stored(t, imipUID)
	if partstatOf(ev) != "DECLINED" || rawPartstat(t, ev.IcalRaw) != "DECLINED" || e.q.countEvents(t) != 1 {
		t.Errorf("after changing the answer: %q / %q", partstatOf(ev), rawPartstat(t, ev.IcalRaw))
	}
	body := e.view(t, uid)
	if !strings.Contains(body, "You declined this invitation") {
		t.Error("the card does not say what the user answered")
	}
}

func TestAnswerToChosenCalendar(t *testing.T) {
	e := newImipEnv(t)
	calID, _ := e.q.calendars.EnsureDefault(quotaUser)
	second, err := e.q.calendars.Create(quotaUser, &bongov1.Calendar{Name: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	uid := e.invite(t, icsOpts{})
	body := e.view(t, uid)
	if !strings.Contains(body, `name="calendar_id"`) || !strings.Contains(body, "Work") {
		t.Error("no calendar choice with two calendars")
	}
	form := url.Values{"action": {"answer"}, "partstat": {"ACCEPTED"}, "calendar_id": {second}}
	if w := e.post(quotaUser, quotaEmail, uid, form, true); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := e.stored(t, imipUID).CalendarId; got != second || got == calID {
		t.Errorf("calendar = %q, want %q", got, second)
	}
}

func TestDeclinedEventIsHiddenFromViewsAndFreeBusy(t *testing.T) {
	e := newImipEnv(t)
	uid := e.invite(t, icsOpts{start: "20300506T090000Z", end: "20300506T100000Z"})
	e.answer(t, uid, "DECLINED")
	if month := renderMonth(t, e.cal, "en", ""); strings.Contains(month, "Planning") {
		t.Error("a declined event is still in the month view")
	}

	// Accepting brings it back.
	e.answer(t, uid, "ACCEPTED")
	if month := renderMonth(t, e.cal, "en", ""); !strings.Contains(month, "Planning") {
		t.Error("an accepted event is not in the month view")
	}

	busy := func() string {
		w := httptest.NewRecorder()
		e.cal.FreeBusy(w, withUserContext(httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/calendar/freebusy?user=%s&start=1900000000&end=1910000000", quotaUser), nil), quotaUser, quotaEmail))
		return strings.TrimSpace(w.Body.String())
	}
	if got := busy(); got == "null" || got == "[]" {
		t.Errorf("an accepted event does not make the user busy: %s", got)
	}
	e.answer(t, uid, "DECLINED")
	if got := busy(); got != "null" && got != "[]" {
		t.Errorf("a declined event makes the user busy: %s", got)
	}
}

func TestUpdateKeepsOrResetsPartstat(t *testing.T) {
	tests := []struct {
		name     string
		update   icsOpts
		wantPart string
	}{
		{"same time keeps the answer", icsOpts{seq: 1, summary: "Planning v2"}, "ACCEPTED"},
		{"new time resets to NEEDS-ACTION", icsOpts{seq: 1, start: "20300506T110000Z", end: "20300506T120000Z"}, "NEEDS-ACTION"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newImipEnv(t)
			e.answer(t, e.invite(t, icsOpts{}), "ACCEPTED")
			e.fs.reset()

			uid := e.invite(t, tt.update)
			body := e.view(t, uid)
			if !strings.Contains(body, `value="update"`) || !strings.Contains(body, "Update in calendar") {
				t.Fatal("no update button for a higher SEQUENCE")
			}
			// Viewing applies nothing.
			if got := e.stored(t, imipUID); got.Sequence != 0 {
				t.Fatalf("viewing the update changed the event: sequence %d", got.Sequence)
			}
			if w := e.act(uid, "update"); w.Code != http.StatusSeeOther {
				t.Fatalf("update status %d: %s", w.Code, w.Body)
			}
			ev := e.stored(t, imipUID)
			if ev.Sequence != 1 || partstatOf(ev) != tt.wantPart || rawPartstat(t, ev.IcalRaw) != tt.wantPart {
				t.Errorf("sequence %d, PARTSTAT %q / %q, want 1 and %s", ev.Sequence, partstatOf(ev), rawPartstat(t, ev.IcalRaw), tt.wantPart)
			}
			if tt.update.start != "" && ev.StartTime == 0 {
				t.Error("start time lost")
			}
			if n := len(e.fs.messages()); n != 0 {
				t.Errorf("an update sent %d messages", n)
			}
			if e.q.countEvents(t) != 1 {
				t.Error("an update created a second event")
			}
		})
	}
}

func TestAnswerOnNewerRequestAppliesIt(t *testing.T) {
	e := newImipEnv(t)
	e.answer(t, e.invite(t, icsOpts{}), "ACCEPTED")
	uid := e.invite(t, icsOpts{seq: 3, summary: "Moved", start: "20300507T090000Z", end: "20300507T100000Z"})
	if w := e.answer(t, uid, "TENTATIVE"); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	ev := e.stored(t, imipUID)
	if ev.Title != "Moved" || ev.Sequence != 3 || partstatOf(ev) != "TENTATIVE" {
		t.Errorf("event = %q seq %d %s", ev.Title, ev.Sequence, partstatOf(ev))
	}
}

func TestOutdatedRequestIsReadOnly(t *testing.T) {
	e := newImipEnv(t)
	e.answer(t, e.invite(t, icsOpts{seq: 2}), "ACCEPTED")
	e.fs.reset()
	old := e.invite(t, icsOpts{seq: 1, summary: "Old title"})
	body := e.view(t, old)
	if strings.Contains(body, `name="partstat"`) || !strings.Contains(body, "newer version") {
		t.Error("an outdated request still offers answers")
	}
	for _, form := range []url.Values{
		{"action": {"answer"}, "partstat": {"DECLINED"}},
		{"action": {"update"}},
	} {
		if w := e.post(quotaUser, quotaEmail, old, form, true); w.Code != http.StatusConflict {
			t.Errorf("%v = %d, want 409", form, w.Code)
		}
	}
	if ev := e.stored(t, imipUID); ev.Title != "Planning" || partstatOf(ev) != "ACCEPTED" || len(e.fs.messages()) != 0 {
		t.Error("an outdated request changed the event or sent mail")
	}
}

func TestCancelMarksEventAndAllowsRemoval(t *testing.T) {
	e := newImipEnv(t)
	e.answer(t, e.invite(t, icsOpts{}), "ACCEPTED")
	e.fs.reset()

	uid := e.cancelMsg(t, icsOpts{seq: 1, status: "CANCELLED"})
	body := e.view(t, uid)
	if !strings.Contains(body, "Mark as cancelled") {
		t.Fatal("no cancel button")
	}
	if e.stored(t, imipUID).Status == "CANCELLED" {
		t.Fatal("viewing the cancellation applied it")
	}
	if w := e.act(uid, "cancel"); w.Code != http.StatusSeeOther {
		t.Fatalf("cancel status %d: %s", w.Code, w.Body)
	}
	ev := e.stored(t, imipUID)
	if ev.Status != "CANCELLED" || !strings.Contains(string(ev.IcalRaw), "STATUS:CANCELLED") || ev.Sequence != 1 {
		t.Errorf("status %q seq %d raw:\n%s", ev.Status, ev.Sequence, ev.IcalRaw)
	}
	// Struck through in the views, still listed.
	month := renderMonth(t, e.cal, "en", "")
	if !strings.Contains(month, "event-cancelled") || !strings.Contains(month, "Planning") {
		t.Error("a cancelled event is not drawn struck through")
	}

	// Removable, and removing sends nothing.
	if !strings.Contains(e.view(t, uid), `value="remove"`) {
		t.Error("no remove button for a cancelled event")
	}
	if w := e.act(uid, "remove"); w.Code != http.StatusSeeOther {
		t.Fatalf("remove status %d: %s", w.Code, w.Body)
	}
	e.noEvent(t)
	if n := len(e.fs.messages()); n != 0 {
		t.Errorf("cancelling and removing sent %d messages", n)
	}
}

// Deleting a cancelled event through the normal route must not send anything
// either: its organiser is someone else.
func TestDeletingAReceivedEventSendsNothing(t *testing.T) {
	e := newImipEnv(t)
	e.answer(t, e.invite(t, icsOpts{}), "ACCEPTED")
	e.fs.reset()
	guid := uidGuid(imipUID)

	r := chi.NewRouter()
	r.Delete("/calendar/event/{guid}", e.cal.DeleteEvent)
	req := withUserContext(httptest.NewRequest(http.MethodDelete, "/calendar/event/"+guid, nil), quotaUser, quotaEmail)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code >= 400 {
		t.Fatalf("delete status %d: %s", w.Code, w.Body)
	}
	e.noEvent(t)
	if n := len(e.fs.messages()); n != 0 {
		t.Errorf("deleting a received event sent %d messages", n)
	}
}

func TestCancelRefusals(t *testing.T) {
	setup := func(t *testing.T) *imipEnv {
		e := newImipEnv(t)
		e.answer(t, e.invite(t, icsOpts{seq: 2}), "ACCEPTED")
		e.fs.reset()
		return e
	}
	t.Run("wrong organiser", func(t *testing.T) {
		e := setup(t)
		uid := e.cancelMsg(t, icsOpts{seq: 3, organiser: "mallory@example.org", status: "CANCELLED"})
		if strings.Contains(e.view(t, uid), `value="cancel"`) {
			t.Error("cancel offered for a different organiser")
		}
		if w := e.act(uid, "cancel"); w.Code != http.StatusConflict {
			t.Errorf("status %d, want 409", w.Code)
		}
		if e.stored(t, imipUID).Status == "CANCELLED" {
			t.Error("a cancellation from another organiser was applied")
		}
	})
	t.Run("lower sequence", func(t *testing.T) {
		e := setup(t)
		uid := e.cancelMsg(t, icsOpts{seq: 1, status: "CANCELLED"})
		if w := e.act(uid, "cancel"); w.Code != http.StatusConflict {
			t.Errorf("status %d, want 409", w.Code)
		}
		if e.stored(t, imipUID).Status == "CANCELLED" {
			t.Error("an outdated cancellation was applied")
		}
	})
	t.Run("equal sequence applies", func(t *testing.T) {
		e := setup(t)
		uid := e.cancelMsg(t, icsOpts{seq: 2, status: "CANCELLED"})
		if w := e.act(uid, "cancel"); w.Code != http.StatusSeeOther {
			t.Errorf("status %d, want 303", w.Code)
		}
	})
	t.Run("unknown event", func(t *testing.T) {
		e := newImipEnv(t)
		uid := e.cancelMsg(t, icsOpts{uid: "never-seen@example.org", status: "CANCELLED"})
		if strings.Contains(e.view(t, uid), `value="cancel"`) {
			t.Error("cancel offered for an event the user does not have")
		}
		if w := e.act(uid, "cancel"); w.Code != http.StatusConflict {
			t.Errorf("status %d, want 409", w.Code)
		}
		e.noEvent(t)
	})
	t.Run("remove needs a cancelled event", func(t *testing.T) {
		e := setup(t)
		uid := e.cancelMsg(t, icsOpts{seq: 3, status: "CANCELLED"})
		if w := e.act(uid, "remove"); w.Code != http.StatusConflict {
			t.Errorf("status %d, want 409", w.Code)
		}
		if e.q.countEvents(t) != 1 {
			t.Error("an event that is not cancelled was removed")
		}
	})
}

func TestUpdateRefusedFromAnotherOrganiser(t *testing.T) {
	e := newImipEnv(t)
	e.answer(t, e.invite(t, icsOpts{}), "ACCEPTED")
	uid := e.invite(t, icsOpts{seq: 5, organiser: "mallory@example.org", summary: "Hijacked", start: "20300601T090000Z", end: "20300601T100000Z"})
	body := e.view(t, uid)
	if strings.Contains(body, `name="partstat"`) || strings.Contains(body, `value="update"`) {
		t.Error("buttons offered for a request from another organiser")
	}
	for _, form := range []url.Values{{"action": {"update"}}, {"action": {"answer"}, "partstat": {"ACCEPTED"}}} {
		if w := e.post(quotaUser, quotaEmail, uid, form, true); w.Code != http.StatusConflict {
			t.Errorf("%v = %d, want 409", form, w.Code)
		}
	}
	if ev := e.stored(t, imipUID); ev.Title != "Planning" || ev.Sequence != 0 {
		t.Errorf("the event was changed by another organiser: %q seq %d", ev.Title, ev.Sequence)
	}
}

// A forged invitation naming the user as organiser, with attendees of the
// attacker's choosing, must not become an event whose later edits mail those
// attendees as the user.
func TestSelfOrganisedInvitationIsReadOnly(t *testing.T) {
	e := newImipEnv(t)
	uid := e.invite(t, icsOpts{organiser: quotaEmail})
	body := e.view(t, uid)
	if strings.Contains(body, `name="partstat"`) || !strings.Contains(body, "You are the organiser") {
		t.Error("buttons offered on an invitation that names the user as organiser")
	}
	if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusConflict {
		t.Errorf("status %d, want 409", w.Code)
	}
	e.noEvent(t)
	if n := len(e.fs.messages()); n != 0 {
		t.Errorf("%d messages sent", n)
	}
	// An alias the user owns counts too.
	h := e.cal
	if !h.myAddresses(quotaUser, quotaEmail)[quotaEmail] {
		t.Fatal("session address is not one of the user's addresses")
	}
	uid = e.cancelMsg(t, icsOpts{organiser: strings.ToUpper(quotaEmail), status: "CANCELLED"})
	if w := e.act(uid, "cancel"); w.Code != http.StatusConflict {
		t.Errorf("cancel from a self-organised message = %d, want 409", w.Code)
	}
}

func TestNotInvitedIsReadOnly(t *testing.T) {
	e := newImipEnv(t)
	uid := e.invite(t, icsOpts{attendee: "someone-else@example.org"})
	if body := e.view(t, uid); strings.Contains(body, `name="partstat"`) || !strings.Contains(body, "None of your addresses") {
		t.Error("answers offered although no attendee is the user")
	}
	if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusConflict {
		t.Errorf("status %d, want 409", w.Code)
	}
	e.noEvent(t)
}

func TestMalformedOrOversizedPartsAreOrdinaryAttachments(t *testing.T) {
	big := icsOpts{description: strings.Repeat("a", imip.MaxPartBytes)}.build("REQUEST")
	attach := func(ics string) string {
		return "From: a@example.org\r\nTo: " + quotaEmail + "\r\nSubject: x\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=BB\r\n\r\n" +
			"--BB\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
			"--BB\r\nContent-Type: text/calendar; method=REQUEST\r\nContent-Disposition: attachment; filename=invite.ics\r\n\r\n" + ics + "\r\n--BB--\r\n"
	}
	tests := []struct{ name, raw string }{
		{"garbage", attach("not a calendar at all")},
		{"oversize", attach(big)},
		{"method mismatch", inviteMail("CANCEL", icsOpts{}.build("REQUEST"), "")},
		{"two uids", inviteMail("REQUEST", strings.Replace(icsOpts{}.build("REQUEST"), "END:VEVENT", "END:VEVENT\r\nBEGIN:VEVENT\r\nUID:other\r\nDTSTART:20300506T090000Z\r\nEND:VEVENT", 1), "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newImipEnv(t)
			uid := e.deliver(t, quotaUser, tt.raw)
			body := e.view(t, uid)
			if strings.Contains(body, "invite-card") {
				t.Error("a card was built from a part that must be refused")
			}
			if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusUnprocessableEntity {
				t.Errorf("status %d, want 422", w.Code)
			}
			e.noEvent(t)
		})
	}
	t.Run("attachment is listed", func(t *testing.T) {
		e := newImipEnv(t)
		uid := e.deliver(t, quotaUser, attach("not a calendar at all"))
		if !strings.Contains(e.view(t, uid), "invite.ics") {
			t.Error("the refused part is not listed as an attachment")
		}
	})
}

func TestMessageWithoutCalendarHasNoInvitationRoute(t *testing.T) {
	e := newImipEnv(t)
	uid := e.deliver(t, quotaUser, "From: a@example.org\r\nTo: x\r\nSubject: hi\r\n\r\nhello\r\n")
	if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
}

func TestAnswerAuthorisation(t *testing.T) {
	t.Run("csrf", func(t *testing.T) {
		e := newImipEnv(t)
		uid := e.invite(t, icsOpts{})
		w := e.post(quotaUser, quotaEmail, uid, url.Values{"action": {"answer"}, "partstat": {"ACCEPTED"}}, false)
		if w.Code != http.StatusForbidden || !strings.Contains(strings.ToLower(w.Body.String()), "csrf") {
			t.Errorf("without a token: %d %q", w.Code, w.Body)
		}
		e.noEvent(t)
		if len(e.fs.messages()) != 0 {
			t.Error("a request without a token sent mail")
		}
	})
	t.Run("another user's message", func(t *testing.T) {
		e := newImipEnv(t)
		uid := e.invite(t, icsOpts{})
		// The other user has no message with that UID; they must get 404 for
		// the quota user's, and the quota user's mailbox must stay untouched.
		w := e.post(otherUser, otherEmail, uid, url.Values{"action": {"answer"}, "partstat": {"ACCEPTED"}}, true)
		if w.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404", w.Code)
		}
		e.noEvent(t)
		if _, n, _ := e.q.events.ListAll(otherUser, 0, 0); n != 0 {
			t.Error("an event was created for the other user")
		}
		if len(e.fs.messages()) != 0 {
			t.Error("mail was sent")
		}
	})
	t.Run("another user's calendar", func(t *testing.T) {
		e := newImipEnv(t)
		theirs, err := e.q.calendars.Create(otherUser, &bongov1.Calendar{Name: "Theirs"})
		if err != nil {
			t.Fatal(err)
		}
		uid := e.invite(t, icsOpts{})
		form := url.Values{"action": {"answer"}, "partstat": {"ACCEPTED"}, "calendar_id": {theirs}}
		if w := e.post(quotaUser, quotaEmail, uid, form, true); w.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404", w.Code)
		}
		e.noEvent(t)
		if _, n, _ := e.q.events.ListAll(otherUser, 0, 0); n != 0 {
			t.Error("an event was written to the other user's calendar")
		}
		if len(e.fs.messages()) != 0 {
			t.Error("mail was sent")
		}
	})
	t.Run("partstat whitelist", func(t *testing.T) {
		e := newImipEnv(t)
		uid := e.invite(t, icsOpts{})
		for _, ps := range []string{"", "NEEDS-ACTION", "accepted", "DELEGATED", "ACCEPTED\r\nX:y"} {
			if w := e.answer(t, uid, ps); w.Code != http.StatusBadRequest {
				t.Errorf("partstat %q = %d, want 400", ps, w.Code)
			}
		}
		e.noEvent(t)
	})
	t.Run("unknown action", func(t *testing.T) {
		e := newImipEnv(t)
		uid := e.invite(t, icsOpts{})
		if w := e.act(uid, "delete-everything"); w.Code != http.StatusBadRequest {
			t.Errorf("status %d, want 400", w.Code)
		}
	})
	t.Run("form data cannot supply the event", func(t *testing.T) {
		e := newImipEnv(t)
		uid := e.invite(t, icsOpts{})
		form := url.Values{"action": {"answer"}, "partstat": {"ACCEPTED"}, "title": {"Forged"}, "uid": {"forged"}, "organizer": {"x@example.org"}}
		e.post(quotaUser, quotaEmail, uid, form, true)
		if ev := e.stored(t, imipUID); ev.Title != "Planning" || ev.Organizer != imipOrganiser {
			t.Errorf("form fields reached the event: %+v", ev)
		}
	})
}

func TestAnswerWriteFailureSendsNoReply(t *testing.T) {
	e := newImipEnv(t)
	uid := e.invite(t, icsOpts{})
	// Calendars and events live on a store whose writes all fail.
	failing := NewCalendarHandler(store.NewEventStore(e.q.ps, failingApplier{}), e.q.calendars, nil)
	failing.SetInviteSender(e.fs)
	e.mail.SetInvitations(failing)
	w := e.answer(t, uid, "ACCEPTED")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", w.Code, w.Body)
	}
	e.noEvent(t)
	if n := len(e.fs.messages()); n != 0 {
		t.Errorf("a failed write still sent %d messages", n)
	}
}

func TestReplyFailureKeepsEventAndWarns(t *testing.T) {
	e := newImipEnv(t)
	e.fs.err = fmt.Errorf("relay down")
	uid := e.invite(t, icsOpts{})
	w := e.answer(t, uid, "ACCEPTED")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != fmt.Sprintf("/mail/INBOX/%d?ir=failed", uid) {
		t.Fatalf("status %d location %q", w.Code, w.Header().Get("Location"))
	}
	if partstatOf(e.stored(t, imipUID)) != "ACCEPTED" {
		t.Error("the event was not kept")
	}
	w = httptest.NewRecorder()
	e.router(quotaUser, quotaEmail).ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d?pane=1&ir=failed", uid), nil))
	if !strings.Contains(w.Body.String(), "the reply to the organiser could not be sent") {
		t.Error("no warning after a failed reply")
	}
}

func TestReplyUsesTheInvitedAddressOnlyWhenItMaySendAs(t *testing.T) {
	for _, tc := range []struct {
		name         string
		canSend      bool
		wantReply    bool
		wantLocation string
	}{
		{"send-as alias", true, true, "%d"},
		{"receive-only alias", false, false, "%d?ir=failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newImipEnv(t)
			md, err := store.NewMetadataManager(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { md.Close() })
			as := store.NewAddressStore(e.q.ps, &directApplierTags{ps: e.q.ps}, md.System)
			if err := as.Put("alias@example.com", &bongov1.AddressRecord{UserId: quotaUser, CanSend: tc.canSend, Verified: tc.canSend}); err != nil {
				t.Fatal(err)
			}
			e.cal.SetAddressStore(as)

			uid := e.invite(t, icsOpts{attendee: "alias@example.com"})
			w := e.answer(t, uid, "ACCEPTED")
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if got, want := w.Header().Get("Location"), "/mail/INBOX/"+fmt.Sprintf(tc.wantLocation, uid); got != want {
				t.Errorf("location %q, want %q", got, want)
			}
			if ev := e.stored(t, imipUID); myPartstat(ev, map[string]bool{"alias@example.com": true}) != "ACCEPTED" {
				t.Error("the event was not stored with the answer")
			}
			msgs := e.fs.messages()
			if tc.wantReply != (len(msgs) == 1) {
				t.Fatalf("messages = %d, want reply %v", len(msgs), tc.wantReply)
			}
			if tc.wantReply && msgs[0].from != "alias@example.com" {
				t.Errorf("reply sent from %q", msgs[0].from)
			}
		})
	}
}

func TestAnswerQuotaRefused(t *testing.T) {
	e := newImipEnv(t)
	e.cal.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{quotaUser: 1}})
	uid := e.invite(t, icsOpts{})
	if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusInsufficientStorage {
		t.Errorf("status %d, want 507", w.Code)
	}
	e.noEvent(t)
	if len(e.fs.messages()) != 0 {
		t.Error("a refused write sent mail")
	}
}

func TestInstanceMessages(t *testing.T) {
	e := newImipEnv(t)
	series := icsOpts{uid: "weekly@example.org", rrule: "FREQ=WEEKLY;COUNT=4"}
	e.answer(t, e.invite(t, series), "ACCEPTED")
	e.fs.reset()
	rid := "20300513T090000Z"

	t.Run("override is added with the answer", func(t *testing.T) {
		uid := e.invite(t, icsOpts{uid: "weekly@example.org", rid: rid, summary: "Moved week", start: "20300513T130000Z", end: "20300513T140000Z"})
		if w := e.answer(t, uid, "DECLINED"); w.Code != http.StatusSeeOther {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		ev := e.stored(t, "weekly@example.org")
		if ev.Rrule == "" || strings.Count(string(ev.IcalRaw), "BEGIN:VEVENT") != 2 || !strings.Contains(string(ev.IcalRaw), "RECURRENCE-ID:"+rid) {
			t.Errorf("series not merged:\n%s", ev.IcalRaw)
		}
		if partstatOf(ev) != "ACCEPTED" {
			t.Error("answering one instance changed the series answer")
		}
		m := e.fs.messages()
		if len(m) != 1 || m[0].method != "REPLY" || m[0].vevent(t).Props.Get(ical.PropRecurrenceID) == nil {
			t.Errorf("reply for an instance = %+v", m)
		}
	})
	t.Run("instance cancel becomes a cancelled override", func(t *testing.T) {
		uid := e.cancelMsg(t, icsOpts{uid: "weekly@example.org", rid: "20300520T090000Z", status: "CANCELLED"})
		if w := e.act(uid, "cancel"); w.Code != http.StatusSeeOther {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		ev := e.stored(t, "weekly@example.org")
		if ev.Status == "CANCELLED" {
			t.Error("cancelling one instance cancelled the series")
		}
		raw := string(ev.IcalRaw)
		if !strings.Contains(raw, "RECURRENCE-ID:20300520T090000Z") || strings.Count(raw, "STATUS:CANCELLED") != 1 {
			t.Errorf("no cancelled override:\n%s", raw)
		}
	})
	t.Run("an instance that is not in the series is refused", func(t *testing.T) {
		uid := e.invite(t, icsOpts{uid: "weekly@example.org", rid: "20300514T090000Z"})
		if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusConflict {
			t.Errorf("status %d, want 409", w.Code)
		}
	})
	t.Run("an unknown instance is stored as its own event", func(t *testing.T) {
		uid := e.invite(t, icsOpts{uid: "lonely@example.org", rid: rid})
		if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusSeeOther {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if ev := e.stored(t, "lonely@example.org"); !strings.Contains(string(ev.IcalRaw), "RECURRENCE-ID") {
			t.Errorf("raw:\n%s", ev.IcalRaw)
		}
	})
}

func TestUIDIsNeverAKey(t *testing.T) {
	e := newImipEnv(t)
	hostile := "../../x/y@example.org"
	uid := e.invite(t, icsOpts{uid: hostile})
	if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := e.stored(t, hostile).Guid; got != uidGuid(hostile) || strings.ContainsAny(got, "/.") {
		t.Errorf("guid = %q", got)
	}
}

func TestExistingEventNamedByItsUIDIsKnown(t *testing.T) {
	e := newImipEnv(t)
	calID, _ := e.q.calendars.EnsureDefault(quotaUser)
	seed := &bongov1.Event{
		Guid: "named-by-uid", CalendarId: calID, Title: "Already here", Organizer: imipOrganiser,
		StartTime: time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC).Unix(), EndTime: time.Date(2030, 5, 6, 10, 0, 0, 0, time.UTC).Unix(),
		Attendees: []*bongov1.Attendee{{Address: quotaEmail, Partstat: "ACCEPTED"}},
	}
	if _, err := e.q.events.Create(quotaUser, seed); err != nil {
		t.Fatal(err)
	}
	uid := e.invite(t, icsOpts{uid: "named-by-uid"})
	body := e.view(t, uid)
	if !strings.Contains(body, "You accepted this invitation") {
		t.Error("an event named by its UID is not recognised as known")
	}
	e.answer(t, uid, "DECLINED")
	if n := e.q.countEvents(t); n != 1 {
		t.Errorf("%d events, want the existing one updated in place", n)
	}
}

func TestCleanDisplay(t *testing.T) {
	tests := []struct {
		name, in string
		max      int
		lines    bool
		want     string
	}{
		{"control characters", "a\x00b\x1bc\u0085d", 50, false, "abcd"},
		{"newline flattened", "a\nb", 50, false, "ab"},
		{"newline kept", "a\r\nb", 50, true, "a\nb"},
		{"bidi override", "evil\u202egnp.exe", 50, false, "evilgnp.exe"},
		{"isolates and marks", "a\u2066b\u200fc\u200bd", 50, false, "abcd"},
		{"truncated on runes", strings.Repeat("é", 10), 4, false, "ééé…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanDisplay(tt.in, tt.max, tt.lines); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The wiring the agent performs: without a service the message view shows no
// card and the answer route refuses.
func TestInvitationsNotWired(t *testing.T) {
	e := newImipEnv(t)
	uid := e.invite(t, icsOpts{})
	e.mail.SetInvitations(nil)
	if strings.Contains(e.view(t, uid), "invite-card") {
		t.Error("a card without a wired service")
	}
	if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
}

var _ = context.Background

// countingAddresses counts address lookups.
type countingAddresses struct {
	calls int
	list  []string
}

func (c *countingAddresses) ListByUser(string) ([]string, error) {
	c.calls++
	return c.list, nil
}

func TestDeclinedSingleOccurrenceIsHiddenAndNotBusy(t *testing.T) {
	e := newImipEnv(t)
	series := icsOpts{uid: "weekly@example.org", rrule: "FREQ=WEEKLY;COUNT=4"}
	e.answer(t, e.invite(t, series), "ACCEPTED")
	before := strings.Count(renderMonth(t, e.cal, "en", ""), "Planning")
	if before == 0 || before%4 != 0 {
		t.Fatalf("series shows %d times in the month, want a multiple of its 4 occurrences", before)
	}
	per := before / 4
	rid := "20300513T090000Z"
	e.answer(t, e.invite(t, icsOpts{uid: "weekly@example.org", rid: rid}), "DECLINED")

	if after := strings.Count(renderMonth(t, e.cal, "en", ""), "Planning"); after != before-per {
		t.Errorf("month shows Planning %d times after declining one occurrence, want %d", after, before-per)
	}
	busy := func(day int) string {
		start := time.Date(2030, 5, day, 8, 0, 0, 0, time.UTC).Unix()
		w := httptest.NewRecorder()
		e.cal.FreeBusy(w, withUserContext(httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/calendar/freebusy?user=%s&start=%d&end=%d", quotaUser, start, start+7200), nil), quotaUser, quotaEmail))
		return strings.TrimSpace(w.Body.String())
	}
	if got := busy(13); got != "null" && got != "[]" {
		t.Errorf("the declined occurrence makes the user busy: %s", got)
	}
	if got := busy(20); got == "null" || got == "[]" {
		t.Errorf("the rest of the series is no longer busy: %s", got)
	}
}

func TestFreeBusySkipsAddressLookupWithoutDecline(t *testing.T) {
	e := newImipEnv(t)
	e.answer(t, e.invite(t, icsOpts{}), "ACCEPTED")
	fake := &countingAddresses{}
	e.cal.owned = fake
	list := func() {
		w := httptest.NewRecorder()
		e.cal.FreeBusy(w, withUserContext(httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/calendar/freebusy?user=%s&start=1900000000&end=1910000000", quotaUser), nil), quotaUser, quotaEmail))
	}
	list()
	if fake.calls != 0 {
		t.Errorf("%d address lookups with no declined attendee, want 0", fake.calls)
	}
	e.answer(t, e.invite(t, icsOpts{seq: 1}), "DECLINED")
	fake.calls = 0
	list()
	if fake.calls == 0 {
		t.Error("no address lookup although an attendee declined")
	}
}

func TestAnswerKnownEventIgnoresCalendarID(t *testing.T) {
	e := newImipEnv(t)
	uid := e.invite(t, icsOpts{})
	e.answer(t, uid, "ACCEPTED")
	form := url.Values{"action": {"answer"}, "partstat": {"DECLINED"}, "calendar_id": {"does-not-exist"}}
	if w := e.post(quotaUser, quotaEmail, uid, form, true); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := partstatOf(e.stored(t, imipUID)); got != "DECLINED" {
		t.Errorf("partstat = %q, want DECLINED", got)
	}
}
