package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/emersion/go-smtp"
)

const testDomain = "loadtest.test"

type recvMsg struct {
	at       time.Time
	id, mid  string
	sent     string
	rcptHead string
}

// testServer is an in-process go-smtp server on loopback. The recipient's
// local part picks the behaviour: ok accepts, defer answers 451, reject
// answers 550, slow stalls past any sane timeout, lag accepts after 100ms.
type testServer struct {
	addr    string
	accepts atomic.Int64
	stop    chan struct{}

	mu   sync.Mutex
	msgs []recvMsg
}

type countListener struct {
	net.Listener
	n *atomic.Int64
}

func (l countListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.n.Add(1)
	}
	return c, err
}

type testBackend struct{ s *testServer }

func (b testBackend) NewSession(*smtp.Conn) (smtp.Session, error) {
	return &testSession{s: b.s}, nil
}

type testSession struct {
	s     *testServer
	local string
}

func (t *testSession) Reset()        { t.local = "" }
func (t *testSession) Logout() error { return nil }
func (t *testSession) Mail(string, *smtp.MailOptions) error {
	return nil
}

func (t *testSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	t.local, _, _ = strings.Cut(to, "@")
	switch t.local {
	case "defer":
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "try later"}
	case "reject":
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "no such user"}
	}
	return nil
}

func (t *testSession) Data(r io.Reader) error {
	switch t.local {
	case "slow":
		select {
		case <-t.s.stop:
		case <-time.After(5 * time.Second):
		}
		return nil
	case "lag":
		time.Sleep(100 * time.Millisecond)
	}
	m, err := mail.ReadMessage(r)
	if err != nil {
		return err
	}
	t.s.mu.Lock()
	t.s.msgs = append(t.s.msgs, recvMsg{
		at:   time.Now(),
		id:   m.Header.Get("X-Loadgen-Id"),
		mid:  m.Header.Get("Message-ID"),
		sent: m.Header.Get("X-Loadgen-Sent"),
	})
	t.s.mu.Unlock()
	return nil
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ts := &testServer{addr: ln.Addr().String(), stop: make(chan struct{})}
	srv := smtp.NewServer(testBackend{ts})
	srv.Domain = "server.test"
	srv.AllowInsecureAuth = true
	srv.MaxRecipients = 10
	go func() { _ = srv.Serve(countListener{ln, &ts.accepts}) }()
	t.Cleanup(func() {
		close(ts.stop)
		_ = srv.Close()
	})
	return ts
}

func (ts *testServer) received() []recvMsg {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return slices.Clone(ts.msgs)
}

func (ts *testServer) count() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.msgs)
}

func testConfig(t *testing.T, target, rcptLocal string, sc smtpConfig) Config {
	t.Helper()
	if sc.Workers == 0 {
		sc.Workers = 8
	}
	if sc.Timeout == 0 {
		sc.Timeout = 2 * time.Second
	}
	if sc.Size == 0 {
		sc.Size = 256
	}
	if sc.Summary == "" {
		sc.Summary = filepath.Join(t.TempDir(), "summary.json")
	}
	return Config{
		Allow:      []Target{{Host: "127.0.0.1", IP: true}},
		Targets:    []string{target},
		TestDomain: testDomain,
		Senders:    []string{"sender@" + testDomain},
		Recipients: []string{rcptLocal + "@" + testDomain},
		SMTP:       sc,
	}
}

// loose opts keep a healthy run from tripping the saturation checks on a
// loaded machine.
func looseOpts() smtpOpts {
	return smtpOpts{lagThreshold: 500 * time.Millisecond, sampleEvery: 100 * time.Millisecond}
}

func runTest(t *testing.T, cfg Config, opts smtpOpts) *smtpSummary {
	t.Helper()
	sched, err := validateSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := smtpRun(context.Background(), cfg, sched, opts)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func readSummary(t *testing.T, path string) smtpSummary {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s smtpSummary
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("summary is not JSON: %v", err)
	}
	return s
}

