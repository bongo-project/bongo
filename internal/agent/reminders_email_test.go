package agent

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jhillyerd/enmime"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type sentReminder struct {
	userID, to string
	ev         *bongov1.Event
	start      time.Time
}

// fakeReminderSender counts the reminder emails the pass asks for.
type fakeReminderSender struct {
	mu   sync.Mutex
	sent []sentReminder
	err  error
}

func (f *fakeReminderSender) SendReminder(userID, to string, ev *bongov1.Event, start time.Time, _ *time.Location) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentReminder{userID, to, ev, start})
	return f.err
}

func (f *fakeReminderSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

var (
	mailDay   = time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC) // a Monday
	mailNow   = mailDay.Add(-10 * time.Minute)
	emailMode = bongov1.ReminderDelivery_REMINDER_DELIVERY_EMAIL
	bothMode  = bongov1.ReminderDelivery_REMINDER_DELIVERY_BOTH
)

func newMailUser(t *testing.T, a *Agent, name string) string {
	t.Helper()
	uid, err := auth.CreateUser(a, a.Metadata, name+"@localhost", name, "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return uid
}

func mailEvent(t *testing.T, a *Agent, uid string, ev *bongov1.Event) string {
	t.Helper()
	if ev.CalendarId == "" {
		ev.CalendarId = "default"
	}
	if ev.Title == "" {
		ev.Title = "Sync"
	}
	if ev.StartTime == 0 {
		ev.StartTime = mailDay.Unix()
		ev.EndTime = mailDay.Add(time.Hour).Unix()
	}
	if ev.ReminderMinutes == 0 {
		ev.ReminderMinutes = 15
	}
	guid, err := a.Events.Create(uid, ev)
	if err != nil {
		t.Fatal(err)
	}
	return guid
}

func newPass(a *Agent, s reminderSender) *emailReminderPass {
	p := a.newEmailReminderPass(s)
	p.barrier = func(time.Duration) error { return nil }
	return p
}

func TestDueRemindersFilters(t *testing.T) {
	a := reminderTestAgent(t)
	other := "colleague@example.com"
	att := func(addr, ps string) []*bongov1.Attendee {
		return []*bongov1.Attendee{{Address: addr, Partstat: ps}}
	}
	cases := []struct {
		name string
		ev   func(me string) *bongov1.Event
		want int
	}{
		{"plain", func(string) *bongov1.Event { return &bongov1.Event{} }, 1},
		{"cancelled single event", func(string) *bongov1.Event { return &bongov1.Event{Status: "CANCELLED"} }, 0},
		{"owner declined", func(me string) *bongov1.Event { return &bongov1.Event{Attendees: att(me, "DECLINED")} }, 0},
		{"owner declined, other case", func(me string) *bongov1.Event {
			return &bongov1.Event{Attendees: att(strings.ToUpper(me), "DECLINED")}
		}, 0},
		{"another attendee declined", func(string) *bongov1.Event { return &bongov1.Event{Attendees: att(other, "DECLINED")} }, 1},
		{"owner accepted", func(me string) *bongov1.Event { return &bongov1.Event{Attendees: att(me, "ACCEPTED")} }, 1},
		{"not yet due", func(string) *bongov1.Event { return &bongov1.Event{ReminderMinutes: 5} }, 0},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("due%d", i)
			uid := newMailUser(t, a, name)
			mailEvent(t, a, uid, tc.ev(name+"@localhost"))
			if got := a.dueReminders(mailNow, []string{uid}); len(got) != tc.want {
				t.Fatalf("due = %d, want %d", len(got), tc.want)
			}
		})
	}
}

