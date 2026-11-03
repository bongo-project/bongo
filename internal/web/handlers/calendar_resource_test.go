package handlers

import (
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	resID    = "res-1"
	resMail  = "room@example.org"
	aliceID  = "alice-1"
	aliceAdr = "alice@example.org"
)

type resourceEnv struct {
	*replyEnv
	as *store.AddressStore
}

// newResourceEnv builds a room (room@example.org), a person on its domain
// (alice@example.org) and a person on another domain (carl@other.org).
func newResourceEnv(t *testing.T) *resourceEnv {
	t.Helper()
	e := &resourceEnv{replyEnv: newReplyEnv(t)}
	applier := &directApplierTags{ps: e.q.ps}
	e.as = store.NewAddressStore(e.q.ps, applier, nil)
	e.h.SetInviteSender(e.fs)
	e.h.SetZoneSource(fixedZone{time.UTC})
	e.h.SetProfiles(e.q.ps)
	e.h.SetAddressStore(e.as)
	e.putAccount(t, resID, resMail, "resource")
	e.putAccount(t, aliceID, aliceAdr, "")
	e.putAccount(t, "carl-1", "carl@other.org", "")
	return e
}

func (e *resourceEnv) putAccount(t *testing.T, id, email, kind string) {
	t.Helper()
	p := &bongov1.UserProfile{Email: email, DisplayName: "Room One", Kind: kind}
	if kind == "resource" {
		p.ResourceType, p.Capacity = "room", 8
	}
	data, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&directApplierTags{ps: e.q.ps}).ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/user/" + id + "/profile"), Value: data,
	}, time.Second); err != nil {
		t.Fatal(err)
	}
	dom := email[strings.Index(email, "@")+1:]
	if err := e.as.Put(email, &bongov1.AddressRecord{UserId: id, Domain: dom, Kind: "primary", CanSend: true, Verified: true}); err != nil {
		t.Fatal(err)
	}
}

type bookOpts struct {
	uid, organiser, rrule, method string
	start, end                    string // 20300506T090000Z
	attendee                      string
	exdate                        string   // 20300513T090000Z
	overrides                     []string // extra VEVENT blocks, each a slice of lines joined by \n
}

func (o bookOpts) ics() string {
	if o.uid == "" {
		o.uid = "meet-1@example.org"
	}
	if o.organiser == "" {
		o.organiser = aliceAdr
	}
	if o.method == "" {
		o.method = "REQUEST"
	}
	if o.start == "" {
		o.start, o.end = "20300506T090000Z", "20300506T100000Z"
	}
	if o.attendee == "" {
		o.attendee = resMail
	}
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//EN", "METHOD:" + o.method, "BEGIN:VEVENT",
		"UID:" + o.uid, "DTSTAMP:20300101T000000Z", "DTSTART:" + o.start, "DTEND:" + o.end,
		"SUMMARY:Planning", "SEQUENCE:0", "ORGANIZER;CN=Org:mailto:" + o.organiser,
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:" + o.attendee,
	}
	if o.rrule != "" {
		lines = append(lines, "RRULE:"+o.rrule)
	}
	if o.exdate != "" {
		lines = append(lines, "EXDATE:"+o.exdate)
	}
	lines = append(lines, "END:VEVENT")
	for _, ov := range o.overrides {
		lines = append(lines, strings.Split(ov, "\n")...)
	}
	lines = append(lines, "END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n"
}

// book delivers the request to the resource as the delivery stage would.
func (e *resourceEnv) book(userID, sender string, o bookOpts) {
	m := o.method
	if m == "" {
		m = "REQUEST"
	}
	e.h.AnswerBooking(userID, sender, []byte(o.ics()), m)
}

