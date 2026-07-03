package middleware

import (
	"net"
	"net/http"
)

// ClientIP returns the client IP for the request. Forwarded headers are only
// trusted when the direct peer is on a trusted local network.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !trustForwardedFor(host) {
		return host
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				xff = xff[:i]
				break
			}
		}
		for len(xff) > 0 && xff[0] == ' ' {
			xff = xff[1:]
		}
		for len(xff) > 0 && xff[len(xff)-1] == ' ' {
			xff = xff[:len(xff)-1]
		}
		if xff != "" {
			return xff
		}
	}

	return host
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