func within(got, want int, frac float64, slack int) bool {
	d := float64(got - want)
	if d < 0 {
		d = -d
	}
	return d <= max(frac*float64(want), float64(slack))
}

func TestSMTPRateHeld(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 150, Duration: 3 * time.Second})
	sum := runTest(t, cfg, looseOpts())

	got := ts.count()
	if !within(got, 450, 0.05, 0) {
		t.Errorf("server received %d, want 450 within 5%%", got)
	}
	if !sum.RateHeld {
		t.Errorf("rate_held false: %+v", sum.Steps)
	}
	if sum.GeneratorSaturated || !sum.Valid {
		t.Errorf("healthy run reported saturated: %v", sum.SaturationReasons)
	}
	if sum.PoolExhausted {
		t.Error("healthy run reported pool exhausted")
	}
	if !sum.Reconciled {
		t.Errorf("counters do not reconcile: %+v due %d", sum.Counts, sum.Due)
	}
	if int(sum.Counts.OK) != got {
		t.Errorf("ok counter %d, server received %d", sum.Counts.OK, got)
	}
	disk := readSummary(t, cfg.SMTP.Summary)
	if disk.RunID != sum.RunID || disk.Partial {
		t.Errorf("summary file differs from the run: %+v", disk)
	}
}

func TestSMTPRampSteps(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "ok", smtpConfig{Ramp: "50:50:1s:150", Duration: 3500 * time.Millisecond})
	sum := runTest(t, cfg, looseOpts())

	if len(sum.Steps) != 3 {
		t.Fatalf("steps = %+v, want 3", sum.Steps)
	}
	for i, want := range []float64{50, 100, 150} {
		if sum.Steps[i].TargetRate != want {
			t.Errorf("step %d rate = %v, want %v", i, sum.Steps[i].TargetRate, want)
		}
	}
	// Count what the server saw in each window, by its own clock, not what
	// the generator says it did.
	counts := make([]int, 3)
	for _, m := range ts.received() {
		el := m.at.Sub(sum.Start)
		switch {
		case el < time.Second:
			counts[0]++
		case el < 2*time.Second:
			counts[1]++
		case el <= 3500*time.Millisecond+100*time.Millisecond:
			counts[2]++
		}
	}
	for i, want := range []int{50, 100, 225} {
		if !within(counts[i], want, 0.1, 5) {
			t.Errorf("server saw %d in step %d, want %d", counts[i], i, want)
		}
	}
	if !sum.RateHeld {
		t.Errorf("ramp did not hold: %+v", sum.Steps)
	}
	if !sum.Steps[2].Full || !sum.Steps[2].Held {
		t.Errorf("rate at max not held: %+v", sum.Steps[2])
	}
}

func TestScheduleMath(t *testing.T) {
	r, err := parseRamp("10:10:2s:30")
	if err != nil {
		t.Fatal(err)
	}
	s := rampSchedule(r)
	s500 := func(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }
	tests := []struct {
		at   time.Duration
		rate float64
		due  int64
	}{
		{0, 10, 0},
		{s500(1000), 10, 10},
		{s500(1999), 10, 19},
		{s500(2000), 20, 20},
		{s500(3000), 20, 40},
		{s500(4000), 30, 60},
		{s500(5000), 30, 90},
		{s500(100000), 30, 20 + 40 + 30*96},
	}
	for _, tt := range tests {
		if got := s.rateAt(tt.at); got != tt.rate {
			t.Errorf("rateAt(%v) = %v, want %v", tt.at, got, tt.rate)
		}
		if got := s.dueCount(tt.at); got != tt.due {
			t.Errorf("dueCount(%v) = %d, want %d", tt.at, got, tt.due)
		}
	}
	// dueTime inverts dueCount: message n is due exactly when the count
	// reaches n.
	for n := int64(1); n < 200; n++ {
		at := s.dueTime(n)
		if got := s.dueCount(at + time.Microsecond); got < n {
			t.Fatalf("dueCount at dueTime(%d)=%v is %d", n, at, got)
		}
		if got := s.dueCount(at - time.Microsecond); got >= n {
			t.Fatalf("dueCount just before dueTime(%d)=%v is %d", n, at, got)
		}
	}
	c := constSchedule(150)
	if got := c.dueCount(3 * time.Second); got != 450 {
		t.Errorf("constant 150/s over 3s = %d", got)
	}
	if got := len(s.windows()); got != 3 {
		t.Errorf("windows = %d, want 3", got)
	}
}

