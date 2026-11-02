package handlers

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	replyUID   = "series-1@example.org"
	replyBob   = "bob@example.org"
	replyCarol = "carol@example.org"
	replyRID   = "20300507T090000Z"
)

type ownedAddrs []string

func (o ownedAddrs) ListByUser(string) ([]string, error) { return o, nil }

// putCounter counts the writes that reach the store.
type putCounter struct {
	inner store.RaftApplier
	n     atomic.Int32
}

func (c *putCounter) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	c.n.Add(1)
	return c.inner.ApplyCommand(cmd, d)
}

type replyEnv struct {
	*imipEnv
	h      *CalendarHandler
	events *store.EventStore
	writes *putCounter
}

func newReplyEnv(t *testing.T) *replyEnv {
	t.Helper()
	e := newImipEnv(t)
	ca := &putCounter{inner: &directApplierTags{ps: e.q.ps}}
	events := store.NewEventStore(e.q.ps, ca)
	h := NewCalendarHandler(events, e.q.calendars, nil)
	h.owned = ownedAddrs{quotaEmail}
	return &replyEnv{imipEnv: e, h: h, events: events, writes: ca}
}

type seed struct {
	uid, organiser string
	seq            int
	rrule          string
	noRaw          bool
	user           string
	override       string // RECURRENCE-ID of an existing override, "" for none
	noBob          bool
}

func organiserRaw(s seed, bobPartstat string) string {
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//EN", "BEGIN:VEVENT",
		"UID:" + s.uid, "DTSTAMP:20300101T000000Z", "DTSTART:20300506T090000Z", "DTEND:20300506T100000Z",
		"SUMMARY:Planning", fmt.Sprintf("SEQUENCE:%d", s.seq),
		"ORGANIZER;CN=Owner:mailto:" + s.organiser,
		"ATTENDEE;PARTSTAT=" + bobPartstat + ";RSVP=TRUE:mailto:" + replyBob,
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:" + replyCarol,
	}
	if s.rrule != "" {
		lines = append(lines, "RRULE:"+s.rrule)
	}
	lines = append(lines, "END:VEVENT")
	if s.override != "" {
		lines = append(lines, "BEGIN:VEVENT", "UID:"+s.uid, "DTSTAMP:20300101T000000Z",
			"RECURRENCE-ID:"+s.override, "DTSTART:20300507T110000Z", "DTEND:20300507T120000Z",
			"SUMMARY:Moved", "SEQUENCE:2", "ORGANIZER;CN=Owner:mailto:"+s.organiser,
			"ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:"+replyBob,
			"ATTENDEE;PARTSTAT=ACCEPTED:mailto:"+replyCarol, "END:VEVENT")
	}
	lines = append(lines, "END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n"
}

// seed stores the organiser's event and resets the write counter.
func (e *replyEnv) seed(t *testing.T, s seed) *bongov1.Event {
	t.Helper()
	if s.uid == "" {
		s.uid = replyUID
	}
	if s.organiser == "" {
		s.organiser = quotaEmail
	}
	if s.user == "" {
		s.user = quotaUser
	}
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC).Unix()
	ev := &bongov1.Event{
		Guid: s.uid, CalendarId: "cal", Title: "Planning", StartTime: start, EndTime: start + 3600,
		Organizer: s.organiser, Sequence: int32(s.seq), Rrule: s.rrule,
		Attendees: []*bongov1.Attendee{
			{Address: replyCarol, Partstat: "ACCEPTED"},
		},
	}
	if !s.noBob {
		ev.Attendees = append([]*bongov1.Attendee{{Address: replyBob, Partstat: "NEEDS-ACTION", Rsvp: true}}, ev.Attendees...)
	}
	if !s.noRaw {
		ev.IcalRaw = []byte(organiserRaw(s, "NEEDS-ACTION"))
	}
	if _, err := e.events.Create(s.user, ev); err != nil {
		t.Fatal(err)
	}
	e.writes.n.Store(0)
	return ev
}

type replyOpts struct {
	uid, organiser, rid, method string
	seq                         int
	attendees                   []string // "addr=PARTSTAT"
}