func (e *resourceEnv) count(t *testing.T) int {
	t.Helper()
	_, n, err := e.events.ListAll(resID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// answers returns the PARTSTATs of the replies sent, in order.
func (e *resourceEnv) answers(t *testing.T) []string {
	t.Helper()
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	var out []string
	for _, s := range e.fs.sent {
		if s.method != "REPLY" || s.cal == nil {
			continue
		}
		for _, ve := range s.cal.Events() {
			for _, a := range ve.Props.Values(ical.PropAttendee) {
				out = append(out, a.Params.Get(ical.ParamParticipationStatus))
			}
		}
	}
	return out
}

func TestAnswerBookingDecisions(t *testing.T) {
	const (
		slotA = "20300506T090000Z"
		endA  = "20300506T100000Z"
	)
	tests := []struct {
		name string
		// first, when set, is booked by alice before the case runs.
		first      *bookOpts
		sender     string
		opts       bookOpts
		to         string // recipient user, default the resource
		wantAnswer []string
		wantEvents int
	}{
		{name: "free slot is accepted", sender: aliceAdr, opts: bookOpts{}, wantAnswer: []string{"ACCEPTED"}, wantEvents: 1},
		{name: "overlap is declined", first: &bookOpts{uid: "first@example.org"}, sender: aliceAdr,
			opts: bookOpts{uid: "second@example.org", start: "20300506T093000Z", end: "20300506T103000Z"}, wantAnswer: []string{"ACCEPTED", "DECLINED"}, wantEvents: 1},
		{name: "adjacent slot after is accepted", first: &bookOpts{uid: "first@example.org"}, sender: aliceAdr,
			opts: bookOpts{uid: "second@example.org", start: endA, end: "20300506T110000Z"}, wantAnswer: []string{"ACCEPTED", "ACCEPTED"}, wantEvents: 2},
		{name: "adjacent slot before is accepted", first: &bookOpts{uid: "first@example.org"}, sender: aliceAdr,
			opts: bookOpts{uid: "second@example.org", start: "20300506T080000Z", end: slotA}, wantAnswer: []string{"ACCEPTED", "ACCEPTED"}, wantEvents: 2},
		{name: "same uid moves its own booking", first: &bookOpts{uid: "first@example.org"}, sender: aliceAdr,
			opts: bookOpts{uid: "first@example.org", start: "20300506T093000Z", end: "20300506T103000Z"}, wantAnswer: []string{"ACCEPTED", "ACCEPTED"}, wantEvents: 1},
		{name: "declined update releases the old booking", first: &bookOpts{uid: "first@example.org"}, sender: aliceAdr,
			opts: bookOpts{uid: "first@example.org", start: "20300506T093000Z", end: "20300506T103000Z", attendee: resMail, rrule: "FREQ=DAILY"}, wantAnswer: []string{"ACCEPTED", "DECLINED"}, wantEvents: 0},
		{name: "recurring series colliding", first: &bookOpts{uid: "first@example.org", start: "20300520T090000Z", end: "20300520T100000Z"}, sender: aliceAdr,
			opts: bookOpts{uid: "series@example.org", rrule: "FREQ=WEEKLY;COUNT=4"}, wantAnswer: []string{"ACCEPTED", "DECLINED"}, wantEvents: 1},
		{name: "recurring series free", first: &bookOpts{uid: "first@example.org", start: "20300521T090000Z", end: "20300521T100000Z"}, sender: aliceAdr,
			opts: bookOpts{uid: "series@example.org", rrule: "FREQ=WEEKLY;COUNT=4"}, wantAnswer: []string{"ACCEPTED", "ACCEPTED"}, wantEvents: 2},
		{name: "recurring series touching a booking is accepted", first: &bookOpts{uid: "first@example.org", start: "20300513T100000Z", end: "20300513T110000Z"}, sender: aliceAdr,
			opts: bookOpts{uid: "series@example.org", rrule: "FREQ=WEEKLY;COUNT=2"}, wantAnswer: []string{"ACCEPTED", "ACCEPTED"}, wantEvents: 2},
		{name: "unbounded series cannot be checked", sender: aliceAdr,
			opts: bookOpts{rrule: "FREQ=WEEKLY"}, wantAnswer: []string{"DECLINED"}, wantEvents: 0},
		{name: "series running past the horizon", sender: aliceAdr,
			opts: bookOpts{rrule: "FREQ=YEARLY;COUNT=20"}, wantAnswer: []string{"DECLINED"}, wantEvents: 0},
		{name: "verified sender on another domain is declined", sender: "carl@other.org",
			opts: bookOpts{organiser: "carl@other.org"}, wantAnswer: []string{"DECLINED"}, wantEvents: 0},
		{name: "same domain without an account is declined", sender: "eve@example.org",
			opts: bookOpts{organiser: "eve@example.org"}, wantAnswer: []string{"DECLINED"}, wantEvents: 0},
		{name: "unverified sender gets no answer", sender: "",
			opts: bookOpts{}, wantAnswer: nil, wantEvents: 0},
		{name: "organiser differs from the verified sender gets no answer", sender: "carl@other.org",
			opts: bookOpts{organiser: aliceAdr}, wantAnswer: nil, wantEvents: 0},
		{name: "resource not an attendee", sender: aliceAdr,
			opts: bookOpts{attendee: "someone@example.org"}, wantAnswer: nil, wantEvents: 0},
		{name: "recipient is a person", sender: "carl@other.org", to: aliceID,
			opts: bookOpts{organiser: "carl@other.org", attendee: aliceAdr}, wantAnswer: nil, wantEvents: 0},
		{name: "another organiser cannot replace a booking", first: &bookOpts{uid: "first@example.org"}, sender: "carl@other.org",
			opts: bookOpts{uid: "first@example.org", organiser: "carl@other.org"}, wantAnswer: []string{"ACCEPTED", "DECLINED"}, wantEvents: 1},
		{name: "a request from the resource itself", sender: resMail,
			opts: bookOpts{organiser: resMail}, wantAnswer: nil, wantEvents: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newResourceEnv(t)
			if tt.first != nil {
				e.book(resID, aliceAdr, *tt.first)
			}
			to := tt.to
			if to == "" {
				to = resID
			}
			e.book(to, tt.sender, tt.opts)

			got := e.answers(t)
			if fmt.Sprint(got) != fmt.Sprint(tt.wantAnswer) {
				t.Errorf("answers = %v, want %v", got, tt.wantAnswer)
			}
			if n := e.count(t); n != tt.wantEvents {
				t.Errorf("bookings = %d, want %d", n, tt.wantEvents)
			}
		})
	}
}

func TestAnswerBookingStoresTheBookingAndRepliesAsTheResource(t *testing.T) {
	e := newResourceEnv(t)
	e.book(resID, aliceAdr, bookOpts{})

	ev, err := e.events.Get(resID, uidGuid("meet-1@example.org"))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Organizer != aliceAdr || len(ev.Attendees) != 1 || ev.Attendees[0].Partstat != "ACCEPTED" {
		t.Errorf("stored booking = %v", ev)
	}
	if ev.CalendarId == "" || !strings.Contains(string(ev.IcalRaw), "PARTSTAT=ACCEPTED") || strings.Contains(string(ev.IcalRaw), "METHOD") {
		t.Errorf("stored object = %q (calendar %q)", ev.IcalRaw, ev.CalendarId)
	}
	if len(e.fs.sent) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(e.fs.sent))
	}
	s := e.fs.sent[0]
	if s.userID != resID || s.from != resMail || len(s.to) != 1 || s.to[0] != aliceAdr || s.method != "REPLY" {
		t.Errorf("sent = %+v", s)
	}
}

