package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/emersion/go-smtp"
)

const sinkOutDomain = "outbound.test"

type sinkHarness struct {
	addr    string
	summary string
	cancel  context.CancelFunc
	done    chan struct{}
	sum     *sinkSummary
	err     error
}

func startSink(t *testing.T, mutate func(*Config)) *sinkHarness {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Allow:      []Target{{Host: "127.0.0.1", IP: true}},
		TestDomain: testDomain,
		Sink: sinkConfig{
			Domain: sinkOutDomain, Summary: filepath.Join(t.TempDir(), "sink.json"),
			MaxSize: 1 << 20, MaxRecipients: 10, Timeout: 5 * time.Second, MaxIDs: 100,
		},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &sinkHarness{addr: ln.Addr().String(), summary: cfg.Sink.Summary, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		h.sum, h.err = sinkRun(ctx, cfg, ln, sinkOpts{grace: time.Second})
	}()
	t.Cleanup(func() { cancel(); <-h.done })
	return h
}

func (h *sinkHarness) stop(t *testing.T) {
	t.Helper()
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Fatal("sink did not stop")
	}
	if h.err != nil {
		t.Fatal(h.err)
	}
}

func sendOne(t *testing.T, addr, from string, rcpts []string, msg string) error {
	t.Helper()
	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Mail(from, nil); err != nil {
		return err
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r, nil); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	return w.Close()
}

func mkMsg(id, body string) string {
	return "From: a@" + testDomain + "\r\nSubject: SECRETSUBJECT\r\nX-Loadgen-Id: " + id + "\r\n\r\n" + body + "\r\n"
}

func TestSinkCountsAndNeverRecordsContent(t *testing.T) {
	h := startSink(t, nil)
	const marker = "BODYMARKER-7f3a"
	if err := sendOne(t, h.addr, "s@"+testDomain, []string{"a@" + testDomain, "b@" + sinkOutDomain}, mkMsg("id-one", marker)); err != nil {
		t.Fatal(err)
	}
	// The null sender is what bounces use.
	if err := sendOne(t, h.addr, "", []string{"a@" + testDomain}, mkMsg("id-two", marker)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.summary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("summary exists before the interrupt: %v", err)
	}
	h.stop(t)
	raw, err := os.ReadFile(h.summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{marker, "SECRETSUBJECT", "@" + testDomain, "@" + sinkOutDomain} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("summary holds %q", leak)
		}
	}
	var s sinkSummary
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Messages != 2 || s.Recipients != 3 || s.Bytes <= 0 || s.RefusedRecipients != 0 {
		t.Errorf("counts: %+v", s)
	}
	if len(s.IDs) != 2 || s.IDs[0].ID != "id-one" || s.IDs[1].ID != "id-two" || s.IDs[0].Arrived.IsZero() || s.IDs[1].Arrived.Before(s.IDs[0].Arrived) {
		t.Errorf("ids: %+v", s.IDs)
	}
}

func TestSinkRefusesForeignRecipient(t *testing.T) {
	h := startSink(t, nil)
	tests := []struct {
		name string
		rcpt string
		want int
	}{
		{"test domain", "a@" + testDomain, 0},
		{"subdomain", "a@x." + testDomain, 0},
		{"sink domain", "a@" + sinkOutDomain, 0},
		// Same local part, only the domain differs.
		{"foreign", "a@other.invalid", 550},
		{"suffix lookalike", "a@not" + testDomain, 550},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := sendOne(t, h.addr, "s@"+testDomain, []string{tc.rcpt}, mkMsg("x", "b"))
			var se *smtp.SMTPError
			switch {
			case tc.want == 0 && err != nil:
				t.Errorf("refused: %v", err)
			case tc.want != 0 && (!errors.As(err, &se) || se.Code != tc.want):
				t.Errorf("err = %v, want %d", err, tc.want)
			}
		})
	}
	h.stop(t)
	if h.sum.RefusedRecipients != 2 || h.sum.Messages != 3 {
		t.Errorf("summary: %+v", h.sum)
	}
}

func TestSinkNoSinkDomainAcceptsOnlyTestDomain(t *testing.T) {
	h := startSink(t, func(c *Config) { c.Sink.Domain = "" })
	err := sendOne(t, h.addr, "s@"+testDomain, []string{"a@" + sinkOutDomain}, mkMsg("x", "b"))
	var se *smtp.SMTPError
	if !errors.As(err, &se) || se.Code != 550 {
		t.Errorf("err = %v, want 550", err)
	}
}

func TestSinkOversize(t *testing.T) {
	h := startSink(t, func(c *Config) { c.Sink.MaxSize = 4096 })
	err := sendOne(t, h.addr, "s@"+testDomain, []string{"a@" + testDomain}, mkMsg("big", strings.Repeat(strings.Repeat("x", 100)+"\r\n", 200)))
	var se *smtp.SMTPError
	if !errors.As(err, &se) || se.Code != 552 {
		t.Fatalf("err = %v, want 552", err)
	}
	h.stop(t)
	if h.sum.Messages != 0 || h.sum.TooLarge != 1 {
		t.Errorf("summary: %+v", h.sum)
	}
}