func (o replyOpts) ics() string {
	if o.uid == "" {
		o.uid = replyUID
	}
	if o.organiser == "" {
		o.organiser = quotaEmail
	}
	if o.method == "" {
		o.method = "REPLY"
	}
	if o.attendees == nil {
		o.attendees = []string{replyBob + "=ACCEPTED"}
	}
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//EN", "METHOD:" + o.method, "BEGIN:VEVENT",
		"UID:" + o.uid, "DTSTAMP:20300102T000000Z", "DTSTART:20300506T090000Z", "DTEND:20300506T100000Z",
		fmt.Sprintf("SEQUENCE:%d", o.seq), "ORGANIZER:mailto:" + o.organiser,
	}
	if o.rid != "" {
		lines = append(lines, "RECURRENCE-ID:"+o.rid)
	}
	for _, a := range o.attendees {
		addr, ps, _ := strings.Cut(a, "=")
		lines = append(lines, "ATTENDEE;PARTSTAT="+ps+":mailto:"+addr)
	}
	lines = append(lines, "END:VEVENT", "END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n"
}

func (e *replyEnv) apply(sender string, o replyOpts) {
	e.h.ApplyReply(quotaUser, sender, []byte(o.ics()), strings.ToUpper(o.method))
}

func (e *replyEnv) get(t *testing.T, user string) *bongov1.Event {
	t.Helper()
	ev, err := e.events.Get(user, replyUID)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func structuredPartstat(ev *bongov1.Event, addr string) string {
	for _, a := range ev.Attendees {
		if a.Address == addr {
			return a.Partstat
		}
	}
	return ""
}

// rawPartstatAt returns the PARTSTAT of addr in the VEVENT with this
// RECURRENCE-ID ("" for the master), or "-" when there is no such VEVENT.
func rawPartstatAt(t *testing.T, raw []byte, rid, addr string) string {
	t.Helper()
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		key := ""
		if p := c.Props.Get(ical.PropRecurrenceID); p != nil {
			key, _ = imip.RecurrenceKey(p)
		}
		if key != rid {
			continue
		}
		for _, p := range c.Props.Values(ical.PropAttendee) {
			if strings.EqualFold(p.Value, "mailto:"+addr) {
				return p.Params.Get(ical.ParamParticipationStatus)
			}
		}
		return ""
	}
	return "-"
}

func TestApplyReplySeriesPartstat(t *testing.T) {
	for _, withRaw := range []bool{true, false} {
		for _, ps := range []string{"ACCEPTED", "TENTATIVE", "DECLINED"} {
			t.Run(fmt.Sprintf("%s raw=%v", ps, withRaw), func(t *testing.T) {
				e := newReplyEnv(t)
				e.seed(t, seed{noRaw: !withRaw})
				e.apply(replyBob, replyOpts{attendees: []string{replyBob + "=" + ps}})
				got := e.get(t, quotaUser)
				if structuredPartstat(got, replyBob) != ps {
					t.Errorf("structured bob = %q, want %q", structuredPartstat(got, replyBob), ps)
				}
				if structuredPartstat(got, replyCarol) != "ACCEPTED" {
					t.Errorf("carol changed to %q", structuredPartstat(got, replyCarol))
				}
				if got.Attendees[0].Rsvp {
					t.Error("an answered attendee still asks for an RSVP")
				}
				if withRaw {
					if rp := rawPartstatAt(t, got.IcalRaw, "", replyBob); rp != ps {
						t.Errorf("raw bob = %q, want %q", rp, ps)
					}
					if rp := rawPartstatAt(t, got.IcalRaw, "", replyCarol); rp != "ACCEPTED" {
						t.Errorf("raw carol = %q", rp)
					}
				}
				if n := e.writes.n.Load(); n != 1 {
					t.Errorf("%d writes, want exactly one PUT", n)
				}
			})
		}
	}
}

