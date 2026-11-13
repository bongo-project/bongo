package pop3

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/authlimit"
)

// A successful PASS is logged at INFO with the source IP; the password never
// appears.
func TestPassSuccessLoggedAtInfo(t *testing.T) {
	a, mb := newThrottleAuth(t, map[string]string{"alice@example.com": "s3cret-pw"})
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	s := newSession(serverConn, a, mb, nil, nil, nil, false, true, authlimit.New(pop3MaxAuthFailures, time.Minute), logger)
	go func() { _, _ = io.Copy(io.Discard, clientConn) }()
	s.username = "alice@example.com"
	s.handlePass("s3cret-pw")

	out := buf.String()
	if !strings.Contains(out, `level=INFO msg="pop3: auth succeeded"`) || !strings.Contains(out, "ip=") {
		t.Errorf("no INFO auth line with the IP:\n%s", out)
	}
	if strings.Contains(out, "s3cret-pw") {
		t.Errorf("password logged:\n%s", out)
	}
}
