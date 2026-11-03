package handlers

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type schedPrefs map[string]string

func (p schedPrefs) Get(userID string) *bongov1.UserPreferences {
	if l, ok := p[userID]; ok {
		return &bongov1.UserPreferences{Locale: l}
	}
	return nil
}

type schedEmails map[string]string

func (e schedEmails) UserEmail(userID string) string { return e[userID] }

type schedHarness struct {
	h      *CalendarHandler
	sched  *CalDAVScheduler
	sender *fakeInviteSender
	events *store.EventStore
	acl    *store.ACLStore
	addrs  *store.AddressStore
	prefs  schedPrefs
	ps     *store.PebbleStore
}

const (
	schedAlice = "u-alice"
	schedBob   = "u-bob"
	schedCarol = "u-carol"
)

func newSchedHarness(t *testing.T) *schedHarness {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	mgr, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.Close() })
	applier := &directApplierTags{ps: ps}
	hn := &schedHarness{
		events: store.NewEventStore(ps, applier),
		acl:    store.NewACLStore(ps, applier),
		addrs:  store.NewAddressStore(ps, applier, mgr.System),
		sender: &fakeInviteSender{},
		prefs:  schedPrefs{},
		ps:     ps,
	}
	h := NewCalendarHandler(hn.events, store.NewCalendarStore(ps, applier), nil)
	h.SetACL(hn.acl)
	h.SetAddressStore(hn.addrs)
	h.SetInviteSender(hn.sender)
	hn.h = h
	hn.sched = NewCalDAVScheduler(h, hn.prefs, schedEmails{schedAlice: "alice@d.test", schedBob: "bob@d.test", schedCarol: "carol@d.test"})
	for _, r := range []struct {
		addr, user, kind  string
		canSend, verified bool
	}{
		{addr: "alice@d.test", user: schedAlice, kind: "primary", canSend: true, verified: true},
		{addr: "alice.work@d.test", user: schedAlice, kind: "send-as", canSend: true, verified: true},
		{addr: "alice.pending@d.test", user: schedAlice, kind: "send-as", canSend: true, verified: false},
		{addr: "sales@d.test", user: schedAlice, kind: "alias"},
		{addr: "bob@d.test", user: schedBob, kind: "primary", canSend: true, verified: true},
		{addr: "carol@d.test", user: schedCarol, kind: "primary", canSend: true, verified: true},
	} {
		if err := hn.addrs.Put(r.addr, &bongov1.AddressRecord{UserId: r.user, Kind: r.kind, CanSend: r.canSend, Verified: r.verified, Domain: "d.test"}); err != nil {
			t.Fatal(err)
		}
	}
	return hn
}

