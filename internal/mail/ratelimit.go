package mail

import (
	"math"
	"sync"
	"time"
)

// RateLimiter enforces per-sender outbound message rate limits using a sliding window.
type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	senders map[string][]time.Time
}

// NewRateLimiter creates a rate limiter.
// If msgsPerHour <= 0, the limiter is disabled (Allow always returns true).
func NewRateLimiter(msgsPerHour int) *RateLimiter {
	return &RateLimiter{
		limit:   msgsPerHour,
		window:  time.Hour,
		senders: make(map[string][]time.Time),
	}
}

// Allow checks if the sender is allowed to send another message.
func (rl *RateLimiter) Allow(sender string) bool {
	return rl.AllowN(sender, 1)
}

// AllowN records n units against the sender's window, all or nothing: if fewer
// than n units remain nothing is recorded and false is returned. n <= 0 is
// always allowed and records nothing.
func (rl *RateLimiter) AllowN(sender string, n int) bool {
	if rl.limit <= 0 || n <= 0 {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	valid := rl.trimLocked(sender, now)
	if len(valid)+n > rl.limit {
		return false
	}
	for range n {
		valid = append(valid, now)
	}
	rl.senders[sender] = valid
	return true
}

// Remaining reports how many units the sender may still record in the current
// window. A disabled limiter reports math.MaxInt.
func (rl *RateLimiter) Remaining(sender string) int {
	if rl.limit <= 0 {
		return math.MaxInt
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return max(rl.limit-len(rl.trimLocked(sender, time.Now())), 0)
}

// trimLocked drops expired timestamps for sender and returns the live ones.
// The caller must hold rl.mu.
func (rl *RateLimiter) trimLocked(sender string, now time.Time) []time.Time {
	cutoff := now.Add(-rl.window)
	var valid []time.Time
	for _, ts := range rl.senders[sender] {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	if len(valid) == 0 {
		delete(rl.senders, sender)
	} else {
		rl.senders[sender] = valid
	}
	return valid
}