func TestSinkIDsAreSanitisedAndBounded(t *testing.T) {
	h := startSink(t, func(c *Config) { c.Sink.MaxIDs = 2 })
	long := strings.Repeat("a", 500)
	for _, id := range []string{long, "ok \xc3\xa9-id", "third"} {
		if err := sendOne(t, h.addr, "s@"+testDomain, []string{"a@" + testDomain}, mkMsg(id, "b")); err != nil {
			t.Fatal(err)
		}
	}
	h.stop(t)
	s := h.sum
	if s.Messages != 3 || len(s.IDs) != 2 || s.IDsDropped != 1 {
		t.Fatalf("summary: %+v", s)
	}
	if len(s.IDs[0].ID) != sinkIDLimit {
		t.Errorf("id length %d, want %d", len(s.IDs[0].ID), sinkIDLimit)
	}
	if s.IDs[1].ID != "ok -id" {
		t.Errorf("id = %q", s.IDs[1].ID)
	}
}

func TestSinkHeaderWithoutBlankLineIsBounded(t *testing.T) {
	h := startSink(t, nil)
	// 200 KiB of one folded header: the read stops at the header limit and
	// the rest is counted and discarded.
	// The ID sits past the limit, so seeing it would mean the bound is gone.
	msg := "X-Filler: " + strings.Repeat(strings.Repeat("y", 100)+"\r\n ", 2000) + "\r\nX-Loadgen-Id: late\r\n"
	if err := sendOne(t, h.addr, "s@"+testDomain, []string{"a@" + testDomain}, msg); err != nil {
		t.Fatal(err)
	}
	h.stop(t)
	if h.sum.Messages != 1 || h.sum.Bytes < 200000 || len(h.sum.IDs) != 1 || h.sum.IDs[0].ID != "" {
		t.Errorf("summary: %+v", h.sum)
	}
}

func TestSinkOffersNoAuthOrStartTLS(t *testing.T) {
	h := startSink(t, nil)
	c, err := smtp.Dial(h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Hello("client.test"); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"AUTH", "STARTTLS"} {
		if ok, _ := c.Extension(ext); ok {
			t.Errorf("sink advertises %s", ext)
		}
	}
}

func sinkRunArgs(extra ...string) []string {
	return append([]string{"sink", "--target-allow", "127.0.0.1", "--test-domain", testDomain}, extra...)
}

func TestSinkListenValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing", sinkRunArgs(), "--sink-listen is required"},
		{"empty host", sinkRunArgs("--sink-listen", ":2525"), "explicit IP"},
		{"wildcard v4", sinkRunArgs("--sink-listen", "0.0.0.0:2525"), "every interface"},
		{"wildcard v6", sinkRunArgs("--sink-listen", "[::]:2525"), "every interface"},
		{"name", sinkRunArgs("--sink-listen", "localhost:2525"), "IP literal"},
		{"no port", sinkRunArgs("--sink-listen", "127.0.0.1"), "port"},
		{"port zero", sinkRunArgs("--sink-listen", "127.0.0.1:0"), "port"},
		{"not allowed", sinkRunArgs("--sink-listen", "127.0.0.2:2525"), "--target-allow"},
		{"bad sink domain", sinkRunArgs("--sink-listen", "127.0.0.1:2525", "--sink-domain", "tydewhatmay.eu"), "--sink-domain"},
		{"single label sink domain", sinkRunArgs("--sink-listen", "127.0.0.1:2525", "--sink-domain", "com"), "--sink-domain"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb); code != exitRefuse {
				t.Fatalf("exit %d, want %d; stderr %s", code, exitRefuse, errb.String())
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Errorf("stderr %q lacks %q", errb.String(), tc.want)
			}
		})
	}
}

func TestSinkFlagsDoNotCollide(t *testing.T) {
	// Building the shared FlagSet for every subcommand panics on a clash.
	for name := range commands {
		var cfg Config
		_ = newFlagSet(name, &cfg, &bytes.Buffer{})
	}
}

// TestSinkSigint proves the real signal wiring and exit code: a separate
// process gets SIGINT and leaves its summary behind.
func TestSinkSigint(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	summary := filepath.Join(t.TempDir(), "sig.json")
	args := sinkRunArgs("--sink-listen", addr, "--sink-summary", summary)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSMTPHelperProcess$") // #nosec G204 -- re-executes this test binary
	cmd.Env = append(os.Environ(), helperEnv+"="+strings.Join(args, "\x1f"))
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("sink never listened: %s", errb.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := sendOne(t, addr, "s@"+testDomain, []string{"a@" + testDomain}, mkMsg("sig-id", "b")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(summary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("summary exists before SIGINT: %v", err)
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("exit = %v, want 0; stderr %s", err, errb.String())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child did not exit after SIGINT")
	}
	raw, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	var s sinkSummary
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Messages != 1 || len(s.IDs) != 1 || s.IDs[0].ID != "sig-id" {
		t.Errorf("summary after SIGINT: %+v", s)
	}
}
