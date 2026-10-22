package smtp

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

const (
	smtpClientA = "203.0.113.7"
	smtpClientB = "198.51.100.9"
)

type smtpTestConn struct {
	net.Conn
	r *bufio.Reader
}

// dialSMTPAs connects to addr and claims ip through a PROXY header; loopback
// is a trusted proxy, so the session keys on ip rather than 127.0.0.1.
func dialSMTPAs(t *testing.T, addr, ip string) *smtpTestConn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := io.WriteString(c, "PROXY TCP4 "+ip+" 10.0.0.1 40000 25\r\n"); err != nil {
		t.Fatal(err)
	}
	return &smtpTestConn{Conn: c, r: bufio.NewReader(c)}
}

func (c *smtpTestConn) line(d time.Duration) (string, bool) {
	_ = c.SetReadDeadline(time.Now().Add(d))
	s, err := c.r.ReadString('\n')
	return strings.TrimSpace(s), err == nil
}

func (c *smtpTestConn) want(t *testing.T, prefix string) {
	t.Helper()
	got, ok := c.line(5 * time.Second)
	if !ok || !strings.HasPrefix(got, prefix) {
		t.Fatalf("greeting = %q (ok=%v), want prefix %q", got, ok, prefix)
	}
}

// hello sends EHLO (go-smtp creates the session, and so takes the per-IP slot,
// only at the first greeting command) and returns the final reply line.
func (c *smtpTestConn) hello(t *testing.T) string {
	t.Helper()
	c.want(t, "220")
	if _, err := io.WriteString(c, "EHLO client.example\r\n"); err != nil {
		t.Fatal(err)
	}
	for {
		got, ok := c.line(5 * time.Second)
		if !ok {
			t.Fatal("no reply to EHLO")
		}
		if len(got) < 4 || got[3] != '-' {
			return got
		}
	}
}

// TestSMTPPerIPConnectionCap proves Server wires config.Limits.MaxConnectionsPerIP
// into the shared limiter, refuses the excess with 421, frees a slot on
// Logout, and keys on the PROXY-derived client IP.
func TestSMTPPerIPConnectionCap(t *testing.T) {
	cfg := &config.SMTPConfig{
		Port:           2525,
		SubmissionPort: 2587,
		ProxyProtocol:  true,
		Limits:         config.SMTPLimits{MaxConnections: 100, MaxConnectionsPerIP: 2},
	}
	srv := NewServer(cfg, NewBackend(nil, nil, testLogger()), true, testLogger())
	srv.mx.Addr = "127.0.0.1:0"
	srv.submission.Addr = "127.0.0.1:0"
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	addr := srv.MXAddr()

	c1 := dialSMTPAs(t, addr, smtpClientA)
	if got := c1.hello(t); !strings.HasPrefix(got, "250") {
		t.Fatalf("c1 EHLO = %q", got)
	}
	c2 := dialSMTPAs(t, addr, smtpClientA)
	if got := c2.hello(t); !strings.HasPrefix(got, "250") {
		t.Fatalf("c2 EHLO = %q", got)
	}

	c3 := dialSMTPAs(t, addr, smtpClientA)
	if got := c3.hello(t); !strings.HasPrefix(got, "421") {
		t.Fatalf("c3 EHLO = %q, want 421", got)
	}

	other := dialSMTPAs(t, addr, smtpClientB)
	if got := other.hello(t); !strings.HasPrefix(got, "250") {
		t.Fatalf("other EHLO = %q", got)
	}

	// Closing one must free a slot (Logout releases it).
	_ = c1.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c := dialSMTPAs(t, addr, smtpClientA)
		got := c.hello(t)
		_ = c.Close()
		if strings.HasPrefix(got, "250") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot was never freed after close")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
