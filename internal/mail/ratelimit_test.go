package mail

import (
	"testing"
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
