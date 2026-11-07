package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
)

// clientsEnv is a seeded state file plus fakes for IMAP and DAV, all on
// loopback.
type clientsEnv struct {
	t       *testing.T
	cfg     Config
	im      *imapFake
	dav     *davFake
	emails  []string
	pws     []string
	summary string
	dials   atomic.Int64
	deps    clientsDeps
}

// newClientsEnv writes a state file the way seed does and starts the fakes.
// mod adjusts the options; counts left at zero are not run.
func newClientsEnv(t *testing.T, users int, imapMode string, mod func(*clientsOptions)) *clientsEnv {
	t.Helper()
	e := &clientsEnv{t: t, im: newIMAPFake(t, imapMode), dav: newDAVFake(t)}
	spec, err := newPlanSpec("seed.test", testOptions())
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "seed.state")
	st, err := openState(statePath, spec.header())
	if err != nil {
		t.Fatal(err)
	}
	for i := range users {
		pw, err := NewPassword()
		if err != nil {
			t.Fatal(err)
		}
		email := spec.email(i)
		e.emails = append(e.emails, email)
		e.pws = append(e.pws, pw.Reveal())
		if err := st.Pending(i, email, pw.Reveal()); err != nil {
			t.Fatal(err)
		}
		if err := st.UserDone(i); err != nil {
			t.Fatal(err)
		}
		e.im.addUser(email, pw.Reveal())
		e.dav.addUser(email, pw.Reveal())
	}
	_ = st.Close()
	e.summary = filepath.Join(t.TempDir(), "summary.json")
	o := clientsOptions{
		StateFile: statePath, IMAPAddr: e.im.addr(), IMAPTLS: imapTLSPlain,
		DAVAddr: e.dav.addr(), InsecureTLS: true,
		Duration: 2 * time.Second, Timeout: 5 * time.Second, Summary: e.summary, Seed: 7,
		ActiveCycle: 50 * time.Millisecond, FetchCount: 3, BodyShare: 0, FlagEvery: 0,
		DAVInterval: 40 * time.Millisecond, DAVWriteShare: 0, IdleRestart: time.Minute,
	}
	switch imapMode {
	case "starttls", "implicit":
		o.IMAPTLS = imapMode
	}
	if mod != nil {
		mod(&o)
	}
	var allow []Target
	for _, a := range []string{e.im.addr(), e.dav.addr()} {
		tg, err := parseTarget(a)
		if err != nil {
			t.Fatal(err)
		}
		allow = append(allow, tg)
	}
	e.cfg = Config{Allow: allow, TestDomain: "seed.test", Clients: o}
	base := defaultDial()
	e.deps = clientsDeps{dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		e.dials.Add(1)
		return base(ctx, network, addr)
	}}
	return e
}

func (e *clientsEnv) validate() {
	e.t.Helper()
	if err := Validate(e.cfg); err != nil {
		e.t.Fatalf("config refused by Validate: %v", err)
	}
	if err := validateClients(e.cfg); err != nil {
		e.t.Fatalf("config refused by validateClients: %v", err)
	}
}

// run runs to completion and returns the exit code and both streams.
func (e *clientsEnv) run(ctx context.Context) (code int, stdout, stderr string) {
	e.t.Helper()
	e.validate()
	var out, errb bytes.Buffer
	code = clientsMain(ctx, e.cfg, e.deps, &out, &errb)
	return code, out.String(), errb.String()
}

