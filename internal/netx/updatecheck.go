package netx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// The version check posts a small JSON body to the project's endpoint. Like
// PostOneClick it has its own request path: https only, a POST, never a
// redirect, no cookies, no proxy, public addresses only, a fixed User-Agent
// and a response capped at 16 KiB. Nothing but User-Agent and Content-Type is
// set; the transport adds only Content-Length, Accept-Encoding and Connection.

// updateCheckTimeout bounds the whole request and its header wait. It is a
// variable only so a test can shrink it.
var updateCheckTimeout = 10 * time.Second

const (
	updateCheckMaxBody   = 16 << 10
	updateCheckUserAgent = "bongo-go"
)

// PostUpdateCheck POSTs body (JSON) to u and returns the answer body for a 200
// status. A 3xx is ErrRedirect, an answer over 16 KiB (declared or actual) is
// ErrBodyTooLarge, any other status is an error. Errors carry the host only.
func (c *Client) PostUpdateCheck(ctx context.Context, u *url.URL, body []byte) ([]byte, error) {
	if u == nil {
		return nil, fmt.Errorf("%w: no URL", ErrInvalidURL)
	}
	if err := c.checkOneClickURL(u); err != nil {
		return nil, err
	}
	host := u.Hostname()

	tr := &http.Transport{
		Proxy:                 nil, // see the file comment of safeclient.go
		DialContext:           c.dialer().DialContext,
		TLSClientConfig:       c.tlsConfig,
		TLSHandshakeTimeout:   updateCheckTimeout,
		ResponseHeaderTimeout: updateCheckTimeout,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		Timeout:       updateCheckTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("checking for updates at %s: building request", host)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", updateCheckUserAgent)

	resp, err := client.Do(req) // #nosec G704 -- https only, public addresses only (Control hook), no redirects
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drops the URL that url.Error embeds
		}
		return nil, fmt.Errorf("checking for updates at %s: %w", host, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fmt.Errorf("checking for updates at %s: %w (status %d)", host, ErrRedirect, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("checking for updates at %s: status %d", host, resp.StatusCode)
	case resp.ContentLength > updateCheckMaxBody:
		return nil, fmt.Errorf("checking for updates at %s: %w", host, ErrBodyTooLarge)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, updateCheckMaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("checking for updates at %s: reading the answer: %w", host, err)
	}
	if len(data) > updateCheckMaxBody {
		return nil, fmt.Errorf("checking for updates at %s: %w", host, ErrBodyTooLarge)
	}
	return data, nil
}
