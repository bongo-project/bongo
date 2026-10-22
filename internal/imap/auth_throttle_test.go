package imap

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
)

// perUserAuth succeeds only for the accounts in ok.
type perUserAuth struct{ ok map[string]bool }

func (p *perUserAuth) Authenticate(username, _ string) (string, error) {
	if p.ok[username] {
		return "uid-" + username, nil
	}
	return "", auth.ErrBadCredentials
}

func newThrottleSession(a auth.Authenticator, lim *authlimit.Limiter, ip string) *session {
	return &session{
		auth:        a,
		authLimiter: lim,
		remoteIP:    net.ParseIP(ip),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// Logging in to an unrelated account between guesses at a victim must not
// reset the per-IP counter.
func TestLoginOtherAccountSuccessDoesNotClearThrottle(t *testing.T) {
	const ip = "203.0.113.20"
	lim := authlimit.New(imapMaxAuthFailures, time.Minute)
	a := &perUserAuth{ok: map[string]bool{"attacker@example.com": true}}

	for i := 0; i < imapMaxAuthFailures-1; i++ {
		if err := newThrottleSession(a, lim, ip).Login("victim@example.com", "guess"); err == nil {
			t.Fatal("victim guess should fail")
		}
	}
	if err := newThrottleSession(a, lim, ip).Login("attacker@example.com", "mine"); err != nil {
		t.Fatalf("attacker's own login should succeed, got %v", err)
	}
	// The final guess reaches the limit, and later attempts stay throttled.
	_ = newThrottleSession(a, lim, ip).Login("victim@example.com", "guess")
	err := newThrottleSession(a, lim, ip).Login("victim@example.com", "guess")
	if !errors.Is(err, errAuthRateLimited) {
		t.Fatalf("IP should be throttled after attacker success, got %v", err)
	}
}

// Positive control: a user's own success forgives their own earlier typos.
func TestLoginOwnSuccessForgivesOwnFailures(t *testing.T) {
	const ip = "203.0.113.21"
	lim := authlimit.New(imapMaxAuthFailures, time.Minute)
	bad := &perUserAuth{}
	good := &perUserAuth{ok: map[string]bool{"alice@example.com": true}}

	for i := 0; i < imapMaxAuthFailures-1; i++ {
		_ = newThrottleSession(bad, lim, ip).Login("Alice@example.com", "typo")
	}
	if err := newThrottleSession(good, lim, ip).Login("alice@example.com", "right"); err != nil {
		t.Fatalf("login should succeed, got %v", err)
	}
	// One more failure would reach the limit had the earlier ones still counted.
	_ = newThrottleSession(bad, lim, ip).Login("alice@example.com", "typo")
	if lim.Blocked(ip) {
		t.Fatal("own failures should be forgiven; IP must not be blocked")
	}
}