func (e *clientsEnv) readSummary() clientsSummary {
	e.t.Helper()
	data, err := os.ReadFile(e.summary)
	if err != nil {
		e.t.Fatalf("summary not written: %v", err)
	}
	var s clientsSummary
	if err := json.Unmarshal(data, &s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// secrets are every address and password the run could have printed.
func (e *clientsEnv) secrets() []string {
	return append(slices.Clone(e.emails), e.pws...)
}

func (e *clientsEnv) requireNoSecrets(outputs map[string]string) {
	e.t.Helper()
	if len(e.emails) == 0 {
		e.t.Fatal("no secrets to scan for")
	}
	for name, out := range outputs {
		for _, s := range e.secrets() {
			if strings.Contains(out, s) {
				e.t.Errorf("a credential or address appears in %s", name)
			}
		}
	}
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// runUntil starts a run in the background, waits for cond, then interrupts it
// the way SIGINT does and returns what the run produced.
func (e *clientsEnv) runUntil(what string, cond func() bool) (code int, stdout, stderr string) {
	e.t.Helper()
	e.validate()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- clientsMain(ctx, e.cfg, e.deps, &out, &errb) }()
	waitFor(e.t, what, 10*time.Second, cond)
	cancel()
	select {
	case code = <-done:
	case <-time.After(15 * time.Second):
		e.t.Fatal("the run did not stop after the interrupt")
	}
	return code, out.String(), errb.String()
}

func TestClientsIdleHoldsConnectionsAndReIdles(t *testing.T) {
	const n = 5
	e := newClientsEnv(t, 3, "plain", func(o *clientsOptions) {
		o.IMAPIdle = n
		o.IdleRestart = 100 * time.Millisecond
		o.Duration = time.Minute
	})
	// Observed at the server while the run is live: all n connections are in
	// IDLE at once, and each has issued IDLE more than once, so the restart
	// ran rather than the library's 28-minute timer.
	var held atomic.Int64
	code, _, stderr := e.runUntil("every connection to be in IDLE twice", func() bool {
		if e.im.idleNow.Load() == n && e.im.count("IDLE") >= 2*n {
			held.Store(e.im.idleNow.Load())
			return true
		}
		return false
	})
	if code != exitInterrupted {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if held.Load() != n {
		t.Fatalf("%d connections in IDLE at the server, want %d", held.Load(), n)
	}
	s := e.readSummary()
	if s.Idle.MaxOpen != n || s.Idle.Restarts < n || !s.Interrupted {
		t.Errorf("idle summary %+v interrupted=%v", s.Idle, s.Interrupted)
	}
	if s.Idle.Open != 0 {
		t.Errorf("gauge left at %d after shutdown", s.Idle.Open)
	}
	if s.Ops[opLogin].Count != n || s.Ops[opIdleStart].Count < 2*n {
		t.Errorf("login %d, idle_start %d", s.Ops[opLogin].Count, s.Ops[opIdleStart].Count)
	}
	waitFor(t, "the server to see every connection close", 5*time.Second, func() bool { return e.im.idleNow.Load() == 0 })
}

func TestClientsIdleReconnectsAfterTheServerDrops(t *testing.T) {
	const n = 3
	e := newClientsEnv(t, 3, "plain", func(o *clientsOptions) { o.IMAPIdle = n; o.Duration = time.Minute })
	code, _, stderr := e.runUntil("IDLE to be re-established after a drop", func() bool {
		if e.im.idleNow.Load() != n {
			return false
		}
		if e.im.count("DROPPED") == 0 {
			e.im.inc("DROPPED")
			e.im.dropConns()
			return false
		}
		return e.im.count("IDLE") >= 2*n && e.im.idleNow.Load() == n
	})
	if code != exitInterrupted {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	s := e.readSummary()
	if s.Idle.Reconnects < n {
		t.Errorf("%d reconnects after dropping %d connections", s.Idle.Reconnects, n)
	}
	if s.Ops[opIdleHold].Errors["refused_or_closed"] == 0 {
		t.Errorf("a server-closed IDLE was not counted: %+v", s.Ops[opIdleHold])
	}
}

func TestClientsActiveCycleRunsTheListedCommands(t *testing.T) {
	e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) {
		o.IMAPActive = 1
		o.ActiveCycle = time.Hour // one cycle: the first starts at once
		o.Duration = 600 * time.Millisecond
		o.BodyShare = 1
		o.FlagEvery = 1
		o.FetchCount = 3
	})
	e.im.deliver(e.emails[0], 5)
	before := e.im.inspect(e.emails[0], e.pws[0], "INBOX")
	if len(before) != 5 {
		t.Fatalf("setup: %d messages", len(before))
	}
	for _, m := range before {
		if slices.Contains(m.Flags, imap.FlagFlagged) {
			t.Fatal("setup: message already flagged")
		}
	}
	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d, stdout %s, stderr %s", code, stdout, stderr)
	}
	// One cycle, so exactly one of each command, and the STORE changed the
	// stored flag of the newest message only.
	for _, c := range []string{"LIST", "SELECT", "FETCH_HEADERS", "FETCH_BODY"} {
		if got := e.im.count(c); got < 1 {
			t.Errorf("server saw no %s", c)
		}
	}
	if got := e.im.count("STORE"); got != 1 {
		t.Errorf("server saw %d STORE, want 1", got)
	}
	after := e.im.inspect(e.emails[0], e.pws[0], "INBOX")
	for i, m := range after {
		flagged := slices.Contains(m.Flags, imap.FlagFlagged)
		if flagged != (i == len(after)-1) {
			t.Errorf("message %d flagged=%v after the cycle", i, flagged)
		}
	}
	// A second run toggles the same flag back, so a long run does not
	// drift every message to flagged.
	if code, _, stderr := e.run(context.Background()); code != exitOK {
		t.Fatalf("second run: exit %d: %s", code, stderr)
	}
	for i, m := range e.im.inspect(e.emails[0], e.pws[0], "INBOX") {
		if slices.Contains(m.Flags, imap.FlagFlagged) {
			t.Errorf("message %d is still flagged after the second cycle", i)
		}
	}
	s := e.readSummary()
	if s.Active.Cycles != 1 || s.Active.Completed != 1 {
		t.Errorf("active summary %+v", s.Active)
	}
}

func TestClientsActiveFlagsOnTheConfiguredCycle(t *testing.T) {
	e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) {
		o.IMAPActive = 1
		o.Duration = 700 * time.Millisecond
		o.FlagEvery = 3
	})
	e.im.deliver(e.emails[0], 4)
	if code, _, stderr := e.run(context.Background()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	s := e.readSummary()
	if s.Active.Cycles < 6 || s.Active.Cycles != s.Active.Completed {
		t.Fatalf("active summary %+v", s.Active)
	}
	if got, want := e.im.count("STORE"), s.Active.Completed/3; got != want {
		t.Errorf("%d STOREs over %d cycles, want %d (every 3rd)", got, s.Active.Completed, want)
	}
	if got := e.im.count("FETCH_BODY"); got != 0 {
		t.Errorf("%d body fetches with --clients-body-share 0", got)
	}
}

