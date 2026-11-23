package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/idna"
)

// This file is the raw-TCP dialer for the remote-account fetcher (POP3 and
// IMAP). The host is typed by a user and the server is internet-facing, so the
// same rule as the HTTP client applies: the address check sits in the dialer's
// Control hook, which runs after DNS resolution and before every connect
// attempt (including each Happy Eyeballs fallback), so a name that resolves to
// 127.0.0.1 or a rebinding answer is refused by the same code. The hook is
// shared with the HTTP client through controlFor; the two cannot drift.

// ErrInvalidMailHost is returned for a host the dialer will not resolve. The
// text never contains the host.
var ErrInvalidMailHost = errors.New("invalid mail host")

const (
	mailDialTimeout = 10 * time.Second
	maxMailHostLen  = 253
)

// controlFor returns the dialer hook that judges every connection attempt: it
// parses the post-DNS ip:port, refuses a port portOK rejects and, unless
// allowPrivate, refuses a non-public address. The HTTP client and the mail
// dialer both build their hook here.
func controlFor(portOK func(uint16) bool, allowPrivate bool) func(ctx context.Context, network, address string, rc syscall.RawConn) error {
	return func(_ context.Context, _, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("%w: unparsable dial address", ErrForbiddenAddress)
		}
		if !portOK(ap.Port()) {
			return ErrForbiddenPort
		}
		if !allowPrivate && IsForbiddenAddr(ap.Addr()) {
			return ErrForbiddenAddress
		}
		return nil
	}
}

// MailDialerOptions configures a MailDialer.
type MailDialerOptions struct {
	// AllowPrivateNetworks lifts the address rule for operators who fetch from
	// a server on their own network (fetch.allow_private_networks). It does
	// not lift the port rule.
	AllowPrivateNetworks bool
}

// MailDialer dials the four mail ports (993, 995, 143, 110) on public
// addresses only. It is safe for concurrent use.
type MailDialer struct {
	opts MailDialerOptions
	// extraPorts admits further ports. Tests set it, because a fake server
	// listens on a random port; nothing outside this package can.
	extraPorts map[uint16]bool
}

// NewMailDialer returns a MailDialer enforcing the address and port rules.
func NewMailDialer(opts MailDialerOptions) *MailDialer {
	return &MailDialer{opts: opts}
}

// AllowsPrivateNetworks reports whether the address rule is lifted.
func (d *MailDialer) AllowsPrivateNetworks() bool { return d.opts.AllowPrivateNetworks }

func (d *MailDialer) portAllowed(port uint16) bool {
	switch port {
	case 993, 995, 143, 110:
		return true
	}
	return d.extraPorts[port]
}

func (d *MailDialer) dialer() *net.Dialer {
	return &net.Dialer{
		Timeout:        mailDialTimeout,
		ControlContext: controlFor(d.portAllowed, d.opts.AllowPrivateNetworks),
	}
}

// DialContext connects to host:port. The host is validated, resolved by the
// dialer and every resolved address is judged before its connect. Errors carry
// the host at most, never the resolved address.
func (d *MailDialer) DialContext(ctx context.Context, host string, port uint16) (net.Conn, error) {
	host, err := NormalizeMailHost(host)
	if err != nil {
		return nil, err
	}
	if !d.portAllowed(port) {
		return nil, ErrForbiddenPort
	}
	conn, err := d.dialer().DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		var oe *net.OpError
		if errors.As(err, &oe) && oe.Err != nil {
			err = oe.Err
		}
		return nil, fmt.Errorf("dialing %s: %w", host, err)
	}
	return conn, nil
}

// MailHostOK reports whether host is acceptable as a remote mail host.
func MailHostOK(host string) error {
	_, err := NormalizeMailHost(host)
	return err
}

// NormalizeMailHost validates host and returns its canonical form: lower-case
// ASCII (IDNA ToASCII for an internationalised name) letters, digits and
// hyphens in labels, or an IP literal without brackets or zone. It refuses an
// empty or over-long name, control characters, a zone, brackets, a port, a
// trailing dot and an all-numeric last label that is not an IP address (the C
// resolver reads "2130706433" as 127.0.0.1). The error never contains host.
func NormalizeMailHost(host string) (string, error) {
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("%w: %s", ErrInvalidMailHost, why)
	}
	if host == "" {
		return bad("empty")
	}
	if len(host) > 4*maxMailHostLen {
		return bad("too long")
	}
	for _, r := range host {
		if r <= ' ' || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return bad("control or space character")
		}
	}
	if strings.ContainsAny(host, "%[]/@\\") {
		return bad("forbidden character")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return bad("zone")
		}
		return addr.String(), nil
	}
	if strings.Contains(host, ":") {
		return bad("not a bare host name")
	}
	ascii, err := idna.Lookup.ToASCII(strings.ToLower(host))
	if err != nil {
		return bad("not a valid name")
	}
	ascii = strings.ToLower(ascii)
	if len(ascii) > maxMailHostLen {
		return bad("too long")
	}
	labels := strings.Split(ascii, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return bad("bad label")
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return bad("bad label character")
			}
		}
	}
	last := labels[len(labels)-1]
	if strings.Trim(last, "0123456789") == "" {
		return bad("numeric host that is not an address")
	}
	return ascii, nil
}
