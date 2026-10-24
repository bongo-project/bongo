package agent

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

func TestAgentTrustedProxiesWiring(t *testing.T) {
	tests := []struct {
		name      string
		configure []string
		wantEmpty bool
		trusted   []string
		untrusted []string
	}{
		{
			name:      "unset trusts nobody",
			wantEmpty: true,
			untrusted: []string{"10.0.0.5", "172.16.0.9", "192.168.1.1", "127.0.0.1", "::1"},
		},
		{
			name:      "listed proxy only",
			configure: []string{"10.9.9.9", "172.18.0.0/16"},
			trusted:   []string{"10.9.9.9", "172.18.4.4"},
			untrusted: []string{"10.0.0.5", "172.19.0.1", "127.0.0.1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.TrustedProxies = tc.configure
			a := &Agent{Config: cfg, logger: slog.New(slog.DiscardHandler)}

			tp := a.trustedProxies()
			if tp.Empty() != tc.wantEmpty {
				t.Fatalf("Empty() = %v, want %v", tp.Empty(), tc.wantEmpty)
			}
			for _, h := range tc.trusted {
				if !tp.TrustsHost(h) {
					t.Errorf("TrustsHost(%s) = false, want true", h)
				}
			}
			for _, h := range tc.untrusted {
				if tp.TrustsHost(h) {
					t.Errorf("TrustsHost(%s) = true, want false", h)
				}
			}

			// The middleware global must be fed the same set.
			middleware.SetTrustedProxies(tp)
			t.Cleanup(func() { middleware.SetTrustedProxies(nil) })
			for _, h := range append(append([]string{}, tc.trusted...), tc.untrusted...) {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.RemoteAddr = net.JoinHostPort(h, "5000")
				r.Header.Set("X-Forwarded-For", "8.8.8.8")
				want := h
				if tp.TrustsHost(h) {
					want = "8.8.8.8"
				}
				if got := middleware.ClientIP(r); got != want {
					t.Errorf("ClientIP via %s = %s, want %s", h, got, want)
				}
			}
		})
	}
}

func TestAgentLogProxyTrust(t *testing.T) {
	logOut := func(trusted []string, behindTLS bool) string {
		var buf bytes.Buffer
		cfg := config.Defaults()
		cfg.Server.TrustedProxies = trusted
		cfg.Web.BehindTLSProxy = behindTLS
		a := &Agent{Config: cfg, logger: slog.New(slog.NewTextHandler(&buf, nil))}
		a.logProxyTrust()
		return buf.String()
	}
	if out := logOut([]string{"10.9.9.9/32"}, true); !strings.Contains(out, "10.9.9.9/32") || strings.Contains(out, "level=WARN") {
		t.Errorf("configured: want INFO naming the proxy and no WARN, got %q", out)
	}
	if out := logOut(nil, true); !strings.Contains(out, "level=WARN") {
		t.Errorf("empty + behind_tls_proxy: want WARN, got %q", out)
	}
	if out := logOut(nil, false); strings.Contains(out, "level=WARN") {
		t.Errorf("empty, not behind proxy: want no WARN, got %q", out)
	}
}

// TestRunWiresProxyTrustIntoEveryListener starts a real dev-mode Agent with
// proxy_protocol on every protocol and no trusted proxy, then forges PROXY
// headers from the (unlisted) loopback peer. Each forged source must be
// ignored, so a per-IP cap of one refuses the second connection. Handing any
// listener, or the web middleware, a wider trust set in Run turns this red;
// the per-package tests cannot see that wiring.
func TestRunWiresProxyTrustIntoEveryListener(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = true
	cfg.SMTP.ProxyProtocol = true
	cfg.SMTP.Limits.MaxConnectionsPerIP = 1
	cfg.IMAP.Enabled = true
	cfg.IMAP.ProxyProtocol = true
	cfg.IMAP.MaxConnectionsPerIP = 1
	cfg.POP3.Enabled = true
	cfg.POP3.ProxyProtocol = true
	cfg.POP3.MaxConnectionsPerIP = 1

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned error: %v", err)
		}
		middleware.SetTrustedProxies(nil)
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !a.Ready() {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent did not become ready")
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	if got := middleware.ClientIP(r); got != "127.0.0.1" {
		t.Errorf("web ClientIP honoured XFF from an unlisted peer: %s", got)
	}

	// SMTP enforces its per-IP cap at EHLO, not at the greeting.
	greeting := func(c net.Conn, ehlo bool) string {
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		br := bufio.NewReader(c)
		line, _ := br.ReadString('\n')
		if ehlo {
			_, _ = io.WriteString(c, "EHLO client.test\r\n")
			line, _ = br.ReadString('\n')
		}
		return line
	}
	dial := func(t *testing.T, addr, forged string) net.Conn {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial %s: %v", addr, err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if _, err := io.WriteString(c, "PROXY TCP4 "+forged+" 10.0.0.1 40000 25\r\n"); err != nil {
			t.Fatal(err)
		}
		return c
	}

	for _, l := range []struct {
		name, addr, ok string
		ehlo           bool
	}{
		{"smtp mx", a.SMTP.MXAddr(), "250", true},
		{"smtp submission", a.SMTP.SubmissionAddr(), "250", true},
		{"imap", a.IMAP.PlainAddr(), "* OK", false},
		{"pop3", a.POP3.PlainAddr(), "+OK", false},
	} {
		// Subtests close their connections on exit: the cap is per real peer
		// across all SMTP listeners, so a held slot would starve the next one.
		t.Run(l.name, func(t *testing.T) {
			// The server frees a closed connection's slot asynchronously, so
			// the previous subtest's may still be held: retry while refused
			// for that reason only.
			var got string
			for deadline := time.Now().Add(3 * time.Second); ; {
				c := dial(t, l.addr, "192.0.2.10")
				got = greeting(c, l.ehlo)
				if !strings.Contains(got, "Too many connections") || time.Now().After(deadline) {
					break
				}
				_ = c.Close()
				time.Sleep(20 * time.Millisecond)
			}
			if !strings.HasPrefix(got, l.ok) {
				t.Fatalf("first greeting = %q, want prefix %q", got, l.ok)
			}
			if got := greeting(dial(t, l.addr, "192.0.2.11"), l.ehlo); strings.HasPrefix(got, l.ok) {
				t.Fatalf("second forged source got %q: PROXY header from an unlisted peer was honoured", got)
			}
		})
	}
}

// TestSMTPServerBuiltOnlyThroughTrustHelper keeps the dev/server path (Run)
// and the agent-role path (startSMTPAgent) from diverging: both must build the
// server via newSMTPServer, which is the one place proxy trust is attached.
func TestSMTPServerBuiltOnlyThroughTrustHelper(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(src), "bongosmtp.NewServer("); n != 1 {
		t.Fatalf("agent.go calls bongosmtp.NewServer %d times, want exactly 1 (inside newSMTPServer)", n)
	}
	if n := strings.Count(string(src), "a.newSMTPServer("); n != 2 {
		t.Fatalf("agent.go calls a.newSMTPServer %d times, want 2 (Run and startSMTPAgent)", n)
	}
}