func TestClientsActiveSkipsFetchOnAnEmptyInbox(t *testing.T) {
	e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) {
		o.IMAPActive = 1
		o.Duration = 400 * time.Millisecond
		o.FlagEvery = 1
	})
	if code, _, stderr := e.run(context.Background()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	s := e.readSummary()
	if s.Active.FetchSkippedEmpty == 0 || s.Active.FetchSkippedEmpty != s.Active.Completed {
		t.Errorf("active summary %+v", s.Active)
	}
	if e.im.count("FETCH_HEADERS")+e.im.count("STORE") != 0 {
		t.Error("FETCH or STORE sent for an empty INBOX")
	}
	if e.im.count("LIST") == 0 || e.im.count("SELECT") == 0 {
		t.Error("LIST and SELECT were not sent")
	}
}

func TestClientsDAVWriteShare(t *testing.T) {
	for _, tc := range []struct {
		name  string
		share float64
		check func(t *testing.T, puts, cycles int64)
	}{
		{"none", 0, func(t *testing.T, puts, _ int64) {
			if puts != 0 {
				t.Errorf("%d PUTs with share 0", puts)
			}
		}},
		{"every cycle", 1, func(t *testing.T, puts, cycles int64) {
			if puts != cycles {
				t.Errorf("%d PUTs over %d cycles with share 1", puts, cycles)
			}
		}},
		{"half", 0.5, func(t *testing.T, puts, cycles int64) {
			if puts <= 0 || puts >= cycles {
				t.Errorf("%d PUTs over %d cycles with share 0.5: not a share", puts, cycles)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) {
				o.DAV = 1
				o.Duration = 700 * time.Millisecond
				o.DAVWriteShare = tc.share
			})
			if code, _, stderr := e.run(context.Background()); code != exitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			s := e.readSummary()
			if s.DAV.Cycles < 8 {
				t.Fatalf("only %d cycles", s.DAV.Cycles)
			}
			tc.check(t, e.dav.put.Load(), s.DAV.Cycles)
			if e.dav.propfind.Load() == 0 || e.dav.report.Load() == 0 {
				t.Errorf("PROPFIND %d, REPORT %d", e.dav.propfind.Load(), e.dav.report.Load())
			}
			if s.DAV.Writes != e.dav.put.Load() {
				t.Errorf("summary says %d writes, server saw %d PUTs", s.DAV.Writes, e.dav.put.Load())
			}
			// The tasks calendar refuses events, so none may go there, and
			// the rotation keeps the set of hrefs small.
			paths := e.dav.putPaths()
			seen := map[string]bool{}
			for _, p := range paths {
				seen[p] = true
				if strings.Contains(p, "/tasks/") {
					t.Errorf("an event was written to the tasks calendar: %s", p)
				}
			}
			if len(seen) > 2*davSlots {
				t.Errorf("%d distinct hrefs, want at most %d", len(seen), 2*davSlots)
			}
		})
	}
}

