package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"time"
)

// POP3 transport choices reuse the IMAP mode names, so one flag vocabulary
// serves both protocols.
const (
	pop3TLSAuto     = imapTLSAuto
	pop3TLSImplicit = imapTLSImplicit
	pop3TLSStartTLS = imapTLSStartTLS
	// pop3TLSPlain sends credentials in clear. It exists for a dev-role
	// server only and is never chosen by auto.
	pop3TLSPlain = imapTLSPlain
)

// errPOP3NoTLS marks a server that offers no STLS where the run needs TLS.
// It wraps errNoStartTLS so it lands in the tls class, a problem of the setup.
var errPOP3NoTLS = fmt.Errorf("the POP3 server offers no STLS: %w", errNoStartTLS)

// errPOP3Malformed is a reply that is neither +OK nor -ERR.
var errPOP3Malformed = errors.New("pop3 reply is neither +OK nor -ERR")

// errPOP3Credential rejects a user name or password that would end the line
// early and let the rest of it be read as another command.
var errPOP3Credential = errors.New("pop3 credential holds a line break")

// pop3Err is a -ERR reply. Its text is kept for classification only: the
// server chooses what to echo, so Error never repeats it.
type pop3Err struct{ text string }

func (e *pop3Err) Error() string { return "pop3 server refused the command" }

// pop3Conn is one POP3 session after the greeting, and after STLS where used.
type pop3Conn struct {
	conn net.Conn
	tp   *textproto.Conn
}

func (c *pop3Conn) Close() error { return c.tp.Close() }

// status reads one reply line and discards an +OK.
func (c *pop3Conn) status() error {
	_, err := c.statusLine()
	return err
}

// statusLine reads one reply line; -ERR becomes a *pop3Err.
func (c *pop3Conn) statusLine() (string, error) {
	line, err := c.tp.ReadLine()
	if err != nil {
		return "", err
	}
	switch {
	case line == "+OK" || strings.HasPrefix(line, "+OK "):
		return line, nil
	case strings.HasPrefix(line, "-ERR"):
		return "", &pop3Err{text: line}
	}
	return "", errPOP3Malformed
}

// cmd sends one command line and reads its status reply.
func (c *pop3Conn) cmd(format string, args ...any) (string, error) {
	if err := c.tp.PrintfLine(format, args...); err != nil {
		return "", err
	}
	return c.statusLine()
}

// skipBody reads a multi-line reply to its terminator without keeping it, so
// a large message is never held in memory.
func (c *pop3Conn) skipBody() error {
	_, err := io.Copy(io.Discard, c.tp.DotReader())
	return err
}

// pop3Dialer opens POP3 sessions. Every connection goes through dial, which
// callers build with guardedDial, so checkTarget runs before each one.
type pop3Dialer struct {
	addr     string
	mode     string
	insecure bool
	dial     dialFunc
	timeout  time.Duration

	mu       sync.Mutex
	resolved string
}

func newPOP3Dialer(addr, mode string, insecure bool, dial dialFunc, timeout time.Duration) *pop3Dialer {
	return &pop3Dialer{addr: addr, mode: mode, insecure: insecure, dial: dial, timeout: timeout}
}

func (d *pop3Dialer) tlsConfig() (*tls.Config, error) {
	host, _, err := net.SplitHostPort(d.addr)
	if err != nil {
		return nil, fmt.Errorf("pop3 address: %w", err)
	}
	return &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		// #nosec G402 -- opt-in for self-signed certificates on an allow-listed test target
		InsecureSkipVerify: d.insecure,
	}, nil
}

// transport settles on implicit TLS, STLS or plain. In auto mode the
// well-known TLS ports mean implicit TLS; any other port is probed once with
// CAPA and the answer kept. A server that offers STLS is never used in clear,
// and one that offers nothing is refused: cleartext needs the explicit mode.
func (d *pop3Dialer) transport(ctx context.Context) (string, error) {
	if d.mode != pop3TLSAuto {
		return d.mode, nil
	}
	_, port, err := net.SplitHostPort(d.addr)
	if err != nil {
		return "", fmt.Errorf("pop3 address: %w", err)
	}
	if port == "995" || port == "1995" {
		return pop3TLSImplicit, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.resolved != "" {
		return d.resolved, nil
	}
	conn, err := d.dial(ctx, "tcp", d.addr)
	if err != nil {
		return "", fmt.Errorf("probe pop3: %w", err)
	}
	c := &pop3Conn{conn: conn, tp: textproto.NewConn(conn)}
	defer c.Close()
	_ = conn.SetDeadline(time.Now().Add(d.timeout))
	if err := c.status(); err != nil {
		return "", fmt.Errorf("probe pop3 greeting: %w", err)
	}
	offered, err := c.offersSTLS()
	if err != nil {
		return "", fmt.Errorf("probe pop3 capabilities: %w", err)
	}
	if !offered {
		return "", fmt.Errorf("%w; use --clients-pop3-tls implicit, or plain for a dev-role server", errPOP3NoTLS)
	}
	d.resolved = pop3TLSStartTLS
	return d.resolved, nil
}

// offersSTLS asks CAPA. A server without CAPA answers -ERR, which means no
// STLS either.
func (c *pop3Conn) offersSTLS() (bool, error) {
	if _, err := c.cmd("CAPA"); err != nil {
		var pe *pop3Err
		if errors.As(err, &pe) {
			return false, nil
		}
		return false, err
	}
	dr := c.tp.DotReader()
	data, err := io.ReadAll(dr)
	if err != nil {
		return false, err
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.EqualFold(strings.TrimSpace(l), "STLS") {
			return true, nil
		}
	}
	return false, nil
}

// connect returns a session that has read the greeting and, where the mode
// needs it, completed TLS. The deadline is cleared afterwards; each command
// sets its own.
func (d *pop3Dialer) connect(ctx context.Context) (*pop3Conn, error) {
	mode, err := d.transport(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := d.tlsConfig()
	if err != nil {
		return nil, err
	}
	conn, err := d.dial(ctx, "tcp", d.addr)
	if err != nil {
		return nil, fmt.Errorf("dial pop3: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(d.timeout))
	if mode == pop3TLSImplicit {
		tc := tls.Client(conn, cfg)
		hctx, cancel := context.WithTimeout(ctx, d.timeout)
		err = tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("pop3 tls handshake: %w", err)
		}
		conn = tc
	}
	c := &pop3Conn{conn: conn, tp: textproto.NewConn(conn)}
	if err := c.status(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("pop3 greeting: %w", err)
	}
	if mode == pop3TLSStartTLS {
		if _, err := c.cmd("STLS"); err != nil {
			_ = c.Close()
			var pe *pop3Err
			if errors.As(err, &pe) {
				return nil, fmt.Errorf("%w: it refused STLS", errPOP3NoTLS)
			}
			return nil, fmt.Errorf("pop3 stls: %w", err)
		}
		tc := tls.Client(conn, cfg)
		hctx, cancel := context.WithTimeout(ctx, d.timeout)
		err = tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("pop3 tls handshake: %w", err)
		}
		// A fresh reader: nothing the server sent before TLS may be trusted
		// after it.
		c = &pop3Conn{conn: tc, tp: textproto.NewConn(tc)}
	}
	_ = c.conn.SetDeadline(time.Time{})
	return c, nil
}
