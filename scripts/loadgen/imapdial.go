package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// IMAP transport choices for --imap-tls and --clients-imap-tls.
const (
	imapTLSAuto     = "auto"
	imapTLSImplicit = "implicit"
	imapTLSStartTLS = "starttls"
	// imapTLSPlain sends credentials in clear. It exists for a dev-role
	// server only and is never chosen by auto.
	imapTLSPlain = "plain"
)

func validIMAPTLS(mode string) bool {
	switch mode {
	case imapTLSAuto, imapTLSImplicit, imapTLSStartTLS, imapTLSPlain:
		return true
	}
	return false
}

// imapDialer opens authenticated-capable IMAP connections. Every connection
// goes through dial, which callers build with guardedDial, so checkTarget
// runs before each one. imapclient.Dial* are not used because they dial the
// address themselves.
type imapDialer struct {
	addr     string
	mode     string
	insecure bool
	dial     dialFunc
	timeout  time.Duration

	// handler, when set, receives unilateral data (EXISTS during IDLE) on
	// every connection this dialer opens. It must not block.
	handler *imapclient.UnilateralDataHandler

	mu       sync.Mutex
	resolved string
}

func (d *imapDialer) options() *imapclient.Options {
	if d.handler == nil {
		return nil
	}
	return &imapclient.Options{UnilateralDataHandler: d.handler}
}

func newIMAPDialer(addr, mode string, insecure bool, dial dialFunc, timeout time.Duration) *imapDialer {
	return &imapDialer{addr: addr, mode: mode, insecure: insecure, dial: dial, timeout: timeout}
}

func (d *imapDialer) tlsConfig() (*tls.Config, error) {
	host, _, err := net.SplitHostPort(d.addr)
	if err != nil {
		return nil, fmt.Errorf("imap address: %w", err)
	}
	return &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		// #nosec G402 -- opt-in for self-signed certificates on an allow-listed test target
		InsecureSkipVerify: d.insecure,
	}, nil
}

// transport settles on implicit TLS, STARTTLS or plain. In auto mode the
// well-known TLS ports mean implicit TLS; any other port is probed once and
// the answer kept. A server that offers STARTTLS is never used in clear, and
// one that offers neither is refused: cleartext needs the explicit mode.
func (d *imapDialer) transport(ctx context.Context) (string, error) {
	if d.mode != imapTLSAuto {
		return d.mode, nil
	}
	_, port, err := net.SplitHostPort(d.addr)
	if err != nil {
		return "", fmt.Errorf("imap address: %w", err)
	}
	if port == "993" || port == "1993" {
		return imapTLSImplicit, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.resolved != "" {
		return d.resolved, nil
	}
	conn, err := d.dial(ctx, "tcp", d.addr)
	if err != nil {
		return "", fmt.Errorf("probe imap: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(d.timeout))
	c := imapclient.New(conn, nil)
	defer c.Close()
	if err := c.WaitGreeting(); err != nil {
		return "", fmt.Errorf("probe imap greeting: %w", err)
	}
	caps := c.Caps()
	switch {
	case caps.Has(imap.CapStartTLS):
		d.resolved = imapTLSStartTLS
	case caps.Has(imap.CapLoginDisabled):
		return "", fmt.Errorf("%w: it disables LOGIN without TLS and offers no STARTTLS; use --imap-tls implicit or a TLS port", errNoStartTLS)
	default:
		return "", fmt.Errorf("%w: it offers no STARTTLS; use --imap-tls implicit, or --imap-tls plain for a dev-role server", errNoStartTLS)
	}
	return d.resolved, nil
}

// connect returns a client that has read the greeting. The handshake is
// bounded by the dialer's timeout; the deadline is cleared afterwards so a
// long IDLE is not cut off by it.
func (d *imapDialer) connect(ctx context.Context) (*imapclient.Client, error) {
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
		return nil, fmt.Errorf("dial imap: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(d.timeout))
	var c *imapclient.Client
	switch mode {
	case imapTLSImplicit:
		tc := tls.Client(conn, cfg)
		hctx, cancel := context.WithTimeout(ctx, d.timeout)
		err = tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("imap tls handshake: %w", err)
		}
		c = imapclient.New(tc, d.options())
	case imapTLSStartTLS:
		c, err = imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: cfg, UnilateralDataHandler: d.handler})
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("imap starttls: %w", err)
		}
	default:
		c = imapclient.New(conn, d.options())
	}
	if err := c.WaitGreeting(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("imap greeting: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	return c, nil
}

// errIMAPTimeout marks an operation the timer cut off.
var errIMAPTimeout = errors.New("imap operation timed out")

// imapBound runs fn and closes c if timeout passes or ctx ends first.
// imapclient takes no context, so closing the connection is the only way to
// unblock a read that is stuck. A timeout is reported as errIMAPTimeout.
func imapBound(ctx context.Context, c *imapclient.Client, timeout time.Duration, fn func() error) error {
	var fired bool
	var mu sync.Mutex
	t := time.AfterFunc(timeout, func() {
		mu.Lock()
		fired = true
		mu.Unlock()
		_ = c.Close()
	})
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	err := fn()
	t.Stop()
	stop()
	mu.Lock()
	defer mu.Unlock()
	if err != nil && fired {
		return errIMAPTimeout
	}
	return err
}