func TestClientsSummaryHasLatencyForEveryOperation(t *testing.T) {
	e := newClientsEnv(t, 3, "plain", func(o *clientsOptions) {
		o.IMAPIdle, o.IMAPActive, o.DAV = 1, 1, 1
		o.Duration = 800 * time.Millisecond
		o.BodyShare, o.FlagEvery, o.DAVWriteShare = 1, 1, 1
		o.IdleRestart = 100 * time.Millisecond
	})
	for _, em := range e.emails {
		e.im.deliver(em, 3)
	}
	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "summary:") {
		t.Errorf("stdout: %s", stdout)
	}
	s := e.readSummary()
	for _, op := range []string{opConnect, opLogin, opList, opSelect, opFetchHeaders, opFetchBody, opStore, opIdleStart, opDAVPropfind, opDAVReport, opDAVPut} {
		o, ok := s.Ops[op]
		if !ok || o.Count == 0 || o.MaxMs <= 0 {
			t.Errorf("op %s: %+v", op, o)
		}
		if o.P50Ms > o.P95Ms || o.P95Ms > o.P99Ms || o.P99Ms > o.MaxMs+1e-9 {
			t.Errorf("op %s quantiles out of order: %+v", op, o)
		}
		if len(o.Errors) != int(numClasses) {
			t.Errorf("op %s: %d error classes, want %d", op, len(o.Errors), numClasses)
		}
	}
	if s.RunID == "" {
		t.Error("no run id")
	}
	info, err := os.Stat(e.summary)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("summary mode %v, %v", info.Mode(), err)
	}
	e.requireNoSecrets(map[string]string{"stdout": stdout, "stderr": stderr, "summary": readFile(t, e.summary)})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClientsAuthFailureOutputHasNoSecrets(t *testing.T) {
	e := newClientsEnv(t, 2, "plain", func(o *clientsOptions) {
		o.IMAPIdle, o.IMAPActive, o.DAV = 1, 1, 1
		o.Duration = time.Second
	})
	// The servers hold different passwords from the state file, so every
	// login is refused: valid input to the negative path.
	for i, em := range e.emails {
		e.im.addUser(em, "server-side-password-"+string(rune('a'+i))+"-000000")
		e.dav.addUser(em, "server-side-password-"+string(rune('a'+i))+"-000000")
	}
	code, stdout, stderr := e.run(context.Background())
	if code != exitConfigOrUnreachable {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if e.im.count("LOGIN_FAILED") == 0 || e.dav.unauthorised.Load() == 0 {
		t.Fatal("the fakes never refused a login, so the check is vacuous")
	}
	s := e.readSummary()
	if s.Ops[opLogin].Errors["auth"] == 0 || s.Ops[opDAVPropfind].Errors["auth"] == 0 {
		t.Errorf("auth errors not counted: login %+v, propfind %+v", s.Ops[opLogin].Errors, s.Ops[opDAVPropfind].Errors)
	}
	// Three refusals in a row end a client, so a wrong password cannot trip
	// the server's lockout for everyone.
	if s.Idle.StoppedAuth != 1 || s.Active.StoppedAuth != 1 || s.DAV.StoppedAuth != 1 {
		t.Errorf("clients not stopped after auth failures: %+v %+v %+v", s.Idle, s.Active, s.DAV)
	}
	if got := e.im.count("LOGIN_FAILED"); got > 3*2+2 {
		t.Errorf("%d refused logins: clients kept trying", got)
	}
	secrets := e.secrets()
	secrets = append(secrets, "server-side-password-a-000000", "server-side-password-b-000000")
	for name, out := range map[string]string{"stdout": stdout, "stderr": stderr, "summary": readFile(t, e.summary)} {
		for _, sec := range secrets {
			if strings.Contains(out, sec) {
				t.Errorf("a credential or address appears in %s", name)
			}
		}
	}
}

func TestClientsDAVServerErrorsAreClassifiedNotPrinted(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusForbidden} {
		e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) {
			o.DAV = 1
			o.Duration = 500 * time.Millisecond
		})
		e.dav.fail.Store(int64(status))
		code, stdout, stderr := e.run(context.Background())
		if code != exitServerBehind {
			t.Fatalf("status %d: exit %d, stderr %s", status, code, stderr)
		}
		s := e.readSummary()
		class := "http_5xx"
		if status == http.StatusForbidden {
			class = "http_4xx"
		}
		if s.Ops[opDAVPropfind].Errors[class] == 0 {
			t.Errorf("status %d: not counted as %s: %+v", status, class, s.Ops[opDAVPropfind].Errors)
		}
		// The body echoed the user name and the path; none of it may appear.
		e.requireNoSecrets(map[string]string{"stdout": stdout, "stderr": stderr, "summary": readFile(t, e.summary)})
		if strings.Contains(stdout+stderr+readFile(t, e.summary), "/caldav/") {
			t.Error("a request path appears in the output")
		}
	}
}

