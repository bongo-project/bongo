package netx

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// TrustedProxies is the operator-configured set of proxy addresses whose PROXY
// protocol headers and X-Forwarded-* headers are believed. The zero value and
// nil trust nobody: there is no implicit loopback or private-range exception,
// because any co-tenant container on a private network could otherwise forge a
// client IP and defeat lockout, rate limits and DNSBL checks.
type TrustedProxies struct {
	prefixes []netip.Prefix
}

// ParseTrustedProxies parses bare IPs (treated as /32 or /128) and CIDRs. A
// malformed entry fails with an error naming the value, so a typo fails
// startup rather than silently dropping trust for that entry. It returns nil
// when entries is empty.
func ParseTrustedProxies(entries []string) (*TrustedProxies, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		var p netip.Prefix
		if strings.Contains(entry, "/") {
			parsed, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy %q: %w", entry, err)
			}
			p = parsed
		} else {
			addr, err := netip.ParseAddr(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy %q: %w", entry, err)
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		// Normalise IPv4-mapped prefixes so they match unmapped peer addresses.
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		prefixes = append(prefixes, p.Masked())
	}
	return &TrustedProxies{prefixes: prefixes}, nil
}

// Empty reports whether no proxy is trusted.
func (t *TrustedProxies) Empty() bool {
	return t == nil || len(t.prefixes) == 0
}

// Strings returns the configured prefixes for logging.
func (t *TrustedProxies) Strings() []string {
	if t == nil {
		return nil
	}
	out := make([]string, len(t.prefixes))
	for i, p := range t.prefixes {
		out[i] = p.String()
	}
	return out
}

// Trusts reports whether addr (a peer address, with or without port) is a
// listed proxy.
func (t *TrustedProxies) Trusts(addr net.Addr) bool {
	if addr == nil {
		return false
	}
	return t.TrustsHost(addr.String())
}

// TrustsHost is Trusts for a "host" or "host:port" string.
func (t *TrustedProxies) TrustsHost(host string) bool {
	if t.Empty() {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range t.prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
