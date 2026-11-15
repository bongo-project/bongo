package mail

import (
	"math"
	"sync"
	"time"
)

// FanoutLimiter bounds how many group member copies one key may cause in a
// sliding hour. It differs from RateLimiter in what it stores: one entry per
// charge, not one timestamp per unit, so a 1000-member post costs one entry
// and not about 24 KB. Keys from the internet (source IPs) are not bounded by
// the number of users, so expired keys are also swept from time to time and
// not only when the same key returns.
type FanoutLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	keys      map[string][]fanoutCharge
	now       func() time.Time // nil = time.Now
	lastSweep time.Time
}

type fanoutCharge struct {
	at time.Time
	n  int
}

// fanoutSweepInterval is how often an AllowN call also walks every key.
const fanoutSweepInterval = time.Minute

// NewFanoutLimiter creates a limiter of perHour member copies per key. A value
// of 0 or less disables it.
func NewFanoutLimiter(perHour int) *FanoutLimiter {
	return &FanoutLimiter{
		limit:  perHour,
		window: time.Hour,
		keys:   make(map[string][]fanoutCharge),
	}
}

// SetClock replaces the time source, for tests. A nil function restores
// time.Now.
func (l *FanoutLimiter) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// Limit returns the configured limit per hour (0 = disabled).
func (l *FanoutLimiter) Limit() int { return l.limit }

func (l *FanoutLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// Remaining reports how many copies key may still cause. A disabled limiter
// reports math.MaxInt. It records nothing.
func (l *FanoutLimiter) Remaining(key string) int {
	if l.limit <= 0 {
		return math.MaxInt
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return max(l.limit-l.usedLocked(key, l.clock()), 0)
}

// AllowN records n copies against key, all or nothing. n <= 0 is always
// allowed and records nothing.
func (l *FanoutLimiter) AllowN(key string, n int) bool {
	if l.limit <= 0 || n <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	l.sweepLocked(now)
	if l.usedLocked(key, now)+n > l.limit {
		return false
	}
	l.keys[key] = append(l.keys[key], fanoutCharge{at: now, n: n})
	return true
}

// Keys returns the number of keys held, for tests and metrics.
func (l *FanoutLimiter) Keys() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.keys)
}

// usedLocked drops key's expired charges and returns the live total.
func (l *FanoutLimiter) usedLocked(key string, now time.Time) int {
	charges := l.keys[key]
	if len(charges) == 0 {
		return 0
	}
	cutoff := now.Add(-l.window)
	live := charges[:0]
	total := 0
	for _, c := range charges {
		if c.at.After(cutoff) {
			live = append(live, c)
			total += c.n
		}
	}
	if len(live) == 0 {
		delete(l.keys, key)
		return 0
	}
	l.keys[key] = live
	return total
}

// sweepLocked removes every key with no live charge, at most once per
// fanoutSweepInterval.
func (l *FanoutLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < fanoutSweepInterval {
		return
	}
	l.lastSweep = now
	for key := range l.keys {
		l.usedLocked(key, now)
	}
}