func TestSchedulerAddressSet(t *testing.T) {
	hn := newSchedHarness(t)
	got := hn.sched.Addresses(schedAlice)
	slices.Sort(got)
	if want := "alice.work@d.test,alice@d.test"; strings.Join(got, ",") != want {
		t.Errorf("Addresses = %v, want primary and the verified send-as only (%s)", got, want)
	}
	for addr, want := range map[string]bool{
		"alice@d.test": true, "ALICE.WORK@d.test": true, "sales@d.test": true, "alice.pending@d.test": true,
		"bob@d.test": false, "nobody@d.test": false, "": false,
	} {
		if got := hn.sched.Owns(schedAlice, addr); got != want {
			t.Errorf("Owns(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestSchedulerFreeBusyGating(t *testing.T) {
	hn := newSchedHarness(t)
	for _, e := range []*bongov1.Event{
		{Guid: "e1", Title: "Busy", StartTime: 1000, EndTime: 2000},
		{Guid: "e2", Title: "Cancelled", StartTime: 3000, EndTime: 4000, Status: "CANCELLED"},
		{Guid: "e5", Title: "Declined", StartTime: 9000, EndTime: 9500, Organizer: "zed@elsewhere.test",
			Attendees: []*bongov1.Attendee{{Address: "bob@d.test", Partstat: "DECLINED"}}},
		{Guid: "e3", Title: "Free time", StartTime: 5000, EndTime: 6000, Transparent: true},
		{Guid: "e4", Title: "Later", StartTime: 7000, EndTime: 8000},
	} {
		if _, err := hn.events.Create(schedBob, e); err != nil {
			t.Fatal(err)
		}
	}
	freebusy := func(requester, addr string) ([][2]int64, bool) {
		return hn.sched.FreeBusy(requester, addr, 0, 9999)
	}

	// No grant: indistinguishable from an unknown address.
	if _, ok := freebusy(schedAlice, "bob@d.test"); ok {
		t.Error("free/busy of a user who shared nothing was answered")
	}
	if _, ok := freebusy(schedAlice, "nobody@d.test"); ok {
		t.Error("free/busy of an unknown address was answered")
	}
	// A pending grant does not count.
	cal, err := hn.h.calendars.Create(schedBob, &bongov1.Calendar{Name: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := hn.acl.SetCalendarGrant(schedBob, cal, schedAlice, store.GrantLevelFreeBusy, store.GrantStatePending, schedBob, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := freebusy(schedAlice, "bob@d.test"); ok {
		t.Error("a pending grant opened free/busy")
	}
	if err := hn.acl.SetCalendarGrant(schedBob, cal, schedAlice, store.GrantLevelFreeBusy, store.GrantStateAccepted, schedBob, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	busy, ok := freebusy(schedAlice, "bob@d.test")
	if !ok || len(busy) != 2 || busy[0] != [2]int64{1000, 2000} || busy[1] != [2]int64{7000, 8000} {
		t.Errorf("free/busy = %v %v, want the two opaque, uncancelled, undeclined events", busy, ok)
	}
	// carol shared nothing with alice, and bob's grant is not carol's.
	if _, ok := freebusy(schedAlice, "carol@d.test"); ok {
		t.Error("another user's free/busy was answered through bob's grant")
	}
	// One's own is always answered.
	if _, ok := freebusy(schedBob, "bob@d.test"); !ok {
		t.Error("a user could not see their own free/busy")
	}
}

func schedMeeting(attendees ...*bongov1.Attendee) *bongov1.Event {
	return &bongov1.Event{
		Guid: "m1", Title: "Planning", StartTime: 1904288400, EndTime: 1904292000,
		Organizer: "alice@d.test", Attendees: attendees,
	}
}

func TestSchedulerOrganise(t *testing.T) {
	bobA := &bongov1.Attendee{Address: "bob@example.org"}
	carolA := &bongov1.Attendee{Address: "carol@example.org"}
	hn := newSchedHarness(t)

	// A create messages everyone but the ones the client handles.
	st := hn.sched.Organise(schedAlice, nil, schedMeeting(bobA, carolA), map[string]bool{"carol@example.org": true})
	if len(st) != 1 || st["bob@example.org"] != "1.1" {
		t.Errorf("statuses = %v, want bob 1.1 only", st)
	}
	msgs := hn.sender.messages()
	if len(msgs) != 1 || msgs[0].method != "REQUEST" || strings.Join(msgs[0].to, ",") != "bob@example.org" || msgs[0].from != "alice@d.test" {
		t.Fatalf("messages = %+v", msgs)
	}
	for _, c := range msgs[0].cal.Children {
		if c.Name == ical.CompEvent && strings.Contains(strings.ToUpper(encodeProps(c)), "SCHEDULE-") {
			t.Error("the message carries a scheduling parameter")
		}
	}

	// A change that tells nobody anything sends nothing and reports nothing.
	hn.sender.reset()
	reminded := schedMeeting(bobA, carolA)
	reminded.ReminderMinutes = 15
	if st := hn.sched.Organise(schedAlice, schedMeeting(bobA, carolA), reminded, nil); len(st) != 0 || len(hn.sender.messages()) != 0 {
		t.Errorf("a reminder change sent %v / %d messages", st, len(hn.sender.messages()))
	}

	// A rename is shown to attendees, so it is sent (US-010).
	hn.sender.reset()
	renamed := schedMeeting(bobA, carolA)
	renamed.Title = "Renamed"
	if st := hn.sched.Organise(schedAlice, schedMeeting(bobA, carolA), renamed, nil); len(st) != 2 || len(hn.sender.messages()) != 1 {
		t.Errorf("a rename sent %v / %d messages, want both attendees in one REQUEST", st, len(hn.sender.messages()))
	}

	// An unreachable recipient is reported by name and does not stop the rest.
	hn.sender.reset()
	hn.sender.refuse = map[string]bool{"carol@example.org": true}
	st = hn.sched.Organise(schedAlice, nil, schedMeeting(bobA, carolA), nil)
	if st["bob@example.org"] != "1.1" || st["carol@example.org"] != "5.1" {
		t.Errorf("statuses = %v, want bob 1.1 and carol 5.1", st)
	}

	// Someone else's meeting is never sent in their name.
	hn.sender.reset()
	other := schedMeeting(bobA)
	other.Organizer = "mallory@elsewhere.test"
	if st := hn.sched.Organise(schedAlice, nil, other, nil); len(st) != 0 || len(hn.sender.messages()) != 0 {
		t.Errorf("a foreign organiser was sent as: %v", st)
	}

	// A delete cancels.
	hn.sender.reset()
	hn.sender.refuse = nil
	hn.sched.Organise(schedAlice, schedMeeting(bobA), nil, nil)
	if m := hn.sender.messages(); len(m) != 1 || m[0].method != "CANCEL" {
		t.Errorf("messages = %+v, want one CANCEL", m)
	}
}

func encodeProps(c *ical.Component) string {
	var sb strings.Builder
	for _, ps := range c.Props {
		for _, p := range ps {
			sb.WriteString(p.Name + " ")
			for k := range p.Params {
				sb.WriteString(k + " ")
			}
		}
	}
	return sb.String()
}

func TestSchedulerUsesTheUsersLocale(t *testing.T) {
	subject := func(locale string) string {
		hn := newSchedHarness(t)
		if locale != "" {
			hn.prefs[schedAlice] = locale
		}
		hn.sched.Organise(schedAlice, nil, schedMeeting(&bongov1.Attendee{Address: "bob@example.org"}), nil)
		m := hn.sender.messages()
		if len(m) != 1 {
			t.Fatalf("messages = %d", len(m))
		}
		return m[0].subject
	}
	en, de := subject(""), subject("de")
	if en == de {
		t.Errorf("subject is %q in English and German alike: the user's locale was not used", en)
	}
}

func TestSchedulerReply(t *testing.T) {
	ev := &bongov1.Event{
		Guid: "g", Organizer: "zed@elsewhere.test", Sequence: 2, StartTime: 1904288400, EndTime: 1904292000, Title: "Their meeting",
		Attendees: []*bongov1.Attendee{{Address: "alice@d.test", Name: "Alice", Partstat: "NEEDS-ACTION", Rsvp: true}},
		IcalRaw: []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//EN\r\nBEGIN:VEVENT\r\nUID:their-uid\r\nDTSTAMP:20300101T000000Z\r\n" +
			"DTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\nSUMMARY:Their meeting\r\nSEQUENCE:2\r\nORGANIZER:mailto:zed@elsewhere.test\r\n" +
			"ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE;SCHEDULE-STATUS=1.1:mailto:alice@d.test\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"),
	}
	hn := newSchedHarness(t)
	if st := hn.sched.Reply(schedAlice, ev, "alice@d.test", "ACCEPTED", ""); st != "1.1" {
		t.Fatalf("reply status %q, want 1.1", st)
	}
	msgs := hn.sender.messages()
	if len(msgs) != 1 || msgs[0].method != "REPLY" || strings.Join(msgs[0].to, ",") != "zed@elsewhere.test" || msgs[0].from != "alice@d.test" {
		t.Fatalf("messages = %+v", msgs)
	}
	ve := msgs[0].vevent(t)
	if got := ve.Props.Get(ical.PropUID).Value; got != "their-uid" {
		t.Errorf("UID = %q: the organiser matches the reply on the UID it chose", got)
	}
	atts := ve.Props.Values(ical.PropAttendee)
	if len(atts) != 1 || atts[0].Params.Get("PARTSTAT") != "ACCEPTED" || atts[0].Params.Get("SCHEDULE-STATUS") != "" {
		t.Errorf("ATTENDEE = %+v", atts)
	}

	// One occurrence.
	hn.sender.reset()
	if st := hn.sched.Reply(schedAlice, ev, "alice@d.test", "DECLINED", "20300513T090000Z"); st != "1.1" {
		t.Fatalf("instance reply status %q, want 1.1", st)
	}
	if rid := hn.sender.messages()[0].vevent(t).Props.Get(ical.PropRecurrenceID); rid == nil || rid.Value != "20300513T090000Z" {
		t.Errorf("RECURRENCE-ID = %v", rid)
	}

	// An address the user cannot send as is never answered as.
	hn.sender.reset()
	if st := hn.sched.Reply(schedAlice, ev, "sales@d.test", "ACCEPTED", ""); st != "3.8" || len(hn.sender.messages()) != 0 {
		t.Errorf("a reply was sent as an alias, or its status is %q, want 3.8", st)
	}
	if st := hn.sched.Reply(schedAlice, ev, "bob@d.test", "ACCEPTED", ""); st != "3.8" || len(hn.sender.messages()) != 0 {
		t.Errorf("a reply was sent as another user, or its status is %q, want 3.8", st)
	}
}

// The CalDAV outbox and the web endpoint answer free/busy from one
// computation, so an all-day event is treated the same by both.
func TestSchedulerFreeBusyAgreesWithWeb(t *testing.T) {
	hn := newSchedHarness(t)
	day := func(d int) int64 { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC).Unix() }
	timedStart := day(9) + 10*3600
	for _, e := range []*bongov1.Event{
		{Guid: "t", Title: "Timed", StartTime: timedStart, EndTime: timedStart + 3600},
		// All-day events make the user busy for the whole date (operator
		// decision 2026-11-02).
		{Guid: "d", Title: "Leave", StartTime: day(10), EndTime: day(11), AllDay: true},
		// Transparent (TRANSP:TRANSPARENT) events do not.
		{Guid: "x", Title: "Reminder only", StartTime: day(12) + 3600, EndTime: day(12) + 7200, Transparent: true},
		{Guid: "y", Title: "Holiday marker", StartTime: day(13), EndTime: day(14), AllDay: true, Transparent: true},
	} {
		if _, err := hn.events.Create(schedBob, e); err != nil {
			t.Fatal(err)
		}
	}
	from, to := day(1), day(20)
	busy, ok := hn.sched.FreeBusy(schedBob, "bob@d.test", from, to)
	if !ok {
		t.Fatal("own free/busy not answered")
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/calendar/freebusy?user=%s&start=%d&end=%d", schedBob, from, to), nil)
	req = req.WithContext(middleware.WithUserContext(req.Context(), schedBob, "bob@d.test"))
	rr := httptest.NewRecorder()
	hn.h.FreeBusy(rr, req)
	var web []struct{ Start, End int64 }
	if err := json.Unmarshal(rr.Body.Bytes(), &web); err != nil {
		t.Fatal(err)
	}
	var fromWeb [][2]int64
	for _, w := range web {
		fromWeb = append(fromWeb, [2]int64{w.Start, w.End})
	}
	slices.SortFunc(busy, func(a, b [2]int64) int { return cmp.Compare(a[0], b[0]) })
	slices.SortFunc(fromWeb, func(a, b [2]int64) int { return cmp.Compare(a[0], b[0]) })
	// The all-day block runs from local midnight to local midnight in the
	// user's zone.
	loc := hn.h.userLocation(schedBob)
	want := [][2]int64{{timedStart, timedStart + 3600}, {localMidnight(day(10), loc), localMidnight(day(11), loc)}}
	if !slices.Equal(busy, fromWeb) || !slices.Equal(busy, want) {
		t.Errorf("outbox %v, web %v; want %v", busy, fromWeb, want)
	}
}

func TestLocalMidnightPlacesAllDayInUserZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata")
	}
	stored := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC).Unix()
	got := time.Unix(localMidnight(stored, berlin), 0).In(berlin)
	if got.Year() != 2026 || got.Month() != 11 || got.Day() != 10 || got.Hour() != 0 {
		t.Errorf("all-day 10 Nov placed at %v in Berlin, want 2026-11-10 00:00", got)
	}
	if want := time.Date(2026, 11, 9, 23, 0, 0, 0, time.UTC).Unix(); localMidnight(stored, berlin) != want {
		t.Errorf("localMidnight = %d, want %d (23:00 UTC the day before)", localMidnight(stored, berlin), want)
	}
}

// A send-as lookup that fails is not a refusal: the answer is reported as 5.1,
// never stored as 3.8 (no authority), and nothing is sent.
func TestSchedulerReplyLookupFailureIsNotNoAuthority(t *testing.T) {
	ev := &bongov1.Event{
		Guid: "g", Organizer: "zed@elsewhere.test", Title: "Their meeting",
		Attendees: []*bongov1.Attendee{{Address: "broken@d.test", Partstat: "NEEDS-ACTION"}},
	}
	hn := newSchedHarness(t)
	// A record that does not decode makes AddressStore.Get fail.
	if err := hn.ps.Set([]byte("/system/address/broken@d.test"), []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hn.addrs.Get("broken@d.test"); err == nil {
		t.Fatal("test setup: the lookup should fail")
	}
	if st := hn.sched.Reply(schedAlice, ev, "broken@d.test", "ACCEPTED", ""); st != "5.1" {
		t.Errorf("status = %q on a failed lookup, want 5.1", st)
	}
	if n := len(hn.sender.messages()); n != 0 {
		t.Errorf("%d messages sent", n)
	}
}
