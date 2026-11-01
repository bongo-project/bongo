package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const subSecretURL = "https://feeds.example.com/s3cret-token/cal.ics?key=hunter2"

// fakeFetcher returns scripted results and records the validators it saw.
type fakeFetcher struct {
	mu     sync.Mutex
	result *netx.Result
	err    error
	calls  int
	seen   []netx.Validators
	onCall func()
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string, v netx.Validators) (*netx.Result, error) {
	f.mu.Lock()
	f.calls++
	f.seen = append(f.seen, v)
	cb := f.onCall
	res, err := f.result, f.err
	f.mu.Unlock()
	if cb != nil {
		cb()
	}
	return res, err
}

func (f *fakeFetcher) set(res *netx.Result, err error) {
	f.mu.Lock()
	f.result, f.err = res, err
	f.mu.Unlock()
}

type subEnv struct {
	r      *subscriptionRefresher
	ps     *store.PebbleStore
	app    *recordingApplier
	cal    string
	fetch  *fakeFetcher
	logBuf *bytes.Buffer
	now    time.Time
}

func newSubEnv(t *testing.T) *subEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	app := &recordingApplier{ps: ps}
	buf := &bytes.Buffer{}
	fetch := &fakeFetcher{}
	cals := store.NewCalendarStore(ps, app)
	r := &subscriptionRefresher{
		users:     func() ([]string, error) { return []string{"u1"}, nil },
		calendars: cals,
		events:    store.NewEventStore(ps, app),
		fetcher:   fetch,
		logger:    slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	guid, err := cals.Create("u1", &bongov1.Calendar{
		Name: "Feed", Color: "#111111",
		Subscription: &bongov1.CalendarSubscription{SourceUrl: subSecretURL},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &subEnv{r: r, ps: ps, app: app, cal: guid, fetch: fetch, logBuf: buf, now: time.Unix(1_800_000_000, 0)}
}

func (e *subEnv) pass(t *testing.T) int {
	t.Helper()
	return e.r.pass(context.Background(), func() bool { return true }, e.now)
}

func (e *subEnv) sub(t *testing.T) *bongov1.CalendarSubscription {
	t.Helper()
	c, err := e.r.calendars.Get("u1", e.cal)
	if err != nil {
		t.Fatal(err)
	}
	return c.Subscription
}

func (e *subEnv) calEvents(t *testing.T) map[string]*bongov1.Event {
	t.Helper()
	all, _, err := e.r.events.ListAll("u1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*bongov1.Event{}
	for _, ev := range all {
		if ev.CalendarId == e.cal {
			out[ev.Guid] = ev
		}
	}
	return out
}

func feedVEvent(uid, summary string) string {
	return "BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260301T100000Z\r\nDTEND:20260301T110000Z\r\nSUMMARY:" + summary + "\r\nEND:VEVENT\r\n"
}

func feedBody(events ...string) []byte {
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n" + strings.Join(events, "") + "END:VCALENDAR\r\n")
}

func ok(body []byte) *netx.Result {
	return &netx.Result{Body: body, ETag: `"v1"`, LastModified: "Mon, 02 Jan 2006 15:04:05 GMT"}
}

func TestRefreshCreatesEventsAndState(t *testing.T) {
	e := newSubEnv(t)
	e.fetch.set(ok(feedBody(feedVEvent("a", "A"), feedVEvent("b", "B"))), nil)
	if n := e.pass(t); n != 1 {
		t.Fatalf("attempted %d", n)
	}
	evs := e.calEvents(t)
	if len(evs) != 2 {
		t.Fatalf("events = %d", len(evs))
	}
	for guid, ev := range evs {
		if !strings.HasPrefix(guid, "sub-") || ev.CalendarId != e.cal {
			t.Fatalf("event %s in %q", guid, ev.CalendarId)
		}
	}
	if _, ok := evs[store.SubscriptionEventGUID(e.cal, "a")]; !ok {
		t.Fatal("event not under the derived guid")
	}
	s := e.sub(t)
	if s.LastSuccessAt != e.now.Unix() || s.LastError != "" || s.Etag != `"v1"` || s.EventCount != 2 {
		t.Fatalf("state: %+v", s)
	}
	if want := e.now.Add(6 * time.Hour).Unix(); s.NextRefreshAt != want {
		t.Fatalf("next = %d, want %d", s.NextRefreshAt, want)
	}
	if s.SourceUrl != subSecretURL {
		t.Fatal("source url lost")
	}
}

func TestRefreshReplacesByUID(t *testing.T) {
	e := newSubEnv(t)
	e.fetch.set(ok(feedBody(feedVEvent("keep", "K"), feedVEvent("change", "C1"), feedVEvent("gone", "G"))), nil)
	e.pass(t)
	before := e.app.calls.Load()

	e.now = e.now.Add(7 * time.Hour)
	e.fetch.set(ok(feedBody(feedVEvent("keep", "K"), feedVEvent("change", "C2"), feedVEvent("new", "N"))), nil)
	e.pass(t)

	evs := e.calEvents(t)
	if len(evs) != 3 {
		t.Fatalf("events = %d", len(evs))
	}
	if _, ok := evs[store.SubscriptionEventGUID(e.cal, "gone")]; ok {
		t.Fatal("missing event not deleted")
	}
	if evs[store.SubscriptionEventGUID(e.cal, "change")].Title != "C2" {
		t.Fatal("changed event not updated")
	}
	if _, ok := evs[store.SubscriptionEventGUID(e.cal, "new")]; !ok {
		t.Fatal("new event not written")
	}
	// One update, one create, one delete, one state write: an unchanged event
	// is not rewritten.
	if got := e.app.calls.Load() - before; got != 4 {
		t.Fatalf("raft writes = %d, want 4 (update, create, delete, state)", got)
	}
}

func TestRefreshUnchangedFeedWritesStateOnly(t *testing.T) {
	e := newSubEnv(t)
	body := feedBody(feedVEvent("a", "A"), feedVEvent("b", "B"))
	e.fetch.set(ok(body), nil)
	e.pass(t)
	before := e.app.calls.Load()
	e.now = e.now.Add(7 * time.Hour)
	e.pass(t)
	if got := e.app.calls.Load() - before; got != 1 {
		t.Fatalf("writes = %d, want only the state record", got)
	}
}

func TestRefreshNotModified(t *testing.T) {
	e := newSubEnv(t)
	e.fetch.set(ok(feedBody(feedVEvent("a", "A"))), nil)
	e.pass(t)
	before := e.app.calls.Load()

	e.now = e.now.Add(7 * time.Hour)
	e.fetch.set(&netx.Result{NotModified: true}, nil)
	e.pass(t)

	if got := e.app.calls.Load() - before; got != 1 {
		t.Fatalf("writes = %d, want only the state record", got)
	}
	if v := e.fetch.seen[len(e.fetch.seen)-1]; v.ETag != `"v1"` || v.LastModified == "" {
		t.Fatalf("validators not sent: %+v", v)
	}
	s := e.sub(t)
	if s.Etag != `"v1"` || s.LastSuccessAt != e.now.Unix() || s.EventCount != 1 {
		t.Fatalf("state after 304: %+v", s)
	}
	if len(e.calEvents(t)) != 1 {
		t.Fatal("events changed on 304")
	}
}

func TestRefreshHonoursFeedIntervalWithFloor(t *testing.T) {
	tests := []struct {
		hint string
		want time.Duration
	}{
		{"", 6 * time.Hour},
		{"REFRESH-INTERVAL;VALUE=DURATION:PT12H\r\n", 12 * time.Hour},
		{"REFRESH-INTERVAL;VALUE=DURATION:PT5M\r\n", time.Hour},
		{"X-PUBLISHED-TTL:P30D\r\n", 24 * time.Hour},
	}
	for _, tc := range tests {
		e := newSubEnv(t)
		body := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\n" + tc.hint + feedVEvent("a", "A") + "END:VCALENDAR\r\n")
		e.fetch.set(ok(body), nil)
		e.pass(t)
		if got := time.Duration(e.sub(t).NextRefreshAt-e.now.Unix()) * time.Second; got != tc.want {
			t.Errorf("%q: interval %v, want %v", tc.hint, got, tc.want)
		}
	}
}

func TestRefreshSkipsNotDueAndNonLeader(t *testing.T) {
	e := newSubEnv(t)
	e.fetch.set(ok(feedBody(feedVEvent("a", "A"))), nil)
	if n := e.r.pass(context.Background(), func() bool { return false }, e.now); n != 0 || e.fetch.calls != 0 {
		t.Fatalf("non-leader fetched: %d %d", n, e.fetch.calls)
	}
	e.pass(t)
	if n := e.pass(t); n != 0 {
		t.Fatalf("not-due subscription refreshed (%d)", n)
	}
	if e.fetch.calls != 1 {
		t.Fatalf("fetch calls = %d", e.fetch.calls)
	}
}

// A bad fetch must change nothing but the state record: no event written, no
// event deleted, whatever the failure.
func TestRefreshFailuresWriteNoEvents(t *testing.T) {
	var big strings.Builder
	for i := range store.MaxEventsPerSubscription + 1 {
		big.WriteString(feedVEvent(fmt.Sprintf("u%d", i), "x"))
	}
	truncated := append(feedBody(feedVEvent("a", "A"), feedVEvent("z", "Z")), []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:q\r\n")...)
	tests := []struct {
		name string
		res  *netx.Result
		err  error
		want string
	}{
		{"oversize body", nil, fmt.Errorf("fetching feeds.example.com: %w", netx.ErrBodyTooLarge), "too large"},
		{"network error", nil, fmt.Errorf("fetching feeds.example.com: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}), errConnect},
		{"refused address", nil, fmt.Errorf("fetching feeds.example.com: %w", netx.ErrForbiddenAddress), errConnect},
		{"not a calendar", ok([]byte("<html>login</html>")), nil, ""},
		{"empty 200", ok(nil), nil, "no calendar"},
		{"no events", ok(feedBody()), nil, "no events"},
		{"truncated", ok(truncated), nil, "truncated"},
		{"over the event cap", ok(feedBody(big.String())), nil, "too many"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newSubEnv(t)
			e.fetch.set(ok(feedBody(feedVEvent("a", "A"), feedVEvent("z", "Z"))), nil)
			e.pass(t)
			if len(e.calEvents(t)) != 2 {
				t.Fatal("setup")
			}
			okSucc := e.sub(t).LastSuccessAt

			e.now = e.now.Add(7 * time.Hour)
			e.app.keys.Store(nil)
			e.fetch.set(tc.res, tc.err)
			e.pass(t)

			if got := len(e.calEvents(t)); got != 2 {
				t.Fatalf("events = %d after a failed refresh, want the 2 kept", got)
			}
			for _, k := range e.app.recorded() {
				if strings.Contains(k, "/event/") {
					t.Fatalf("event key written on failure: %s", k)
				}
			}
			s := e.sub(t)
			if s.LastError == "" || !strings.Contains(s.LastError, tc.want) {
				t.Fatalf("last_error = %q, want mention of %q", s.LastError, tc.want)
			}
			if s.LastSuccessAt != okSucc {
				t.Fatal("failure moved last_success_at")
			}
			if s.NextRefreshAt != e.now.Add(time.Hour).Unix() {
				t.Fatalf("retry at %d, want one hour on", s.NextRefreshAt-e.now.Unix())
			}
			if s.Etag != `"v1"` {
				t.Fatal("validators of the last good fetch were dropped")
			}
		})
	}
}

func TestRefreshAtEventCapSucceeds(t *testing.T) {
	e := newSubEnv(t)
	var b strings.Builder
	for i := range store.MaxEventsPerSubscription {
		b.WriteString(feedVEvent(fmt.Sprintf("u%d", i), "x"))
	}
	e.fetch.set(ok(feedBody(b.String())), nil)
	e.pass(t)
	if s := e.sub(t); s.LastError != "" || s.EventCount != int32(store.MaxEventsPerSubscription) {
		t.Fatalf("at the cap: %+v", s)
	}
}

// A feed may use any UID. It must never reach the user's own events or those
// of another subscription, only events of this calendar.
func TestRefreshNeverTouchesOtherEvents(t *testing.T) {
	e := newSubEnv(t)
	own := &bongov1.Event{Guid: "mine", Title: "Mine", CalendarId: "default"}
	other, _ := e.r.calendars.Create("u1", &bongov1.Calendar{Name: "Other", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://other.example.com/x.ics", NextRefreshAt: 1 << 40}})
	foreign := &bongov1.Event{Guid: store.SubscriptionEventGUID(other, "a"), Title: "Foreign", CalendarId: other}
	for _, ev := range []*bongov1.Event{own, foreign} {
		if _, err := e.r.events.Create("u1", ev); err != nil {
			t.Fatal(err)
		}
	}
	// The feed uses the guid of the user's own event as its UID.
	e.fetch.set(ok(feedBody(feedVEvent("mine", "Hostile"), feedVEvent("a", "Same UID as the other feed"))), nil)
	e.pass(t)

	got, err := e.r.events.Get("u1", "mine")
	if err != nil || got.Title != "Mine" || got.CalendarId != "default" {
		t.Fatalf("own event overwritten: %v %v", got, err)
	}
	f, err := e.r.events.Get("u1", foreign.Guid)
	if err != nil || f.Title != "Foreign" {
		t.Fatalf("another subscription's event touched: %v %v", f, err)
	}
	// And a later refresh whose feed drops everything deletes only its own.
	e.now = e.now.Add(7 * time.Hour)
	e.fetch.set(ok(feedBody(feedVEvent("only", "Only"))), nil)
	e.pass(t)
	if _, err := e.r.events.Get("u1", "mine"); err != nil {
		t.Fatal("own event deleted by a refresh")
	}
	if _, err := e.r.events.Get("u1", foreign.Guid); err != nil {
		t.Fatal("other calendar's event deleted by a refresh")
	}
}

func TestRefreshKeepsRenameMadeDuringFetch(t *testing.T) {
	e := newSubEnv(t)
	e.fetch.set(ok(feedBody(feedVEvent("a", "A"))), nil)
	e.fetch.onCall = func() {
		c, _ := e.r.calendars.Get("u1", e.cal)
		c.Name, c.Color = "Renamed", "#999999"
		if err := e.r.calendars.Update("u1", e.cal, c); err != nil {
			t.Error(err)
		}
	}
	e.pass(t)
	c, _ := e.r.calendars.Get("u1", e.cal)
	if c.Name != "Renamed" || c.Color != "#999999" {
		t.Fatalf("rename undone: %q %q", c.Name, c.Color)
	}
	if c.Subscription.LastSuccessAt == 0 {
		t.Fatal("state not written")
	}
}

func TestRefreshDoesNotResurrectDeletedCalendar(t *testing.T) {
	e := newSubEnv(t)
	e.fetch.set(ok(feedBody(feedVEvent("a", "A"))), nil)
	e.fetch.onCall = func() {
		if err := e.r.calendars.Delete("u1", e.cal); err != nil {
			t.Error(err)
		}
	}
	e.pass(t)
	if _, err := e.r.calendars.Get("u1", e.cal); err == nil {
		t.Fatal("deleted calendar was written back")
	}
	if n := len(e.calEvents(t)); n != 0 {
		t.Fatalf("%d events orphaned for the deleted calendar", n)
	}
}

// Deleted between the events being written and the state being written: the
// events just created are removed again.
func TestRefreshPurgesEventsWhenCalendarGoneAfterApply(t *testing.T) {
	e := newSubEnv(t)
	e.fetch.set(ok(feedBody(feedVEvent("a", "A"), feedVEvent("b", "B"))), nil)
	// Delete the calendar on the first event write.
	var once sync.Once
	e.app.onApply = func(cmd *bongov1.RaftCommand) {
		if strings.Contains(string(cmd.Key), "/event/") {
			once.Do(func() { _ = e.ps.Delete([]byte("/user/u1/calendar/" + e.cal)) })
		}
	}
	e.pass(t)
	if n := len(e.calEvents(t)); n != 0 {
		t.Fatalf("%d events left for a deleted calendar", n)
	}
}

// A write failing part-way leaves a mix; the next refresh corrects it.
func TestRefreshConvergesAfterPartialFailure(t *testing.T) {
	e := newSubEnv(t)
	e.fetch.set(ok(feedBody(feedVEvent("a", "A"), feedVEvent("b", "B"), feedVEvent("c", "C"))), nil)
	e.app.failOn = e.app.calls.Load() + 2 // the second write: one event lands, the pass stops
	e.pass(t)
	if got := len(e.calEvents(t)); got != 1 {
		t.Fatalf("events after the failure = %d, want 1", got)
	}
	s := e.sub(t)
	if s.LastError == "" || s.LastSuccessAt != 0 {
		t.Fatalf("partial refresh recorded as success: %+v", s)
	}
	if s.Etag != "" {
		t.Fatal("validators saved for a half-applied feed: a 304 would skip the repair")
	}

	e.app.failOn = 0
	e.now = e.now.Add(2 * time.Hour)
	e.pass(t)
	if got := len(e.calEvents(t)); got != 3 {
		t.Fatalf("events after the retry = %d, want 3", got)
	}
	if s := e.sub(t); s.LastError != "" || s.LastSuccessAt == 0 {
		t.Fatalf("state after retry: %+v", s)
	}
}

func TestRefreshStopsPassOnWriteFailure(t *testing.T) {
	e := newSubEnv(t)
	second, _ := e.r.calendars.Create("u1", &bongov1.Calendar{Name: "Second", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://two.example.com/x.ics"}})
	_ = second
	e.fetch.set(ok(feedBody(feedVEvent("a", "A"))), nil)
	e.app.failOn = e.app.calls.Load() + 1
	if n := e.pass(t); n != 1 {
		t.Fatalf("attempted %d subscriptions, want the pass to stop after the failure", n)
	}
}

func TestRefreshLogsHostOnly(t *testing.T) {
	e := newSubEnv(t)
	e.fetch.set(nil, errors.New("fetching feeds.example.com: connection refused"))
	e.pass(t)
	e.fetch.set(ok(feedBody(feedVEvent("a", "A"))), nil)
	e.now = e.now.Add(2 * time.Hour)
	e.pass(t)
	e.fetch.set(&netx.Result{NotModified: true}, nil)
	e.now = e.now.Add(7 * time.Hour)
	e.pass(t)

	out := e.logBuf.String()
	if !strings.Contains(out, "feeds.example.com") {
		t.Fatal("host missing from the log")
	}
	for _, secret := range []string{"s3cret-token", "hunter2", "cal.ics"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log leaks %q:\n%s", secret, out)
		}
	}
	if s := e.sub(t); strings.Contains(s.LastError, "s3cret") {
		t.Fatalf("last_error leaks the URL: %q", s.LastError)
	}
}

func TestSanitiseError(t *testing.T) {
	got := sanitiseError("bad\r\nthing\x00" + strings.Repeat("é", 400))
	if strings.ContainsAny(got, "\r\n\x00") || len(got) > subscriptionMaxErrorLen+3 {
		t.Fatalf("not sanitised: %q", got)
	}
}

// Deleting the line in Run that starts the worker, or the one that passes the
// operator's option to the client, turns this red.
func TestCalendarSubscriptionsRunWiring(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.Calendar = &config.CalendarConfig{Subscriptions: &config.CalendarSubscriptionsConfig{AllowPrivateNetworks: true}}
	isolatePorts(t, cfg)
	a := runWiringAgent(t, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	a.bgMu.Lock()
	live := a.bgLive["calendar-subscriptions"]
	a.bgMu.Unlock()
	if live != 1 {
		t.Fatalf("calendar-subscriptions workers running = %d, want 1", live)
	}
	if a.subscriptionsCancel == nil {
		t.Fatal("worker has no cancel: Shutdown cannot stop it")
	}
	if a.subscriptionClient == nil || !a.subscriptionClient.AllowsPrivateNetworks() {
		t.Fatal("calendar.subscriptions.allow_private_networks did not reach the client")
	}
}

func TestCalendarSubscriptionsDefaultClientIsStrict(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	isolatePorts(t, cfg)
	a := runWiringAgent(t, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if a.subscriptionClient == nil || a.subscriptionClient.AllowsPrivateNetworks() {
		t.Fatal("default client must refuse private networks")
	}
}

// A connection failure must not reveal the address a name resolved to or
// whether the name exists.
func TestFailureMessageHidesNetworkDetail(t *testing.T) {
	ip := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 443}, Err: errors.New("connect: 10.0.0.5 refused")}
	tests := []error{
		fmt.Errorf("fetching internal.corp: %w", ip),
		fmt.Errorf("fetching internal.corp: %w", &net.DNSError{Err: "no such host", Name: "internal.corp"}),
		fmt.Errorf("fetching internal.corp: %w: 10.0.0.5", netx.ErrForbiddenAddress),
		fmt.Errorf("fetching internal.corp: %w", context.DeadlineExceeded),
	}
	for _, err := range tests {
		got := failureMessage(err)
		if got != errConnect {
			t.Errorf("failureMessage(%v) = %q, want %q", err, got, errConnect)
		}
		if strings.Contains(got, "10.0.0.5") {
			t.Errorf("message leaks address: %q", got)
		}
	}
	if got := failureMessage(errors.New("fetching feeds.example.com: status 404")); !strings.Contains(got, "404") {
		t.Errorf("status error lost: %q", got)
	}
}