func TestParseRamp(t *testing.T) {
	tests := []struct {
		in      string
		wantErr string
	}{
		{"50:50:30s:500", ""},
		{"50:50:30s", "start:step:interval:max"},
		{"0:50:30s:500", "above 0"},
		{"50:0:30s:500", "step"},
		{"50:50:500ms:500", "below 1s"},
		{"600:50:30s:500", "above max"},
		{"50:50:30s:999999", "cap"},
		{"1:0.001:30s:100", "steps"},
		{"x:50:30s:500", "not a number"},
		{"50:50:soon:500", "interval"},
	}
	for _, tt := range tests {
		_, err := parseRamp(tt.in)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("%q: %v", tt.in, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("%q: err %v, want %q", tt.in, err, tt.wantErr)
		}
	}
}

func TestValidateSMTP(t *testing.T) {
	base := func() Config {
		return Config{
			Targets: []string{"127.0.0.1:25"}, Senders: []string{"a@" + testDomain}, Recipients: []string{"b@" + testDomain},
			SMTP: smtpConfig{Rate: 10, Duration: time.Minute, Workers: 4, Timeout: time.Second, Size: 10, Summary: "x.json"},
		}
	}
	tests := []struct {
		name    string
		mut     func(*Config)
		wantErr string
	}{
		{"ok", func(*Config) {}, ""},
		{"ramp ok", func(c *Config) { c.SMTP.Rate = 0; c.SMTP.Ramp = "5:5:1s:20" }, ""},
		{"neither", func(c *Config) { c.SMTP.Rate = 0 }, "required"},
		{"both", func(c *Config) { c.SMTP.Ramp = "5:5:1s:20" }, "mutually exclusive"},
		{"negative rate", func(c *Config) { c.SMTP.Rate = -1 }, "above 0"},
		{"huge rate", func(c *Config) { c.SMTP.Rate = 1e9 }, "cap"},
		{"no target", func(c *Config) { c.Targets = nil }, "--target"},
		{"no from", func(c *Config) { c.Senders = nil }, "--from"},
		{"no rcpt", func(c *Config) { c.Recipients = nil }, "--rcpt"},
		{"no workers", func(c *Config) { c.SMTP.Workers = 0 }, "workers"},
		{"no duration", func(c *Config) { c.SMTP.Duration = 0 }, "duration"},
		{"no timeout", func(c *Config) { c.SMTP.Timeout = 0 }, "timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mut(&cfg)
			_, err := validateSMTP(cfg)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatal(err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSMTPCommandRefusesMissingLists(t *testing.T) {
	args := []string{"smtp", "--target-allow", "127.0.0.1", "--target", "127.0.0.1:25", "--test-domain", testDomain, "--rate", "10"}
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitRefuse || !strings.Contains(errb.String(), "--from") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
}

// TestSMTPCounters sends only failing mail and checks each failure lands in
// its own counter and nowhere else.
func TestSMTPCounters(t *testing.T) {
	closed := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		a := ln.Addr().String()
		_ = ln.Close()
		return a
	}()
	tests := []struct {
		name   string
		target string // empty means the test server
		rcpt   string
		get    func(summaryCounts) int64
	}{
		{"ok", "", "ok", func(c summaryCounts) int64 { return c.OK }},
		{"4xx", "", "defer", func(c summaryCounts) int64 { return c.Deferred4xx }},
		{"5xx", "", "reject", func(c summaryCounts) int64 { return c.Rejected5xx }},
		{"timeout", "", "slow", func(c summaryCounts) int64 { return c.TimedOut }},
		{"connection", closed, "ok", func(c summaryCounts) int64 { return c.ConnErrors }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			target := tt.target
			if target == "" {
				target = ts.addr
			}
			sc := smtpConfig{Rate: 40, Duration: time.Second}
			if tt.rcpt == "slow" {
				sc.Timeout = 150 * time.Millisecond
				sc.Workers = 40
			}
			sum := runTest(t, testConfig(t, target, tt.rcpt, sc), looseOpts())
			c := sum.Counts
			if tt.get(c) == 0 {
				t.Fatalf("own counter is zero: %+v", c)
			}
			all := c.OK + c.Rejected5xx + c.Deferred4xx + c.TimedOut + c.ConnErrors + c.OtherErrors
			if all != tt.get(c) {
				t.Errorf("another counter moved: %+v", c)
			}
			if !sum.Reconciled || sum.Due != all+c.Unsent {
				t.Errorf("due %d != %+v", sum.Due, c)
			}
		})
	}
}

func TestSMTPIDsUnique(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 100, Duration: 1500 * time.Millisecond})
	sum := runTest(t, cfg, looseOpts())
	msgs := ts.received()
	if int64(len(msgs)) != sum.Counts.OK || len(msgs) == 0 {
		t.Fatalf("received %d, ok %d", len(msgs), sum.Counts.OK)
	}
	ids, mids := map[string]bool{}, map[string]bool{}
	for _, m := range msgs {
		if !strings.HasPrefix(m.id, sum.RunID+"-") {
			t.Fatalf("id %q lacks run id %q", m.id, sum.RunID)
		}
		if !strings.HasSuffix(m.mid, "@"+testDomain+">") {
			t.Fatalf("Message-ID %q is not on the test domain", m.mid)
		}
		if _, err := time.Parse(time.RFC3339Nano, m.sent); err != nil {
			t.Fatalf("X-Loadgen-Sent %q: %v", m.sent, err)
		}
		ids[m.id], mids[m.mid] = true, true
	}
	if len(ids) != len(msgs) || len(mids) != len(msgs) {
		t.Errorf("duplicates: %d messages, %d ids, %d message-ids", len(msgs), len(ids), len(mids))
	}
}

