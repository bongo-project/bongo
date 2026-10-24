package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

type discardDeliverer struct{}

func (discardDeliverer) Deliver(string, []string, io.Reader) error { return nil }

// recordCounter counts the log records a handler receives at or above Warn.
type recordCounter struct {
	mu   sync.Mutex
	msgs []string
}

func (c *recordCounter) Enabled(context.Context, slog.Level) bool { return true }
func (c *recordCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		c.mu.Lock()
		c.msgs = append(c.msgs, r.Message)
		c.mu.Unlock()
	}
	return nil
}
func (c *recordCounter) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *recordCounter) WithGroup(string) slog.Handler      { return c }

// agentSMTPRcpt starts the agent-role SMTP backend on a real listener and
// returns a function that issues MAIL FROM + RCPT TO and returns the RCPT error.
func agentSMTPRcpt(t *testing.T, secret string, logger *slog.Logger) func(from, to string) error {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "agent"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Web.SessionSecret = secret
	cfg.SMTP.Enabled = true
	cfg.SMTP.Port = freePort(t)
	cfg.SMTP.SubmissionPort = freePort(t)
	a := &Agent{Config: cfg, logger: logger}
	backend, err := a.newAgentSMTPBackend(discardDeliverer{})
	if err != nil {
		t.Fatal(err)
	}
	srv := a.newSMTPServer(backend, false)
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })

	return func(from, to string) error {
		c, err := smtp.Dial(net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.SMTP.Port)))
		if err != nil {
			t.Fatalf("dial smtp: %v", err)
		}
		defer c.Close()
		if err := c.Hello("client.example"); err != nil {
			t.Fatalf("EHLO: %v", err)
		}
		if err := c.Mail(from); err != nil {
			t.Fatalf("MAIL FROM <%s>: %v", from, err)
		}
		return c.Rcpt(to)
	}
}

// isMailboxUnavailable reports the server role's refusal text, 550 5.1.1
// Mailbox unavailable, exactly.
func isMailboxUnavailable(err error) bool {
	var tp *textproto.Error
	return errors.As(err, &tp) && tp.Code == 550 && tp.Msg == "5.1.1 Mailbox unavailable"
}

func TestAgentSMTPSRSRcptMatrix(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	rcpt := agentSMTPRcpt(t, secret, testLogger())

	ref := &Agent{Config: config.Defaults(), logger: testLogger()}
	ref.initSRS([]byte(secret))
	valid, err := ref.SRS.Forward("bob@example.org", "anywhere.example")
	if err != nil {
		t.Fatal(err)
	}
	first, err := ref.SRS.Forward("bob@example.org", "first.example")
	if err != nil {
		t.Fatal(err)
	}
	valid1, err := ref.SRS.Forward(first, "second.example")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, from, to string
		want550        bool
	}{
		{"valid SRS0 null sender", "", valid, false},
		{"valid SRS1 null sender", "", valid1, false},
		{"valid SRS non-null sender", "mallory@evil.example", valid, true},
		{"wrong hash", "", flipHash(valid), true},
		{"wrong hash SRS1", "", flipHash(valid1), true},
		{"ordinary recipient", "x@y.example", "anyone@anywhere.example", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rcpt(tt.from, tt.to)
			if tt.want550 {
				if !isMailboxUnavailable(err) {
					t.Fatalf("err = %v, want 550 5.1.1 Mailbox unavailable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want accepted", err)
			}
		})
	}
}

// TestAgentSMTPSRSNoSecretFailsClosed: an agent with no session secret has no
// key to verify with, so every SRS-shaped recipient (including one minted under
// a real key) is refused, ordinary recipients still pass, and the condition is
// logged once at startup, not per message.
func TestAgentSMTPSRSNoSecretFailsClosed(t *testing.T) {
	counter := &recordCounter{}
	rcpt := agentSMTPRcpt(t, "", slog.New(counter))

	ref := &Agent{Config: config.Defaults(), logger: testLogger()}
	ref.initSRS([]byte("0123456789abcdef0123456789abcdef"))
	valid, err := ref.SRS.Forward("bob@example.org", "anywhere.example")
	if err != nil {
		t.Fatal(err)
	}
	first, err := ref.SRS.Forward("bob@example.org", "first.example")
	if err != nil {
		t.Fatal(err)
	}
	valid1, err := ref.SRS.Forward(first, "second.example")
	if err != nil {
		t.Fatal(err)
	}

	for name, to := range map[string]string{"SRS0": valid, "SRS1": valid1, "tampered": flipHash(valid)} {
		if err := rcpt("", to); !isMailboxUnavailable(err) {
			t.Errorf("%s: err = %v, want 550 5.1.1", name, err)
		}
	}
	if err := rcpt("x@y.example", "anyone@anywhere.example"); err != nil {
		t.Errorf("ordinary recipient: err = %v, want accepted", err)
	}

	counter.mu.Lock()
	defer counter.mu.Unlock()
	var n int
	for _, m := range counter.msgs {
		if strings.Contains(m, "no web.session_secret") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("startup log lines about the missing secret = %d, want exactly 1 (warn records: %v)", n, counter.msgs)
	}
}
