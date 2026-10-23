package mail

import (
	"testing"
	"time"
)

func TestRateLimiterAllowed(t *testing.T) {
	rl := NewRateLimiter(5)

	for i := 0; i < 5; i++ {
		if !rl.Allow("alice@example.com") {
			t.Fatalf("message %d should be allowed", i+1)
		}
	}

	// 6th should be rejected.
	if rl.Allow("alice@example.com") {
		t.Error("6th message should be rejected")
	}
}

func TestRateLimiterDifferentSenders(t *testing.T) {
	rl := NewRateLimiter(2)

	if !rl.Allow("alice@example.com") {
		t.Fatal("alice message 1 should be allowed")
	}
	if !rl.Allow("alice@example.com") {
		t.Fatal("alice message 2 should be allowed")
	}
	if rl.Allow("alice@example.com") {
		t.Error("alice message 3 should be rejected")
	}

	// bob should still be allowed.
	if !rl.Allow("bob@example.com") {
		t.Error("bob message 1 should be allowed")
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	rl := NewRateLimiter(0)

	for i := 0; i < 100; i++ {
		if !rl.Allow("alice@example.com") {
			t.Fatalf("disabled limiter should always allow (msg %d)", i+1)
		}
	}
}

func TestRateLimiterAllowNAllOrNothing(t *testing.T) {
	rl := NewRateLimiter(7)

	if !rl.AllowN("u", 5) {
		t.Fatal("AllowN(5) under limit 7 should succeed")
	}
	if rl.AllowN("u", 3) {
		t.Fatal("AllowN(3) with 2 remaining should fail")
	}
	if got := rl.Remaining("u"); got != 2 {
		t.Fatalf("Remaining after refused AllowN = %d, want 2 (refusal must record nothing)", got)
	}
	if !rl.AllowN("u", 2) {
		t.Fatal("AllowN(2) with 2 remaining should succeed")
	}
	if rl.Allow("u") {
		t.Error("window should be exhausted")
	}
	if got := rl.Remaining("other"); got != 7 {
		t.Errorf("Remaining for untouched key = %d, want 7", got)
	}
}

func TestRateLimiterAllowNWindowExpiry(t *testing.T) {
	rl := NewRateLimiter(3)
	rl.window = 30 * time.Millisecond

	if !rl.AllowN("u", 3) {
		t.Fatal("AllowN(3) should succeed")
	}
	if rl.Allow("u") {
		t.Fatal("window should be full")
	}
	time.Sleep(60 * time.Millisecond)
	if got := rl.Remaining("u"); got != 3 {
		t.Fatalf("Remaining after window = %d, want 3", got)
	}
	if !rl.AllowN("u", 3) {
		t.Error("AllowN should succeed after the window expires")
	}
}

func TestRateLimiterDisabledAllowN(t *testing.T) {
	rl := NewRateLimiter(0)
	if !rl.AllowN("u", 1000) {
		t.Error("disabled limiter must allow")
	}
	if rl.Remaining("u") < 1000 {
		t.Error("disabled limiter must report ample remaining")
	}
}

func TestRateLimiterSlidingClockAndRetryAfter(t *testing.T) {
	start := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	now := start
	rl := NewRateLimiter(2)
	rl.SetClock(func() time.Time { return now })

	if d := rl.RetryAfter("a"); d != 0 {
		t.Fatalf("unused sender RetryAfter = %v, want 0", d)
	}
	rl.Allow("a") // t=0
	now = start.Add(10*time.Minute + 3*time.Second)
	rl.Allow("a") // t=10m03s
	if rl.Allow("a") {
		t.Fatal("third request inside the window should be rejected")
	}
	if d, want := rl.RetryAfter("a"), 49*time.Minute+57*time.Second; d != want {
		t.Errorf("RetryAfter = %v, want %v", d, want)
	}
	if d := rl.RetryAfter("other"); d != 0 {
		t.Errorf("other sender RetryAfter = %v, want 0", d)
	}

	tests := []struct {
		name    string
		at      time.Duration
		allowed bool
	}{
		{"59m59s still limited", 59*time.Minute + 59*time.Second, false},
		{"60m01s first entry expired", 60*time.Minute + time.Second, true},
	}
	for _, tt := range tests {
		now = start.Add(tt.at)
		if got := rl.Allow("a"); got != tt.allowed {
			t.Errorf("%s: Allow = %v, want %v", tt.name, got, tt.allowed)
		}
	}
	// Disabled limiter never reports a wait.
	off := NewRateLimiter(0)
	if d := off.RetryAfter("a"); d != 0 {
		t.Errorf("disabled RetryAfter = %v, want 0", d)
	}
}