func TestClientsDoNotFollowRedirects(t *testing.T) {
	var hits atomic.Int64
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer elsewhere.Close()
	e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) {
		o.DAV = 1
		o.Duration = 400 * time.Millisecond
	})
	// The other host is allowed here, so a followed redirect would reach it;
	// without that the dial guard would hide a redirect bug.
	other, err := parseTarget(strings.TrimPrefix(elsewhere.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	e.cfg.Allow = append(e.cfg.Allow, other)
	e.dav.redirectTo.Store(elsewhere.URL + "/caldav/")
	code, _, stderr := e.run(context.Background())
	if code != exitConfigOrUnreachable {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if hits.Load() != 0 {
		t.Fatalf("a redirect to a host outside --target-allow was followed %d times", hits.Load())
	}
	if e.readSummary().Ops[opDAVPropfind].Errors["redirect"] == 0 {
		t.Error("the redirect was not counted as a redirect error")
	}
}

func TestClientsTLSModes(t *testing.T) {
	for _, mode := range []string{"starttls", "implicit"} {
		t.Run(mode+" with explicit mode", func(t *testing.T) {
			e := newClientsEnv(t, 1, mode, func(o *clientsOptions) {
				o.IMAPActive = 1
				o.Duration = 300 * time.Millisecond
			})
			if code, _, stderr := e.run(context.Background()); code != exitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			if e.im.count("LOGIN") == 0 {
				t.Error("no login reached the server")
			}
		})
	}
	t.Run("auto picks STARTTLS when it is offered", func(t *testing.T) {
		e := newClientsEnv(t, 1, "starttls", func(o *clientsOptions) {
			o.IMAPActive = 1
			o.IMAPTLS = imapTLSAuto
			o.Duration = 300 * time.Millisecond
		})
		if code, _, stderr := e.run(context.Background()); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if e.im.count("LOGIN") == 0 {
			t.Error("no login reached the server")
		}
	})
	t.Run("auto never sends a password in clear", func(t *testing.T) {
		e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) {
			o.IMAPActive = 1
			o.IMAPTLS = imapTLSAuto
			o.Duration = 300 * time.Millisecond
		})
		code, _, stderr := e.run(context.Background())
		if code != exitConfigOrUnreachable {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		// The server accepts clear logins (InsecureAuth), so a LOGIN here
		// would be the downgrade.
		if n := e.im.count("LOGIN"); n != 0 {
			t.Fatalf("%d LOGIN commands reached a server that offers no TLS", n)
		}
		if e.readSummary().Ops[opConnect].Errors["tls"] == 0 {
			t.Errorf("the refusal was not counted as a TLS error: %+v", e.readSummary().Ops[opConnect].Errors)
		}
	})
	t.Run("a self-signed certificate is refused unless the flag is set", func(t *testing.T) {
		e := newClientsEnv(t, 1, "starttls", func(o *clientsOptions) {
			o.IMAPActive = 1
			o.InsecureTLS = false
			o.Duration = 300 * time.Millisecond
		})
		code, _, _ := e.run(context.Background())
		if code != exitConfigOrUnreachable || e.im.count("LOGIN") != 0 {
			t.Fatalf("exit %d, %d logins: the certificate was accepted", code, e.im.count("LOGIN"))
		}
		if e.readSummary().Ops[opConnect].Errors["tls"] == 0 {
			t.Errorf("the verification failure was not counted as tls: %+v", e.readSummary().Ops[opConnect].Errors)
		}
	})
}

func TestClientsCountRefusedConnectionsSeparately(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var accepted atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = c.Close() // what a connection limit does
		}
	}()
	e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) {
		o.IMAPActive = 1
		o.IMAPAddr = ln.Addr().String()
		o.Duration = 300 * time.Millisecond
	})
	tg, _ := parseTarget(ln.Addr().String())
	e.cfg.Allow = append(e.cfg.Allow, tg)
	code, _, _ := e.run(context.Background())
	if code != exitServerBehind {
		t.Fatalf("exit %d", code)
	}
	if accepted.Load() == 0 {
		t.Fatal("nothing connected")
	}
	if e.readSummary().Ops[opConnect].Errors["refused_or_closed"] == 0 {
		t.Errorf("a closed connection was not counted as refused_or_closed: %+v", e.readSummary().Ops[opConnect].Errors)
	}
}

