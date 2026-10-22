package imap

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/netx"
)

const (
	clientA = "203.0.113.7"
	clientB = "198.51.100.9"
)

func selfSignedConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
}

// startLimitedServer starts an IMAP server behind a PROXY-protocol listener
// (loopback is a trusted proxy) so each test dial chooses its client IP.
func startLimitedServer(t *testing.T, maxConn, maxPerIP int) (plain, imaps string) {
	t.Helper()
	tlsCfg := selfSignedConfig(t)
	s := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, tlsCfg, false, slog.New(slog.DiscardHandler))
	s.SetConnectionLimits(maxConn, maxPerIP)
	if err := s.Listen("127.0.0.1:0", "127.0.0.1:0", tlsCfg, true); err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve() }()
	t.Cleanup(func() { _ = s.Close() })
	return s.plainListener.Addr().String(), s.tlsListener.Addr().String()
}

// clientConn wraps the TLS client lazily so a test can observe the raw socket
// (no greeting while the global cap holds) before starting the handshake. A
// read deadline expiring mid-handshake would poison a tls.Conn.
type clientConn struct {
	net.Conn
	r       *bufio.Reader
	useTLS  bool
	started bool
}

func (c *clientConn) start() {
	if c.started {
		return
	}
	c.started = true
	if c.useTLS {
		c.Conn = tls.Client(c.Conn, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test only
	}
	c.r = bufio.NewReader(c.Conn)
}

// quiet reports whether the server stays silent for d. A TLS server only
// speaks after the handshake, so for TLS the handshake itself must not
// complete; it is left running so a later read can finish it.
func (c *clientConn) quiet(d time.Duration) bool {
	if c.useTLS {
		c.start()
		done := make(chan error, 1)
		go func() { done <- c.Conn.(*tls.Conn).Handshake() }()
		select {
		case <-done:
			return false
		case <-time.After(d):
			return true
		}
	}
	_ = c.SetReadDeadline(time.Now().Add(d))
	var b [1]byte
	_, err := c.Read(b[:])
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func dialAs(t *testing.T, addr, ip string, useTLS bool) *clientConn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := io.WriteString(c, "PROXY TCP4 "+ip+" 10.0.0.1 40000 143\r\n"); err != nil {
		t.Fatal(err)
	}
	return &clientConn{Conn: c, useTLS: useTLS}
}

// line reads one line with a deadline; ok is false on timeout or EOF.
func (c *clientConn) line(d time.Duration) (string, bool) {
	c.start()
	_ = c.SetReadDeadline(time.Now().Add(d))
	s, err := c.r.ReadString('\n')
	return strings.TrimSpace(s), err == nil
}

func (c *clientConn) wantGreeting(t *testing.T, prefix string) {
	t.Helper()
	got, ok := c.line(5 * time.Second)
	if !ok || !strings.HasPrefix(got, prefix) {
		t.Fatalf("greeting = %q (ok=%v), want prefix %q", got, ok, prefix)
	}
}

// wantRefused expects the server to drop the connection without a greeting
// (refusal happens before the TLS handshake, so there is no BYE to read).
func (c *clientConn) wantRefused(t *testing.T) {
	t.Helper()
	c.start()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := c.r.ReadByte()
	var ne net.Error
	if err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		t.Fatalf("expected connection to be closed, got %v", err)
	}
}

// waitForSlot polls until a fresh connection from ip is greeted with OK.
func waitForSlot(t *testing.T, addr, ip string, useTLS bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c := dialAs(t, addr, ip, useTLS)
		if got, ok := c.line(time.Second); ok && strings.HasPrefix(got, "* OK") {
			_ = c.Close()
			return
		}
		_ = c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("slot was never freed")
}

func TestIMAPPerIPConnectionCap(t *testing.T) {
	plain, _ := startLimitedServer(t, 100, 2)

	c1 := dialAs(t, plain, clientA, false)
	c1.wantGreeting(t, "* OK")
	c2 := dialAs(t, plain, clientA, false)
	c2.wantGreeting(t, "* OK")

	c3 := dialAs(t, plain, clientA, false)
	c3.wantRefused(t)

	// A different client is unaffected while A is full.
	other := dialAs(t, plain, clientB, false)
	other.wantGreeting(t, "* OK")

	// Closing one frees a slot.
	_ = c1.Close()
	waitForSlot(t, plain, clientA, false)
}

func TestIMAPSharedLimitAcrossPlainAndTLS(t *testing.T) {
	plain, imaps := startLimitedServer(t, 100, 2)

	c1 := dialAs(t, plain, clientA, false)
	c1.wantGreeting(t, "* OK")
	c2 := dialAs(t, imaps, clientA, true)
	c2.wantGreeting(t, "* OK")

	c3 := dialAs(t, imaps, clientA, true)
	c3.wantRefused(t)
	c4 := dialAs(t, plain, clientA, false)
	c4.wantRefused(t)

	other := dialAs(t, imaps, clientB, true)
	other.wantGreeting(t, "* OK")
}

func TestIMAPPerIPNoLeakOnReconnect(t *testing.T) {
	plain, _ := startLimitedServer(t, 100, 1)
	for range 5 {
		waitForSlot(t, plain, clientA, false)
	}
}

func TestIMAPGlobalConnectionCap(t *testing.T) {
	for _, useTLS := range []bool{false, true} {
		name := "plain"
		if useTLS {
			name = "imaps"
		}
		t.Run(name, func(t *testing.T) {
			plain, secure := startLimitedServer(t, 2, 0)
			addr := plain
			if useTLS {
				addr = secure
			}

			c1 := dialAs(t, addr, clientA, useTLS)
			c1.wantGreeting(t, "* OK")
			c2 := dialAs(t, addr, clientB, useTLS)
			c2.wantGreeting(t, "* OK")

			// The listener holds the third connection without accepting it.
			c3 := dialAs(t, addr, clientA, useTLS)
			if !c3.quiet(300 * time.Millisecond) {
				t.Fatal("third connection got data despite global cap")
			}

			_ = c1.Close()
			c3.wantGreeting(t, "* OK")
		})
	}
}

// A client over its per-IP cap that never sends a ClientHello must not park
// on a global slot: refusing it may not wait on the TLS handshake.
func TestIMAPSSilentOverCapClientsDoNotStarveOthers(t *testing.T) {
	_, imaps := startLimitedServer(t, 3, 1)

	a := dialAs(t, imaps, clientA, true)
	a.wantGreeting(t, "* OK")

	// Over A's cap, silent: raw dials that never start a handshake.
	dialAs(t, imaps, clientA, true)
	dialAs(t, imaps, clientA, true)
	time.Sleep(300 * time.Millisecond)

	b := dialAs(t, imaps, clientB, true)
	b.wantGreeting(t, "* OK")
}

// A freshly built server must enforce the default per-IP cap even if the
// agent never calls SetConnectionLimits.
func TestIMAPNewServerDefaultsPerIPCap(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, false, slog.New(slog.DiscardHandler))
	if got := s.connLimit.Max(); got != netx.DefaultMaxConnectionsPerIP {
		t.Fatalf("default per-IP cap = %d, want %d", got, netx.DefaultMaxConnectionsPerIP)
	}
}