// A retried delivery repeats the call; the booking is replaced, not doubled,
// and does not collide with itself.
func TestAnswerBookingIsIdempotentPerUID(t *testing.T) {
	e := newResourceEnv(t)
	for range 3 {
		e.book(resID, aliceAdr, bookOpts{})
	}
	if n := e.count(t); n != 1 {
		t.Errorf("bookings = %d, want 1", n)
	}
	if got := e.answers(t); fmt.Sprint(got) != "[ACCEPTED ACCEPTED ACCEPTED]" {
		t.Errorf("answers = %v", got)
	}
}

func TestAnswerBookingConcurrentRequestsBookOnce(t *testing.T) {
	e := newResourceEnv(t)
	const n = 12
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.book(resID, aliceAdr, bookOpts{uid: fmt.Sprintf("race-%d@example.org", i)})
		}()
	}
	wg.Wait()
	accepted, declined := 0, 0
	for _, a := range e.answers(t) {
		switch a {
		case "ACCEPTED":
			accepted++
		case "DECLINED":
			declined++
		}
	}
	if accepted != 1 || declined != n-1 || e.count(t) != 1 {
		t.Errorf("accepted %d, declined %d, bookings %d; want 1, %d, 1", accepted, declined, e.count(t), n-1)
	}
}

func TestAnswerBookingCancelReleasesTheSlot(t *testing.T) {
	e := newResourceEnv(t)
	e.book(resID, aliceAdr, bookOpts{})
	if e.count(t) != 1 {
		t.Fatal("no booking")
	}
	// Someone else cannot release it, even naming the same UID.
	e.book(resID, "carl@other.org", bookOpts{method: "CANCEL", organiser: "carl@other.org"})
	if e.count(t) != 1 {
		t.Fatal("a stranger's CANCEL released the booking")
	}
	e.book(resID, aliceAdr, bookOpts{method: "CANCEL"})
	if e.count(t) != 0 {
		t.Fatal("the organiser's CANCEL did not release the booking")
	}
	e.book(resID, aliceAdr, bookOpts{uid: "next@example.org"})
	if got := e.answers(t); fmt.Sprint(got) != "[ACCEPTED ACCEPTED]" {
		t.Errorf("answers = %v", got)
	}
}

