package smtp

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"time"
)

// relayCommandTimeout bounds each pre-client command, matching the 5 minutes
// go-smtp applies to its own commands (RFC 5321 section 4.5.3.2).
const relayCommandTimeout = 5 * time.Minute

// errSTARTTLSRefused marks a STARTTLS command the remote answered with an
// error reply. The connection is still plaintext and usable; any other
// STARTTLS error (a failed handshake) leaves it unusable.
var errSTARTTLSRefused = errors.New("STARTTLS refused by remote")

// probeSTARTTLS reads the greeting and sends the first EHLO, reporting whether
// the remote offers STARTTLS. A remote that does not know EHLO is greeted with
// HELO instead and offers nothing. It leaves conn at a command boundary.
func probeSTARTTLS(conn net.Conn, helo string) (bool, error) {
	if err := conn.SetDeadline(time.Now().Add(relayCommandTimeout)); err != nil {
		return false, err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	tp := textproto.NewConn(conn)
	if _, _, err := tp.ReadResponse(220); err != nil {
		return false, err
	}
	if err := tp.PrintfLine("EHLO %s", helo); err != nil {
		return false, err
	}
	_, msg, err := tp.ReadResponse(250)
	if err != nil {
		var pe *textproto.Error
		if errors.As(err, &pe) && (pe.Code == 500 || pe.Code == 502) {
			if err := tp.PrintfLine("HELO %s", helo); err != nil {
				return false, err
			}
			if _, _, err := tp.ReadResponse(250); err != nil {
				return false, err
			}
			return false, nil
		}
		return false, err
	}
	lines := strings.Split(msg, "\n")
	for _, l := range lines[min(1, len(lines)):] {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(l, " ", 2)[0]), "STARTTLS") {
			return true, nil
		}
	}
	return false, nil
}

// startTLSOn sends STARTTLS and completes the handshake. A refusal by the
// remote returns an error wrapping errSTARTTLSRefused with conn still usable;
// a handshake failure returns the handshake error.
func startTLSOn(conn net.Conn, cfg *tls.Config) (*tls.Conn, error) {
	if err := conn.SetDeadline(time.Now().Add(relayCommandTimeout)); err != nil {
		return nil, err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	tp := textproto.NewConn(conn)
	if err := tp.PrintfLine("STARTTLS"); err != nil {
		return nil, err
	}
	if _, _, err := tp.ReadResponse(220); err != nil {
		var pe *textproto.Error
		if errors.As(err, &pe) {
			return nil, fmt.Errorf("%w: %w", errSTARTTLSRefused, err)
		}
		return nil, err
	}
	tconn := tls.Client(conn, cfg)
	if err := tconn.Handshake(); err != nil {
		return nil, err
	}
	return tconn, nil
}

// greetedConn wraps the connection, so the go-smtp client's TLSConnectionState()
// reports no TLS even after STARTTLS. Do not use it for a REQUIRETLS or other
// TLS-state check; sendToHostOpts tracks that itself (tlsCompleted).
//
// greetedConn presents the 220 greeting go-smtp's client expects before its
// first command. The real greeting was consumed by probeSTARTTLS, and after
// STARTTLS the remote sends none.
type greetedConn struct {
	net.Conn
	pre []byte
}

func (c *greetedConn) Read(p []byte) (int, error) {
	if c.pre == nil {
		c.pre = []byte(syntheticGreeting)
	}
	if len(c.pre) > 0 {
		n := copy(p, c.pre)
		c.pre = c.pre[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

const syntheticGreeting = "220 ready\r\n"
