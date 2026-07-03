// Package netx provides small networking helpers shared across protocol
// listeners.
package netx

import (
	"net"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
)

// MaybeProxyProtocol wraps a listener so it reads the PROXY protocol header
// (as sent by a TCP-terminating reverse proxy such as Traefik) and reports the
// real client address via Conn.RemoteAddr(). This is needed so inbound SPF,
// per-IP limits and logging see the true sender rather than the proxy's IP.
//
// When enabled is false the listener is returned unchanged.
//
// The PROXY header is only trusted when the immediate peer (the proxy) is on a
// loopback or private network; headers from any other source are ignored so an
// attacker connecting directly cannot spoof the client IP.
func MaybeProxyProtocol(l net.Listener, enabled bool) net.Listener {
	if !enabled || l == nil {
		return l
	}
	return &proxyproto.Listener{
		Listener:          l,
		ReadHeaderTimeout: 10 * time.Second,
		ConnPolicy: func(opts proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
			if isTrustedProxy(opts.Upstream) {
				return proxyproto.USE, nil
			}
			// Accept the connection but ignore any PROXY header it sends.
			return proxyproto.IGNORE, nil
		},
	}
}

func isTrustedProxy(addr net.Addr) bool {
	if addr == nil {
		return false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
