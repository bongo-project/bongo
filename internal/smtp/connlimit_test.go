package smtp

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/netx"
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

// waitConnLimitDrained blocks until the backend's per-IP limiter holds no
// slot. The server releases a slot asynchronously once it sees the client
// close, so a test that reuses one server across subtests must wait for it
// instead of assuming a closed connection is already uncounted.
func waitConnLimitDrained(t *testing.T, srv *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for srv.backend.connLimit.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("per-IP limiter still holds %d source(s) 5s after the connections closed", srv.backend.connLimit.Len())
		}
		time.Sleep(5 * time.Millisecond)
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
	srv := NewServer(cfg, "", NewBackend(nil, nil, testLogger()), true, testLogger())
	tp, err := netx.ParseTrustedProxies([]string{"127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetTrustedProxies(tp)
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

// TestSMTPPROXYHeaderFromUnlistedPeerIgnored proves a PROXY header from a peer
// outside the trust set is dropped: forged distinct sources all key on the
// real peer, so the per-IP cap still applies.
func TestSMTPPROXYHeaderFromUnlistedPeerIgnored(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trusted []string
	}{
		{"empty", nil},
		{"other peer listed", []string{"10.9.9.9/32"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.SMTPConfig{
				Port:           2525,
				SubmissionPort: 2587,
				ProxyProtocol:  true,
				Limits:         config.SMTPLimits{MaxConnections: 100, MaxConnectionsPerIP: 2},
			}
			srv := NewServer(cfg, "", NewBackend(nil, nil, testLogger()), true, testLogger())
			tp, err := netx.ParseTrustedProxies(tc.trusted)
			if err != nil {
				t.Fatal(err)
			}
			srv.SetTrustedProxies(tp)
			srv.mx.Addr = "127.0.0.1:0"
			srv.submission.Addr = "127.0.0.1:0"
			if err := srv.Listen(); err != nil {
				t.Fatal(err)
			}
			go func() { _ = srv.Serve() }()
			t.Cleanup(func() { _ = srv.Close() })
			for _, l := range []struct{ name, addr string }{
				{"mx", srv.MXAddr()},
				{"submission", srv.SubmissionAddr()},
			} {
				t.Run(l.name, func(t *testing.T) {
					// Registered before any dial so it runs after the dial
					// cleanups have closed the connections: the next listener
					// shares this limiter and must start from zero.
					t.Cleanup(func() { waitConnLimitDrained(t, srv) })
					for _, ip := range []string{smtpClientA, smtpClientB} {
						c := dialSMTPAs(t, l.addr, ip)
						if got := c.hello(t); !strings.HasPrefix(got, "250") {
							t.Fatalf("EHLO from forged %s = %q", ip, got)
						}
					}
					c3 := dialSMTPAs(t, l.addr, "192.0.2.55")
					if got := c3.hello(t); !strings.HasPrefix(got, "421") {
						t.Fatalf("third EHLO = %q, want 421 (forged sources must share the real peer's cap)", got)
					}
				})
			}
		})
	}
}
