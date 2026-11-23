// Package fetch collects mail from a user's remote POP3 and IMAP accounts: the
// connection layer, the IMAP client over go-imap and the error codes. The POP3
// client is in the pop3 subpackage, the password sealing in credcrypt.
//
// Nothing here starts a fetch. The worker and the settings page that use it
// are separate; this package only knows how to talk to one server safely:
//
//   - every connection goes through a Dialer. In production that is
//     netx.MailDialer, which judges every resolved address before the connect,
//     so a typed host cannot reach the server's own network;
//   - TLS is always on, either from the first byte or by STARTTLS/STLS, and a
//     failed upgrade is an error, never a fallback. Certificates are verified
//     against the host the user typed, TLS 1.2 at least. There is no option to
//     skip verification;
//   - every connection has a read budget, an idle timeout and a hard deadline
//     from the caller's context, so a hostile server cannot hold the worker or
//     feed it without bound;
//   - no server text is kept: failures are the sentinels in fetchx, mapped to
//     codes by Code.
//
// The go-imap Options.DebugWriter is never set. It would write credentials.
package fetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/bongo-project/bongo/internal/fetch/fetchx"
	"github.com/bongo-project/bongo/internal/netx"
)

// Security modes of an account.
const (
	SecurityTLS      = "tls"
	SecurityStartTLS = "starttls"
)

// Protocols of an account.
const (
	ProtocolIMAP = "imap"
	ProtocolPOP3 = "pop3"
)

// PortFor derives the port from the protocol and security mode: 993 and 143 for
// IMAP, 995 and 110 for POP3. There is no free port, which keeps one choice out
// of the address surface. It returns 0 for an unknown combination.
func PortFor(protocol, security string) uint16 {
	switch protocol + "/" + security {
	case "imap/tls":
		return 993
	case "imap/starttls":
		return 143
	case "pop3/tls":
		return 995
	case "pop3/starttls":
		return 110
	}
	return 0
}

// Dialer opens connections to host:port. netx.MailDialer is the production
// implementation; tests inject a loopback dialer.
type Dialer interface {
	DialContext(ctx context.Context, host string, port uint16) (net.Conn, error)
}

var _ Dialer = (*netx.MailDialer)(nil)

// Limits bound one session.
type Limits struct {
	// Idle is how long the server may stay silent while a command is waiting
	// for its answer. It does not apply while the client is busy with its own
	// work between commands.
	Idle time.Duration
	// Budget is the most bytes a session may read from the server.
	Budget int64
	// HandshakeTimeout bounds each TLS handshake.
	HandshakeTimeout time.Duration
	// Control bounds the bytes one command other than a message download may
	// read; zero means DefaultControlBudget. It stops a server from answering
	// a SIZE or UID listing with a gigabyte.
	Control int64
}

// DefaultControlBudget is the per-command read budget for everything except a
// message body.
const DefaultControlBudget = 16 << 20

// MaxRemoteMessages is the most messages one remote folder may list. A longer
// listing is a protocol error: the run would never finish it anyway.
const MaxRemoteMessages = 200000

// DefaultLimits are the production limits for a run that may fetch up to
// runBytes of mail: 60 s idle, the run's bytes plus 8 MiB of protocol
// overhead, a 15 s handshake.
func DefaultLimits(runBytes int64) Limits {
	return Limits{Idle: 60 * time.Second, Budget: runBytes + 8<<20, HandshakeTimeout: 15 * time.Second}
}

// Client opens sessions. The zero Limits are replaced by DefaultLimits(256 MiB).
type Client struct {
	Dialer Dialer
	Limits Limits
	// RootCAs, when set, replaces the system roots as the trust anchors. It
	// exists for tests with a private CA. It cannot disable verification, and
	// nothing sets it from configuration.
	RootCAs *x509.CertPool
}

// NewClient returns a Client dialling through d.
func NewClient(d Dialer) *Client {
	return &Client{Dialer: d, Limits: DefaultLimits(256 << 20)}
}

// Params identify the server and the account.
type Params struct {
	Host     string
	Port     uint16
	Security string // SecurityTLS or SecurityStartTLS
	Username string
	Password string
}

// String redacts the password, so a Params that reaches a log line or an error
// with %v or %+v does not carry it. GoString and LogValue do the same for %#v
// and slog.
func (p Params) String() string {
	return fmt.Sprintf("{Host:%s Port:%d Security:%s Username:%s Password:[redacted]}", p.Host, p.Port, p.Security, p.Username)
}

// GoString implements fmt.GoStringer.
func (p Params) GoString() string { return p.String() }

// LogValue implements slog.LogValuer. The user name is omitted too: the logging
// rules keep remote user names out of logs.
func (p Params) LogValue() slog.Value {
	return slog.GroupValue(slog.String("host", p.Host), slog.Int("port", int(p.Port)), slog.String("security", p.Security))
}

func (c *Client) tlsConfig(host string, protocol string) *tls.Config {
	return &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		RootCAs:    c.RootCAs,
		NextProtos: []string{protocol},
	}
}

