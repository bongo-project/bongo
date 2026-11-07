package smtp

import (
	"bufio"
	"io"
	"net"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/observability/obstest"
)

var smtpLabels = map[string]string{"protocol": "smtp"}

// TestSMTPMetrics runs a session against the real server: the connection
// gauge and counter move with it, and MAIL and RSET are observed.
func TestSMTPMetrics(t *testing.T) {
	cfg := &config.SMTPConfig{
		Port:           2525,
		SubmissionPort: 2587,
		Limits:         config.SMTPLimits{MaxConnections: 100, MaxConnectionsPerIP: 10},
	}
	srv := NewServer(cfg, "", NewBackend(nil, nil, testLogger()), true, testLogger())
	srv.mx.Addr = "127.0.0.1:0"
	srv.submission.Addr = "127.0.0.1:0"
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })

	open0 := obstest.Gauge(t, "bongo_protocol_connections", smtpLabels)
	acc0 := obstest.Counter(t, "bongo_protocol_connections_accepted_total", smtpLabels)
	mail0 := obstest.Command(t, "smtp", "mail")
	reset0 := obstest.Command(t, "smtp", "reset")

	// A plain dial: this server has no PROXY listener, so dialSMTPAs's header
	// would be read as a command.
	raw, err := net.Dial("tcp", srv.MXAddr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	c := &smtpTestConn{Conn: raw, r: bufio.NewReader(raw)}
	c.want(t, "220")
	if got := obstest.Gauge(t, "bongo_protocol_connections", smtpLabels) - open0; got != 1 {
		t.Errorf("open delta = %v while connected, want 1", got)
	}
	if got := obstest.Counter(t, "bongo_protocol_connections_accepted_total", smtpLabels) - acc0; got != 1 {
		t.Errorf("accepted delta = %v, want 1", got)
	}

	for _, cmd := range []string{"EHLO client.example", "MAIL FROM:<a@example.com>", "RSET"} {
		if _, err := io.WriteString(c, cmd+"\r\n"); err != nil {
			t.Fatal(err)
		}
		for {
			line, ok := c.line(5 * time.Second)
			if !ok {
				t.Fatalf("no reply to %q", cmd)
			}
			if len(line) < 4 || line[3] != '-' {
				break
			}
		}
	}
	if got := obstest.Command(t, "smtp", "mail") - mail0; got != 1 {
		t.Errorf("mail samples +%d, want +1", got)
	}
	if got := obstest.Command(t, "smtp", "reset") - reset0; got < 1 {
		t.Errorf("reset samples +%d, want at least 1", got)
	}

	if _, err := io.WriteString(c, "QUIT\r\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for obstest.Gauge(t, "bongo_protocol_connections", smtpLabels) != open0 {
		if time.Now().After(deadline) {
			t.Fatalf("open gauge delta = %v after QUIT, want 0",
				obstest.Gauge(t, "bongo_protocol_connections", smtpLabels)-open0)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