func TestAnswerBookingCancelledOrTransparentDoNotHold(t *testing.T) {
	e := newResourceEnv(t)
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC).Unix()
	cal, err := e.q.calendars.EnsureDefault(resID)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range []*bongov1.Event{
		{Guid: "c1", CalendarId: cal, Title: "x", StartTime: start, EndTime: start + 3600, Status: "CANCELLED"},
		{Guid: "c2", CalendarId: cal, Title: "x", StartTime: start, EndTime: start + 3600,
			IcalRaw: []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:c2\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\nTRANSP:TRANSPARENT\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")},
	} {
		if _, err := e.events.Create(resID, ev); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
	e.book(resID, aliceAdr, bookOpts{})
	if got := e.answers(t); fmt.Sprint(got) != "[ACCEPTED]" {
		t.Errorf("answers = %v", got)
	}
}

// With the profile reader unwired nothing answers: resource calendars are off.
func TestAnswerBookingNeedsProfiles(t *testing.T) {
	e := newResourceEnv(t)
	e.h.SetProfiles(nil)
	e.book(resID, aliceAdr, bookOpts{})
	if len(e.answers(t)) != 0 || e.count(t) != 0 {
		t.Error("answered without a profile reader")
	}
}

// The organiser's own reply handling accepts the resource's answer: the
// resource's primary address can be sent as, and the answer is applied.
func TestResourceAnswerIsAppliedByTheOrganiser(t *testing.T) {
	e := newResourceEnv(t)
	// Alice's own copy of the meeting, as the web UI stores it.
	uid := "meet-1@example.org"
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC).Unix()
	own := &bongov1.Event{
		Guid: uid, CalendarId: "cal", Title: "Planning", StartTime: start, EndTime: start + 3600, Organizer: aliceAdr,
		Attendees: []*bongov1.Attendee{{Address: resMail, Partstat: "NEEDS-ACTION", Rsvp: true}},
		IcalRaw:   []byte(bookOpts{}.ics()),
	}
	own.IcalRaw = []byte(strings.Replace(string(own.IcalRaw), "METHOD:REQUEST\r\n", "", 1))
	if _, err := e.events.Create(aliceID, own); err != nil {
		t.Fatal(err)
	}
	e.book(resID, aliceAdr, bookOpts{})

	e.fs.mu.Lock()
	sent := e.fs.sent
	e.fs.mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages", len(sent))
	}
	ok, err := store.CanSendAs(e.as, sent[0].userID, resMail, sent[0].from)
	if err != nil || !ok {
		t.Fatalf("CanSendAs(resource, %q) = %v, %v; the organiser would refuse the answer", sent[0].from, ok, err)
	}
	var buf strings.Builder
	if err := ical.NewEncoder(&buf).Encode(sent[0].cal); err != nil {
		t.Fatal(err)
	}
	e.h.owned = ownedAddrs{aliceAdr}
	e.h.ApplyReply(aliceID, resMail, []byte(buf.String()), "REPLY")
	got, err := e.events.Get(aliceID, uid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attendees[0].Partstat != "ACCEPTED" {
		t.Errorf("organiser's attendee = %v, want ACCEPTED", got.Attendees[0])
	}
}

