package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bongo-project/bongo/internal/netx"
)

// withTrustedProxies installs a proxy set for one test and restores the
// previous value afterwards. Tests using it must not run in parallel.
func withTrustedProxies(t *testing.T, entries ...string) {
	t.Helper()
	tp, err := netx.ParseTrustedProxies(entries)
	if err != nil {
		t.Fatal(err)
	}
	prev := trustedProxies.Load()
	SetTrustedProxies(tp)
	t.Cleanup(func() { SetTrustedProxies(prev) })
}

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
			handler := PrivateOnly(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	handler := PrivateOnly(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	withTrustedProxies(t, "127.0.0.1/32")
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
			handler := PrivateOnly(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// An RFC1918 peer that is not listed must not be able to dictate the client IP,
// and a listed one must. Empty list trusts nobody, loopback included.
func TestClientIPAndIsHTTPSHonourOnlyListedProxies(t *testing.T) {
	tests := []struct {
		name       string
		trusted    []string
		remoteAddr string
		wantIP     string
		wantHTTPS  bool
	}{
		{"empty list, private peer", nil, "10.0.0.5:1", "10.0.0.5", false},
		{"empty list, loopback peer", nil, "127.0.0.1:1", "127.0.0.1", false},
		{"listed peer", []string{"10.0.0.5/32"}, "10.0.0.5:1", "1.2.3.4", true},
		{"other private peer unlisted", []string{"10.0.0.5/32"}, "172.16.0.9:1", "172.16.0.9", false},
		{"private peer in same range unlisted", []string{"10.0.0.5/32"}, "10.0.0.6:1", "10.0.0.6", false},
		{"listed by CIDR", []string{"172.16.0.0/12"}, "172.16.0.9:1", "1.2.3.4", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTrustedProxies(t, tt.trusted...)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Header.Set("X-Forwarded-For", "1.2.3.4")
			req.Header.Set("X-Forwarded-Proto", "https")
			if got := ClientIP(req); got != tt.wantIP {
				t.Errorf("ClientIP() = %q, want %q", got, tt.wantIP)
			}
			if got := IsHTTPS(req); got != tt.wantHTTPS {
				t.Errorf("IsHTTPS() = %v, want %v", got, tt.wantHTTPS)
			}
		})
	}
}

// A listed proxy forwarding a public client must be refused, and an unlisted
// private peer forging a private XFF must not be served on the strength of it.
func TestPrivateOnlyWithTrustedProxyList(t *testing.T) {
	tests := []struct {
		name       string
		trusted    []string
		remoteAddr string
		xff        string
		wantCode   int
	}{
		{"listed proxy, public client", []string{"10.0.0.5/32"}, "10.0.0.5:1", "8.8.8.8", http.StatusForbidden},
		{"listed proxy, private client", []string{"10.0.0.5/32"}, "10.0.0.5:1", "192.168.1.9", http.StatusOK},
		{"unlisted private peer, public XFF", nil, "10.0.0.5:1", "8.8.8.8", http.StatusOK}, // peer itself is private
		{"unlisted public peer, private XFF", nil, "8.8.8.8:1", "10.0.0.1", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTrustedProxies(t, tt.trusted...)
			h := PrivateOnly(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			req := httptest.NewRequest("GET", "/metrics", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Header.Set("X-Forwarded-For", tt.xff)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantCode)
			}
		})
	}
}

// TestPrivateOnlyBehindTLSProxyNeedsTrustedProxy: behind a TLS proxy with no
// trusted_proxies, ClientIP is the proxy's private address for everyone, so
// the private-range check would serve public clients. It must fail closed.
func TestPrivateOnlyBehindTLSProxyNeedsTrustedProxy(t *testing.T) {
	tests := []struct {
		name       string
		trusted    []string
		behind     bool
		remoteAddr string
		xff        string
		wantServed bool
	}{
		{"behind proxy, none trusted: proxy peer denied", nil, true, "172.18.0.2:5555", "8.8.8.8", false},
		{"behind proxy, none trusted: loopback denied", nil, true, "127.0.0.1:5555", "", false},
		{"behind proxy, proxy trusted: private client served", []string{"172.18.0.2/32"}, true, "172.18.0.2:5555", "10.0.0.7", true},
		{"behind proxy, proxy trusted: public client denied", []string{"172.18.0.2/32"}, true, "172.18.0.2:5555", "8.8.8.8", false},
		{"not behind proxy, none trusted: private peer served", nil, false, "10.1.2.3:5555", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTrustedProxies(t, tt.trusted...)
			served := false
			h := PrivateOnly(tt.behind)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served = true }))
			req := httptest.NewRequest("GET", "/metrics", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if served != tt.wantServed {
				t.Errorf("served = %v, want %v", served, tt.wantServed)
			}
			if !tt.wantServed && rr.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rr.Code)
			}
		})
	}
}
