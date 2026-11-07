package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-smtp"
)

const (
	probeEmail = "probe-account@" + testDomain
	probePW    = "probe-pw-marker-7Qx2LmN9"
)

// probeEnv is an SMTP server that accepts at once and delivers into an
// in-memory IMAP mailbox d1 later, and an IMAP server whose SEARCH shows a
// message only d2 after delivery. Like Bongo's HEADER X-Loadgen-Id search it
// answers with every visible message of the run, not just the one asked for.
type probeEnv struct {
	t        *testing.T
	d1, d2   time.Duration
	smtpAddr string
	imapAddr string
	user     *imapmemserver.User
	state    string

	mu        sync.Mutex
	delivered map[imap.UID]deliveredMsg
	// decoyHits counts searches that answered with other messages of the run
	// and not the one asked for.
	decoyHits atomic.Int64
	// idleBlocked makes IDLE never announce anything: EXISTS is swallowed.
	noopOnly bool
	// idleOnly makes a NOOP carry nothing, so every announcement goes out
	// during IDLE and none can legitimately be a NOOP find.
	idleOnly bool
}

type deliveredMsg struct {
	id string
	at time.Time
}

type probeBackend struct{ e *probeEnv }

func (b probeBackend) NewSession(*smtp.Conn) (smtp.Session, error) { return &probeSMTP{e: b.e}, nil }

type probeSMTP struct{ e *probeEnv }

func (s *probeSMTP) Reset()                               {}
func (s *probeSMTP) Logout() error                        { return nil }
func (s *probeSMTP) Mail(string, *smtp.MailOptions) error { return nil }
func (s *probeSMTP) Rcpt(string, *smtp.RcptOptions) error { return nil }
func (s *probeSMTP) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	id := m.Header.Get("X-Loadgen-Id")
	// The reply goes out now; the message lands later. The extra 20ms keeps
	// the measured delay above d1 whatever the reply's own latency.
	time.AfterFunc(s.e.d1+20*time.Millisecond, func() {
		res, err := s.e.user.Append("INBOX", literalOf(string(raw)), &imap.AppendOptions{})
		if err != nil {
			s.e.t.Errorf("append: %v", err)
			return
		}
		s.e.mu.Lock()
		s.e.delivered[res.UID] = deliveredMsg{id: id, at: time.Now()}
		s.e.mu.Unlock()
	})
	return nil
}

type hideSession struct {
	imapserver.Session
	e         *probeEnv
	afterIdle atomic.Bool
}

func (s *hideSession) Search(kind imapserver.NumKind, c *imap.SearchCriteria, o *imap.SearchOptions) (*imap.SearchData, error) {
	if kind != imapserver.NumKindUID || len(c.Header) != 1 || c.Header[0].Key != "X-Loadgen-Id" {
		return s.Session.Search(kind, c, o)
	}
	want := c.Header[0].Value
	prefix := want[:strings.LastIndex(want, "-")+1]
	now := time.Now()
	var uids []imap.UID
	var hit bool
	s.e.mu.Lock()
	for uid, m := range s.e.delivered {
		if strings.HasPrefix(m.id, prefix) && now.Sub(m.at) >= s.e.d2 {
			uids = append(uids, uid)
			hit = hit || m.id == want
		}
	}
	s.e.mu.Unlock()
	if len(uids) > 0 && !hit {
		s.e.decoyHits.Add(1)
	}
	slices.Sort(uids)
	return &imap.SearchData{All: imap.UIDSetNum(uids...), Count: uint32(len(uids))}, nil
}

func (s *hideSession) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	if s.e.idleOnly || s.afterIdle.CompareAndSwap(true, false) {
		return nil
	}
	return s.Session.Poll(w, allowExpunge)
}

func (s *hideSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if s.e.noopOnly {
		// Hold the connection without ever announcing mail.
		<-stop
		// The server polls once as IDLE ends; that poll must not announce
		// mail either, or IDLE would have found it after all.
		s.afterIdle.Store(true)
		return nil
	}
	return s.Session.Idle(w, stop)
}

func newProbeEnv(t *testing.T, d1, d2 time.Duration) *probeEnv {
	t.Helper()
	e := &probeEnv{t: t, d1: d1, d2: d2, delivered: map[imap.UID]deliveredMsg{}}

	mem := imapmemserver.New()
	e.user = imapmemserver.NewUser(probeEmail, probePW)
	if err := e.user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(e.user)
	imapSrv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &hideSession{Session: mem.NewSession(), e: e}, nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}},
		Logger:       quietLogger{},
		InsecureAuth: true,
	})
	iln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.imapAddr = iln.Addr().String()
	go func() { _ = imapSrv.Serve(iln) }()

	smtpSrv := smtp.NewServer(probeBackend{e})
	smtpSrv.Domain = "server.test"
	smtpSrv.AllowInsecureAuth = true
	sln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.smtpAddr = sln.Addr().String()
	go func() { _ = smtpSrv.Serve(sln) }()
	t.Cleanup(func() { _ = imapSrv.Close(); _ = smtpSrv.Close() })

	e.state = writeState(t, 0o600, stHeader,
		`{"t":"user-pending","u":0,"email":"`+probeEmail+`","pw":"`+probePW+`"}`, stD0)
	return e
}