func TestApplyReplyRefusals(t *testing.T) {
	tests := []struct {
		name   string
		seed   seed
		sender string
		opts   replyOpts
	}{
		{"baseline control is applied elsewhere", seed{seq: 1}, "", replyOpts{}},
		{"unverified sender", seed{}, "", replyOpts{}},
		{"sender is not the replying attendee", seed{}, replyCarol, replyOpts{}},
		{"sender not an attendee of the reply", seed{}, "dave@example.org", replyOpts{attendees: []string{"dave@example.org=ACCEPTED"}}},
		{"attendee not on the event", seed{noBob: true}, replyBob, replyOpts{}},
		{"organiser is not the user", seed{organiser: "olive@example.org"}, replyBob, replyOpts{organiser: "olive@example.org"}},
		{"organiser differs from the reply's", seed{}, replyBob, replyOpts{organiser: "mallory@example.org"}},
		{"event only in another user's keys", seed{user: otherUser}, replyBob, replyOpts{}},
		{"unknown uid", seed{}, replyBob, replyOpts{uid: "other-uid@example.org"}},
		{"counter", seed{}, replyBob, replyOpts{method: "COUNTER"}},
		{"refresh", seed{}, replyBob, replyOpts{method: "REFRESH"}},
		{"request", seed{}, replyBob, replyOpts{method: "REQUEST"}},
		{"partstat not an answer", seed{}, replyBob, replyOpts{attendees: []string{replyBob + "=NEEDS-ACTION"}}},
		{"partstat arbitrary", seed{}, replyBob, replyOpts{attendees: []string{replyBob + "=DELEGATED"}}},
		{"higher sequence", seed{seq: 2}, replyBob, replyOpts{seq: 3}},
		{"instance not an occurrence", seed{rrule: "FREQ=DAILY;COUNT=3"}, replyBob, replyOpts{rid: "20301231T090000Z"}},
		{"instance of a series without recurrence", seed{}, replyBob, replyOpts{rid: replyRID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newReplyEnv(t)
			e.seed(t, tt.seed)
			sender := tt.sender
			if tt.name == "baseline control is applied elsewhere" {
				// The same input with a verified sender is the accepted case.
				e.apply(replyBob, tt.opts)
				if e.writes.n.Load() != 1 {
					t.Fatal("the control input was not applied: the refusals below prove nothing")
				}
				return
			}
			e.apply(sender, tt.opts)
			if n := e.writes.n.Load(); n != 0 {
				t.Errorf("%d writes, want none", n)
			}
		})
	}
}

func TestApplyReplySequence(t *testing.T) {
	tests := []struct {
		replySeq int
		want     string
	}{
		{4, "NEEDS-ACTION"},
		{3, "ACCEPTED"},
		{1, "ACCEPTED"},
		{0, "ACCEPTED"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.replySeq), func(t *testing.T) {
			e := newReplyEnv(t)
			e.seed(t, seed{seq: 3})
			e.apply(replyBob, replyOpts{seq: tt.replySeq})
			got := e.get(t, quotaUser)
			if structuredPartstat(got, replyBob) != tt.want {
				t.Errorf("structured = %q, want %q", structuredPartstat(got, replyBob), tt.want)
			}
			if rp := rawPartstatAt(t, got.IcalRaw, "", replyBob); rp != tt.want {
				t.Errorf("raw = %q, want %q", rp, tt.want)
			}
		})
	}
}

func TestApplyReplyOnlyTheSendersAttendee(t *testing.T) {
	e := newReplyEnv(t)
	e.seed(t, seed{})
	e.apply(replyBob, replyOpts{attendees: []string{replyBob + "=DECLINED", replyCarol + "=DECLINED"}})
	got := e.get(t, quotaUser)
	if structuredPartstat(got, replyBob) != "DECLINED" || rawPartstatAt(t, got.IcalRaw, "", replyBob) != "DECLINED" {
		t.Error("bob's answer was not applied")
	}
	if structuredPartstat(got, replyCarol) != "ACCEPTED" || rawPartstatAt(t, got.IcalRaw, "", replyCarol) != "ACCEPTED" {
		t.Error("carol's attendee was changed by a reply from bob")
	}
}

func TestApplyReplyDoesNotCopyReceivedProperties(t *testing.T) {
	e := newReplyEnv(t)
	e.seed(t, seed{})
	o := replyOpts{}
	ics := strings.Replace(o.ics(), "SEQUENCE:0", "SEQUENCE:0\r\nSUMMARY:Pwned\r\nLOCATION:Elsewhere", 1)
	e.h.ApplyReply(quotaUser, replyBob, []byte(ics), "REPLY")
	got := e.get(t, quotaUser)
	if got.Title != "Planning" || got.Location != "" || strings.Contains(string(got.IcalRaw), "Pwned") {
		t.Errorf("received properties leaked into the organiser's event: %q %q", got.Title, got.Location)
	}
	if structuredPartstat(got, replyBob) != "ACCEPTED" {
		t.Error("the answer itself was not applied")
	}
}

