package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newPOP3Env is a clients env that runs POP3 clients only, against a fake in
// the given mode, with every seeded account known to the fake.
func newPOP3Env(t *testing.T, users int, mode string, mod func(*clientsOptions, *pop3Fake)) (*clientsEnv, *pop3Fake) {
	t.Helper()
	f := newPOP3Fake(t, mode)
	e := newClientsEnv(t, users, "plain", func(o *clientsOptions) {
		o.POP3 = 1
		o.POP3Addr = f.addr()
		o.POP3TLS = pop3TLSStartTLS
		if mode == "implicit" {
			o.POP3TLS = pop3TLSImplicit
		}
		if mode == "nostls" {
			o.POP3TLS = pop3TLSPlain
		}
		o.POP3Cycle = 40 * time.Millisecond
		o.RetrCount = 3
		o.Duration = 500 * time.Millisecond
		if mod != nil {
			mod(o, f)
		}
	})
	for i, em := range e.emails {
		f.addUser(em, e.pws[i])
	}
	tg, err := parseTarget(f.addr())
	if err != nil {
		t.Fatal(err)
	}
	e.cfg.Allow = append(e.cfg.Allow, tg)
	return e, f
}

func TestPOP3CycleRunsTheListedCommandsOverSTLS(t *testing.T) {
	e, f := newPOP3Env(t, 1, "stls", nil)
	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, v := range []string{"STLS", "USER", "PASS", "STAT", "LIST", "RETR", "QUIT"} {
		if f.count(v) == 0 {
			t.Errorf("%s never sent", v)
		}
	}
	// Nothing past the greeting travels in clear.
	var credentials int
	for _, c := range f.commands() {
		if c.verb == "USER" || c.verb == "PASS" {
			credentials++
			if !c.tls {
				t.Fatalf("%s arrived without TLS", c.verb)
			}
		}
	}
	if credentials == 0 {
		t.Fatal("no credentials seen, so the TLS check is vacuous")
	}
	// The newest three of five messages.
	for _, c := range f.commands() {
		if c.verb == "RETR" && c.arg != "3" && c.arg != "4" && c.arg != "5" {
			t.Errorf("RETR %s is not among the newest three", c.arg)
		}
	}
	s := e.readSummary()
	if s.POP3.Completed == 0 || s.POP3.Retrieved != s.POP3.Completed*3 {
		t.Errorf("pop3 summary %+v", s.POP3)
	}
	if s.POP3.Deleted != 0 || f.count("DELE") != 0 {
		t.Errorf("DELE sent at the default share: %+v", s.POP3)
	}
	if s.Params.POP3 != 1 || s.Params.RetrCount != 3 || s.Params.POP3TLS != pop3TLSStartTLS {
		t.Errorf("params %+v", s.Params)
	}
	e.requireNoSecrets(map[string]string{"stdout": stdout, "stderr": stderr, "summary": readFile(t, e.summary)})
}

