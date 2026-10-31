package smtp

import (
	"bufio"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/security"
)

// writeTestCert writes a throwaway certificate and key and returns the paths.
func writeTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	certPEM, keyPEM, err := security.GenerateSelfSigned([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "smtp.crt")
	keyFile = filepath.Join(dir, "smtp.key")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

// submissionsServer starts a non-dev server with a certificate, all three
// listeners on ephemeral loopback ports.
func submissionsServer(t *testing.T, mutate func(*config.SMTPConfig)) (*Server, *Backend) {
	t.Helper()
	certFile, keyFile := writeTestCert(t)
	cfg := &config.SMTPConfig{
		Port:            2525,
		SubmissionPort:  2587,
		SubmissionsPort: 2465,
		TLS:             config.TLSConfig{CertFile: certFile, KeyFile: keyFile},
	}
	if mutate != nil {
		mutate(cfg)
	}
	b, _, _ := testBackend("bob@example.com")
	b.auth = &fakeAuth{userID: "uid-alice@example.com"}
	b.connLimit = netx.NewPerIPLimiter(0)
	b.authFail = NewBackend(nil, nil, testLogger()).authFail
	b.logger = testLogger()
	srv := NewServer(cfg, "", b, false, testLogger())
	if cfg.ProxyProtocol {
		tp, err := netx.ParseTrustedProxies([]string{"127.0.0.1/32"})
		if err != nil {
			t.Fatal(err)
		}
		srv.SetTrustedProxies(tp)
	}
	srv.mx.Addr = "127.0.0.1:0"
	srv.submission.Addr = "127.0.0.1:0"
	if srv.submissions != nil {
		srv.submissions.Addr = "127.0.0.1:0"
	}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	// The 587 listener is recognised by its local port, so tell the backend
	// the ephemeral port Listen picked.
	_, portStr, err := net.SplitHostPort(srv.SubmissionAddr())
	if err != nil {
		t.Fatal(err)
	}
	b.subPort, _ = strconv.Atoi(portStr)
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv, b
}

// listenerCase is one way to reach a submission service.
type listenerCase struct {
	name string
	// dial returns an EHLO'd client with TLS established, and the extensions
	// the server offered in its first EHLO.
	dial func(t *testing.T, srv *Server) (c *gosmtp.Client, preTLSStartTLS bool)
}

var tlsInsecure = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // #nosec G402 -- test client against a throwaway certificate

var submissionListeners = []listenerCase{
	{"587 STARTTLS", func(t *testing.T, srv *Server) (*gosmtp.Client, bool) {
		plain, err := gosmtp.Dial(srv.SubmissionAddr())
		if err != nil {
			t.Fatal(err)
		}
		if err := plain.Hello("client.example"); err != nil {
			t.Fatal(err)
		}
		offered, _ := plain.Extension("STARTTLS")
		_ = plain.Close()
		c, err := gosmtp.DialStartTLS(srv.SubmissionAddr(), tlsInsecure)
		if err != nil {
			t.Fatalf("STARTTLS: %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Hello("client.example"); err != nil {
			t.Fatal(err)
		}
		return c, offered
	}},
	{"465 implicit TLS", func(t *testing.T, srv *Server) (*gosmtp.Client, bool) {
		c, err := gosmtp.DialTLS(srv.SubmissionsAddr(), tlsInsecure)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Hello("client.example"); err != nil {
			t.Fatal(err)
		}
		offered, _ := c.Extension("STARTTLS")
		return c, offered
	}},
}

func isCode(err error, code int) bool {
	var se *gosmtp.SMTPError
	return errors.As(err, &se) && se.Code == code
}

// TestSubmissionListeners runs the same submission expectations against 587
// (STARTTLS) and 465 (implicit TLS).
func TestSubmissionListeners(t *testing.T) {
	for _, lc := range submissionListeners {
		t.Run(lc.name, func(t *testing.T) {
			t.Run("EHLO offers AUTH, and STARTTLS only on 587", func(t *testing.T) {
				srv, _ := submissionsServer(t, nil)
				c, startTLS := lc.dial(t, srv)
				wantStartTLS := strings.HasPrefix(lc.name, "587")
				if startTLS != wantStartTLS {
					t.Errorf("STARTTLS offered = %v, want %v", startTLS, wantStartTLS)
				}
				ok, params := c.Extension("AUTH")
				if !ok || !strings.Contains(params, "PLAIN") || !strings.Contains(params, "LOGIN") {
					t.Errorf("AUTH = %v %q, want PLAIN and LOGIN", ok, params)
				}
				if _, ok := c.TLSConnectionState(); !ok {
					t.Error("connection is not TLS")
				}
			})

			t.Run("unauthenticated MAIL gets 530", func(t *testing.T) {
				srv, _ := submissionsServer(t, nil)
				c, _ := lc.dial(t, srv)
				if err := c.Mail("alice@example.com", nil); !isCode(err, 530) {
					t.Fatalf("MAIL = %v, want 530", err)
				}
			})

			t.Run("send-as is enforced", func(t *testing.T) {
				srv, _ := submissionsServer(t, nil)
				c, _ := lc.dial(t, srv)
				if err := c.Auth(sasl.NewPlainClient("", "alice", "pw")); err != nil {
					t.Fatalf("AUTH PLAIN over TLS: %v", err)
				}
				if err := c.Mail("mallory@example.com", nil); err == nil {
					t.Fatal("MAIL as an address the user cannot send as was accepted")
				}
				if err := c.Mail("alice@example.com", nil); err != nil {
					t.Fatalf("MAIL as own address: %v", err)
				}
			})

			t.Run("outbound rate limit applies", func(t *testing.T) {
				srv, b := submissionsServer(t, nil)
				b.outboundRL = newInboundRateLimiter(1)
				send := func() error {
					c, _ := lc.dial(t, srv)
					if err := c.Auth(sasl.NewLoginClient("alice", "pw")); err != nil {
						t.Fatalf("AUTH LOGIN: %v", err)
					}
					return c.SendMail("alice@example.com", []string{"outsider@example.net"}, strings.NewReader(authedMsg))
				}
				if err := send(); err != nil {
					t.Fatalf("first message: %v", err)
				}
				if err := send(); !isCode(err, 452) {
					t.Fatalf("second message = %v, want 452", err)
				}
			})

			t.Run("recipient budget applies", func(t *testing.T) {
				srv, b := submissionsServer(t, nil)
				b.SetRecipientLimiter(mail.NewRateLimiter(1))
				c, _ := lc.dial(t, srv)
				if err := c.Auth(sasl.NewPlainClient("", "alice", "pw")); err != nil {
					t.Fatal(err)
				}
				if err := c.Mail("alice@example.com", nil); err != nil {
					t.Fatal(err)
				}
				if err := c.Rcpt("one@example.net", nil); err != nil {
					t.Fatalf("first external recipient: %v", err)
				}
				if err := c.Rcpt("two@example.net", nil); !isCode(err, 452) {
					t.Fatalf("second external recipient = %v, want 452", err)
				}
			})
		})
	}
}

// TestMXListenerOffersNoAuth keeps the MX port from becoming a submission
// port: AUTH is not offered on 25 and MAIL needs no authentication there.
func TestMXListenerOffersNoAuth(t *testing.T) {
	srv, _ := submissionsServer(t, nil)
	c, err := gosmtp.Dial(srv.MXAddr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Hello("client.example"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Extension("AUTH"); ok {
		t.Error("AUTH offered on the MX listener")
	}
}

// TestSubmissionsAuthWithoutInsecureAuth proves 465 authenticates with
// AllowInsecureAuth false: the dev flag is not what lets AUTH through.
func TestSubmissionsAuthWithoutInsecureAuth(t *testing.T) {
	srv, _ := submissionsServer(t, nil)
	if srv.submissions.AllowInsecureAuth {
		t.Fatal("AllowInsecureAuth is set on the 465 server")
	}
	c, err := gosmtp.DialTLS(srv.SubmissionsAddr(), tlsInsecure)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Auth(sasl.NewPlainClient("", "alice", "pw")); err != nil {
		t.Fatalf("AUTH on 465: %v", err)
	}
}

// TestSubmissionsPlaintextClientIsRefused proves 465 speaks only TLS: a
// plaintext EHLO never gets a 220 greeting or an SMTP reply.
func TestSubmissionsPlaintextClientIsRefused(t *testing.T) {
	srv, _ := submissionsServer(t, nil)
	conn, err := net.Dial("tcp", srv.SubmissionsAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "EHLO client.example\r\n")
	line, _ := bufio.NewReader(conn).ReadString('\n')
	if strings.HasPrefix(line, "220") || strings.HasPrefix(line, "250") {
		t.Fatalf("plaintext SMTP reply on 465: %q", line)
	}
}

// TestSubmissionsMarkedWithoutPort proves the 465 session is a submission
// session because of the listener, not the local port: the backend has no
// subPort at all, and the plain Backend would call such a session MX.
func TestSubmissionsMarkedWithoutPort(t *testing.T) {
	srv, b := submissionsServer(t, nil)
	b.subPort = 0
	c, err := gosmtp.DialTLS(srv.SubmissionsAddr(), tlsInsecure)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Hello("client.example"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Extension("AUTH"); !ok {
		t.Error("AUTH not offered on 465 when the backend has no submission port")
	}
	if err := c.Mail("alice@example.com", nil); !isCode(err, 530) {
		t.Fatalf("unauthenticated MAIL = %v, want 530", err)
	}
}

func TestSubmissionsDisabledAndNoCert(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.SMTPConfig)
		dev    bool
		want   bool
	}{
		{"port 0", func(c *config.SMTPConfig) { c.SubmissionsPort = 0 }, false, false},
		{"no certificate outside dev", func(c *config.SMTPConfig) { c.TLS = config.TLSConfig{} }, false, false},
		{"no certificate in dev", func(c *config.SMTPConfig) { c.TLS = config.TLSConfig{} }, true, true},
		{"certificate", nil, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			certFile, keyFile := writeTestCert(t)
			cfg := &config.SMTPConfig{
				Port: 2525, SubmissionPort: 2587, SubmissionsPort: 2465,
				TLS: config.TLSConfig{CertFile: certFile, KeyFile: keyFile},
			}
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			srv := NewServer(cfg, "", NewBackend(nil, nil, testLogger()), tt.dev, testLogger())
			if got := srv.SubmissionsEnabled(); got != tt.want {
				t.Fatalf("SubmissionsEnabled = %v, want %v", got, tt.want)
			}
			if !tt.want && srv.SubmissionsAddr() != "" {
				t.Errorf("SubmissionsAddr = %q for a disabled listener", srv.SubmissionsAddr())
			}
		})
	}
}

// TestSubmissionsBrokenCertFailsListen: a configured certificate that cannot
// be loaded fails startup rather than serving plaintext or nothing.
func TestSubmissionsBrokenCertFailsListen(t *testing.T) {
	certFile, keyFile := writeTestCert(t)
	if err := os.WriteFile(keyFile, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.SMTPConfig{
		Port: 2525, SubmissionPort: 2587, SubmissionsPort: 2465,
		TLS: config.TLSConfig{CertFile: certFile, KeyFile: keyFile},
	}
	srv := NewServer(cfg, "", NewBackend(nil, nil, testLogger()), false, testLogger())
	srv.mx.Addr = "127.0.0.1:0"
	srv.submission.Addr = "127.0.0.1:0"
	srv.submissions.Addr = "127.0.0.1:0"
	if err := srv.Listen(); err == nil {
		_ = srv.Close()
		t.Fatal("Listen succeeded with an unloadable smtp.tls certificate")
	}
}

// TestSubmissionsBindFailureClosesOthers: a taken 465 fails Listen and
// releases the MX and submission sockets it had already bound.
func TestSubmissionsBindFailureClosesOthers(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()

	certFile, keyFile := writeTestCert(t)
	cfg := &config.SMTPConfig{
		Port: 2525, SubmissionPort: 2587, SubmissionsPort: 2465,
		TLS: config.TLSConfig{CertFile: certFile, KeyFile: keyFile},
	}
	srv := NewServer(cfg, "", NewBackend(nil, nil, testLogger()), false, testLogger())

	// Reserve two ports for mx and submission, then free them so Listen can
	// bind them, and keep the third occupied.
	mxProbe, _ := net.Listen("tcp", "127.0.0.1:0")
	subProbe, _ := net.Listen("tcp", "127.0.0.1:0")
	srv.mx.Addr = mxProbe.Addr().String()
	srv.submission.Addr = subProbe.Addr().String()
	_ = mxProbe.Close()
	_ = subProbe.Close()
	srv.submissions.Addr = taken.Addr().String()

	if err := srv.Listen(); err == nil {
		_ = srv.Close()
		t.Fatal("Listen succeeded although the 465 port was taken")
	}
	// Both earlier sockets must be free again.
	for _, addr := range []string{srv.mx.Addr, srv.submission.Addr} {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("%s still bound after a failed Listen: %v", addr, err)
		}
		_ = l.Close()
	}
}

// TestSubmissionsBehindProxyProtocol: PROXY is read before TLS on 465, and
// the session is a submission session whatever local port the header claims.
func TestSubmissionsBehindProxyProtocol(t *testing.T) {
	srv, b := submissionsServer(t, func(c *config.SMTPConfig) { c.ProxyProtocol = true })
	b.subPort = 0

	raw, err := net.Dial("tcp", srv.SubmissionsAddr())
	if err != nil {
		t.Fatal(err)
	}
	// The header claims port 25 as the destination.
	if _, err := io.WriteString(raw, "PROXY TCP4 192.0.2.9 10.0.0.1 40000 25\r\n"); err != nil {
		t.Fatal(err)
	}
	cl := gosmtp.NewClient(tls.Client(raw, tlsInsecure))
	t.Cleanup(func() { _ = cl.Close() })
	if err := cl.Hello("client.example"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := cl.Extension("AUTH"); !ok {
		t.Error("AUTH not offered on 465 behind PROXY protocol with a port-25 destination")
	}
	if err := cl.Mail("alice@example.com", nil); !isCode(err, 530) {
		t.Fatalf("unauthenticated MAIL = %v, want 530", err)
	}
}
