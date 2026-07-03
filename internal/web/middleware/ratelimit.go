package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// DefaultMaxAttempts is the default number of failed attempts before rate limiting.
	DefaultMaxAttempts = 5
	// DefaultWindow is the default window for rate limiting.
	DefaultWindow = 15 * time.Minute
)

// bucket tracks failed attempts for a single IP address.
type bucket struct {
	count   int
	resetAt time.Time
}

// LoginRateLimiter tracks failed login attempts per IP address.
type LoginRateLimiter struct {
	mu          sync.RWMutex
	buckets     map[string]*bucket
	maxAttempts int
	window      time.Duration
	stopCh      chan struct{}
}

// NewLoginRateLimiter creates a new LoginRateLimiter with the given limits.
// A background goroutine prunes expired entries every 5 minutes; call Stop() to
// shut it down.
func NewLoginRateLimiter(maxAttempts int, window time.Duration) *LoginRateLimiter {
	rl := &LoginRateLimiter{
		buckets:     make(map[string]*bucket),
		maxAttempts: maxAttempts,
		window:      window,
		stopCh:      make(chan struct{}),
	}
	go rl.pruneLoop()
	return rl
}

// Stop terminates the background pruning goroutine.
func (rl *LoginRateLimiter) Stop() {
	close(rl.stopCh)
}

// RecordFailure increments the failure counter for the given IP.
func (rl *LoginRateLimiter) RecordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[ip]
	if !ok || time.Now().After(b.resetAt) {
		rl.buckets[ip] = &bucket{count: 1, resetAt: time.Now().Add(rl.window)}
		return
	}
	b.count++
}

// Reset clears the failure counter for the given IP (e.g. on successful login).
func (rl *LoginRateLimiter) Reset(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.buckets, ip)
}

// IsLimited reports whether the given IP has exceeded the maximum number of
// failed attempts within the configured window.
func (rl *LoginRateLimiter) IsLimited(ip string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	b, ok := rl.buckets[ip]
	if !ok {
		return false
	}
	if time.Now().After(b.resetAt) {
		return false
	}
	return b.count >= rl.maxAttempts
}

// LoginRateLimit returns chi-compatible middleware that rejects requests from
// IPs that have exceeded the configured failure threshold with HTTP 429.
func (rl *LoginRateLimiter) LoginRateLimit() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ClientIP(r)
			if rl.IsLimited(ip) {
				retryAfter := int(rl.window.Seconds())
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// pruneLoop runs in the background and removes expired buckets every 5 minutes.
func (rl *LoginRateLimiter) pruneLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.prune()
		case <-rl.stopCh:
			return
		}
	}
}

func (rl *LoginRateLimiter) prune() {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, b := range rl.buckets {
		if now.After(b.resetAt) {
			delete(rl.buckets, ip)
		}
	}
}