// TestClientsConnectRefusedIsAConfigurationExit: nothing listens, so nothing
// was measured. It is not the server falling behind.
func TestClientsConnectRefusedIsAConfigurationExit(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) {
		o.IMAPActive = 1
		o.IMAPAddr = addr
		o.Duration = 300 * time.Millisecond
	})
	tg, _ := parseTarget(addr)
	e.cfg.Allow = append(e.cfg.Allow, tg)
	code, _, stderr := e.run(context.Background())
	if code != exitConfigOrUnreachable {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if e.readSummary().Ops[opConnect].Errors["connect_refused"] == 0 {
		t.Errorf("not counted as connect_refused: %+v", e.readSummary().Ops[opConnect].Errors)
	}
	if strings.Contains(stderr, addr) {
		t.Errorf("the address appears on stderr: %s", stderr)
	}
}

func TestClientsAddressesOutsideAllowListMakeNoConnection(t *testing.T) {
	var accepted atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = c.Close()
		}
	}()
	for name, mod := range map[string]func(*clientsOptions){
		"imap": func(o *clientsOptions) { o.IMAPActive = 1; o.IMAPAddr = ln.Addr().String() },
		"dav":  func(o *clientsOptions) { o.DAV = 1; o.DAVAddr = ln.Addr().String() },
	} {
		e := newClientsEnv(t, 1, "plain", mod)
		var out, errb bytes.Buffer
		code := clientsMain(context.Background(), e.cfg, e.deps, &out, &errb)
		if code != exitRefuse || !strings.Contains(errb.String(), "not in --target-allow") {
			t.Errorf("%s: exit %d, stderr %q", name, code, errb.String())
		}
		if e.dials.Load() != 0 || accepted.Load() != 0 {
			t.Errorf("%s: a connection was made", name)
		}
		if _, err := os.Stat(e.summary); err == nil {
			t.Errorf("%s: summary written for a refused run", name)
		}
	}
}

// Every connection of a run is opened through one dialer that checks
// --target-allow first. The runner's dialers are called directly with an
// address that is not allowed, so the wiring is tested and not only the helper.
func TestClientsRunnerDialersAreGuarded(t *testing.T) {
	e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) { o.IMAPActive = 1; o.DAV = 1 })
	var reached atomic.Int64
	e.deps = clientsDeps{dial: func(context.Context, string, string) (net.Conn, error) {
		reached.Add(1)
		return nil, os.ErrInvalid
	}}
	r, err := newClientsRunner(e.cfg, e.deps)
	if err != nil {
		t.Fatal(err)
	}
	for name, dial := range map[string]dialFunc{"runner": r.dial, "imap": r.imap.dial} {
		if _, err := dial(context.Background(), "tcp", "10.9.9.9:143"); err == nil || !strings.Contains(err.Error(), "not in --target-allow") {
			t.Errorf("%s dialer: err %v", name, err)
		}
	}
	if reached.Load() != 0 {
		t.Fatalf("the base dialer was reached %d times for an address outside --target-allow", reached.Load())
	}
	if _, err := r.dial(context.Background(), "tcp", e.cfg.Clients.IMAPAddr); !errors.Is(err, os.ErrInvalid) || reached.Load() != 1 {
		t.Errorf("an allowed address did not reach the base dialer: %v", err)
	}
}

