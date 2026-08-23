package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPrivateOnly covers the middleware that keeps /metrics off the public
// internet at the code layer. Nothing referenced PrivateOnly before this file
// existed, so the whole guard could be inverted or deleted silently, publishing
// queue depths, per-user counters and cluster topology to any scraper.
func TestPrivateOnly(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		wantServed bool
	}{
		{"loopback v4", "127.0.0.1:5555", true},
		{"loopback v6", "[::1]:5555", true},
		{"private 10/8", "10.1.2.3:5555", true},
		{"private 192.168/16", "192.168.1.10:5555", true},
		{"private 172.16/12", "172.16.5.5:5555", true},
		{"link-local", "169.254.1.1:5555", true},
		{"public v4", "8.8.8.8:5555", false},
		{"public v6", "[2001:4860:4860::8888]:5555", false},
		{"unparseable address", "not-an-ip", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			served := false
			handler := PrivateOnly()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				served = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/metrics", nil)
			req.RemoteAddr = tt.remoteAddr
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if served != tt.wantServed {
				t.Errorf("handler served = %v, want %v (RemoteAddr=%q)", served, tt.wantServed, tt.remoteAddr)
			}
			if !tt.wantServed && rr.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rr.Code)
			}
		})
	}
}

// TestPrivateOnlyIgnoresForwardedHeaderFromPublicPeer is the case that makes
// the guard worth having. A public client that sets X-Forwarded-For to a
// private address must still be refused: ClientIP only honours forwarded
// headers when the direct TCP peer is itself trusted, so the spoofed value
// must never reach the IsPrivate check.
//
// Without this, PrivateOnly would be trivially bypassable by anyone who can
// set a header, and the /metrics protection would be decorative.
func TestPrivateOnlyIgnoresForwardedHeaderFromPublicPeer(t *testing.T) {
	served := false
	handler := PrivateOnly()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
	}))

	req := httptest.NewRequest("GET", "/metrics", nil)
	req.RemoteAddr = "8.8.8.8:5555"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if served {
		t.Error("a public peer spoofed X-Forwarded-For: 127.0.0.1 and was served /metrics")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

// TestPrivateOnlyTrustsForwardedHeaderFromTrustedProxy is the other half: when
// the direct peer IS a trusted proxy, the forwarded value is authoritative. A
// public client behind Traefik must be refused even though the peer is local.
func TestPrivateOnlyTrustsForwardedHeaderFromTrustedProxy(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		wantServed bool
	}{
		{"proxy reports public client", "8.8.8.8", false},
		{"proxy reports private client", "10.0.0.5", true},
		{"proxy chain, rightmost wins", "8.8.8.8, 10.0.0.5", true},
		{"proxy chain, rightmost is public", "10.0.0.5, 8.8.8.8", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			served := false
			handler := PrivateOnly()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				served = true
			}))

			req := httptest.NewRequest("GET", "/metrics", nil)
			req.RemoteAddr = "127.0.0.1:5555"
			req.Header.Set("X-Forwarded-For", tt.xff)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if served != tt.wantServed {
				t.Errorf("handler served = %v, want %v (XFF=%q)", served, tt.wantServed, tt.xff)
			}
		})
	}
}