func TestPOP3ImplicitTLS(t *testing.T) {
	e, f := newPOP3Env(t, 1, "implicit", nil)
	if code, _, stderr := e.run(context.Background()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if f.count("PASS") == 0 || f.count("STLS") != 0 {
		t.Errorf("PASS %d, STLS %d", f.count("PASS"), f.count("STLS"))
	}
}

func TestPOP3AutoRefusesAServerWithoutSTLS(t *testing.T) {
	e, f := newPOP3Env(t, 1, "nostls", func(o *clientsOptions, _ *pop3Fake) { o.POP3TLS = pop3TLSAuto })
	code, stdout, stderr := e.run(context.Background())
	if code != exitConfigOrUnreachable {
		t.Fatalf("exit %d, want %d: %s", code, exitConfigOrUnreachable, stderr)
	}
	if f.count("CAPA") == 0 {
		t.Fatal("the server was never probed, so the refusal proves nothing")
	}
	if f.count("USER") != 0 || f.count("PASS") != 0 {
		t.Error("a credential was sent to a server that offers no TLS")
	}
	s := e.readSummary()
	if s.Ops[opPOP3Connect].Errors["tls"] == 0 {
		t.Errorf("not classified as tls: %+v", s.Ops[opPOP3Connect].Errors)
	}
	e.requireNoSecrets(map[string]string{"stdout": stdout, "stderr": stderr})
}

func TestPOP3AutoUsesSTLSWhenOffered(t *testing.T) {
	e, f := newPOP3Env(t, 1, "stls", func(o *clientsOptions, _ *pop3Fake) { o.POP3TLS = pop3TLSAuto })
	if code, _, stderr := e.run(context.Background()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if f.count("CAPA") != 1 {
		t.Errorf("probed %d times, want once", f.count("CAPA"))
	}
	for _, c := range f.commands() {
		if c.verb == "PASS" && !c.tls {
			t.Fatal("PASS in clear")
		}
	}
}

func TestPOP3RefusedLoginsAreAConfigurationExit(t *testing.T) {
	e, f := newPOP3Env(t, 1, "stls", func(o *clientsOptions, f *pop3Fake) {
		f.refusePW, f.echoPW = true, true
		o.POP3Cycle = 10 * time.Millisecond
	})
	code, stdout, stderr := e.run(context.Background())
	// The connections succeed, so only excluding pop3_connect from the totals
	// makes this a configuration failure and not an ok run.
	if code != exitConfigOrUnreachable {
		t.Fatalf("exit %d, want %d: %s", code, exitConfigOrUnreachable, stderr)
	}
	s := e.readSummary()
	if s.Ops[opPOP3Connect].Count == 0 || s.Ops[opPOP3Login].Errors["auth"] == 0 {
		t.Errorf("connect %+v, login %+v", s.Ops[opPOP3Connect], s.Ops[opPOP3Login])
	}
	if s.POP3.StoppedAuth != 1 || f.count("PASS") != maxAuthFailures {
		t.Errorf("stopped %d, PASS %d", s.POP3.StoppedAuth, f.count("PASS"))
	}
	// The fake put the password in its -ERR text.
	e.requireNoSecrets(map[string]string{"stdout": stdout, "stderr": stderr, "summary": readFile(t, e.summary)})
}

func TestPOP3DeleShare(t *testing.T) {
	t.Run("default deletes nothing", func(t *testing.T) {
		e, f := newPOP3Env(t, 1, "stls", func(o *clientsOptions, _ *pop3Fake) { o.Duration = time.Second })
		if code, _, stderr := e.run(context.Background()); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if f.count("RETR") < 6 {
			t.Fatalf("only %d RETR, too few cycles to mean anything", f.count("RETR"))
		}
		if f.count("DELE") != 0 {
			t.Errorf("%d DELE at share 0", f.count("DELE"))
		}
	})
	t.Run("share one deletes what it retrieved", func(t *testing.T) {
		e, f := newPOP3Env(t, 1, "stls", func(o *clientsOptions, _ *pop3Fake) { o.DeleShare = 1 })
		if code, _, stderr := e.run(context.Background()); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		s := e.readSummary()
		if s.POP3.Deleted == 0 || s.POP3.Deleted != s.POP3.Retrieved || int64(f.count("DELE")) != s.POP3.Deleted {
			t.Errorf("deleted %d, retrieved %d, fake saw %d", s.POP3.Deleted, s.POP3.Retrieved, f.count("DELE"))
		}
		if s.Ops[opPOP3Dele].Count != s.POP3.Deleted || s.Ops[opPOP3Quit].Count == 0 {
			t.Errorf("dele %+v quit %+v", s.Ops[opPOP3Dele], s.Ops[opPOP3Quit])
		}
	})
	t.Run("a share between is neither all nor none", func(t *testing.T) {
		e, _ := newPOP3Env(t, 1, "stls", func(o *clientsOptions, _ *pop3Fake) {
			o.DeleShare = 0.5
			o.Duration = time.Second
		})
		if code, _, stderr := e.run(context.Background()); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		s := e.readSummary()
		if s.POP3.Deleted == 0 || s.POP3.Deleted >= s.POP3.Retrieved {
			t.Errorf("deleted %d of %d", s.POP3.Deleted, s.POP3.Retrieved)
		}
	})
}

func TestPOP3RecordsLatencyAndErrors(t *testing.T) {
	e, _ := newPOP3Env(t, 1, "stls", nil)
	if code, _, stderr := e.run(context.Background()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	s := e.readSummary()
	for _, op := range []string{opPOP3Connect, opPOP3Login, opPOP3Stat, opPOP3List, opPOP3Retr, opPOP3Quit} {
		if s.Ops[op].Count == 0 || s.Ops[op].P99Ms <= 0 {
			t.Errorf("%s: %+v", op, s.Ops[op])
		}
	}
	// POP3 timings are not merged into IMAP's.
	if s.Ops[opLogin].Count != 0 || s.Ops[opConnect].Count != 0 {
		t.Errorf("imap ops hold pop3 data: login %+v connect %+v", s.Ops[opLogin], s.Ops[opConnect])
	}

	e, _ = newPOP3Env(t, 1, "stls", func(o *clientsOptions, f *pop3Fake) { f.retrErr = true })
	if code, _, stderr := e.run(context.Background()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	s = e.readSummary()
	if s.Ops[opPOP3Retr].Errors["protocol_no_bad"] == 0 || s.Ops[opPOP3Retr].Count != 0 {
		t.Errorf("retr %+v", s.Ops[opPOP3Retr])
	}
	if s.Ops[opPOP3Login].Count == 0 {
		t.Error("logins not recorded")
	}
}

func TestPOP3EmptyMailboxSkipsRetr(t *testing.T) {
	e, f := newPOP3Env(t, 1, "stls", func(o *clientsOptions, f *pop3Fake) { f.messages = 0 })
	code, _, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	s := e.readSummary()
	if s.POP3.SkippedEmpty == 0 || s.POP3.SkippedEmpty != s.POP3.Completed {
		t.Errorf("pop3 %+v", s.POP3)
	}
	if f.count("RETR") != 0 || f.count("LIST") != 0 || f.count("QUIT") == 0 {
		t.Errorf("RETR %d LIST %d QUIT %d", f.count("RETR"), f.count("LIST"), f.count("QUIT"))
	}
	if s.Ops[opPOP3Retr].ErrorTotal != 0 {
		t.Errorf("an empty mailbox counted as an error: %+v", s.Ops[opPOP3Retr])
	}
}

func TestPOP3FewerMessagesThanRetrCount(t *testing.T) {
	e, f := newPOP3Env(t, 1, "stls", func(o *clientsOptions, f *pop3Fake) { f.messages = 2; o.RetrCount = 10 })
	if code, _, stderr := e.run(context.Background()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, c := range f.commands() {
		if c.verb == "RETR" && c.arg != "1" && c.arg != "2" {
			t.Errorf("RETR %s", c.arg)
		}
	}
	if f.count("RETR") == 0 {
		t.Error("nothing retrieved")
	}
}

func TestPOP3CredentialsWithLineBreaksAreNeverSent(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	r := &clientsRunner{st: newClientStats(), hard: context.Background(), cfg: clientsOptions{Timeout: time.Second}}
	c := &pop3Conn{conn: a, tp: textproto.NewConn(a)}
	for _, acct := range []account{
		{email: "u@seed.test\r\nDELE 1", pw: mustPassword(t, "secret-secret-1")},
		{email: "u@seed.test", pw: mustPassword(t, "secret\nDELE 1")},
	} {
		err := r.pop3Login(c, acct)
		if !errors.Is(err, errPOP3Credential) {
			t.Fatalf("err %v", err)
		}
	}
	_ = b.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, _ := b.Read(make([]byte, 16)); n != 0 {
		t.Errorf("%d bytes were written for a rejected credential", n)
	}
	if r.st.ops[opPOP3Login].errs[classAuth].Load() != 2 {
		t.Error("rejections not counted as auth")
	}
}

func mustPassword(t *testing.T, s string) Password {
	t.Helper()
	return Password{v: s}
}

func TestClassifyPOP3(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		login bool
		want  errClass
	}{
		{"wrong password", &pop3Err{text: "-ERR authentication failed"}, true, classAuth},
		{"locked out", &pop3Err{text: "-ERR too many failed attempts"}, true, classAuth},
		{"unknown refusal at login", &pop3Err{text: "-ERR no"}, true, classAuth},
		{"mailbox load failed", &pop3Err{text: "-ERR failed to load mailbox"}, true, classProtocol},
		{"must issue stls", &pop3Err{text: "-ERR must issue STLS first"}, true, classTLS},
		{"refusal after login", &pop3Err{text: "-ERR no such message"}, false, classProtocol},
		{"no stls", errPOP3NoTLS, false, classTLS},
		{"malformed", errPOP3Malformed, false, classProtocol},
		{"credential", errPOP3Credential, true, classAuth},
		{"deadline", context.DeadlineExceeded, false, classTimeout},
	} {
		if got := classifyPOP3(tc.err, tc.login); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, classNames[got], classNames[tc.want])
		}
	}
}

func TestValidatePOP3Options(t *testing.T) {
	good := func() Config {
		return Config{
			Allow: []Target{{Host: "127.0.0.1", IP: true}}, TestDomain: "seed.test",
			Clients: clientsOptions{
				POP3: 1, StateFile: "s", POP3Addr: "127.0.0.1:110", POP3TLS: pop3TLSAuto, POP3Cycle: time.Second,
				RetrCount: 1, Duration: time.Second, Timeout: time.Second, Summary: "x.json",
				ActiveCycle: time.Second, FetchCount: 1, DAVInterval: time.Second, IdleRestart: time.Second,
			},
		}
	}
	if err := validateClients(good()); err != nil {
		t.Fatalf("good config refused: %v", err)
	}
	for name, mod := range map[string]func(*clientsOptions){
		"negative pop3":   func(o *clientsOptions) { o.POP3 = -1 },
		"too many pop3":   func(o *clientsOptions) { o.POP3 = maxClientsPerKind + 1 },
		"no address":      func(o *clientsOptions) { o.POP3Addr = "" },
		"not allowed":     func(o *clientsOptions) { o.POP3Addr = "10.9.9.9:110" },
		"bad tls":         func(o *clientsOptions) { o.POP3TLS = "maybe" },
		"zero cycle":      func(o *clientsOptions) { o.POP3Cycle = 0 },
		"zero retr":       func(o *clientsOptions) { o.RetrCount = 0 },
		"huge retr":       func(o *clientsOptions) { o.RetrCount = maxFetchCount + 1 },
		"share high":      func(o *clientsOptions) { o.DeleShare = 1.1 },
		"share negative":  func(o *clientsOptions) { o.DeleShare = -0.1 },
		"share not float": func(o *clientsOptions) { o.DeleShare = nan() },
	} {
		cfg := good()
		mod(&cfg.Clients)
		if err := validateClients(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func nan() float64 { var z float64; return z / z }

func TestPOP3HelpNamesTheServerLimits(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"clients", "-h"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	help := out.String() + errb.String()
	for _, want := range []string{"-pop3", "-clients-pop3-addr", "-clients-pop3-tls", "-clients-pop3-cycle",
		"-clients-pop3-retr-count", "-clients-pop3-dele-share", "pop3.max_connections_per_ip", "pop3.max_connections"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

// TestReportCarriesAndGatesPOP3 builds a report from clients summaries that
// hold POP3 operations and compares two of them. TestEveryReportKeyIsClassified
// cannot show this: its fixture holds one map entry named "op".
func TestReportCarriesAndGatesPOP3(t *testing.T) {
	dir := t.TempDir()
	build := func(name string, retrP99 float64) string {
		sum := clientsSummary{RunID: "pppp", Ops: map[string]opSummary{
			opPOP3Login:   {Count: 40, P50Ms: 4, P95Ms: 8, P99Ms: 16, MaxMs: 20},
			opPOP3Retr:    {Count: 200, P50Ms: 4, P95Ms: 8, P99Ms: retrP99, MaxMs: 90, ErrorTotal: 1},
			opPOP3Quit:    {Count: 40, P50Ms: 2, P95Ms: 4, P99Ms: 8, MaxMs: 9},
			opPOP3Connect: {Count: 40, P50Ms: 1, P95Ms: 1, P99Ms: 2, MaxMs: 2},
		}}
		data, err := json.Marshal(sum)
		if err != nil {
			t.Fatal(err)
		}
		in := filepath.Join(dir, name+"-clients.json")
		if err := os.WriteFile(in, data, 0o600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, name)
		var so, se bytes.Buffer
		cfg := Config{Report: reportConfig{Report: reportOptions{Clients: in, Out: out}}}
		if code := runReport(cfg, &so, &se); code != exitOK {
			t.Fatalf("report exit %d: %s", code, se.String())
		}
		return out
	}
	base, same, worse := build("base", 32), build("same", 32), build("worse", 64)

	js, err := os.ReadFile(base + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var doc reportDoc
	if err := json.Unmarshal(js, &doc); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{opPOP3Login, opPOP3Retr, opPOP3Quit} {
		if doc.Clients.Ops[op].Count == 0 {
			t.Errorf("report lacks %s", op)
		}
	}
	flat, err := flattenMetrics(js)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"clients.ops.pop3_login.p99_ms", "clients.ops.pop3_retr.p99_ms", "clients.ops.pop3_retr.errors"} {
		if _, ok := flat[k]; !ok {
			t.Errorf("flattened report lacks %s", k)
		} else if _, known := classifyMetric(k); !known {
			t.Errorf("%s is not classified", k)
		}
	}
	md, err := os.ReadFile(base + ".md")
	if err != nil || !strings.Contains(string(md), "pop3_retr") || !strings.Contains(string(md), "## IMAP, POP3 and DAV") {
		t.Errorf("markdown lacks the POP3 rows: %v", err)
	}

	compare := func(a, b string) (int, string) {
		var so, se bytes.Buffer
		cfg := Config{Report: reportConfig{Compare: compareOptions{Baseline: a + ".json", Candidate: b + ".json", Threshold: 10}}}
		code := runReportCompare(cfg, &so, &se)
		return code, so.String() + se.String()
	}
	if code, out := compare(base, same); code != exitOK {
		t.Errorf("identical runs: exit %d\n%s", code, out)
	}
	code, out := compare(base, worse)
	if code != exitCompareDiffers || !strings.Contains(out, "clients.ops.pop3_retr.p99_ms") {
		t.Errorf("a doubled pop3_retr p99: exit %d\n%s", code, out)
	}
}