func TestApplyReplyInstance(t *testing.T) {
	t.Run("no override yet: one is created, series untouched", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=DAILY;COUNT=5", seq: 2})
		e.apply(replyBob, replyOpts{rid: replyRID, seq: 2, attendees: []string{replyBob + "=DECLINED"}})
		got := e.get(t, quotaUser)
		if rp := rawPartstatAt(t, got.IcalRaw, replyRID, replyBob); rp != "DECLINED" {
			t.Errorf("override bob = %q, want DECLINED", rp)
		}
		if rp := rawPartstatAt(t, got.IcalRaw, "", replyBob); rp != "NEEDS-ACTION" {
			t.Errorf("master bob = %q, want it untouched", rp)
		}
		if structuredPartstat(got, replyBob) != "NEEDS-ACTION" {
			t.Errorf("structured attendees changed by an instance reply: %q", structuredPartstat(got, replyBob))
		}
		if got.Sequence != 2 {
			t.Errorf("sequence = %d, want it kept at 2", got.Sequence)
		}
		// A second answer from the same attendee still matches.
		e.apply(replyBob, replyOpts{rid: replyRID, seq: 2, attendees: []string{replyBob + "=ACCEPTED"}})
		if rp := rawPartstatAt(t, e.get(t, quotaUser).IcalRaw, replyRID, replyBob); rp != "ACCEPTED" {
			t.Errorf("second answer = %q", rp)
		}
	})
	t.Run("existing override: only it changes", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=DAILY;COUNT=5", seq: 2, override: replyRID})
		e.apply(replyBob, replyOpts{rid: replyRID, seq: 2, attendees: []string{replyBob + "=TENTATIVE"}})
		got := e.get(t, quotaUser)
		if rp := rawPartstatAt(t, got.IcalRaw, replyRID, replyBob); rp != "TENTATIVE" {
			t.Errorf("override bob = %q", rp)
		}
		if rp := rawPartstatAt(t, got.IcalRaw, replyRID, replyCarol); rp != "ACCEPTED" {
			t.Errorf("override carol = %q", rp)
		}
		if rp := rawPartstatAt(t, got.IcalRaw, "", replyBob); rp != "NEEDS-ACTION" {
			t.Errorf("master bob = %q, want it untouched", rp)
		}
		if !strings.Contains(string(got.IcalRaw), "Moved") {
			t.Error("the override's own fields were lost")
		}
	})
	t.Run("override sequence higher than the reply's is applied, lower is not", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=DAILY;COUNT=5", seq: 2, override: replyRID})
		e.apply(replyBob, replyOpts{rid: replyRID, seq: 3})
		if e.writes.n.Load() != 0 {
			t.Error("a reply newer than the override was applied")
		}
	})
	t.Run("other attendees in a reply are never read", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=DAILY;COUNT=5", seq: 2, override: replyRID})
		e.apply(replyCarol, replyOpts{rid: replyRID, seq: 2, attendees: []string{"erin@example.org=ACCEPTED", replyCarol + "=DECLINED"}})
		got := e.get(t, quotaUser)
		if rp := rawPartstatAt(t, got.IcalRaw, replyRID, replyCarol); rp != "DECLINED" {
			t.Errorf("carol in the override = %q", rp)
		}
	})
}

func TestApplyReplyWriteFailureIsHarmless(t *testing.T) {
	e := newReplyEnv(t)
	e.seed(t, seed{})
	before := e.get(t, quotaUser)
	failing := NewCalendarHandler(store.NewEventStore(e.q.ps, failingApplier{}), e.q.calendars, nil)
	failing.owned = ownedAddrs{quotaEmail}
	failing.ApplyReply(quotaUser, replyBob, []byte(replyOpts{}.ics()), "REPLY")
	after := e.get(t, quotaUser)
	if structuredPartstat(after, replyBob) != structuredPartstat(before, replyBob) {
		t.Error("a failed write changed the stored event")
	}
}

