package smtp

import (
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/authlimit"
	gosmtp "github.com/emersion/go-smtp"
)

func throttleTestBackend(authErr error) *Backend {
	return &Backend{
		auth:     &fakeAuth{err: authErr},
		perIP:    map[string]int{},
		authFail: authlimit.New(maxAuthFailures, authFailWindow),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func newSessionForIP(b *Backend, ip string) *Session {
	return &Session{backend: b, logger: b.logger, submission: true, remoteIP: net.ParseIP(ip)}
}

// After maxAuthFailures on one connection, the next attempt is a 454 throttle.
func TestAuthThrottleSameSession(t *testing.T) {
	b := throttleTestBackend(auth.ErrBadCredentials)
	s := newSessionForIP(b, "203.0.113.7")

	for i := 0; i < maxAuthFailures; i++ {
		if code := smtpCode(s.AuthPlain("bob@example.com", "nope")); code != 0 {
			t.Fatalf("attempt %d should return the raw auth error, got SMTP code %d", i+1, code)
		}
	}
	if code := smtpCode(s.AuthPlain("bob@example.com", "nope")); code != 454 {
		t.Fatalf("attempt %d should be throttled with 454, got code %d", maxAuthFailures+1, code)
	}
}

// A brand-new connection from an IP that already exhausted its budget is
// throttled immediately — the real brute-force protection across reconnects.
func TestAuthThrottlePersistsAcrossSessions(t *testing.T) {
	b := throttleTestBackend(auth.ErrBadCredentials)

	s1 := newSessionForIP(b, "203.0.113.9")
	for i := 0; i < maxAuthFailures; i++ {
		_ = s1.AuthPlain("bob@example.com", "nope")
	}

	s2 := newSessionForIP(b, "203.0.113.9")
	if code := smtpCode(s2.AuthPlain("bob@example.com", "nope")); code != 454 {
		t.Fatalf("new connection from a locked-out IP should be throttled with 454, got code %d", code)
	}

	// A different IP is unaffected.
	s3 := newSessionForIP(b, "198.51.100.1")
	if code := smtpCode(s3.AuthPlain("bob@example.com", "nope")); code != 0 {
		t.Fatalf("unrelated IP should not be throttled, got code %d", code)
	}
}

// A successful auth clears the failure counter for that IP.
func TestAuthThrottleResetsOnSuccess(t *testing.T) {
	b := throttleTestBackend(nil) // Authenticate always succeeds
	s := newSessionForIP(b, "203.0.113.11")

	// Pre-load some failures directly, then a success should clear them.
	b.authFail.Record("203.0.113.11")
	b.authFail.Record("203.0.113.11")

	if err := s.AuthPlain("bob@example.com", "correct"); err != nil {
		t.Fatalf("valid auth should succeed, got %v", err)
	}
	if b.authFail.Blocked("203.0.113.11") {
		t.Fatal("failure counter should be cleared after a successful auth")
	}
}

// Nil remoteIP (no per-IP key) must not panic and still throttles per session.
func TestAuthThrottleNilIP(t *testing.T) {
	b := throttleTestBackend(auth.ErrBadCredentials)
	s := &Session{backend: b, logger: b.logger, submission: true}

	var err error
	for i := 0; i < maxAuthFailures+1; i++ {
		err = s.AuthPlain("bob@example.com", "nope")
	}
	if _, ok := err.(*gosmtp.SMTPError); !ok {
		t.Fatalf("session with nil IP should still be throttled, got %v", err)
	}
}
