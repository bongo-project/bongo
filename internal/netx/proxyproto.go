// Package netx provides small networking helpers shared across protocol
// listeners.
package netx

import (
	"bufio"
	"bytes"
	"log/slog"
	"net"
	"sync"
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
// The PROXY header is only trusted when the immediate peer is listed in trust
// (server.trusted_proxies). Headers from any other peer, private ranges
// included, are read and discarded so a co-tenant cannot spoof the client IP.
// A nil or empty trust set honours no header at all.
//
// With WithRejectLogger, a PROXY header sent by a peer outside trust is logged
// at WARN (peer IP only, at most once a minute), so a trusted_proxies value
// that does not name the real proxy shows up within minutes of a deploy.
func MaybeProxyProtocol(l net.Listener, enabled bool, trust *TrustedProxies, opts ...ProxyOption) net.Listener {
	if !enabled || l == nil {
		return l
	}
	var o proxyOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.logger != nil {
		l = &rejectWarnListener{Listener: l, trust: trust, logger: o.logger}
	}
	return &proxyproto.Listener{
		Listener:          l,
		ReadHeaderTimeout: 10 * time.Second,
		ConnPolicy: func(opts proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
			if trust.Trusts(opts.Upstream) {
				return proxyproto.USE, nil
			}
			// Accept the connection but ignore any PROXY header it sends.
			return proxyproto.IGNORE, nil
		},
	}
}

type proxyOptions struct {
	logger *slog.Logger
}

// ProxyOption configures MaybeProxyProtocol.
type ProxyOption func(*proxyOptions)

// WithRejectLogger makes MaybeProxyProtocol warn when an untrusted peer sends
// a PROXY header.
func WithRejectLogger(l *slog.Logger) ProxyOption {
	return func(o *proxyOptions) { o.logger = l }
}

// rejectWarnInterval bounds the WARN rate per listener.
const rejectWarnInterval = time.Minute

// rejectWarnListener sits beneath the PROXY listener and peeks at the first
// bytes of connections from untrusted peers, without consuming them.
type rejectWarnListener struct {
	net.Listener
	trust  *TrustedProxies
	logger *slog.Logger

	mu       sync.Mutex
	last     time.Time
	suppress int
}

func (l *rejectWarnListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil || l.trust.Trusts(c.RemoteAddr()) {
		return c, err
	}
	return &peekConn{Conn: c, br: bufio.NewReader(c), onHeader: l.warn}, nil
}

func (l *rejectWarnListener) warn(peer net.Addr) {
	l.mu.Lock()
	now := time.Now()
	if !l.last.IsZero() && now.Sub(l.last) < rejectWarnInterval {
		l.suppress++
		l.mu.Unlock()
		return
	}
	suppressed := l.suppress
	l.last, l.suppress = now, 0
	l.mu.Unlock()

	host := peer.String()
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	l.logger.Warn("proxy protocol header from a peer not in server.trusted_proxies was ignored; client addresses will be the peer's, so set server.trusted_proxies to the proxy's address",
		"peer", host, "suppressed", suppressed)
}

var (
	proxyV1Prefix = []byte("PROXY")
	proxyV2Prefix = []byte("\r\n\r\n\x00")
)

// peekConn inspects the first bytes once, then passes everything through.
type peekConn struct {
	net.Conn
	br       *bufio.Reader
	once     sync.Once
	onHeader func(net.Addr)
}

func (c *peekConn) Read(p []byte) (int, error) {
	c.once.Do(func() {
		// Same 5-byte peek the PROXY parser itself makes; an error or short
		// read just means no header was recognised.
		b, _ := c.br.Peek(len(proxyV1Prefix))
		if bytes.Equal(b, proxyV1Prefix) || bytes.Equal(b, proxyV2Prefix) {
			c.onHeader(c.Conn.RemoteAddr())
		}
	})
	return c.br.Read(p)
}