func TestApplyReplyConcurrentAnswersAreAllKept(t *testing.T) {
	e := newReplyEnv(t)
	e.seed(t, seed{})
	var wg sync.WaitGroup
	for _, who := range []string{replyBob, replyCarol} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.apply(who, replyOpts{attendees: []string{who + "=DECLINED"}})
		}()
	}
	wg.Wait()
	got := e.get(t, quotaUser)
	for _, who := range []string{replyBob, replyCarol} {
		if structuredPartstat(got, who) != "DECLINED" || rawPartstatAt(t, got.IcalRaw, "", who) != "DECLINED" {
			t.Errorf("%s's answer was lost", who)
		}
	}
}

func TestApplyReplyRoundTripFromSendReply(t *testing.T) {
	e := newReplyEnv(t)
	e.seed(t, seed{seq: 1})
	// What sendReply builds: the organiser's object with the attendee's answer
	// set, and the one attendee.
	reply := &bongov1.Event{
		Title: "Planning", StartTime: time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC).Unix(),
		EndTime: time.Date(2030, 5, 6, 10, 0, 0, 0, time.UTC).Unix(), Organizer: quotaEmail, Sequence: 1,
		IcalRaw:   []byte(organiserRaw(seed{uid: replyUID, organiser: quotaEmail, seq: 1}, "TENTATIVE")),
		Attendees: []*bongov1.Attendee{{Address: replyBob, Partstat: "TENTATIVE"}},
	}
	cal, err := imip.BuildCalendar(imip.MethodReply, reply, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	part, err := encodeCalendar(cal)
	if err != nil {
		t.Fatal(err)
	}
	e.h.ApplyReply(quotaUser, replyBob, part, "REPLY")
	got := e.get(t, quotaUser)
	if structuredPartstat(got, replyBob) != "TENTATIVE" || rawPartstatAt(t, got.IcalRaw, "", replyBob) != "TENTATIVE" {
		t.Errorf("round trip not applied: %q", structuredPartstat(got, replyBob))
	}
}

func TestEventReplyLocksRelease(t *testing.T) {
	l := &replyLocks{m: map[string]*replyLock{}}
	l.lock("a")()
	l.lock("a")()
	if len(l.m) != 0 {
		t.Errorf("%d lock entries left behind", len(l.m))
	}
}

// A REPLY for an occurrence with no override creates one, so the number is
// capped: a verified attendee must not be able to grow the organiser's event
// by one component per message. The cap'th override is made, the next is not,
// and a REPLY to an override that exists still applies at the cap.
func TestApplyReplyOverrideCap(t *testing.T) {
	e := newReplyEnv(t)
	e.seed(t, seed{rrule: "FREQ=DAILY;COUNT=400", seq: 1})
	rid := func(day int) string {
		return time.Date(2030, 5, 6+day, 9, 0, 0, 0, time.UTC).Format("20060102T150405Z")
	}
	overrides := func() int {
		return strings.Count(string(e.get(t, quotaUser).IcalRaw), "RECURRENCE-ID")
	}
	for day := 1; day <= maxReplyOverrides; day++ {
		e.apply(replyBob, replyOpts{rid: rid(day), seq: 1})
	}
	if n := overrides(); n != maxReplyOverrides {
		t.Fatalf("overrides = %d, want the cap %d", n, maxReplyOverrides)
	}
	e.writes.n.Store(0)
	e.apply(replyBob, replyOpts{rid: rid(maxReplyOverrides + 1), seq: 1})
	if n := overrides(); n != maxReplyOverrides || e.writes.n.Load() != 0 {
		t.Errorf("cap+1: overrides = %d, writes = %d; want %d and 0", n, e.writes.n.Load(), maxReplyOverrides)
	}
	e.apply(replyBob, replyOpts{rid: rid(1), seq: 1, attendees: []string{replyBob + "=DECLINED"}})
	if rp := rawPartstatAt(t, e.get(t, quotaUser).IcalRaw, rid(1), replyBob); rp != "DECLINED" {
		t.Errorf("an existing override at the cap = %q, want DECLINED", rp)
	}
}