func freeBusyRequest(t *testing.T, h *CalendarHandler, caller, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/calendar/freebusy?user="+target+"&start=0&end=9999999999", nil)
	req = req.WithContext(middleware.WithUserContext(req.Context(), caller, caller+"@x.com"))
	rr := httptest.NewRecorder()
	h.FreeBusy(rr, req)
	return rr
}

func TestFreeBusyOfResources(t *testing.T) {
	e := newResourceEnv(t)
	e.putAccount(t, "alice2", "alice2@other.org", "")
	e.book(resID, aliceAdr, bookOpts{})

	tests := []struct {
		name         string
		caller       string
		target       string
		wantStatus   int
		wantBusyJSON bool
	}{
		{"user on the resource's domain", aliceID, resID, 200, true},
		{"user on another domain", "carl-1", resID, 403, false},
		{"another domain, second user", "alice2", resID, 403, false},
		{"target is a person on the same domain", aliceID, "carl-1", 403, false},
		{"unknown caller", "nobody", resID, 403, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := freeBusyRequest(t, e.h, tt.caller, tt.target)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBusyJSON && !strings.Contains(rec.Body.String(), `"start"`) {
				t.Errorf("body = %s, want a busy block", rec.Body.String())
			}
		})
	}
}

// resourcePrefs gives the resource a stored preferred locale.
type resourcePrefs map[string]string

func (p resourcePrefs) Get(userID string) *bongov1.UserPreferences {
	if l, ok := p[userID]; ok {
		return &bongov1.UserPreferences{Locale: l}
	}
	return nil
}

// Booking answers and cancellation notices speak the resource's stored
// language, and the server default when it has none.
func TestResourceAnswersUseTheResourceLocale(t *testing.T) {
	subjects := func(e *resourceEnv) []string {
		e.fs.mu.Lock()
		defer e.fs.mu.Unlock()
		var out []string
		for _, s := range e.fs.sent {
			out = append(out, s.subject)
		}
		return out
	}
	run := func(prefs localeSource) []string {
		e := newResourceEnv(t)
		e.h.SetLocaleSource(prefs)
		e.book(resID, aliceAdr, bookOpts{})
		if err := e.h.CancelBooking(resID, uidGuid("meet-1@example.org")); err != nil {
			t.Fatal(err)
		}
		return subjects(e)
	}
	english := run(nil)
	german := run(resourcePrefs{resID: "de"})
	if len(english) != 2 || len(german) != 2 {
		t.Fatalf("messages sent: en %v, de %v; want an answer and a notice each", english, german)
	}
	for i := range english {
		if english[i] == "" || german[i] == english[i] {
			t.Errorf("message %d: German subject %q equals the English %q", i, german[i], english[i])
		}
	}
	// A preference of another user does not apply.
	if other := run(resourcePrefs{aliceID: "de"}); other[0] != english[0] || other[1] != english[1] {
		t.Errorf("another user's locale changed the subjects: %v", other)
	}
}

// override builds a RECURRENCE-ID VEVENT of the booking series.
func override(uid, recurrenceID, start, end, extra string) string {
	l := []string{
		"BEGIN:VEVENT", "UID:" + uid, "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:" + recurrenceID,
		"DTSTART:" + start, "DTEND:" + end, "SUMMARY:Moved", "SEQUENCE:0",
		"ORGANIZER;CN=Org:mailto:" + aliceAdr,
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:" + resMail,
	}
	if extra != "" {
		l = append(l, extra)
	}
	l = append(l, "END:VEVENT")
	return strings.Join(l, "\n")
}