func (e *probeEnv) config(t *testing.T, mod func(*probeOptions)) Config {
	t.Helper()
	o := probeOptions{
		StateFile: e.state, IMAPAddr: e.imapAddr, IMAPTLS: imapTLSPlain,
		Interval: 150 * time.Millisecond, Duration: time.Second, Timeout: 5 * time.Second,
		Poll: time.Hour, Size: 300, Summary: filepath.Join(t.TempDir(), "probe.json"),
	}
	if mod != nil {
		mod(&o)
	}
	return Config{
		Allow:      []Target{{Host: "127.0.0.1", IP: true}},
		Targets:    []string{e.smtpAddr},
		TestDomain: testDomain,
		Senders:    []string{"sender@" + testDomain},
		SMTP:       smtpConfig{Timeout: 2 * time.Second},
		Report:     reportConfig{Probe: o},
	}
}

var fastTune = probeTuning{backoffStart: 10 * time.Millisecond, backoffCap: 100 * time.Millisecond}

func TestProbeMeasuresDelayAndSearchLag(t *testing.T) {
	const d1, d2 = 120 * time.Millisecond, 200 * time.Millisecond
	e := newProbeEnv(t, d1, d2)
	cfg := e.config(t, nil)
	sum, err := probeRun(context.Background(), cfg, defaultDial(), fastTune)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sent < 4 || sum.Seen != sum.Sent || sum.Found != sum.Sent || sum.Lost != 0 || sum.SearchLost != 0 {
		t.Fatalf("sent %d seen %d found %d lost %d search-lost %d: %+v", sum.Sent, sum.Seen, sum.Found, sum.Lost, sum.SearchLost, sum)
	}
	if sum.SeenByNoop != 0 {
		t.Errorf("%d messages found by the NOOP poll with a working IDLE", sum.SeenByNoop)
	}
	for i, d := range sum.DelayMs {
		if d < millis(d1) || d > millis(d1)+800 {
			t.Errorf("delay %d = %.1fms, want at least %v", i, d, d1)
		}
	}
	// The mailbox holds other messages of the run, so a probe that took any
	// hit for its own would report a lag near zero for every message but the
	// first.
	for i, l := range sum.SearchLagMs {
		if l < millis(d1+d2) || l > millis(d1+d2)+1000 {
			t.Errorf("search lag %d = %.1fms, want at least %v", i, l, d1+d2)
		}
	}
	if e.decoyHits.Load() == 0 {
		t.Fatal("no search was ever answered with the wrong messages, so the lag check proves nothing")
	}
	if sum.Params.ResolutionMs != 100 {
		t.Errorf("resolution %v, want the backoff cap", sum.Params.ResolutionMs)
	}
	if got := readProbeSummary(t, cfg.Report.Probe.Summary); got.Sent != sum.Sent || got.Partial {
		t.Errorf("written summary %+v", got)
	}
}

// TestProbeCountsNoopFindsApart: with an IDLE that never announces, the NOOP
// poll still finds the messages and says so.
func TestProbeCountsNoopFindsApart(t *testing.T) {
	e := newProbeEnv(t, 50*time.Millisecond, 0)
	e.noopOnly = true
	cfg := e.config(t, func(o *probeOptions) { o.Poll = 100 * time.Millisecond; o.Duration = 600 * time.Millisecond })
	sum, err := probeRun(context.Background(), cfg, defaultDial(), fastTune)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Seen == 0 || sum.SeenByNoop != sum.Seen {
		t.Fatalf("seen %d, by NOOP %d: a broken IDLE must show", sum.Seen, sum.SeenByNoop)
	}
}

