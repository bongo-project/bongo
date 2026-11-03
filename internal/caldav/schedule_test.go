package caldav

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	schUser  = "alice-id"
	schEmail = "alice@d.test"
)

type organiseCall struct {
	user          string
	before, after *bongov1.Event
	skip          map[string]bool
}

type replyCall struct {
	user, attendee, partstat, recurrenceID string
}

// fakeSched records what the backend asks of the scheduler.
type fakeSched struct {
	mu       sync.Mutex
	addrs    []string        // calendar-user-address-set of schUser
	aliases  map[string]bool // owned but not in the set
	status   string          // SCHEDULE-STATUS given to every attempted address
	busy     map[string][][2]int64
	organise []organiseCall
	replies  []replyCall
}

func newFakeSched() *fakeSched {
	return &fakeSched{addrs: []string{schEmail, "alice.work@d.test"}, aliases: map[string]bool{"sales@d.test": true}, status: "1.1",
		busy: map[string][][2]int64{}}
}

func (f *fakeSched) Addresses(string) []string { return f.addrs }

func (f *fakeSched) Owns(_, addr string) bool {
	for _, a := range f.addrs {
		if a == addr {
			return true
		}
	}
	return f.aliases[addr]
}

func (f *fakeSched) Organise(user string, before, after *bongov1.Event, skip map[string]bool) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.organise = append(f.organise, organiseCall{user, before, after, skip})
	ev := after
	if ev == nil {
		ev = before
	}
	out := map[string]string{}
	for _, a := range ev.Attendees {
		k := strings.ToLower(a.Address)
		if !skip[k] && k != schEmail {
			out[k] = f.status
		}
	}
	return out
}

func (f *fakeSched) Reply(user string, _ *bongov1.Event, attendee, partstat, rid string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, replyCall{user, attendee, partstat, rid})
	return scheduleStatusSent
}

func (f *fakeSched) FreeBusy(requester, address string, from, to int64) ([][2]int64, bool) {
	p, ok := f.busy[address]
	return p, ok
}

func (f *fakeSched) counts() (organise, replies int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.organise), len(f.replies)
}

type schedEnv struct {
	t      *testing.T
	b      *Backend
	srv    *httptest.Server
	sched  *fakeSched
	inbox  *store.ScheduleInboxStore
	events *store.EventStore
	ps     *store.PebbleStore
	app    *grantApplier // set by realSchedEnv
}