// budgetConn bounds a connection: it fails reads past the byte budget, gives
// reads an idle deadline while a command is outstanding and ignores deadlines
// set by the protocol library, which would override the idle rule (go-imap
// clears its deadline while idle, which is right for IDLE and wrong here).
type budgetConn struct {
	net.Conn
	idle    time.Duration
	control int64
	left    atomic.Int64
	cmd     atomic.Int64 // bytes the current command may still read
	busy    atomic.Bool
	closed  atomic.Bool
}

func newBudgetConn(conn net.Conn, l Limits) *budgetConn {
	b := &budgetConn{Conn: conn, idle: l.Idle, control: l.Control}
	if b.control <= 0 {
		b.control = DefaultControlBudget
	}
	b.left.Store(l.Budget)
	b.cmd.Store(b.control)
	b.busy.Store(true)
	return b
}

func (b *budgetConn) Read(p []byte) (int, error) {
	left := b.left.Load()
	if cmd := b.cmd.Load(); cmd < left {
		left = cmd
	}
	if left <= 0 {
		return 0, fetchx.ErrBudget
	}
	if int64(len(p)) > left {
		p = p[:left]
	}
	if b.busy.Load() && b.idle > 0 {
		_ = b.Conn.SetReadDeadline(time.Now().Add(b.idle))
	}
	n, err := b.Conn.Read(p)
	b.left.Add(-int64(n))
	b.cmd.Add(-int64(n))
	return n, err
}

func (b *budgetConn) Write(p []byte) (int, error) {
	if b.idle > 0 {
		_ = b.Conn.SetWriteDeadline(time.Now().Add(b.idle))
	}
	return b.Conn.Write(p)
}

// SetDeadline, SetReadDeadline and SetWriteDeadline are ignored: the budget
// conn owns the deadlines.
func (b *budgetConn) SetDeadline(time.Time) error      { return nil }
func (b *budgetConn) SetReadDeadline(time.Time) error  { return nil }
func (b *budgetConn) SetWriteDeadline(time.Time) error { return nil }

// beginControl starts a command whose answer is bounded by the control budget.
func (b *budgetConn) beginControl() {
	b.cmd.Store(b.control)
	b.setBusy(true)
}

// beginUnbounded starts a message download, bounded only by the session budget
// and the caller's own limit.
func (b *budgetConn) beginUnbounded() {
	b.cmd.Store(1 << 62)
	b.setBusy(true)
}

// spent reports whether either budget ran out.
func (b *budgetConn) spent() bool { return b.left.Load() <= 0 || b.cmd.Load() <= 0 }

// setBusy says whether a command is outstanding. Entering busy arms the idle
// deadline on a read that is already blocked; leaving it removes the deadline,
// so a session may wait for the client's own work as long as the run's context
// allows.
func (b *budgetConn) setBusy(busy bool) {
	b.busy.Store(busy)
	if !busy {
		// Unsolicited data between commands is under the control budget, even
		// after a message download lifted it.
		b.cmd.Store(b.control)
	}
	if busy && b.idle > 0 {
		_ = b.Conn.SetReadDeadline(time.Now().Add(b.idle))
	} else {
		_ = b.Conn.SetReadDeadline(time.Time{})
	}
}

func (b *budgetConn) Close() error {
	b.closed.Store(true)
	return b.Conn.Close()
}

// dial opens the raw connection through the Dialer and wraps it.
func (c *Client) dial(ctx context.Context, p Params) (*budgetConn, error) {
	if c.Dialer == nil {
		return nil, errors.New("fetch: no dialer")
	}
	limits := c.Limits
	if limits.Budget == 0 && limits.Idle == 0 {
		limits = DefaultLimits(256 << 20)
	}
	raw, err := c.Dialer.DialContext(ctx, p.Host, p.Port)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w", fetchx.ErrTimeout)
		}
		// The dialer's error may name an address the server resolved; keep the
		// class (Code reads it) and drop the text.
		return nil, wrapConnect(err)
	}
	return newBudgetConn(raw, limits), nil
}

// wrapConnect keeps the sentinels of the netx dialer reachable through
// errors.Is and replaces everything else with ErrConnect.
func wrapConnect(err error) error {
	for _, s := range []error{netx.ErrForbiddenAddress, netx.ErrForbiddenPort, netx.ErrInvalidMailHost} {
		if errors.Is(err, s) {
			return fmt.Errorf("%w", s)
		}
	}
	return fmt.Errorf("%w", fetchx.ErrConnect)
}

// handshake runs the client side of TLS over conn.
func (c *Client) handshake(ctx context.Context, conn net.Conn, p Params, protocol string) (*tls.Conn, error) {
	l := c.Limits
	if l.HandshakeTimeout == 0 {
		l.HandshakeTimeout = 15 * time.Second
	}
	hctx, cancel := context.WithTimeout(ctx, l.HandshakeTimeout)
	defer cancel()
	tc := tls.Client(conn, c.tlsConfig(p.Host, protocol))
	if err := tc.HandshakeContext(hctx); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w", fetchx.ErrTimeout)
		}
		return nil, fmt.Errorf("%w", fetchx.ErrTLS)
	}
	return tc, nil
}

// watch closes conn when ctx ends, which is how the per-account deadline and
// shutdown reach a blocked read. The returned stop must be called when the
// session ends.
func watch(ctx context.Context, conn net.Conn) (stop func() bool) {
	return context.AfterFunc(ctx, func() { _ = conn.Close() })
}