// A series is checked as it will happen: an override sits at its own time and
// an excluded occurrence does not exist.
func TestAnswerBookingSeriesOverridesAndExclusions(t *testing.T) {
	const uid = "series-1@example.org"
	weekly := "FREQ=WEEKLY;COUNT=3"
	tests := []struct {
		name string
		opts bookOpts
		want string
	}{
		{"override moved onto a booked slot", bookOpts{
			uid: uid, rrule: weekly, start: "20300506T090000Z", end: "20300506T100000Z",
			overrides: []string{override(uid, "20300513T090000Z", "20300509T133000Z", "20300509T143000Z", "")},
		}, "DECLINED"},
		{"override moved before the series start onto a booked slot", bookOpts{
			// The rule starts on the 13th; its second occurrence (the 20th) is
			// moved back to the 9th, before DTSTART, onto the held slot.
			uid: uid, rrule: weekly, start: "20300513T090000Z", end: "20300513T100000Z",
			overrides: []string{override(uid, "20300520T090000Z", "20300509T133000Z", "20300509T143000Z", "")},
		}, "DECLINED"},
		{"only colliding occurrence excluded", bookOpts{
			uid: uid, rrule: "FREQ=DAILY;COUNT=5", start: "20300507T133000Z", end: "20300507T143000Z",
			exdate: "20300509T133000Z",
		}, "ACCEPTED"},
		{"cancelled override on a booked slot", bookOpts{
			uid: uid, rrule: weekly, start: "20300506T090000Z", end: "20300506T100000Z",
			overrides: []string{override(uid, "20300513T090000Z", "20300509T133000Z", "20300509T143000Z", "STATUS:CANCELLED")},
		}, "ACCEPTED"},
		{"no collision", bookOpts{
			uid: uid, rrule: weekly, start: "20300506T090000Z", end: "20300506T100000Z",
		}, "ACCEPTED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newResourceEnv(t)
			e.book(resID, aliceAdr, bookOpts{uid: "held@example.org", start: "20300509T133000Z", end: "20300509T143000Z"})
			e.book(resID, aliceAdr, tt.opts)
			got := e.answers(t)
			if len(got) != 2 || got[1] != tt.want {
				t.Fatalf("answers = %v, want the series %s", got, tt.want)
			}
			wantCount := 1
			if tt.want == "ACCEPTED" {
				wantCount = 2
			}
			if n := e.count(t); n != wantCount {
				t.Errorf("bookings = %d, want %d", n, wantCount)
			}
		})
	}
}

func TestMayReadFreeBusyOfResourcesOverCalDAV(t *testing.T) {
	e := newResourceEnv(t)
	e.putAccount(t, "alice2", "alice2@other.org", "")
	e.putAccount(t, "dave-1", "dave@example.org", "")
	e.book(resID, aliceAdr, bookOpts{})
	s := NewCalDAVScheduler(e.h, nil, schedEmails{aliceID: aliceAdr})
	tests := []struct {
		name, requester, addr string
		want                  bool
	}{
		{"same domain user", aliceID, resMail, true},
		{"other domain user", "carl-1", resMail, false},
		{"person on another domain", aliceID, "alice2@other.org", false},
		{"person on the same domain who shared nothing", aliceID, "dave@example.org", false},
		{"unknown address", aliceID, "nobody@example.org", false},
		{"empty requester", "", resMail, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			busy, ok := s.FreeBusy(tt.requester, tt.addr, 0, 4000000000)
			if ok != tt.want || (ok && len(busy) != 1) {
				t.Errorf("FreeBusy = %v %v, want ok=%v", busy, ok, tt.want)
			}
		})
	}
}

// ridICS is an iTIP message about one occurrence only: a lone override.
func ridICS(method, uid, organiser, rid, start, end string) string {
	l := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//EN", "METHOD:" + method, "BEGIN:VEVENT",
		"UID:" + uid, "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:" + rid, "DTSTART:" + start, "DTEND:" + end,
		"SUMMARY:Moved", "SEQUENCE:1", "ORGANIZER;CN=Org:mailto:" + organiser,
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:" + resMail, "END:VEVENT", "END:VCALENDAR",
	}
	return strings.Join(l, "\r\n") + "\r\n"
}

// answerRIDs returns the RECURRENCE-ID of each REPLY sent, "" for none.
func (e *resourceEnv) answerRIDs(t *testing.T) []string {
	t.Helper()
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	var out []string
	for _, s := range e.fs.sent {
		if s.method != "REPLY" || s.cal == nil {
			continue
		}
		for _, ve := range s.cal.Events() {
			rid := ""
			if p := ve.Props.Get(ical.PropRecurrenceID); p != nil {
				rid = p.Value
			}
			out = append(out, rid)
		}
	}
	return out
}