func newSchedEnv(t *testing.T, withScheduler bool) *schedEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &grantApplier{ps: ps}
	e := &schedEnv{t: t, ps: ps, sched: newFakeSched(), events: store.NewEventStore(ps, app), inbox: store.NewScheduleInboxStore(ps, app)}
	cals := store.NewCalendarStore(ps, app)
	if _, err := cals.EnsureDefault(schUser); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.b = NewBackend(e.events, cals, logger)
	if withScheduler {
		e.b.SetScheduler(e.sched)
		e.b.SetScheduleInbox(e.inbox)
	}
	h := ScheduleHandler(PrivilegeFilter(&caldav.Handler{Backend: e.b, Prefix: "/caldav"}, e.b.ReadOnlyShare, logger), e.b, logger)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), r.Header.Get("X-Test-User"))))
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *schedEnv) do(user, method, path, body string, hdr map[string]string) (int, string, http.Header) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("X-Test-User", user)
	if method == "PUT" {
		req.Header.Set("Content-Type", "text/calendar")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func objPath(uid string) string { return "/caldav/" + schUser + "/calendars/default/" + uid + ".ics" }

func (e *schedEnv) put(uid, ics string) (int, string, http.Header) {
	return e.do(schUser, "PUT", objPath(uid), ics, nil)
}

// meeting builds a client's VEVENT upload. extra lines go after the UID.
func meeting(uid string, lines ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Client//EN\r\nBEGIN:VEVENT\r\nUID:" + uid +
		"\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\nSUMMARY:Planning\r\n" +
		strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

const orgLine = "ORGANIZER;CN=Alice:mailto:" + schEmail

func (e *schedEnv) stored(uid string) *bongov1.Event {
	e.t.Helper()
	ev, err := e.events.Get(schUser, uid)
	if err != nil {
		e.t.Fatalf("event %s not stored: %v", uid, err)
	}
	return ev
}

func attendeeParam(t *testing.T, raw []byte, addr, param string) string {
	t.Helper()
	cal := decodeRaw(raw)
	for _, p := range masterOf(cal).Props.Values(ical.PropAttendee) {
		if strings.EqualFold(p.Value, "mailto:"+addr) {
			return p.Params.Get(param)
		}
	}
	t.Fatalf("no ATTENDEE %s in\n%s", addr, raw)
	return ""
}

func TestPutAsOrganiserSchedulesOnceAndWritesStatus(t *testing.T) {
	e := newSchedEnv(t, true)
	code, body, hdr := e.put("m1", meeting("m1", orgLine,
		"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:bob@d.test",
		"ATTENDEE;SCHEDULE-AGENT=CLIENT:mailto:carol@d.test",
		"ATTENDEE;SCHEDULE-AGENT=NONE:mailto:dave@d.test",
		"ATTENDEE:mailto:not an address",
		"ATTENDEE;SCHEDULE-AGENT=CLIENT:mailto:also not an address"))
	if code != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	if o, _ := e.sched.counts(); o != 1 {
		t.Fatalf("Organise calls = %d, want 1", o)
	}
	c := e.sched.organise[0]
	if c.before != nil || c.after == nil || c.user != schUser {
		t.Errorf("first save: before=%v after=%v user=%q", c.before, c.after, c.user)
	}
	if !c.skip["carol@d.test"] || !c.skip["dave@d.test"] || c.skip["bob@d.test"] {
		t.Errorf("skip = %v, want carol and dave only", c.skip)
	}

	ev := e.stored("m1")
	if got := attendeeParam(t, ev.IcalRaw, "bob@d.test", "SCHEDULE-STATUS"); got != "1.1" {
		t.Errorf("bob SCHEDULE-STATUS = %q, want 1.1", got)
	}
	for _, a := range []string{"carol@d.test", "dave@d.test"} {
		if got := attendeeParam(t, ev.IcalRaw, a, "SCHEDULE-STATUS"); got != "" {
			t.Errorf("%s got SCHEDULE-STATUS %q although the client schedules for them", a, got)
		}
	}
	if !strings.Contains(string(ev.IcalRaw), "SCHEDULE-STATUS=3.7") {
		t.Errorf("an ATTENDEE that is no address got no 3.7:\n%s", ev.IcalRaw)
	}
	if got := attendeeParam(t, ev.IcalRaw, "also not an address", "SCHEDULE-STATUS"); got != "" {
		t.Errorf("an ATTENDEE the client schedules for got SCHEDULE-STATUS %q", got)
	}
	// The stored object differs from the client's, so the client must not be
	// told its own ETag is current.
	_, _, get := e.do(schUser, "GET", objPath("m1"), "", nil)
	if hdr.Get("ETag") == "" || hdr.Get("ETag") != get.Get("ETag") {
		t.Errorf("PUT ETag %q, GET ETag %q: must name the object as stored", hdr.Get("ETag"), get.Get("ETag"))
	}
}

func TestPutStatusFailureNeverUndoesTheSave(t *testing.T) {
	e := newSchedEnv(t, true)
	e.sched.status = "5.1"
	code, body, _ := e.put("m2", meeting("m2", orgLine, "ATTENDEE:mailto:bob@d.test"))
	if code != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	if got := attendeeParam(t, e.stored("m2").IcalRaw, "bob@d.test", "SCHEDULE-STATUS"); got != "5.1" {
		t.Errorf("status = %q, want 5.1 for an attendee that was not reached", got)
	}
}

func TestRePutWithEchoedStatusKeepsStatus(t *testing.T) {
	e := newSchedEnv(t, true)
	e.put("m3", meeting("m3", orgLine, "ATTENDEE:mailto:bob@d.test"))
	echo := meeting("m3", orgLine, "ATTENDEE;SCHEDULE-STATUS=1.1:mailto:bob@d.test")
	if code, body, _ := e.put("m3", echo); code != http.StatusCreated {
		t.Fatalf("re-PUT = %d: %s", code, body)
	}
	if o, _ := e.sched.counts(); o != 2 {
		t.Fatalf("Organise calls = %d: the backend asks every time and the scheduler decides", o)
	}
	last := e.sched.organise[1]
	if last.before == nil || last.after == nil {
		t.Errorf("re-PUT must present both versions: %+v", last)
	}
	if got := attendeeParam(t, e.stored("m3").IcalRaw, "bob@d.test", "SCHEDULE-STATUS"); got != "1.1" {
		t.Errorf("status after re-PUT = %q", got)
	}
}

func TestDeleteAsOrganiserCancels(t *testing.T) {
	e := newSchedEnv(t, true)
	e.put("m4", meeting("m4", orgLine, "ATTENDEE:mailto:bob@d.test"))
	if code, _, _ := e.do(schUser, "DELETE", objPath("m4"), "", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if o, _ := e.sched.counts(); o != 2 {
		t.Fatalf("Organise calls = %d, want create and cancel", o)
	}
	c := e.sched.organise[1]
	if c.before == nil || c.after != nil {
		t.Errorf("cancel: before=%v after=%v, want a delete", c.before, c.after)
	}
}

func TestDeleteOfAnEventSomeoneElseOrganisesSendsNothing(t *testing.T) {
	e := newSchedEnv(t, true)
	e.put("m5", meeting("m5", "ORGANIZER:mailto:zed@elsewhere.test", "ATTENDEE;SCHEDULE-AGENT=NONE:mailto:bob@d.test"))
	e.do(schUser, "DELETE", objPath("m5"), "", nil)
	if o, r := e.sched.counts(); o != 0 || r != 0 {
		t.Errorf("organise=%d reply=%d for someone else's meeting", o, r)
	}
}

func TestForeignOrganiserScope(t *testing.T) {
	foreign := "ORGANIZER:mailto:zed@elsewhere.test"
	tests := []struct {
		name     string
		first    string // stored before, "" for none
		put      string
		wantCode int
		wantBody string
		stored   bool
	}{
		{name: "create naming an organiser the user may not send as", put: meeting("f1", foreign, "ATTENDEE:mailto:bob@d.test"),
			wantCode: http.StatusForbidden, wantBody: "valid-organizer"},
		{name: "create as an alias is not an address the user sends as", put: meeting("f2", "ORGANIZER:mailto:sales@d.test", "ATTENDEE:mailto:bob@d.test"),
			wantCode: http.StatusForbidden, wantBody: "valid-organizer"},
		{name: "create without schedulable attendees is stored", put: meeting("f3", foreign, "ATTENDEE;SCHEDULE-AGENT=CLIENT:mailto:bob@d.test"),
			wantCode: http.StatusCreated, stored: true},
		{name: "create without attendees is stored", put: meeting("f4", foreign), wantCode: http.StatusCreated, stored: true},
		{name: "user is an attendee of the foreign organiser", put: meeting("f5", foreign, "ATTENDEE:mailto:"+schEmail, "ATTENDEE:mailto:bob@d.test"),
			wantCode: http.StatusCreated, stored: true},
		{name: "user is invited through an alias", put: meeting("f6", foreign, "ATTENDEE:mailto:sales@d.test", "ATTENDEE:mailto:bob@d.test"),
			wantCode: http.StatusCreated, stored: true},
		{name: "unchanged re-PUT of a stored event", first: meeting("f7", foreign, "ATTENDEE;SCHEDULE-AGENT=CLIENT:mailto:bob@d.test"),
			put: meeting("f7", foreign, "ATTENDEE:mailto:bob@d.test"), wantCode: http.StatusCreated, stored: true},
		{name: "change of the stored organiser", first: meeting("f8", foreign, "ATTENDEE;SCHEDULE-AGENT=CLIENT:mailto:bob@d.test"),
			put:      meeting("f8", "ORGANIZER:mailto:yann@elsewhere.test", "ATTENDEE:mailto:bob@d.test"),
			wantCode: http.StatusForbidden, wantBody: "valid-organizer", stored: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSchedEnv(t, true)
			uid := ""
			for _, l := range strings.Split(tt.put, "\r\n") {
				if v, ok := strings.CutPrefix(l, "UID:"); ok {
					uid = v
				}
			}
			if tt.first != "" {
				if code, body, _ := e.put(uid, tt.first); code != http.StatusCreated {
					t.Fatalf("setup PUT = %d: %s", code, body)
				}
			}
			code, body, _ := e.put(uid, tt.put)
			if code != tt.wantCode || !strings.Contains(body, tt.wantBody) {
				t.Fatalf("PUT = %d %q, want %d containing %q", code, body, tt.wantCode, tt.wantBody)
			}
			if _, err := e.events.Get(schUser, uid); (err == nil) != tt.stored {
				t.Errorf("stored = %v, want %v", err == nil, tt.stored)
			}
			// A refusal sends nothing, and nobody but the user is ever
			// messaged for an organiser that is not theirs.
			if o, _ := e.sched.counts(); o != 0 {
				t.Errorf("Organise called %d times for a foreign organiser", o)
			}
		})
	}
}

func TestAttendeeLimit(t *testing.T) {
	for _, n := range []int{100, 101} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			e := newSchedEnv(t, true)
			lines := []string{orgLine}
			for i := range n {
				lines = append(lines, fmt.Sprintf("ATTENDEE:mailto:p%d@d.test", i))
			}
			code, body, _ := e.put("big", meeting("big", lines...))
			if n == 100 && code != http.StatusCreated {
				t.Fatalf("100 attendees: %d %s", code, body)
			}
			if n == 101 {
				if code != http.StatusForbidden || !strings.Contains(body, "max-attendees-per-instance") {
					t.Fatalf("101 attendees: %d %s", code, body)
				}
				if _, err := e.events.Get(schUser, "big"); err == nil {
					t.Error("refused PUT was stored")
				}
				if o, _ := e.sched.counts(); o != 0 {
					t.Errorf("Organise called %d times", o)
				}
			}
		})
	}
}