func TestDueRemindersHonoursExdateAndOverride(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "series")
	mailEvent(t, a, uid, &bongov1.Event{
		Rrule: "FREQ=WEEKLY;BYDAY=MO",
		IcalRaw: icalSeries(
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "DTSTART:20300506T090000Z", "DTEND:20300506T100000Z",
			"RRULE:FREQ=WEEKLY;BYDAY=MO", "EXDATE:20300513T090000Z", "END:VEVENT",
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20300520T090000Z",
			"DTSTART:20300520T150000Z", "DTEND:20300520T160000Z", "SUMMARY:Moved", "END:VEVENT"),
	})
	at := func(d, h, m int) time.Time { return time.Date(2030, 5, d, h, m, 0, 0, time.UTC) }
	for _, tc := range []struct {
		name string
		now  time.Time
		want int
	}{
		{"first occurrence", at(6, 8, 50), 1},
		{"excluded day", at(13, 8, 50), 0},
		{"moved occurrence at its old time", at(20, 8, 50), 0},
		{"moved occurrence at its new time", at(20, 14, 50), 1},
	} {
		if got := a.dueReminders(tc.now, []string{uid}); len(got) != tc.want {
			t.Errorf("%s: due = %d, want %d", tc.name, len(got), tc.want)
		}
	}
}

// Browser-only fires SSE only, email only fires email only, both fires both.
func TestReminderDeliveryFilter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      bongov1.ReminderDelivery
		wantSSE   bool
		wantEmail int
	}{
		{"browser", bongov1.ReminderDelivery_REMINDER_DELIVERY_BROWSER, true, 0},
		{"email", emailMode, false, 1},
		{"both", bothMode, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := reminderTestAgent(t)
			uid := newMailUser(t, a, "mode")
			ch := a.Notifier.Subscribe(uid, "INBOX")
			mailEvent(t, a, uid, &bongov1.Event{ReminderEmail: tc.mode})
			if got := fires(t, a, ch, mailNow); got != tc.wantSSE {
				t.Errorf("SSE fired = %v, want %v", got, tc.wantSSE)
			}
			s := &fakeReminderSender{}
			newPass(a, s).run(mailNow)
			if s.count() != tc.wantEmail {
				t.Errorf("emails = %d, want %d", s.count(), tc.wantEmail)
			}
		})
	}
}

func TestEmailReminderSentOncePerOccurrence(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "once")
	guid := mailEvent(t, a, uid, &bongov1.Event{ReminderEmail: emailMode})
	s := &fakeReminderSender{}
	p := newPass(a, s)

	if n := p.run(mailNow); n != 1 {
		t.Fatalf("first pass sent %d", n)
	}
	if s.sent[0].to != "once@localhost" || s.sent[0].ev.Guid != guid || !s.sent[0].start.Equal(mailDay) {
		t.Errorf("sent = %+v", s.sent[0])
	}
	if _, err := a.Store.Get(store.ReminderSentKey(uid, guid, mailDay.Unix())); err != nil {
		t.Errorf("no record after the send: %v", err)
	}
	for range 3 {
		p.run(mailNow.Add(time.Minute))
	}
	if s.count() != 1 {
		t.Errorf("repeat passes sent %d in total, want 1", s.count())
	}
}

// A restart (a new pass, nothing in memory) and a leader change must not
// resend: the record in Pebble is the only memory.
func TestEmailReminderNotResentAfterRestart(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "restart")
	mailEvent(t, a, uid, &bongov1.Event{ReminderEmail: bothMode})
	s := &fakeReminderSender{}
	if newPass(a, s).run(mailNow) != 1 {
		t.Fatal("first pass did not send")
	}
	// A different sender and pass stand in for the restarted process.
	s2 := &fakeReminderSender{}
	if n := newPass(a, s2).run(mailNow.Add(time.Minute)); n != 0 || s2.count() != 0 {
		t.Errorf("restarted pass sent %d", s2.count())
	}
}

func TestEmailReminderWriteFailureSkipsSend(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "wfail")
	guid := mailEvent(t, a, uid, &bongov1.Event{ReminderEmail: emailMode})
	s := &fakeReminderSender{}
	p := newPass(a, s)
	healthy := p.apply
	p.apply = func(*bongov1.RaftCommand, time.Duration) error { return errors.New("raft down") }
	if p.run(mailNow) != 0 || s.count() != 0 {
		t.Fatalf("a failed record write still sent %d", s.count())
	}
	if _, err := a.Store.Get(store.ReminderSentKey(uid, guid, mailDay.Unix())); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("record exists after a failed write: %v", err)
	}
	// When the write works again the reminder is still due and goes out once.
	p.apply = healthy
	if p.run(mailNow.Add(time.Minute)) != 1 || s.count() != 1 {
		t.Errorf("recovered pass sent %d", s.count())
	}
}

