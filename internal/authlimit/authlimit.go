// Package authlimit provides a small per-key sliding-window counter used to
// throttle repeated authentication failures (brute-force defence). It is shared
// by the protocol front-ends (IMAP, CalDAV/CardDAV) that authenticate a client
// and want to lock out a source after too many failures.
package authlimit

import (
	"sync"
	"time"
)

// sweepEvery bounds map growth: every N recorded failures the whole map is
// swept for entries whose window has elapsed, keeping memory O(active failing
// keys) without a background timer.
const sweepEvery = 1024

type state struct {
	count int
	first time.Time // first failure in the current window
}

// Limiter is a concurrency-safe per-key failed-attempt counter. Construct it
// with New; the zero value is not usable. A nil *Limiter is treated as
// disabled (all methods are no-ops / report not-blocked), which keeps callers
// that never configured one simple.
type Limiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	entries map[string]*state
	sweep   int
	nowFn   func() time.Time // nil = time.Now (injectable for tests)
}

// New returns a Limiter that blocks a key once it accumulates maxFailures
// failures within window. window also bounds how long a blocked key stays
// locked out and how long an idle key's count is remembered.
func New(maxFailures int, window time.Duration) *Limiter {
	return &Limiter{
		max:     maxFailures,
		window:  window,
		entries: make(map[string]*state),
	}
}

func (l *Limiter) now() time.Time {
	if l.nowFn != nil {
		return l.nowFn()
	}
	return time.Now()
}

// Blocked reports whether key has exceeded its failure budget within the active
// window. A stale entry (older than window) is dropped and reported as not
// blocked.
func (l *Limiter) Blocked(key string) bool {
	if l == nil || key == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.entries[key]
	if st == nil {
		return false
	}
	if l.now().Sub(st.first) > l.window {
		delete(l.entries, key)
		return false
	}
	return st.count >= l.max
}

// Record registers a failed attempt for key and reports whether key is now
// blocked. A fresh window is started when the previous one has elapsed.
func (l *Limiter) Record(key string) bool {
	if l == nil || key == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep++
	if l.sweep >= sweepEvery {
		l.sweep = 0
		for k, st := range l.entries {
			if l.now().Sub(st.first) > l.window {
				delete(l.entries, k)
			}
		}
	}

	st := l.entries[key]
	if st == nil || l.now().Sub(st.first) > l.window {
		l.entries[key] = &state{count: 1, first: l.now()}
		return 1 >= l.max
	}
	st.count++
	return st.count >= l.max
}

// Reset clears the counter for key, e.g. after a successful authentication.
func (l *Limiter) Reset(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
