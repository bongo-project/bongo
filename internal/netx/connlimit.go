package netx

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
)

// DefaultMaxConnections and DefaultMaxConnectionsPerIP are the connection
// caps applied to the mail protocol listeners when none is configured.
const (
	DefaultMaxConnections      = 1024
	DefaultMaxConnectionsPerIP = 50
)

// PerIPLimiter caps concurrent connections per source IP. The IP must be the
// real client address (after PROXY-protocol parsing), otherwise behind a TCP
// proxy every client shares one key. A nil limiter allows everything.
type PerIPLimiter struct {
	mu  sync.Mutex
	max int
	n   map[string]int
}

// NewPerIPLimiter returns a limiter allowing max concurrent connections per
// IP. max <= 0 disables the limit.
func NewPerIPLimiter(max int) *PerIPLimiter {
	return &PerIPLimiter{max: max, n: make(map[string]int)}
}

// SetMax changes the cap. Existing counts are kept.
func (l *PerIPLimiter) SetMax(max int) {
	l.mu.Lock()
	l.max = max
	l.mu.Unlock()
}

// Acquire reserves a slot for ip and reports whether it was granted. Every
// granted Acquire must be paired with exactly one Release. It always grants
// when the limit is disabled or ip is nil.
func (l *PerIPLimiter) Acquire(ip net.IP) bool {
	if l == nil || ip == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.max <= 0 {
		return true
	}
	k := ip.String()
	if l.n[k] >= l.max {
		return false
	}
	l.n[k]++
	return true
}

// Release returns a slot taken by Acquire. The map entry is removed at zero.
func (l *PerIPLimiter) Release(ip net.IP) {
	if l == nil || ip == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	k := ip.String()
	if l.n[k] > 0 {
		l.n[k]--
		if l.n[k] == 0 {
			delete(l.n, k)
		}
	}
}

// Len returns the number of IPs currently holding at least one slot.
func (l *PerIPLimiter) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.n)
}

// Max returns the configured per-IP cap (<= 0 means disabled).
func (l *PerIPLimiter) Max() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.max
}

// LimitPerIP wraps ln so each accepted connection takes a slot in l for its
// source IP. The check runs on the connection's first Read or Write rather
// than in Accept, because the source IP of a PROXY-protocol connection is only
// known once its header has been read and that must not stall the accept
// loop. Place it between the PROXY-protocol listener and tls.NewListener: a
// refused connection is then closed before any TLS handshake, so a silent
// client over its cap cannot hold a global slot waiting for a ClientHello.
// The slot is released when the connection is closed.
func LimitPerIP(ln net.Listener, l *PerIPLimiter) net.Listener {
	return &limitListener{Listener: ln, l: l}
}

type limitListener struct {
	net.Listener
	l *PerIPLimiter
}

func (ll *limitListener) Accept() (net.Conn, error) {
	c, err := ll.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &limitConn{Conn: c, l: ll.l}, nil
}

// ErrTooManyConnections is returned by I/O on a connection refused by the
// per-IP cap. The connection has already been closed.
var ErrTooManyConnections = errors.New("too many connections from source address")

type limitConn struct {
	net.Conn
	l *PerIPLimiter

	once sync.Once
	ip   net.IP
	held bool

	released atomic.Bool
}

// admit takes the per-IP slot on first use and closes the connection if the
// cap is reached.
func (c *limitConn) admit() error {
	c.once.Do(func() {
		var ip net.IP
		if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
			ip = a.IP
		}
		if c.l.Acquire(ip) {
			c.ip, c.held = ip, true
		}
	})
	if !c.held {
		_ = c.Conn.Close()
		return ErrTooManyConnections
	}
	return nil
}

func (c *limitConn) Read(b []byte) (int, error) {
	if err := c.admit(); err != nil {
		return 0, err
	}
	return c.Conn.Read(b)
}

func (c *limitConn) Write(b []byte) (int, error) {
	if err := c.admit(); err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}

// Close closes the connection and frees its slot. Closing first unblocks an
// admit that is still waiting on a PROXY header, so the Do below returns.
func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {})
	if c.held && c.released.CompareAndSwap(false, true) {
		c.l.Release(c.ip)
	}
	return err
}
