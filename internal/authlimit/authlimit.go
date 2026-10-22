// Package authlimit provides a small per-key sliding-window counter used to
// throttle repeated authentication failures (brute-force defence). It is shared
// by the protocol front-ends (IMAP, CalDAV/CardDAV) that authenticate a client
// and want to lock out a source after too many failures.
package authlimit

import (
	"strings"
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

// pairKey builds the per-(IP, account) key. The prefix and NUL separators keep
// it from colliding with a plain IP key. The account is lowercased and trimmed
// only; aliases are deliberately not resolved, as that would need a lookup that
// reveals whether an account exists. Legacy accounts differing only by case
// share a pair; that edge is accepted. A pair keeps its own window, so a live
// pair started late in an earlier IP window can be forgiven into a new one,
// allowing at most one burst of about twice the limit.
func pairKey(ip, account string) string {
	return "acct\x00" + ip + "\x00" + strings.ToLower(strings.TrimSpace(account))
}

// Fail records a failed attempt against both the source IP and the
// (IP, account) pair, and reports whether the IP is now blocked. An empty ip
// records nothing.
func (l *Limiter) Fail(ip, account string) bool {
	if l == nil || ip == "" {
		return false
	}
	l.Record(pairKey(ip, account))
	return l.Record(ip)
}

// Succeed registers a successful authentication of account from ip. It
// forgives only the failures previously recorded for that same normalised
// (IP, account) pair, by subtracting them from the IP counter. A success never
// clears the IP counter outright: otherwise an attacker could log in to their
// own account between guesses and get unlimited guesses against others.
func (l *Limiter) Succeed(ip, account string) {
	if l == nil || ip == "" {
		return
	}
	pk := pairKey(ip, account)

	l.mu.Lock()
	defer l.mu.Unlock()

	pair := l.entries[pk]
	if pair == nil {
		return
	}
	delete(l.entries, pk)
	if l.now().Sub(pair.first) > l.window {
		return
	}
	st := l.entries[ip]
	if st == nil {
		return
	}
	st.count -= pair.count
	if st.count <= 0 {
		delete(l.entries, ip)
	}
}
