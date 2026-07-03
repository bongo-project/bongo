package smtp

import (
	"sync"
	"time"
)

// inboundRateLimiter enforces per-source-IP message rate limits on the inbound
// SMTP path using a sliding window. It is safe for concurrent use.
//
// Memory: each Allow call trims expired timestamps for the calling IP; stale
// entries for IPs that stop sending persist until the next Allow call from that
// IP (or an explicit prune() call). For a 1-minute window the worst case is
// limit × 16 bytes per dormant IP, which is negligible in practice.
type inboundRateLimiter struct {
	mu      sync.Mutex
	limit   int // max messages per window; 0 means disabled
	window  time.Duration
	entries map[string][]time.Time
	calls   int              // Allow calls since the last full sweep
	nowFn   func() time.Time // nil = time.Now (injectable for tests)
}

// inboundPruneEvery bounds map growth: every N Allow calls the whole map is
// swept for dormant IPs. Keeps memory O(active IPs) without a background timer.
const inboundPruneEvery = 1024

// newInboundRateLimiter creates a sliding-window rate limiter keyed by source IP.
// If msgsPerMinute <= 0 the limiter is disabled (Allow always returns true).
func newInboundRateLimiter(msgsPerMinute int) *inboundRateLimiter {
	return &inboundRateLimiter{
		limit:   msgsPerMinute,
		window:  time.Minute,
		entries: make(map[string][]time.Time),
	}
}

func (rl *inboundRateLimiter) now() time.Time {
	if rl.nowFn != nil {
		return rl.nowFn()
	}
	return time.Now()
}

// Allow reports whether the given IP is within its rate limit, recording an
// accepted-message timestamp if so. It prunes expired timestamps for this IP on
// every call so the per-IP slice stays small.
func (rl *inboundRateLimiter) Allow(ip string) bool {
	if rl.limit <= 0 {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	cutoff := now.Add(-rl.window)

	// Periodically sweep the whole map so IPs that have gone quiet are dropped.
	rl.calls++
	if rl.calls >= inboundPruneEvery {
		rl.calls = 0
		rl.pruneLocked(cutoff)
	}

	// Trim expired timestamps for this IP.
	var valid []time.Time
	for _, ts := range rl.entries[ip] {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}

	if len(valid) >= rl.limit {
		rl.entries[ip] = valid
		return false
	}

	rl.entries[ip] = append(valid, now)
	return true
}

// prune removes map entries whose timestamps have all fallen outside the window.
// Safe to call concurrently. Allow also sweeps opportunistically.
func (rl *inboundRateLimiter) prune() {
	if rl.limit <= 0 {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.pruneLocked(rl.now().Add(-rl.window))
}

// pruneLocked sweeps the map for dormant IPs. The caller must hold rl.mu.
func (rl *inboundRateLimiter) pruneLocked(cutoff time.Time) {
	for ip, timestamps := range rl.entries {
		var valid []time.Time
		for _, ts := range timestamps {
			if ts.After(cutoff) {
				valid = append(valid, ts)
			}
		}
		if len(valid) == 0 {
			delete(rl.entries, ip)
		} else {
			rl.entries[ip] = valid
		}
	}
}
