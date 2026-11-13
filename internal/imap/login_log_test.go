package imap

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/authlimit"
)

// A successful login is logged at INFO with the source IP, so an operator can
// confirm logins after a deploy at the default log level. The password never
// appears.
func TestLoginSuccessLoggedAtInfo(t *testing.T) {
	var buf bytes.Buffer
	s := newThrottleSession(&perUserAuth{ok: map[string]bool{"alice@example.com": true}},
		authlimit.New(imapMaxAuthFailures, time.Minute), "203.0.113.40")
	s.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := s.Login("alice@example.com", "s3cret-pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `level=INFO msg="imap: login succeeded"`) || !strings.Contains(out, "ip=203.0.113.40") {
		t.Errorf("no INFO login line with the IP:\n%s", out)
	}
	if strings.Contains(out, "s3cret-pw") {
		t.Errorf("password logged:\n%s", out)
	}
}
