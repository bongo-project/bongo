package pop3

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// testPOP3 connects to a POP3 server and provides helpers for protocol interaction.
type testPOP3 struct {
	conn   net.Conn
	reader *bufio.Reader
	t      *testing.T
}

func newTestPOP3(t *testing.T, addr string) *testPOP3 {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	tc := &testPOP3{
		conn:   conn,
		reader: bufio.NewReader(conn),
		t:      t,
	}
	// Read greeting.
	greeting := tc.readLine()
	if !strings.HasPrefix(greeting, "+OK") {
		t.Fatalf("expected +OK greeting, got: %s", greeting)
	}
	return tc
}

func (tc *testPOP3) send(cmd string) {
	tc.t.Helper()
	tc.conn.Write([]byte(cmd + "\r\n"))
}

func (tc *testPOP3) readLine() string {
	tc.t.Helper()
	tc.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := tc.reader.ReadString('\n')
	if err != nil {
		tc.t.Fatalf("read: %v", err)
	}
	return strings.TrimRight(line, "\r\n")
}

func (tc *testPOP3) readMultiLine() []string {
	tc.t.Helper()
	var lines []string
	for {
		line := tc.readLine()
		if line == "." {
			break
		}
		lines = append(lines, line)
	}
	return lines
}

func (tc *testPOP3) expectOK() string {
	tc.t.Helper()
	line := tc.readLine()
	if !strings.HasPrefix(line, "+OK") {
		tc.t.Fatalf("expected +OK, got: %s", line)
	}
	return line
}

func (tc *testPOP3) expectErr() string {
	tc.t.Helper()
	line := tc.readLine()
	if !strings.HasPrefix(line, "-ERR") {
		tc.t.Fatalf("expected -ERR, got: %s", line)
	}
	return line
}

func (tc *testPOP3) close() {
	tc.conn.Close()
}

func TestWriteByteStuffed(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		bodyLines int
		want      string
	}{
		{
			name:      "simple message",
			data:      "Subject: test\r\n\r\nHello\r\n",
			bodyLines: -1,
			want:      "Subject: test\r\n\r\nHello\r\n.\r\n",
		},
		{
			name:      "dot stuffing",
			data:      "Subject: test\r\n\r\n.Hello\r\n..World\r\n",
			bodyLines: -1,
			want:      "Subject: test\r\n\r\n..Hello\r\n...World\r\n.\r\n",
		},
		{
			name:      "TOP with limited body lines",
			data:      "Subject: test\r\n\r\nLine1\r\nLine2\r\nLine3\r\n",
			bodyLines: 2,
			want:      "Subject: test\r\n\r\nLine1\r\nLine2\r\n.\r\n",
		},
		{
			name:      "TOP headers only",
			data:      "Subject: test\r\nFrom: a@b.c\r\n\r\nBody\r\n",
			bodyLines: 0,
			want:      "Subject: test\r\nFrom: a@b.c\r\n\r\n.\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			s := &session{
				writer: bufio.NewWriter(&buf),
			}
			s.writeByteStuffed([]byte(tt.data), tt.bodyLines)
			got := buf.String()
			if got != tt.want {
				t.Errorf("writeByteStuffed() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestActiveStats(t *testing.T) {
	s := &session{
		messages: []popMsg{
			{size: 100},
			{size: 200},
			{size: 300},
		},
		deleted: map[int]bool{2: true},
	}

	count, size := s.activeStats()
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if size != 400 {
		t.Errorf("size = %d, want 400", size)
	}
}

func TestSessionIdleConnectionTimesOut(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	s := newSession(serverConn, nil, nil, nil, nil, nil, false, false, slog.Default())
	go func() {
		s.handle()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("idle POP3 connection did not time out")
	}
}

func TestServerListenAndClose(t *testing.T) {
	s := &Server{
		logger:  slog.Default(),
		closeCh: make(chan struct{}),
	}

	if err := s.Listen("127.0.0.1:0", "", false); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	addr := s.PlainAddr()
	if addr == "" {
		t.Fatal("expected non-empty address")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestServerListenTLS(t *testing.T) {
	tlsCfg := testTLSConfig(t)

	s := &Server{
		tlsCfg:  tlsCfg,
		logger:  slog.Default(),
		closeCh: make(chan struct{}),
	}

	if err := s.Listen("127.0.0.1:0", "127.0.0.1:0", false); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	if s.tlsListener == nil {
		t.Fatal("expected TLS listener")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}