// TestAttributeVisible: a message IDLE announced is an IDLE find even when a
// NOOP ran in the same round; only a higher sequence number is a NOOP find.
func TestAttributeVisible(t *testing.T) {
	t0 := time.Unix(1000, 0)
	ev := func(n uint32, ms int) existsEvent {
		return existsEvent{n: n, at: t0.Add(time.Duration(ms) * time.Millisecond)}
	}
	tests := []struct {
		name       string
		idle, noop []existsEvent
		seq        uint32
		wantAt     time.Time
		wantNoop   bool
		wantFound  bool
	}{
		{"idle only", []existsEvent{ev(3, 5)}, nil, 3, t0.Add(5 * time.Millisecond), false, true},
		{"idle and poll ready together, idle covers the seq", []existsEvent{ev(3, 5)}, []existsEvent{ev(3, 9)}, 3, t0.Add(5 * time.Millisecond), false, true},
		{"seq below the idle count", []existsEvent{ev(2, 5), ev(4, 8)}, []existsEvent{ev(4, 9)}, 3, t0.Add(8 * time.Millisecond), false, true},
		{"seq beyond idle is a noop find", []existsEvent{ev(3, 5)}, []existsEvent{ev(4, 9)}, 4, t0.Add(9 * time.Millisecond), true, true},
		{"noop only", nil, []existsEvent{ev(1, 9)}, 1, t0.Add(9 * time.Millisecond), true, true},
		{"no event covers it", []existsEvent{ev(2, 5)}, nil, 3, time.Time{}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at, noop, found := attributeVisible(tt.idle, tt.noop, tt.seq)
			if !at.Equal(tt.wantAt) || noop != tt.wantNoop || found != tt.wantFound {
				t.Errorf("got %v %v %v, want %v %v %v", at, noop, found, tt.wantAt, tt.wantNoop, tt.wantFound)
			}
		})
	}
}

// TestProbeNeverCallsAnIdleAnnouncedMessageANoopFind uses a server whose NOOP
// carries nothing, so every message arrives through IDLE, with a poll short
// enough to be ready together with the announcements.
func TestProbeNeverCallsAnIdleAnnouncedMessageANoopFind(t *testing.T) {
	e := newProbeEnv(t, 5*time.Millisecond, 0)
	e.idleOnly = true
	cfg := e.config(t, func(o *probeOptions) {
		o.Poll = 5 * time.Millisecond
		o.Interval = 30 * time.Millisecond
		o.Duration = 700 * time.Millisecond
	})
	sum, err := probeRun(context.Background(), cfg, defaultDial(), fastTune)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sent < 10 || sum.Seen == 0 {
		t.Fatalf("sent %d seen %d: too little traffic to prove anything", sum.Sent, sum.Seen)
	}
	if sum.SeenByNoop != 0 {
		t.Errorf("%d of %d messages counted as NOOP finds although IDLE announced them", sum.SeenByNoop, sum.Seen)
	}
}

func TestProbeCountsALostMessage(t *testing.T) {
	// The server delivers after the probe has given up on it.
	e := newProbeEnv(t, 2*time.Second, 0)
	cfg := e.config(t, func(o *probeOptions) {
		o.Duration = 300 * time.Millisecond
		o.Interval = 100 * time.Millisecond
		o.Timeout = 400 * time.Millisecond
	})
	sum, err := probeRun(context.Background(), cfg, defaultDial(), fastTune)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sent == 0 || sum.Lost != sum.Sent || sum.Seen != 0 || sum.SearchLost != sum.Sent {
		t.Fatalf("%+v", sum)
	}
}

func TestProbeRefusals(t *testing.T) {
	e := newProbeEnv(t, 0, 0)
	tests := []struct {
		name string
		mod  func(*Config)
	}{
		{"imap address outside the allow list", func(c *Config) { c.Report.Probe.IMAPAddr = "10.9.9.9:143" }},
		{"smtp target outside the allow list", func(c *Config) { c.Targets = []string{"10.9.9.9:25"} }},
		{"sender outside the test domain", func(c *Config) { c.Senders = []string{"x@example.com"} }},
		{"account index past the state file", func(c *Config) { c.Report.Probe.Account = 5 }},
		{"state file readable by others", func(c *Config) {
			p := filepath.Join(t.TempDir(), "wide.state")
			data, _ := os.ReadFile(e.state)
			if err := os.WriteFile(p, data, 0o644); err != nil {
				t.Fatal(err)
			}
			_ = os.Chmod(p, 0o644)
			c.Report.Probe.StateFile = p
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := e.config(t, nil)
			tt.mod(&cfg)
			sum, err := probeRun(context.Background(), cfg, defaultDial(), fastTune)
			if sum != nil || err == nil {
				t.Fatalf("summary %v, err %v: the probe ran", sum, err)
			}
			if strings.Contains(err.Error(), probePW) {
				t.Error("the refusal names the password")
			}
		})
	}
}

func readProbeSummary(t *testing.T, path string) probeSummary {
	t.Helper()
	var s probeSummary
	if err := readJSONFile("test", path, &s); err != nil {
		t.Fatal(err)
	}
	return s
}