func TestAttendeeAnswerSendsOneReply(t *testing.T) {
	foreign := "ORGANIZER:mailto:zed@elsewhere.test"
	invited := func(partstat string) string {
		return meeting("a1", foreign, "ATTENDEE;PARTSTAT="+partstat+":mailto:"+schEmail)
	}
	e := newSchedEnv(t, true)
	// The server holds the invitation as a stored event.
	e.put("a1", invited("NEEDS-ACTION"))
	if _, r := e.sched.counts(); r != 0 {
		t.Fatalf("replies = %d for an unanswered invitation", r)
	}
	e.put("a1", invited("ACCEPTED"))
	if _, r := e.sched.counts(); r != 1 || e.sched.replies[0].partstat != "ACCEPTED" || e.sched.replies[0].attendee != schEmail || e.sched.replies[0].recurrenceID != "" {
		t.Fatalf("replies = %+v, want one ACCEPTED from the user", e.sched.replies)
	}
	// The same answer uploaded again is not a change.
	e.put("a1", invited("ACCEPTED"))
	if _, r := e.sched.counts(); r != 1 {
		t.Errorf("replies = %d after an identical re-PUT", r)
	}
	e.put("a1", invited("DECLINED"))
	if _, r := e.sched.counts(); r != 2 || e.sched.replies[1].partstat != "DECLINED" {
		t.Errorf("replies = %+v", e.sched.replies)
	}
	if o, _ := e.sched.counts(); o != 0 {
		t.Errorf("an attendee's PUT organised %d times", o)
	}
}

