package greylist

import (
	"sync"
	"time"
)

// Bucket is a token bucket: rate tokens per second up to burst. It bounds the
// entries sent to and accepted from gossip peers.
type Bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

// NewBucket returns a full bucket. A nil now means time.Now.
func NewBucket(rate, burst float64, now func() time.Time) *Bucket {
	if now == nil {
		now = time.Now
	}
	return &Bucket{rate: rate, burst: burst, tokens: burst, last: now(), now: now}
}

// Allow takes one token and reports whether one was available.
func (b *Bucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.now()
	if el := t.Sub(b.last).Seconds(); el > 0 {
		b.tokens = min(b.burst, b.tokens+el*b.rate)
	}
	b.last = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
