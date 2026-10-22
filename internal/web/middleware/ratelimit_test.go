package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginRateLimiter_RecordAndIsLimited(t *testing.T) {
	rl := NewLoginRateLimiter(3, time.Minute)
	defer rl.Stop()

	const ip = "1.2.3.4"

	if rl.IsLimited(ip) {
		t.Fatal("expected not limited before any failures")
	}

	rl.RecordFailure(ip, "u")
	rl.RecordFailure(ip, "u")
	if rl.IsLimited(ip) {
		t.Fatal("expected not limited after 2 of 3 allowed failures")
	}

	rl.RecordFailure(ip, "u")
	if !rl.IsLimited(ip) {
		t.Fatal("expected limited after reaching max attempts")
	}
}

func TestLoginRateLimiter_SucceedForgivesOnlyOwnAccount(t *testing.T) {
	const ip = "10.0.0.1"

	rl := NewLoginRateLimiter(3, time.Minute)
	defer rl.Stop()
	rl.RecordFailure(ip, "victim@example.com")
	rl.RecordFailure(ip, "victim@example.com")
	rl.Succeed(ip, "attacker@example.com")
	rl.RecordFailure(ip, "victim@example.com")
	if !rl.IsLimited(ip) {
		t.Fatal("another account's success must not reset the IP counter")
	}

	// Positive control: a user's own success forgives their own failures.
	rl2 := NewLoginRateLimiter(3, time.Minute)
	defer rl2.Stop()
	rl2.RecordFailure(ip, "Alice@example.com")
	rl2.RecordFailure(ip, "alice@example.com")
	rl2.Succeed(ip, "alice@example.com")
	rl2.RecordFailure(ip, "alice@example.com")
	rl2.RecordFailure(ip, "alice@example.com")
	if rl2.IsLimited(ip) {
		t.Fatal("own success should forgive own earlier failures")
	}
}

func TestLoginRateLimiter_WindowExpiry(t *testing.T) {
	shortWindow := 50 * time.Millisecond
	rl := NewLoginRateLimiter(2, shortWindow)
	defer rl.Stop()

	const ip = "192.168.1.1"

	rl.RecordFailure(ip, "u")
	rl.RecordFailure(ip, "u")
	if !rl.IsLimited(ip) {
		t.Fatal("expected limited immediately")
	}

	// Wait for the window to expire.
	time.Sleep(shortWindow + 10*time.Millisecond)

	if rl.IsLimited(ip) {
		t.Fatal("expected not limited after window expiry")
	}

	// A new failure after expiry should start a fresh bucket.
	rl.RecordFailure(ip, "u")
	if rl.IsLimited(ip) {
		t.Fatal("expected not limited after single failure in new window")
	}
}

func TestLoginRateLimiter_DifferentIPs(t *testing.T) {
	rl := NewLoginRateLimiter(2, time.Minute)
	defer rl.Stop()

	rl.RecordFailure("1.1.1.1", "u")
	rl.RecordFailure("1.1.1.1", "u")

	if !rl.IsLimited("1.1.1.1") {
		t.Fatal("expected 1.1.1.1 to be limited")
	}
	if rl.IsLimited("2.2.2.2") {
		t.Fatal("expected 2.2.2.2 to be unlimited")
	}
}

func TestLoginRateLimit_Middleware(t *testing.T) {
	rl := NewLoginRateLimiter(2, time.Minute)
	defer rl.Stop()

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.LoginRateLimit()(next)

	makeRequest := func(remoteAddr string) int {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = remoteAddr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr.Code
	}

	// First two requests should pass through.
	if code := makeRequest("5.5.5.5:1234"); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if code := makeRequest("5.5.5.5:1234"); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	// Trigger the limit externally.
	rl.RecordFailure("5.5.5.5", "u")
	rl.RecordFailure("5.5.5.5", "u")

	// Third request should be blocked (2 middleware passes + 2 external = 4, max 2 → already limited).
	if code := makeRequest("5.5.5.5:1234"); code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", code)
	}
	if called != 2 {
		t.Fatalf("expected next handler to be called 2 times, got %d", called)
	}
}

func TestLoginRateLimit_RetryAfterHeader(t *testing.T) {
	window := 15 * time.Minute
	rl := NewLoginRateLimiter(1, window)
	defer rl.Stop()

	rl.RecordFailure("9.9.9.9", "u")

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	rr := httptest.NewRecorder()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rl.LoginRateLimit()(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
	retryAfter := rr.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	withTrustedProxies(t, "10.0.0.1/32")
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{
			name:       "plain RemoteAddr",
			remoteAddr: "1.2.3.4:5678",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For single",
			remoteAddr: "10.0.0.1:9999",
			xff:        "203.0.113.1",
			want:       "203.0.113.1",
		},
		{
			// Rightmost entry is the one the trusted proxy appended for the
			// real client; earlier entries are client-supplied and untrusted.
			name:       "X-Forwarded-For multiple uses rightmost",
			remoteAddr: "10.0.0.1:9999",
			xff:        "10.0.0.2, 203.0.113.1",
			want:       "203.0.113.1",
		},
		{
			// An attacker prepending a spoofed IP cannot change the result.
			name:       "X-Forwarded-For spoof resistant",
			remoteAddr: "10.0.0.1:9999",
			xff:        "6.6.6.6, 203.0.113.1",
			want:       "203.0.113.1",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "1.2.3.4",
			want:       "1.2.3.4",
		},
		{
			name:       "public peer does not trust XFF",
			remoteAddr: "203.0.113.5:1234",
			xff:        "198.51.100.9",
			want:       "203.0.113.5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			got := ClientIP(req)
			if got != tc.want {
				t.Errorf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}
