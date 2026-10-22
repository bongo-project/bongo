package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/bongo-project/bongo/internal/authlimit"
)

const (
	// DefaultMaxAttempts is the default number of failed attempts before rate limiting.
	DefaultMaxAttempts = 5
	// DefaultWindow is the default window for rate limiting.
	DefaultWindow = 15 * time.Minute
)

// LoginRateLimiter tracks failed login attempts per IP address, backed by
// authlimit. A successful login forgives only the failures recorded under the
// same account from the same IP, so logging in to one's own account between
// guesses at someone else's does not reset the throttle.
type LoginRateLimiter struct {
	lim    *authlimit.Limiter
	window time.Duration
}

// NewLoginRateLimiter creates a new LoginRateLimiter with the given limits.
// Expired entries are swept lazily by the underlying limiter.
func NewLoginRateLimiter(maxAttempts int, window time.Duration) *LoginRateLimiter {
	return &LoginRateLimiter{lim: authlimit.New(maxAttempts, window), window: window}
}

// Stop is retained for callers that manage the limiter lifecycle; there is no
// background goroutine to terminate.
func (rl *LoginRateLimiter) Stop() {}

// RecordFailure counts a failed attempt against the IP and the (IP, account) pair.
func (rl *LoginRateLimiter) RecordFailure(ip, account string) {
	rl.lim.Fail(ip, account)
}

// Succeed forgives the failures recorded for this account from this IP. It
// never clears failures recorded under other account names.
func (rl *LoginRateLimiter) Succeed(ip, account string) {
	rl.lim.Succeed(ip, account)
}

// Window returns the failure window, which is also how long a limited client
// should wait before retrying.
func (rl *LoginRateLimiter) Window() time.Duration {
	return rl.window
}

// IsLimited reports whether the given IP has exceeded the maximum number of
// failed attempts within the configured window.
func (rl *LoginRateLimiter) IsLimited(ip string) bool {
	return rl.lim.Blocked(ip)
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