func TestEmailReminderExistingRecordSkipsSend(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "exists")
	guid := mailEvent(t, a, uid, &bongov1.Event{ReminderEmail: emailMode})
	if err := a.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: store.ReminderSentKey(uid, guid, mailDay.Unix()), Value: store.EncodeReminderSent(mailNow),
	}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	s := &fakeReminderSender{}
	if newPass(a, s).run(mailNow) != 0 || s.count() != 0 {
		t.Errorf("a recorded reminder was sent again")
	}
}

func TestEmailReminderNotLeader(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "follower")
	guid := mailEvent(t, a, uid, &bongov1.Event{ReminderEmail: emailMode})
	s := &fakeReminderSender{}
	p := newPass(a, s)
	writes := 0
	p.apply = func(*bongov1.RaftCommand, time.Duration) error { writes++; return nil }
	p.leader = func() (string, bool) { return "", false }
	if p.run(mailNow) != 0 || s.count() != 0 || writes != 0 {
		t.Errorf("a follower sent %d and wrote %d", s.count(), writes)
	}
	if _, err := a.Store.Get(store.ReminderSentKey(uid, guid, mailDay.Unix())); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a follower left a record: %v", err)
	}
}

// A new leader runs a barrier before its first pass of a term, and a failed
// barrier means no pass.
func TestEmailReminderBarrierPerTerm(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "barrier")
	mailEvent(t, a, uid, &bongov1.Event{ReminderEmail: emailMode, Rrule: "FREQ=HOURLY"})
	s := &fakeReminderSender{}
	p := a.newEmailReminderPass(s)
	term := "3"
	p.leader = func() (string, bool) { return term, term != "" }
	barriers := 0
	var barrierErr error
	p.barrier = func(time.Duration) error { barriers++; return barrierErr }

	barrierErr = errors.New("not caught up")
	if p.run(mailNow) != 0 || s.count() != 0 {
		t.Fatalf("sent %d although the barrier failed", s.count())
	}
	barrierErr = nil
	if p.run(mailNow) != 1 {
		t.Fatalf("pass after a good barrier sent %d", s.count())
	}
	p.run(mailNow)
	if barriers != 2 {
		t.Errorf("barriers = %d, want 2 (one failed, one good, none for the second pass in the term)", barriers)
	}
	term = "" // deposed
	p.run(mailNow)
	term = "4" // elected again
	p.run(mailNow)
	if barriers != 3 {
		t.Errorf("barriers = %d, want a new one in the new term", barriers)
	}
}

func TestEmailReminderSkipsDisabledAndFailedDelivery(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "flaky")
	guid := mailEvent(t, a, uid, &bongov1.Event{ReminderEmail: emailMode})
	s := &fakeReminderSender{err: errors.New("mailbox full")}
	p := newPass(a, s)
	if p.run(mailNow) != 0 || s.count() != 1 {
		t.Fatalf("attempts = %d", s.count())
	}
	// At-most-once: the record stays and the next pass does not retry.
	if _, err := a.Store.Get(store.ReminderSentKey(uid, guid, mailDay.Unix())); err != nil {
		t.Errorf("record missing after a failed delivery: %v", err)
	}
	p.run(mailNow.Add(time.Minute))
	if s.count() != 1 {
		t.Errorf("a failed delivery was retried: %d attempts", s.count())
	}

	// A disabled account gets nothing.
	dis := newMailUser(t, a, "disabled")
	mailEvent(t, a, dis, &bongov1.Event{ReminderEmail: emailMode})
	prof := a.userProfile(dis)
	prof.Disabled = true
	data, err := proto.Marshal(prof)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/user/" + dis + "/profile"), Value: data}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	s2 := &fakeReminderSender{}
	newPass(a, s2).run(mailNow)
	for _, r := range s2.sent {
		if r.userID == dis {
			t.Error("a disabled account was mailed")
		}
	}
}

