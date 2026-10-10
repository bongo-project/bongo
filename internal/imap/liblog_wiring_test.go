package imap

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/security"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A client that drops mid-command makes go-imap log "failed to read command".
// That line must reach bongo's slog logger, on both the plain and TLS servers.
func TestServerRoutesLibraryLogsToSlog(t *testing.T) {
	tlsCfg, err := security.LoadOrGenerate("", "", []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("tls config: %v", err)
	}
	var out syncBuffer
	logger := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, tlsCfg, true, logger)
	if err := s.Listen("127.0.0.1:0", "127.0.0.1:0", tlsCfg, false); err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = s.Serve() }()
	t.Cleanup(func() { _ = s.Close() })

	tests := []struct {
		name string
		dial func() (net.Conn, error)
	}{
		{"plain", func() (net.Conn, error) { return net.Dial("tcp", s.plainListener.Addr().String()) }},
		{"tls", func() (net.Conn, error) {
			return tls.Dial("tcp", s.tlsListener.Addr().String(), &tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- test against self-signed cert
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := strings.Count(out.String(), `"imap.read_error"`)
			conn, err := tt.dial()
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
				t.Fatalf("greeting: %v", err)
			}
			if _, err := conn.Write([]byte("a1 NOOP")); err != nil {
				t.Fatalf("write: %v", err)
			}
			_ = conn.Close()

			deadline := time.Now().Add(5 * time.Second)
			for strings.Count(out.String(), `"imap.read_error"`) == before {
				if time.Now().After(deadline) {
					t.Fatalf("no imap.read_error record; slog output:\n%s", out.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
				var rec map[string]any
				if err := json.Unmarshal([]byte(line), &rec); err != nil {
					t.Fatalf("non-JSON line: %q", line)
				}
				if rec["msg"] == "imap.read_error" && rec["level"] != "DEBUG" {
					t.Errorf("level = %v, want DEBUG", rec["level"])
				}
			}
		})
	}
}