// TestSMTPStarvedGeneratorIsSaturated starves the scheduler while the server
// is fast and idle workers wait. The run must be flagged for the generator,
// and not as pool exhaustion.
func TestSMTPStarvedGeneratorIsSaturated(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 100, Duration: 2500 * time.Millisecond})
	opts := looseOpts()
	opts.lagThreshold = 250 * time.Millisecond
	opts.tickHook = func() { time.Sleep(400 * time.Millisecond) }
	sum := runTest(t, cfg, opts)
	if !sum.GeneratorSaturated || sum.Valid {
		t.Fatalf("starved generator not flagged: %+v", sum)
	}
	if !slices.Contains(sum.SaturationReasons, "scheduler_lag") {
		t.Errorf("reasons = %v, want scheduler_lag", sum.SaturationReasons)
	}
	if sum.PoolExhausted {
		t.Error("starvation was blamed on the pool")
	}
	if sum.SchedulerLag.MaxMillis < 250 {
		t.Errorf("max lag %vms was not recorded", sum.SchedulerLag.MaxMillis)
	}
}

// TestSMTPSlowServerIsNotGeneratorSaturation is the contrast: a slow server
// and a small pool miss the rate, and that is reported as the server's doing.
func TestSMTPSlowServerIsNotGeneratorSaturation(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "lag", smtpConfig{Rate: 100, Duration: 2 * time.Second, Workers: 2})
	sum := runTest(t, cfg, looseOpts())
	if sum.RateHeld {
		t.Error("rate_held true although two workers cannot send 100/s at 100ms each")
	}
	if !sum.PoolExhausted {
		t.Errorf("pool_exhausted false (%.2fs)", sum.PoolExhaustedSeconds)
	}
	if sum.GeneratorSaturated {
		t.Errorf("slow server blamed on the generator: %v", sum.SaturationReasons)
	}
	if sum.Counts.Unsent == 0 || !sum.Reconciled {
		t.Errorf("counts %+v due %d", sum.Counts, sum.Due)
	}
}