// busyStarts lists the start times (UTC) of the events that hold the resource
// in 2030-10 and 2030-11, expanded as free/busy is.
func (e *resourceEnv) busyStarts(t *testing.T) string {
	t.Helper()
	from := time.Date(2030, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(2030, 12, 1, 0, 0, 0, 0, time.UTC).Unix()
	evs, err := e.events.ListByDateRange(resID, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range evs {
		if bookingHolds(ev) {
			out = append(out, time.Unix(ev.StartTime, 0).UTC().Format("20060102T150405Z"))
		}
	}
	sort.Strings(out)
	return fmt.Sprint(out)
}

// bookWeekly books alice's series on Mondays 09:00: 28 October, 4 November and
// 11 November 2030.
func (e *resourceEnv) bookWeekly(t *testing.T) {
	t.Helper()
	if !e.h.AnswerBooking(resID, aliceAdr, []byte(bookOpts{
		uid: "weekly@example.org", rrule: "FREQ=WEEKLY;COUNT=3", start: "20301028T090000Z", end: "20301028T100000Z",
	}.ics()), "REQUEST") {
		t.Fatal("the weekly booking was not answered")
	}
}

const allThree = "[20301028T090000Z 20301104T090000Z 20301111T090000Z]"

func TestAnswerBookingCancelOfOneOccurrenceFreesOnlyThatSlot(t *testing.T) {
	tests := []struct {
		name             string
		uid, sender, rid string
		handled          bool
		busy             string
	}{
		{"organiser cancels the 4th", "weekly@example.org", aliceAdr, "20301104T090000Z", true, "[20301028T090000Z 20301111T090000Z]"},
		{"not an occurrence", "weekly@example.org", aliceAdr, "20301105T090000Z", false, allThree},
		{"unknown series", "other@example.org", aliceAdr, "20301104T090000Z", false, allThree},
		{"someone else's cancel", "weekly@example.org", "carl@other.org", "20301104T090000Z", false, allThree},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newResourceEnv(t)
			e.bookWeekly(t)
			ics := ridICS("CANCEL", tt.uid, tt.sender, tt.rid, tt.rid, "20301104T100000Z")
			if handled := e.h.AnswerBooking(resID, tt.sender, []byte(ics), "CANCEL"); handled != tt.handled {
				t.Errorf("handled = %v, want %v", handled, tt.handled)
			}
			if got := e.busyStarts(t); got != tt.busy {
				t.Errorf("busy = %s, want %s", got, tt.busy)
			}
			if e.count(t) != 1 {
				t.Errorf("bookings = %d, want the one series", e.count(t))
			}
			// A CANCEL sends no reply.
			if got := e.answers(t); fmt.Sprint(got) != "[ACCEPTED]" {
				t.Errorf("answers = %v", got)
			}
		})
	}
}

func TestAnswerBookingMovesOneOccurrence(t *testing.T) {
	tests := []struct {
		name       string
		start, end string
		held       bool // another booking at 2030-11-13 09:00
		want       string
		busy       string
	}{
		{"free slot", "20301113T140000Z", "20301113T150000Z", false, "ACCEPTED",
			"[20301028T090000Z 20301104T090000Z 20301113T140000Z]"},
		{"overlapping its own old slot", "20301111T093000Z", "20301111T103000Z", false, "ACCEPTED",
			"[20301028T090000Z 20301104T090000Z 20301111T093000Z]"},
		{"onto another booking", "20301113T090000Z", "20301113T100000Z", true, "DECLINED",
			// The old slot is released; the other booking stays.
			"[20301028T090000Z 20301104T090000Z 20301113T090000Z]"},
		{"onto another week of the same series", "20301104T093000Z", "20301104T103000Z", false, "DECLINED",
			"[20301028T090000Z 20301104T090000Z]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newResourceEnv(t)
			e.bookWeekly(t)
			if tt.held {
				e.book(resID, aliceAdr, bookOpts{uid: "held@example.org", start: "20301113T090000Z", end: "20301113T100000Z"})
			}
			before := len(e.answers(t))
			ics := ridICS("REQUEST", "weekly@example.org", aliceAdr, "20301111T090000Z", tt.start, tt.end)
			if !e.h.AnswerBooking(resID, aliceAdr, []byte(ics), "REQUEST") {
				t.Fatal("the move was not handled")
			}
			got, rids := e.answers(t)[before:], e.answerRIDs(t)[before:]
			if fmt.Sprint(got) != "["+tt.want+"]" || fmt.Sprint(rids) != "[20301111T090000Z]" {
				t.Errorf("answers = %v with RECURRENCE-IDs %v, want one %s for 20301111T090000Z", got, rids, tt.want)
			}
			if b := e.busyStarts(t); b != tt.busy {
				t.Errorf("busy = %s, want %s", b, tt.busy)
			}
		})
	}
}