func TestClientsProductionHostNeedsOverride(t *testing.T) {
	e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) { o.IMAPActive = 1; o.IMAPAddr = "mail.example.com:993" })
	tg, _ := parseTarget("mail.example.com:993")
	e.cfg.Allow = append(e.cfg.Allow, tg)
	if err := validateClients(e.cfg); err == nil || !strings.Contains(err.Error(), "production") {
		t.Fatalf("err %v", err)
	}
}

func TestValidateClients(t *testing.T) {
	good := func() Config {
		return Config{
			Allow: []Target{{Host: "127.0.0.1", IP: true}}, TestDomain: "seed.test",
			Clients: clientsOptions{
				IMAPActive: 1, IMAPIdle: 1, DAV: 1, StateFile: "s", IMAPAddr: "127.0.0.1:143", IMAPTLS: imapTLSAuto,
				DAVAddr: "127.0.0.1:443", Duration: time.Second, Timeout: time.Second, Summary: "x.json",
				ActiveCycle: time.Second, FetchCount: 1, DAVInterval: time.Second, IdleRestart: time.Second,
			},
		}
	}
	if err := validateClients(good()); err != nil {
		t.Fatalf("good config refused: %v", err)
	}
	for name, mod := range map[string]func(*clientsOptions){
		"no clients":           func(o *clientsOptions) { o.IMAPIdle, o.IMAPActive, o.DAV = 0, 0, 0 },
		"negative idle":        func(o *clientsOptions) { o.IMAPIdle = -1 },
		"too many idle":        func(o *clientsOptions) { o.IMAPIdle = maxClientsPerKind + 1 },
		"too many dav":         func(o *clientsOptions) { o.DAV = maxClientsPerKind + 1 },
		"no state file":        func(o *clientsOptions) { o.StateFile = "" },
		"no imap address":      func(o *clientsOptions) { o.IMAPAddr = "" },
		"no dav address":       func(o *clientsOptions) { o.DAVAddr = "" },
		"imap not allowed":     func(o *clientsOptions) { o.IMAPAddr = "10.9.9.9:143" },
		"dav not allowed":      func(o *clientsOptions) { o.DAVAddr = "10.9.9.9:443" },
		"bad tls mode":         func(o *clientsOptions) { o.IMAPTLS = "maybe" },
		"body share high":      func(o *clientsOptions) { o.BodyShare = 1.5 },
		"write share negative": func(o *clientsOptions) { o.DAVWriteShare = -0.1 },
		"zero duration":        func(o *clientsOptions) { o.Duration = 0 },
		"zero timeout":         func(o *clientsOptions) { o.Timeout = 0 },
		"zero cycle":           func(o *clientsOptions) { o.ActiveCycle = 0 },
		"zero interval":        func(o *clientsOptions) { o.DAVInterval = 0 },
		"zero idle restart":    func(o *clientsOptions) { o.IdleRestart = 0 },
		"negative ramp":        func(o *clientsOptions) { o.Ramp = -time.Second },
		"fetch count zero":     func(o *clientsOptions) { o.FetchCount = 0 },
		"fetch count huge":     func(o *clientsOptions) { o.FetchCount = maxFetchCount + 1 },
		"negative flag every":  func(o *clientsOptions) { o.FlagEvery = -1 },
		"no summary path":      func(o *clientsOptions) { o.Summary = "" },
	} {
		cfg := good()
		mod(&cfg.Clients)
		if err := validateClients(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Only the addresses of the kinds in use are required.
	cfg := good()
	cfg.Clients.IMAPIdle, cfg.Clients.IMAPActive, cfg.Clients.IMAPAddr = 0, 0, ""
	if err := validateClients(cfg); err != nil {
		t.Errorf("dav only: %v", err)
	}
}

func TestClientsFlagsAndHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"clients", "-h"}, &out, &errb); code != exitOK {
		t.Fatalf("-h exit %d", code)
	}
	help := out.String() + errb.String()
	for _, want := range []string{"-imap-idle", "-imap-active", "-dav", "-clients-state-file", "-clients-insecure-tls",
		"imap.max_connections_per_ip", "imap.max_connections", "refused_or_closed", "-clients-idle-restart", "four fixed hrefs"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	if strings.Contains(help, "%!") {
		t.Errorf("help has a format error:\n%s", help)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"clients", "--imap-idle", "1"}, &out, &errb); code != exitRefuse || !strings.Contains(errb.String(), "refused") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
	// The other subcommands do not see these flags.
	if code := run([]string{"check", "--imap-idle", "1"}, &out, &errb); code != exitRefuse {
		t.Errorf("check accepted a clients flag: exit %d", code)
	}
}

