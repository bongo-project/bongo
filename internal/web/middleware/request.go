package middleware

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the client IP for the request. Forwarded headers are only
// trusted when the direct peer is on a trusted local network.
//
// When trusted, it takes the RIGHTMOST X-Forwarded-For entry — the value the
// immediate trusted proxy (e.g. Traefik) appended for the real client. Taking
// the leftmost value would trust a client-supplied header and let an attacker
// spoof the client IP to bypass rate limiting.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !trustForwardedFor(host) {
		return host
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Rightmost entry = appended by the immediate trusted proxy.
		if i := strings.LastIndexByte(xff, ','); i >= 0 {
			xff = xff[i+1:]
		}
		xff = strings.TrimSpace(xff)
		if xff != "" {
			return xff
		}
	}

	return host
}

// IsHTTPS reports whether the request reached the user over HTTPS, either
// directly (r.TLS) or via a trusted TLS-terminating proxy that set
// X-Forwarded-Proto: https.
func IsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !trustForwardedFor(host) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

// PrivateOnly restricts a handler to requests whose real client IP (see
// ClientIP, which only trusts forwarded headers from trusted proxies) is
// loopback, private, or link-local. It keeps control-plane endpoints such as
// /metrics off the public internet at the code layer instead of relying solely
// on the reverse proxy's IP allow-list.
func PrivateOnly() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := net.ParseIP(ClientIP(r))
			if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast()) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func trustForwardedFor(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return true
	}
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
