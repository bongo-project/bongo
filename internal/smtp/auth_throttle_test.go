package smtp

import (
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	gosmtp "github.com/emersion/go-smtp"
)

func throttleTestBackend(authErr error) *Backend {
	return &Backend{
		auth:     &fakeAuth{err: authErr},
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

// perUserAuth fails every login except for the accounts in ok, which must
// present the password mapped to them.
type perUserAuth struct{ ok map[string]string }

func (p *perUserAuth) Authenticate(username, password string) (string, error) {
	if pw, found := p.ok[username]; found && pw == password {
		return "uid-" + username, nil
	}
	return "", auth.ErrBadCredentials
}

// A successful auth forgives the failures typed under the same account name,
// and only those.
func TestAuthThrottleResetsOnSuccess(t *testing.T) {
	const ip = "203.0.113.11"
	b := throttleTestBackend(nil)
	b.auth = &perUserAuth{ok: map[string]string{"bob@example.com": "correct"}}

	// Other names fail from this IP, and Bob mistypes twice before succeeding.
	for i := 0; i < maxAuthFailures-3; i++ {
		_ = newSessionForIP(b, ip).AuthPlain("wrong@example.com", "x")
	}
	for range 2 {
		if err := newSessionForIP(b, ip).AuthPlain("bob@example.com", "bad"); err == nil {
			t.Fatal("Bob's mistyped password should fail")
		}
	}
	if err := newSessionForIP(b, ip).AuthPlain("bob@example.com", "correct"); err != nil {
		t.Fatalf("valid auth should succeed, got %v", err)
	}
	// Bob's two failures are forgiven; the others are not. Two more failures
	// would have reached the limit had Bob's counted still.
	b.authFail.Fail(ip, "wrong@example.com")
	b.authFail.Fail(ip, "wrong@example.com")
	if b.authFail.Blocked(ip) {
		t.Fatal("own failures should be forgiven after a successful auth")
	}
	if !b.authFail.Fail(ip, "wrong@example.com") {
		t.Fatal("failures under other names must still count towards the IP limit")
	}
}

// An attacker logging in to their own account between guesses at a victim must
// not reset the per-IP counter.
func TestAuthThrottleOtherAccountSuccessDoesNotClear(t *testing.T) {
	const ip = "203.0.113.12"
	b := throttleTestBackend(nil)
	b.auth = &perUserAuth{ok: map[string]string{"attacker@example.com": "mine"}}

	for i := 0; i < maxAuthFailures-1; i++ {
		if err := newSessionForIP(b, ip).AuthPlain("victim@example.com", "guess"); err == nil {
			t.Fatal("victim guess should fail")
		}
	}
	if err := newSessionForIP(b, ip).AuthPlain("attacker@example.com", "mine"); err != nil {
		t.Fatalf("attacker's own login should succeed, got %v", err)
	}
	// One more guess reaches the limit; the next connection must be throttled.
	_ = newSessionForIP(b, ip).AuthPlain("victim@example.com", "guess")
	if code := smtpCode(newSessionForIP(b, ip).AuthPlain("victim@example.com", "guess")); code != 454 {
		t.Fatalf("IP should be throttled with 454 after attacker success, got code %d", code)
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