func TestClientsInterruptWritesSummaryAndExits1(t *testing.T) {
	e := newClientsEnv(t, 1, "plain", func(o *clientsOptions) { o.IMAPActive = 1; o.Duration = time.Minute })
	e.im.deliver(e.emails[0], 2)
	code, _, stderr := e.runUntil("a completed cycle", func() bool { return e.im.count("SELECT") >= 2 })
	if code != exitInterrupted || !strings.Contains(stderr, "interrupted") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if s := e.readSummary(); !s.Interrupted || s.Ops[opLogin].Count == 0 {
		t.Errorf("summary %+v", s.Params)
	}
}

func TestClientsDeterministicChoices(t *testing.T) {
	r := &clientsRunner{cfg: clientsOptions{Seed: 9}}
	a, b := r.rngFor(kindDAV, 3), r.rngFor(kindDAV, 3)
	c := r.rngFor(kindDAV, 4)
	same, diff := true, false
	for range 20 {
		x, y, z := a.Float64(), b.Float64(), c.Float64()
		same = same && x == y
		diff = diff || x != z
	}
	if !same || !diff {
		t.Errorf("same-stream equal=%v, other-stream differs=%v", same, diff)
	}
}

func TestClientsAccountAssignment(t *testing.T) {
	accts := []account{{index: 0}, {index: 1}, {index: 2}}
	r := &clientsRunner{cfg: clientsOptions{IMAPIdle: 2, IMAPActive: 2}, accts: accts}
	got := []int{r.account(kindIdle, 0).index, r.account(kindIdle, 1).index, r.account(kindActive, 0).index,
		r.account(kindActive, 1).index, r.account(kindDAV, 0).index, r.account(kindDAV, 5).index}
	if want := []int{0, 1, 2, 0, 1, 0}; !slices.Equal(got, want) {
		t.Errorf("accounts %v, want %v", got, want)
	}
}

func TestClassifyErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		err   error
		login bool
		want  errClass
	}{
		"login no":      {&imap.Error{Type: imap.StatusResponseTypeNo, Text: "nope"}, true, classAuth},
		"select no":     {&imap.Error{Type: imap.StatusResponseTypeNo}, false, classProtocol},
		"bad":           {&imap.Error{Type: imap.StatusResponseTypeBad}, false, classProtocol},
		"auth code":     {&imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeAuthenticationFailed}, false, classAuth},
		"timeout":       {errIMAPTimeout, false, classTimeout},
		"closed":        {errServerClosed, false, classRefusedOrClosed},
		"refused":       {&net.OpError{Op: "dial", Err: os.ErrNotExist}, false, classRefusedOrClosed},
		"conn refused":  {&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, false, classConnectRefused},
		"conn reset":    {&net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}, false, classRefusedOrClosed},
		"unknown ca":    {fmt.Errorf("dial: %w", x509.UnknownAuthorityError{}), false, classTLS},
		"bad hostname":  {fmt.Errorf("dial: %w", x509.HostnameError{Host: "h"}), false, classTLS},
		"verify":        {&net.OpError{Op: "dial", Err: &tls.CertificateVerificationError{}}, false, classTLS},
		"no starttls":   {fmt.Errorf("%w: x", errNoStartTLS), false, classTLS},
		"something odd": {os.ErrInvalid, false, classOther},
	} {
		if got := classifyIMAP(tc.err, tc.login); got != tc.want {
			t.Errorf("%s: class %s, want %s", name, classNames[got], classNames[tc.want])
		}
	}
}
