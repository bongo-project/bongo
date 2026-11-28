package mail

import (
	"math"
	"testing"
	"time"
)

func TestAllowNLimitOverridesGlobal(t *testing.T) {
	tests := []struct {
		name    string
		global  int
		limit   int
		allowed int // how many single units pass before the first refusal
	}{
		{"override below global", 5, 2, 2},
		{"override above global", 2, 4, 4},
		{"override counts when global is off", 0, 3, 3},
		{"zero override is unlimited", 2, 0, 10},
		{"global limit through AllowN", 3, 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRateLimiterWindow(tt.global, time.Minute)
			passed := 0
			for range 10 {
				if !rl.AllowNLimit("u", 1, tt.limit) {
					break
				}
				passed++
			}
			if passed != tt.allowed {
				t.Fatalf("passed %d, want %d", passed, tt.allowed)
			}
		})
	}
}

func TestAllowNLimitAllOrNothingAndRemaining(t *testing.T) {
	rl := NewRateLimiterWindow(0, time.Minute)
	if !rl.AllowNLimit("u", 3, 5) {
		t.Fatal("3 of 5 refused")
	}
	if rl.AllowNLimit("u", 3, 5) {
		t.Fatal("6 of 5 accepted")
	}
	if got := rl.RemainingLimit("u", 5); got != 2 {
		t.Fatalf("remaining = %d, want 2 (a refused request records nothing)", got)
	}
	if got := rl.RemainingLimit("u", 0); got != math.MaxInt {
		t.Fatalf("remaining with no limit = %d", got)
	}
	if rl.Limit() != 0 {
		t.Fatal("Limit changed")
	}
}

func TestRateLimiterWindowExpires(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rl := NewRateLimiterWindow(1, time.Minute)
	rl.SetClock(func() time.Time { return now })
	if !rl.Allow("u") || rl.Allow("u") {
		t.Fatal("first allowed, second refused expected")
	}
	now = now.Add(61 * time.Second)
	if !rl.Allow("u") {
		t.Fatal("still refused after the window")
	}
	// The hour limiter keeps its window.
	h := NewRateLimiter(1)
	h.SetClock(func() time.Time { return now })
	h.Allow("u")
	now = now.Add(2 * time.Minute)
	if h.Allow("u") {
		t.Fatal("hourly limiter let a second unit through after two minutes")
	}
}
