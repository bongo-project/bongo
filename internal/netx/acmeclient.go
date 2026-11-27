package netx

import (
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// maxACMEBody is the most bytes one CA response may carry. A certificate
// chain is a few KiB; this leaves ample room.
const maxACMEBody = 1 << 20

// ErrACMEResponseTooLarge reports a CA response body over the cap.
var ErrACMEResponseTooLarge = errors.New("netx: ACME response body too large")

// limitTransport caps every response body of the wrapped transport.
type limitTransport struct {
	inner http.RoundTripper
	max   int64
}

func (t limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > t.max {
		_ = resp.Body.Close()
		return nil, ErrACMEResponseTooLarge
	}
	resp.Body = &cappedBody{rc: resp.Body, left: t.max}
	return resp, nil
}

// cappedBody fails once more than the cap has been read.
type cappedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, ErrACMEResponseTooLarge
	}
	// Read one byte past the cap so an over-long body is detected.
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	if b.left < 0 {
		return 0, ErrACMEResponseTooLarge
	}
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }

// NewACMEClient returns the HTTP client the ACME manager uses to reach the CA.
// It shares the address control of the other safe clients (public addresses
// only, checked after DNS on every connection) and dials port 443 only. There
// is no proxy and no redirect following, and every phase has a timeout. A CA
// on a private address, such as a self-hosted step-ca, is therefore not
// reachable. x/crypto/acme bounds only the certificate download, not the JSON
// documents or error bodies, so every response body is capped here at
// maxACMEBody and reading past it fails with ErrACMEResponseTooLarge.
func NewACMEClient() *http.Client {
	d := &net.Dialer{
		Timeout:        10 * time.Second,
		ControlContext: controlFor(func(p uint16) bool { return p == 443 }, false),
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          2,
	}
	return &http.Client{
		Transport: limitTransport{inner: tr, max: maxACMEBody},
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