func TestSMTPCPUSaturation(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 50, Duration: time.Second})
	opts := looseOpts()
	t0 := time.Now()
	// A process that used every core for the whole run.
	opts.cpuNow = func() (time.Duration, error) {
		return time.Since(t0) * time.Duration(runtime.GOMAXPROCS(0)), nil
	}
	sum := runTest(t, cfg, opts)
	if !sum.GeneratorSaturated || !slices.Contains(sum.SaturationReasons, "cpu") {
		t.Fatalf("cpu saturation not flagged: %v", sum.SaturationReasons)
	}
	if !sum.CPU.Available || sum.CPU.MaxShare < 0.9 {
		t.Errorf("cpu report = %+v", sum.CPU)
	}
}

func TestSMTPInterruptWritesSummary(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 100, Duration: time.Minute})
	sched, err := validateSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for ts.count() < 20 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	sum, err := smtpRun(ctx, cfg, sched, looseOpts())
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("cancel did not stop the run")
	}
	disk := readSummary(t, cfg.SMTP.Summary)
	if !disk.Interrupted || disk.Counts.OK == 0 || disk.Partial {
		t.Errorf("summary after interrupt: %+v", disk)
	}
	if !sum.Reconciled {
		t.Errorf("counts do not reconcile after interrupt: %+v due %d", sum.Counts, sum.Due)
	}
}

const helperEnv = "LOADGEN_SMTP_HELPER_ARGS"

// TestSMTPHelperProcess is the child of TestSMTPSigterm; it does nothing in a
// normal run.
func TestSMTPHelperProcess(t *testing.T) {
	args := os.Getenv(helperEnv)
	if args == "" {
		t.Skip("helper for TestSMTPSigterm")
	}
	os.Exit(run(strings.Split(args, "\x1f"), os.Stdout, os.Stderr))
}

// TestSMTPSigterm proves the real signal wiring: a separate process receives
// SIGTERM mid-run, exits non-zero and leaves its counts behind.
func TestSMTPSigterm(t *testing.T) {
	ts := newTestServer(t)
	summary := filepath.Join(t.TempDir(), "sig.json")
	args := []string{"smtp", "--target-allow", "127.0.0.1", "--target", ts.addr, "--test-domain", testDomain,
		"--from", "s@" + testDomain, "--rcpt", "ok@" + testDomain, "--rate", "50", "--smtp-duration", "60s",
		"--smtp-summary", summary, "--smtp-workers", "4"}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSMTPHelperProcess$") // #nosec G204 -- re-executes this test binary
	cmd.Env = append(os.Environ(), helperEnv+"="+strings.Join(args, "\x1f"))
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for ts.count() < 10 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if ts.count() < 10 {
		_ = cmd.Process.Kill()
		t.Fatalf("child sent nothing: %s", errb.String())
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != exitInterrupted {
			t.Fatalf("exit = %v, want code %d; stderr %s", err, exitInterrupted, errb.String())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child did not exit after SIGTERM")
	}
	disk := readSummary(t, summary)
	if !disk.Interrupted || disk.Counts.OK == 0 {
		t.Errorf("summary after SIGTERM: %+v", disk)
	}
}

func TestSMTPRefusesBeforeDialling(t *testing.T) {
	ts := newTestServer(t)
	good := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 50, Duration: time.Second})
	sched, err := validateSMTP(good)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		mut  func(*Config)
	}{
		{"target outside allow list", func(c *Config) { c.Allow = []Target{{Host: "10.9.9.9", IP: true}} }},
		{"second target outside allow list", func(c *Config) { c.Targets = append(c.Targets, "10.9.9.9:25") }},
		{"sender outside test domain", func(c *Config) { c.Senders = []string{"a@other.test"} }},
		{"recipient outside test domain", func(c *Config) { c.Recipients = []string{"a@other.test"} }},
		{"production domain", func(c *Config) { c.TestDomain = "example.com" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := good
			cfg.Targets = slices.Clone(good.Targets)
			tt.mut(&cfg)
			if sum, err := smtpRun(context.Background(), cfg, sched, smtpOpts{}); err == nil || sum != nil {
				t.Fatalf("run was not refused: %v", err)
			}
			if n := ts.accepts.Load(); n != 0 {
				t.Fatalf("server accepted %d connections", n)
			}
		})
	}
}

