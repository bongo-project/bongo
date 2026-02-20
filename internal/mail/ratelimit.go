package mail

import (
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
	if rl.limit <= 0 {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Trim old entries.
	timestamps := rl.senders[sender]
	var valid []time.Time
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}

	if len(valid) >= rl.limit {
		rl.senders[sender] = valid
		return false
	}

	rl.senders[sender] = append(valid, now)
	return true
}
