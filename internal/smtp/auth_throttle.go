package smtp

import "time"

const (
	// maxAuthFailures is the number of failed AUTH attempts (per connection and
	// per source IP) tolerated before further attempts are throttled.
	maxAuthFailures = 5

	// authFailWindow is how long a source IP stays locked out after exceeding
	// maxAuthFailures, and the idle period after which its failure count is
	// forgotten.
	authFailWindow = 15 * time.Minute

	// authFailSweepEvery bounds map growth: every N recorded failures the whole
	// map is swept for entries whose window has elapsed. Keeps memory O(active
	// failing IPs) without a background timer.
	authFailSweepEvery = 1024
)

// authFailState is the per-IP failed-AUTH counter.
type authFailState struct {
	count int
	first time.Time // time of the first failure in the current window
}

// authThrottled reports whether the given source IP has exceeded the failed
// AUTH budget within the active window. A stale (expired) entry is dropped and
// treated as not throttled.
func (b *Backend) authThrottled(ip string) bool {
	if ip == "" || b.authFail == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	st := b.authFail[ip]
	if st == nil {
		return false
	}
	if time.Since(st.first) > authFailWindow {
		delete(b.authFail, ip)
		return false
	}
	return st.count >= maxAuthFailures
}

// recordAuthFailure increments the failed-AUTH counter for the given source IP,
// starting a fresh window when the previous one has elapsed.
func (b *Backend) recordAuthFailure(ip string) {
	if ip == "" || b.authFail == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	b.authFailSweep++
	if b.authFailSweep >= authFailSweepEvery {
		b.authFailSweep = 0
		for k, st := range b.authFail {
			if time.Since(st.first) > authFailWindow {
				delete(b.authFail, k)
			}
		}
	}

	st := b.authFail[ip]
	if st == nil || time.Since(st.first) > authFailWindow {
		b.authFail[ip] = &authFailState{count: 1, first: time.Now()}
		return
	}
	st.count++
}

// resetAuthFailures clears the failed-AUTH counter for a source IP after a
// successful authentication.
func (b *Backend) resetAuthFailures(ip string) {
	if ip == "" || b.authFail == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.authFail, ip)
}