// TestSenderRedialIsGated proves the gate sits on the dial itself, so a
// redial after an error cannot reach a target the allow list no longer
// covers.
func TestSenderRedialIsGated(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 1, Duration: time.Second})
	cfg.Allow = []Target{{Host: "10.9.9.9", IP: true}}
	r := &runner{cfg: cfg, domain: testDomain, runCtx: context.Background()}
	s := &sender{r: r, addr: ts.addr}
	err := s.dial()
	if err == nil || classify(err) != outcomeConn {
		t.Fatalf("dial err = %v", err)
	}
	if n := ts.accepts.Load(); n != 0 {
		t.Fatalf("server accepted %d connections", n)
	}
}

func TestClassify(t *testing.T) {
	timeout := &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}
	tests := []struct {
		name string
		err  error
		want outcome
	}{
		{"451", &smtp.SMTPError{Code: 451}, outcomeDeferred},
		{"421 wrapped", fmt.Errorf("x: %w", &smtp.SMTPError{Code: 421}), outcomeDeferred},
		{"550", &smtp.SMTPError{Code: 550}, outcomeRejected},
		{"554", &smtp.SMTPError{Code: 554}, outcomeRejected},
		{"unexpected 2xx", &smtp.SMTPError{Code: 250}, outcomeOther},
		{"timeout", timeout, outcomeTimedOut},
		{"EOF", io.EOF, outcomeConn},
		{"reset", fmt.Errorf("w: %w", syscall.ECONNRESET), outcomeConn},
		{"dial timeout is conn", &dialError{timeout}, outcomeConn},
		{"554 greeting is conn", &dialError{&smtp.SMTPError{Code: 554}}, outcomeConn},
		{"unknown", errors.New("boom"), outcomeOther},
	}
	for _, tt := range tests {
		if got := classify(tt.err); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestBuildMessage(t *testing.T) {
	sent := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	msg := buildMessage("abc", 7, testDomain, "a@"+testDomain, "b@"+testDomain, sent, fillerBody(100))
	if bytes.Contains(bytes.ReplaceAll(msg, []byte("\r\n"), nil), []byte("\n")) {
		t.Error("bare LF in message")
	}
	m, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"X-Loadgen-Id":   "abc-7",
		"Message-ID":     "<abc.7@" + testDomain + ">",
		"X-Loadgen-Sent": sent.Format(time.RFC3339Nano),
	} {
		if got := m.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if got := len(fillerBody(1000)); got != 1000 {
		t.Errorf("filler = %d bytes", got)
	}
}

func TestHist(t *testing.T) {
	var h hist
	if h.quantile(0.99) != 0 {
		t.Error("empty histogram has a quantile")
	}
	for range 99 {
		h.add(time.Millisecond)
	}
	h.add(time.Second)
	if p := h.quantile(0.5); p > 2*time.Millisecond {
		t.Errorf("p50 = %v", p)
	}
	if h.maximum() != time.Second {
		t.Errorf("max = %v", h.maximum())
	}
	if p := h.quantile(1); p < 500*time.Millisecond {
		t.Errorf("p100 = %v", p)
	}
}

func TestWriteSummaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	if err := writeSummaryFile(path, &smtpSummary{RunID: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := writeSummaryFile(path, &smtpSummary{RunID: "y"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
	if got := readSummary(t, path); got.RunID != "y" {
		t.Errorf("run id = %q", got.RunID)
	}
}