// The REPLY to a single-occurrence REQUEST names that occurrence in its own
// RECURRENCE-ID, in the expander's form, whatever form the request used: a
// zoned RECURRENCE-ID is answered as the UTC instant it denotes.
func TestAnswerBookingOccurrenceReplyCarriesItsRecurrenceID(t *testing.T) {
	e := newResourceEnv(t)
	e.bookWeekly(t)
	before := len(e.answers(t))
	ics := strings.Replace(
		ridICS("REQUEST", "weekly@example.org", aliceAdr, "20301111T090000Z", "20301113T140000Z", "20301113T150000Z"),
		"RECURRENCE-ID:20301111T090000Z", "RECURRENCE-ID;TZID=Europe/Berlin:20301111T100000", 1)
	if !e.h.AnswerBooking(resID, aliceAdr, []byte(ics), "REQUEST") {
		t.Fatal("the move was not handled")
	}
	got, rids := e.answers(t)[before:], e.answerRIDs(t)[before:]
	if fmt.Sprint(got) != "[ACCEPTED]" || fmt.Sprint(rids) != "[20301111T090000Z]" {
		t.Errorf("answers = %v with RECURRENCE-IDs %v, want one ACCEPTED for 20301111T090000Z", got, rids)
	}
}

func TestAnswerBookingOccurrenceOfAnUnknownSeriesIsDeclined(t *testing.T) {
	e := newResourceEnv(t)
	ics := ridICS("REQUEST", "nobody@example.org", aliceAdr, "20301111T090000Z", "20301113T140000Z", "20301113T150000Z")
	if !e.h.AnswerBooking(resID, aliceAdr, []byte(ics), "REQUEST") {
		t.Fatal("not handled")
	}
	if got, rids := e.answers(t), e.answerRIDs(t); fmt.Sprint(got) != "[DECLINED]" || fmt.Sprint(rids) != "[20301111T090000Z]" {
		t.Errorf("answers = %v, RECURRENCE-IDs %v", got, rids)
	}
	if e.count(t) != 0 {
		t.Errorf("bookings = %d, want none", e.count(t))
	}
}

// A message is handled only when it reached a final outcome.
func TestAnswerBookingHandledOnlyOnAnOutcome(t *testing.T) {
	e := newResourceEnv(t)
	ics := []byte(bookOpts{}.ics())
	if e.h.AnswerBooking(resID, "", ics, "REQUEST") {
		t.Error("an unverified sender was handled")
	}
	if e.h.AnswerBooking(resID, "carl@other.org", ics, "REQUEST") {
		t.Error("a sender that is not the organiser was handled")
	}
	if e.h.AnswerBooking(aliceID, aliceAdr, ics, "REQUEST") {
		t.Error("a person account was handled")
	}
	if e.h.AnswerBooking(resID, aliceAdr, []byte(bookOpts{attendee: "other@example.org"}.ics()), "REQUEST") {
		t.Error("a request that does not name the resource was handled")
	}
	if e.h.AnswerBooking(resID, aliceAdr, []byte(bookOpts{method: "CANCEL"}.ics()), "CANCEL") {
		t.Error("a CANCEL of an unknown booking was handled")
	}
	if len(e.answers(t)) != 0 {
		t.Errorf("answers = %v", e.answers(t))
	}
	if !e.h.AnswerBooking(resID, aliceAdr, ics, "REQUEST") {
		t.Error("an answered request was not handled")
	}
	if !e.h.AnswerBooking(resID, aliceAdr, []byte(bookOpts{method: "CANCEL"}.ics()), "CANCEL") {
		t.Error("an applied CANCEL was not handled")
	}
}
