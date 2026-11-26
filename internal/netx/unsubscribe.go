package netx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// One-click unsubscribe (RFC 8058) sends a request to a URL chosen by the
// sender of a message, so it is the most hostile use of this package. It has
// its own request path rather than Fetch: https only, a POST, never a
// redirect (RFC 8058 section 3.2 forbids one, and Go would turn a 301, 302 or
// 303 into a GET), no cookies, no proxy, a fixed User-Agent and a small
// response that is read and thrown away.

const (
	oneClickTimeout   = 10 * time.Second
	oneClickMaxBody   = 16 << 10
	oneClickUserAgent = "Bongo-Unsubscribe/1"
	oneClickBody      = "List-Unsubscribe=One-Click"
)

// ErrRedirect is returned when the unsubscribe endpoint answers with a
// redirect. It is never followed.
var ErrRedirect = errors.New("redirect not followed")

// PostOneClick POSTs List-Unsubscribe=One-Click to u and reports success for a
// 2xx answer. The address rule of c applies to every connection, so Options
// for this call must leave AllowPrivateNetworks false. The response body is
// read up to 16 KiB and discarded: nothing the remote side says reaches the
// caller. Errors carry the host and never the rest of the URL, which usually
// holds a token that identifies the subscriber.
func (c *Client) PostOneClick(ctx context.Context, u *url.URL) error {
	if u == nil {
		return fmt.Errorf("%w: no URL", ErrInvalidURL)
	}
	if err := c.checkOneClickURL(u); err != nil {
		return err
	}
	host := u.Hostname()

	tr := &http.Transport{
		Proxy:                 nil, // see the file comment of safeclient.go
		DialContext:           c.dialer().DialContext,
		TLSClientConfig:       c.tlsConfig,
		TLSHandshakeTimeout:   oneClickTimeout,
		ResponseHeaderTimeout: oneClickTimeout,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: tr,
		Timeout:   oneClickTimeout,
		// No Jar: no cookie is stored or sent.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(oneClickBody))
	if err != nil {
		return fmt.Errorf("unsubscribing at %s: building request", host)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", oneClickUserAgent)

	resp, err := client.Do(req) // #nosec G704 -- https only, public addresses only (Control hook), no redirects
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drops the URL that url.Error embeds
		}
		return fmt.Errorf("unsubscribing at %s: %w", host, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, oneClickMaxBody))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fmt.Errorf("unsubscribing at %s: %w (status %d)", host, ErrRedirect, resp.StatusCode)
	default:
		return fmt.Errorf("unsubscribing at %s: status %d", host, resp.StatusCode)
	}
}

// checkOneClickURL enforces the scheme, user-information and port rules for
// the one-click POST: https only, on 443, 8443 or a test port.
func (c *Client) checkOneClickURL(u *url.URL) error {
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("%w: scheme must be https", ErrInvalidURL)
	}
	if u.User != nil {
		return fmt.Errorf("%w: user information is not allowed", ErrInvalidURL)
	}
	if u.Hostname() == "" || strings.IndexFunc(u.Host, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: no host", ErrInvalidURL)
	}
	switch p := u.Port(); p {
	case "", "443", "8443":
	default:
		if !c.extraPorts[p] {
			return fmt.Errorf("%w: port must be 443 or 8443", ErrInvalidURL)
		}
	}
	return nil
}
