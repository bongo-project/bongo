package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/bongo-project/bongo/internal/netx"
)

// trustedProxies is the operator-configured set of proxies whose forwarded
// headers are believed (server.trusted_proxies). Unset trusts nobody.
var trustedProxies atomic.Pointer[netx.TrustedProxies]

// SetTrustedProxies installs the proxy set ClientIP and IsHTTPS consult. The
// agent calls it once before the router starts; nil trusts nobody.
func SetTrustedProxies(t *netx.TrustedProxies) {
	trustedProxies.Store(t)
}

// ClientIP returns the client IP for the request. Forwarded headers are only
// trusted when the direct peer is listed in server.trusted_proxies; a private
// or loopback address alone is not enough.
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
	if !trustedProxies.Load().TrustsHost(host) {
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
	if !trustedProxies.Load().TrustsHost(host) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

// PrivateOnly restricts a handler to requests whose real client IP (see
// ClientIP, which only trusts forwarded headers from trusted proxies) is
// loopback, private, or link-local. It keeps control-plane endpoints such as
// /metrics off the public internet at the code layer instead of relying solely
// on the reverse proxy's IP allow-list.
//
// behindTLSProxy is web.behind_tls_proxy. In that deployment every request
// reaches us from the proxy's private address, so with no trusted proxy
// configured ClientIP can only ever see that address and the private-range
// check would pass public clients. The guard then fails closed with 403.
func PrivateOnly(behindTLSProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if behindTLSProxy && trustedProxies.Load().Empty() {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			ip := net.ParseIP(ClientIP(r))
			if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast()) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
