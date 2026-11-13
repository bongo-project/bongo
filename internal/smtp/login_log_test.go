package smtp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// A successful AUTH is logged at INFO with the source IP; the password never
// appears.
func TestAuthSuccessLoggedAtInfo(t *testing.T) {
	var buf bytes.Buffer
	b := throttleTestBackend(nil)
	b.auth = &perUserAuth{ok: map[string]string{"bob@example.com": "s3cret-pw"}}
	b.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := newSessionForIP(b, "203.0.113.41").AuthPlain("bob@example.com", "s3cret-pw"); err != nil {
		t.Fatalf("AuthPlain: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `level=INFO msg="smtp: auth succeeded"`) || !strings.Contains(out, "ip=203.0.113.41") {
		t.Errorf("no INFO auth line with the IP:\n%s", out)
	}
	if strings.Contains(out, "s3cret-pw") {
		t.Errorf("password logged:\n%s", out)
	}
}