func TestAttendeeAnswerForOneOccurrence(t *testing.T) {
	foreign := "ORGANIZER:mailto:zed@elsewhere.test"
	series := func(extra string) string {
		return strings.TrimSuffix(meeting("a2", foreign, "RRULE:FREQ=WEEKLY", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:"+schEmail), "END:VCALENDAR\r\n") + extra + "END:VCALENDAR\r\n"
	}
	override := "BEGIN:VEVENT\r\nUID:a2\r\nDTSTAMP:20300101T000000Z\r\nRECURRENCE-ID:20300513T090000Z\r\nDTSTART:20300513T090000Z\r\nDTEND:20300513T100000Z\r\n" +
		foreign + "\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:" + schEmail + "\r\nEND:VEVENT\r\n"
	e := newSchedEnv(t, true)
	e.put("a2", series(""))
	e.put("a2", series(override))
	if _, r := e.sched.counts(); r != 1 || e.sched.replies[0].recurrenceID != "20300513T090000Z" || e.sched.replies[0].partstat != "ACCEPTED" {
		t.Fatalf("replies = %+v, want one ACCEPTED for the 13th", e.sched.replies)
	}
}

// Thunderbird accepting an invitation from mail creates the event with the
// user's PARTSTAT already set, in one PUT. The server advertises
// calendar-auto-schedule, so the client sends no iMIP: the organiser is told
// by the REPLY the server sends here, whatever the schedule inbox holds.
func TestAttendeeCreateWithAnswerSendsReply(t *testing.T) {
	foreign := "ORGANIZER:mailto:zed@elsewhere.test"
	for _, tc := range []struct {
		partstat string
		replies  int
	}{
		{"ACCEPTED", 1},
		{"TENTATIVE", 1},
		{"DECLINED", 1},
		{"NEEDS-ACTION", 0},
	} {
		t.Run(tc.partstat, func(t *testing.T) {
			e := newSchedEnv(t, true) // the schedule inbox is empty
			e.put("imp", meeting("imp", foreign, "ATTENDEE;PARTSTAT="+tc.partstat+":mailto:"+schEmail))
			if _, r := e.sched.counts(); r != tc.replies {
				t.Fatalf("replies = %d, want %d", r, tc.replies)
			}
			if tc.replies == 1 {
				got := e.sched.replies[0]
				if got.partstat != tc.partstat || got.attendee != schEmail {
					t.Errorf("reply = %+v", got)
				}
			}
		})
	}
}

func TestOrganiserAskingForClientSchedulingGetsNoReply(t *testing.T) {
	e := newSchedEnv(t, true)
	e.put("c1", meeting("c1", "ORGANIZER;SCHEDULE-AGENT=CLIENT:mailto:zed@elsewhere.test", "ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:"+schEmail))
	e.put("c1", meeting("c1", "ORGANIZER;SCHEDULE-AGENT=CLIENT:mailto:zed@elsewhere.test", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:"+schEmail))
	if _, r := e.sched.counts(); r != 0 {
		t.Errorf("replies = %d, but the organiser handles scheduling itself", r)
	}
}

func TestNoSchedulerMeansNoScheduling(t *testing.T) {
	e := newSchedEnv(t, false)
	// Forged organiser and attendees: stored as before, nothing refused.
	code, body, _ := e.put("n1", meeting("n1", "ORGANIZER:mailto:zed@elsewhere.test", "ATTENDEE:mailto:bob@d.test"))
	if code != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	_, _, hdr := e.do(schUser, "OPTIONS", "/caldav/", "", nil)
	if strings.Contains(strings.Join(hdr.Values("DAV"), ","), scheduleToken) {
		t.Errorf("DAV header %v advertises scheduling without a scheduler", hdr.Values("DAV"))
	}
	if _, body, _ := e.do(schUser, "PROPFIND", "/caldav/"+schUser+"/inbox/", "", map[string]string{"Depth": "0"}); strings.Contains(body, "schedule-inbox") {
		t.Errorf("an inbox collection is served without a scheduler: %s", body)
	}
}

// A write grantee's PUT into a shared calendar is stored and sends nothing:
// the meeting is the owner's, and the grantee cannot send as the owner.
func TestSharedCalendarWriteSchedulesNothing(t *testing.T) {
	e := newShEnv(t)
	f := newFakeSched()
	e.backend.SetScheduler(f)
	ics := shICS("sh-1", "Shared")
	ics = strings.Replace(ics, "SUMMARY:Shared", "SUMMARY:Shared\r\nORGANIZER:mailto:alice@d.test\r\nATTENDEE:mailto:bob@d.test", 1)
	code, body := e.do(shWrite, "PUT", sharedPath(shWrite, shCalA)+"sh-1.ics", ics, nil)
	if code != http.StatusCreated {
		t.Fatalf("grantee PUT = %d: %s", code, body)
	}
	if o, r := f.counts(); o != 0 || r != 0 {
		t.Errorf("organise=%d reply=%d for a shared calendar write", o, r)
	}
	if code, _ := e.do(shWrite, "DELETE", sharedPath(shWrite, shCalA)+"sh-1.ics", "", nil); code != http.StatusNoContent {
		t.Fatalf("grantee DELETE = %d", code)
	}
	if o, _ := f.counts(); o != 0 {
		t.Errorf("a shared calendar DELETE organised %d times", o)
	}
}

func TestWritesRefusedWhileStorageIsLowSendNothing(t *testing.T) {
	e := newSchedEnv(t, true)
	e.b.SetStorageGate(lowGate{})
	code, _, _ := e.put("low", meeting("low", orgLine, "ATTENDEE:mailto:bob@d.test"))
	if code != http.StatusInsufficientStorage {
		t.Fatalf("PUT = %d", code)
	}
	if o, _ := e.sched.counts(); o != 0 {
		t.Errorf("a refused PUT organised %d times", o)
	}
}

type lowGate struct{}

func (lowGate) Low() bool { return true }