// Every occurrence of an HOURLY series sends once.
func TestEmailReminderHourlySeries(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "hourly")
	mailEvent(t, a, uid, &bongov1.Event{ReminderEmail: emailMode, Rrule: "FREQ=HOURLY", EndTime: mailDay.Add(10 * time.Minute).Unix()})
	s := &fakeReminderSender{}
	p := newPass(a, s)
	for i := range 3 {
		tick := mailNow.Add(time.Duration(i) * time.Hour)
		p.run(tick)
		p.run(tick.Add(time.Minute))
	}
	if s.count() != 3 {
		t.Fatalf("sent %d, want one per occurrence (3)", s.count())
	}
	seen := map[int64]bool{}
	for _, r := range s.sent {
		seen[r.start.Unix()] = true
	}
	if len(seen) != 3 {
		t.Errorf("distinct occurrences = %d, want 3", len(seen))
	}
}

func TestSweepReminderSent(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "sweepr")
	now := time.Date(2030, 5, 6, 13, 7, 41, 0, time.UTC)
	put := func(guid string, occ int64, at time.Time) []byte {
		k := store.ReminderSentKey(uid, guid, occ)
		if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: k, Value: store.EncodeReminderSent(at)}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		return k
	}
	old := put("g/1", 1, now.Add(-store.ReminderSentTTL-time.Second))
	fresh := put("g/2", 2, now.Add(-store.ReminderSentTTL+time.Second))
	if got := a.sweepReminderSent(now, store.ReminderSentTTL); got != 1 {
		t.Fatalf("deleted = %d, want 1", got)
	}
	if _, err := a.Store.Get(old); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired record survived: %v", err)
	}
	if _, err := a.Store.Get(fresh); err != nil {
		t.Errorf("fresh record swept: %v", err)
	}
}

// The mailer is wired and its worker runs. Deleting either line in Run sends
// no reminder emails.
func TestReminderMailerWired(t *testing.T) {
	a := reminderTestAgent(t)
	if a.reminderMailer == nil {
		t.Fatal("no reminder mailer after Run")
	}
	a.bgMu.Lock()
	live := a.bgLive["reminder-mailer"]
	a.bgMu.Unlock()
	if live != 1 {
		t.Errorf("reminder-mailer workers live = %d, want 1", live)
	}
}

// The reminder mailer built in Run reads the real preference store: a user
// whose locale is German gets German day and month names. Passing nil
// preferences to NewReminderMailer in Run renders English.
func TestReminderMailerUsesUserLocale(t *testing.T) {
	a := reminderTestAgent(t)
	uid := newMailUser(t, a, "de")
	p := store.DefaultPreferences()
	p.Locale = "de"
	if err := a.Preferences.Set(uid, p); err != nil {
		t.Fatal(err)
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	// 6 May 2030 is a Monday: "Mo" and "Mai" (unlike Mon, May).
	start := time.Date(2030, 5, 6, 10, 30, 0, 0, time.UTC)
	ev := &bongov1.Event{Title: "Sync", CalendarId: "default", ReminderMinutes: 15}
	if err := a.reminderMailer.SendReminder(uid, "de@localhost", ev, start, berlin); err != nil {
		t.Fatalf("SendReminder: %v", err)
	}
	waitFor(t, "reminder in INBOX", func() bool { return mailCount(a, uid, "INBOX") == 1 })

	msgs, err := a.Mailboxes.ListMessages(uid, "INBOX")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessages = %d, %v", len(msgs), err)
	}
	raw, err := a.GetKey([]byte("/user/" + uid + "/mail/" + msgs[0].GetMailGuid()))
	if err != nil {
		t.Fatalf("reading MailMeta: %v", err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	rc, err := a.Blobs.Get(meta.GetRawMessage().GetHash())
	if err != nil {
		t.Fatalf("reading blob: %v", err)
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatal(err)
	}
	env, err := enmime.ReadEnvelope(&buf)
	if err != nil {
		t.Fatal(err)
	}
	body := env.Text + "\n" + env.HTML
	for _, want := range []string{"Beginn", "Mo 6 Mai 2030", "Europe/Berlin"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{"Mon ", "May", "Starts"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains English %q:\n%s", bad, body)
		}
	}
}
