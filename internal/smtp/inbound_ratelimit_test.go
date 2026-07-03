package smtp

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- inboundRateLimiter unit tests -------------------------------------------

func TestInboundRateLimiter_Disabled(t *testing.T) {
	rl := newInboundRateLimiter(0)
	for range 1000 {
		if !rl.Allow("1.2.3.4") {
			t.Fatal("disabled limiter should always return true")
		}
	}
}

func TestInboundRateLimiter_UnderLimit(t *testing.T) {
	rl := newInboundRateLimiter(5)
	for i := range 5 {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("Allow should return true at attempt %d (under limit)", i+1)
		}
	}
}

func TestInboundRateLimiter_AtLimitBlocked(t *testing.T) {
	rl := newInboundRateLimiter(3)

	// Fill the limit.
	for range 3 {
		rl.Allow("1.2.3.4")
	}

	// Next request must be blocked with a transient error surfaced by Data().
	if rl.Allow("1.2.3.4") {
		t.Fatal("Allow should return false when limit is reached")
	}
}

func TestInboundRateLimiter_WindowResetAllowsAgain(t *testing.T) {
	var fakeNow time.Time
	rl := &inboundRateLimiter{
		limit:   3,
		window:  time.Minute,
		entries: make(map[string][]time.Time),
		nowFn:   func() time.Time { return fakeNow },
	}

	fakeNow = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	// Fill the limit at t=0.
	for range 3 {
		if !rl.Allow("10.0.0.1") {
			t.Fatal("should be allowed before limit")
		}
	}
	if rl.Allow("10.0.0.1") {
		t.Fatal("should be blocked at limit")
	}

	// Advance past the 1-minute window.
	fakeNow = fakeNow.Add(61 * time.Second)

	// All previous timestamps are now outside the window; the IP can send again.
	if !rl.Allow("10.0.0.1") {
		t.Fatal("Allow should return true after window reset")
	}
}

func TestInboundRateLimiter_IndependentPerIP(t *testing.T) {
	rl := newInboundRateLimiter(2)

	// Fill limit for IP A.
	rl.Allow("192.168.1.1")
	rl.Allow("192.168.1.1")
	if rl.Allow("192.168.1.1") {
		t.Fatal("IP A should be blocked at limit")
	}

	// IP B is completely independent.
	if !rl.Allow("192.168.1.2") {
		t.Fatal("IP B should be unaffected by IP A being over limit")
	}
}

func TestInboundRateLimiter_PruneCleansStaleEntries(t *testing.T) {
	var fakeNow time.Time
	rl := &inboundRateLimiter{
		limit:   10,
		window:  time.Minute,
		entries: make(map[string][]time.Time),
		nowFn:   func() time.Time { return fakeNow },
	}

	fakeNow = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.2")

	// Advance past the window so all entries are stale.
	fakeNow = fakeNow.Add(2 * time.Minute)
	rl.prune()

	rl.mu.Lock()
	remaining := len(rl.entries)
	rl.mu.Unlock()

	if remaining != 0 {
		t.Fatalf("prune should remove all stale entries, got %d remaining", remaining)
	}
}

func TestInboundRateLimiter_Concurrent(t *testing.T) {
	rl := newInboundRateLimiter(1000)

	const goroutines = 50
	const attemptsEach = 20

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range attemptsEach {
				rl.Allow("10.0.0.1")
			}
		}()
	}
	wg.Wait()
	// No assertion needed — the race detector verifies safety.
}

// --- Data() 451 integration tests -------------------------------------------

func TestDataReturns451WhenRateLimited(t *testing.T) {
	b, _, _ := testBackend("user@example.com")

	// Strict limit: only 1 message per minute.
	b.inboundRL = newInboundRateLimiter(1)

	remoteIP := net.ParseIP("1.2.3.4")

	makeSession := func() *Session {
		return &Session{
			backend:    b,
			logger:     b.logger,
			from:       "sender@remote.org",
			localRcpts: []string{"user@example.com"},
			remoteIP:   remoteIP,
		}
	}

	// First message: must be accepted.
	if err := makeSession().Data(strings.NewReader("Subject: first\r\n\r\nbody")); err != nil {
		t.Fatalf("first message should succeed: %v", err)
	}

	// Second message: must be rejected with a transient 451.
	err := makeSession().Data(strings.NewReader("Subject: second\r\n\r\nbody"))
	if err == nil {
		t.Fatal("expected error on second message (rate limited)")
	}
	if code := smtpCode(err); code != 451 {
		t.Fatalf("expected SMTP 451, got %d (%v)", code, err)
	}
}

func TestDataSubmissionExemptsRateLimit(t *testing.T) {
	b, _, _ := testBackend("user@example.com")

	// Very strict: 0 messages per minute (disabled means allow all; use 1 to test exemption).
	b.inboundRL = newInboundRateLimiter(1)

	remoteIP := net.ParseIP("1.2.3.4")

	makeSession := func() *Session {
		return &Session{
			backend:    b,
			logger:     b.logger,
			from:       "sender@remote.org",
			localRcpts: []string{"user@example.com"},
			remoteIP:   remoteIP,
			submission: true, // submission port — exempt from inbound rate limit
			authed:     true,
		}
	}

	// Both messages must succeed on the submission path.
	for i := range 5 {
		if err := makeSession().Data(strings.NewReader("Subject: test\r\n\r\nbody")); err != nil {
			t.Fatalf("submission message %d should succeed: %v", i+1, err)
		}
	}
}

func TestDataRateLimitNilLimiterAlwaysAllows(t *testing.T) {
	b, _, _ := testBackend("user@example.com")
	// inboundRL is nil — all messages should pass through.
	b.inboundRL = nil

	s := &Session{
		backend:    b,
		logger:     b.logger,
		from:       "sender@remote.org",
		localRcpts: []string{"user@example.com"},
		remoteIP:   net.ParseIP("1.2.3.4"),
	}

	for range 10 {
		s.localRcpts = []string{"user@example.com"} // reset after Reset clears it
		if err := s.Data(strings.NewReader("Subject: test\r\n\r\nbody")); err != nil {
			t.Fatalf("nil limiter should never block: %v", err)
		}
		s.Reset()
		s.from = "sender@remote.org"
		s.localRcpts = []string{"user@example.com"}
	}
}
