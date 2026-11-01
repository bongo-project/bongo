package netx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// This file is the outbound HTTP client for fetching URLs that a user typed
// (calendar subscriptions). The server is internet-facing and the URL is
// attacker-chosen, so the client must not reach the server's own network.
//
// The address check sits in the dialer's Control hook. Control runs after DNS
// resolution and before every connect, including each hop of a redirect, so a
// name that resolves to 127.0.0.1, a rebinding answer between "check" and
// "connect", and a redirect to an internal address are all refused by the
// same code. The transport never consults HTTP_PROXY: a proxy would receive
// the dial instead and the check would see only the proxy's address.

// Limits of the client.
const (
	fetchTimeout   = 30 * time.Second
	maxRedirects   = 3
	maxFeedBody    = 5 << 20
	maxResponseHdr = 20 * time.Second
)

var (
	// ErrForbiddenAddress is returned when a connection would reach a
	// loopback, private or otherwise non-public address.
	ErrForbiddenAddress = errors.New("address not allowed")
	// ErrForbiddenPort is returned for a port outside 80, 443 and 8443.
	ErrForbiddenPort = errors.New("port not allowed")
	// ErrBodyTooLarge is returned when a body exceeds the size limit. The body
	// is never truncated: a partial feed would look like deleted events.
	ErrBodyTooLarge = errors.New("response body too large")
	// ErrInvalidURL is returned for a URL the client will not fetch.
	ErrInvalidURL = errors.New("invalid URL")
)

// forbiddenPrefixes are the ranges a subscription may never reach.
var forbiddenPrefixes = mustPrefixes(
	"0.0.0.0/8",       // "this network", includes 0.0.0.0
	"10.0.0.0/8",      // RFC 1918
	"100.64.0.0/10",   // CGNAT
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local, cloud metadata
	"172.16.0.0/12",   // RFC 1918
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // documentation (TEST-NET-1)
	"192.168.0.0/16",  // RFC 1918
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // documentation (TEST-NET-2)
	"203.0.113.0/24",  // documentation (TEST-NET-3)
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, includes the broadcast address
	"::/128",          // unspecified
	"::1/128",         // loopback
	"fc00::/7",        // unique local
	"fe80::/10",       // link-local
	"ff00::/8",        // multicast
	"2001:2::/48",     // benchmarking
	"2001:db8::/32",   // documentation
	"64:ff9b::/96",    // NAT64: embeds an IPv4 address
	"2002::/16",       // 6to4: embeds an IPv4 address
)

func mustPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, p := range s {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// IsForbiddenAddr reports whether a connection to addr is refused. An
// IPv4-mapped IPv6 address is judged as the IPv4 address it carries.
func IsForbiddenAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() {
		return true
	}
	for _, p := range forbiddenPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Options configures a Client.
type Options struct {
	// AllowPrivateNetworks lifts the address rule for operators who must
	// subscribe to an internal feed. It does not lift the port, scheme or
	// user-information rules.
	AllowPrivateNetworks bool
}

// Validators are the conditional-GET headers saved from the last fetch.
type Validators struct {
	ETag         string
	LastModified string
}

// Result is what a fetch returned.
type Result struct {
	// NotModified is true on a 304; Body is then nil.
	NotModified  bool
	Body         []byte
	ETag         string
	LastModified string
}

// Client is the SSRF-safe feed fetcher. It is safe for concurrent use.
type Client struct {
	opts    Options
	http    *http.Client
	maxBody int64
	// extraPorts admits further ports. Tests set it, because an httptest
	// server listens on a random port; nothing outside this package can.
	extraPorts map[string]bool
}

// NewClient returns a Client enforcing the address, port and size rules.
func NewClient(opts Options) *Client {
	c := &Client{opts: opts, maxBody: maxFeedBody}
	tr := &http.Transport{
		// Proxy stays nil on purpose: see the file comment.
		Proxy:                 nil,
		DialContext:           c.dialer().DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: maxResponseHdr,
		DisableKeepAlives:     true,
	}
	c.http = &http.Client{
		Transport:     tr,
		Timeout:       fetchTimeout, // also bounds reading the body
		CheckRedirect: c.checkRedirect,
	}
	return c
}

// AllowsPrivateNetworks reports whether the address rule is lifted.
func (c *Client) AllowsPrivateNetworks() bool { return c.opts.AllowPrivateNetworks }

func (c *Client) portAllowed(port string) bool {
	switch port {
	case "80", "443", "8443":
		return true
	}
	return c.extraPorts[port]
}

// dialer builds the net.Dialer whose Control hook enforces the address and
// port rules on every connection attempt.
func (c *Client) dialer() *net.Dialer {
	return &net.Dialer{
		Timeout: 10 * time.Second,
		ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: unparsable dial address", ErrForbiddenAddress)
			}
			if !c.portAllowed(fmt.Sprint(ap.Port())) {
				return ErrForbiddenPort
			}
			if !c.opts.AllowPrivateNetworks && IsForbiddenAddr(ap.Addr()) {
				return ErrForbiddenAddress
			}
			return nil
		},
	}
}

// ValidateURL checks raw against the scheme, user-information and port rules
// and returns the URL to fetch: webcal and webcals become https. It makes no
// network call. The returned error never contains raw.
func ValidateURL(raw string) (*url.URL, error) {
	return validateURL(raw, nil)
}

func validateURL(raw string, extraPorts map[string]bool) (*url.URL, error) {
	if raw == "" || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("%w: empty or contains control characters", ErrInvalidURL)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: unparsable", ErrInvalidURL)
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
		u.Scheme = strings.ToLower(u.Scheme)
	case "webcal", "webcals":
		u.Scheme = "https"
	default:
		return nil, fmt.Errorf("%w: scheme must be https, http or webcal", ErrInvalidURL)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: user information is not allowed", ErrInvalidURL)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: no host", ErrInvalidURL)
	}
	if p := u.Port(); p != "" {
		switch p {
		case "80", "443", "8443":
		default:
			if !extraPorts[p] {
				return nil, fmt.Errorf("%w: port must be 80, 443 or 8443", ErrInvalidURL)
			}
		}
	}
	return u, nil
}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if _, err := validateURL(req.URL.String(), c.extraPorts); err != nil {
		return fmt.Errorf("redirect refused: %w", err)
	}
	return nil
}

// Fetch GETs rawURL with conditional headers. Errors carry the host and never
// the rest of the URL, which is a secret: the *url.Error that net/http returns
// embeds it, along with any redirect target.
func (c *Client) Fetch(ctx context.Context, rawURL string, v Validators) (*Result, error) {
	u, err := validateURL(rawURL, c.extraPorts)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: building request", host)
	}
	req.Header.Set("User-Agent", "Bongo-Calendar-Subscription/1")
	req.Header.Set("Accept", "text/calendar, text/plain;q=0.5, */*;q=0.1")
	if v.ETag != "" {
		req.Header.Set("If-None-Match", v.ETag)
	}
	if v.LastModified != "" {
		req.Header.Set("If-Modified-Since", v.LastModified)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drops the URL that url.Error embeds
		}
		return nil, fmt.Errorf("fetching %s: %w", host, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		return &Result{NotModified: true, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}, nil
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("fetching %s: status %d", host, resp.StatusCode)
	}
	if resp.ContentLength > c.maxBody {
		return nil, fmt.Errorf("fetching %s: %w", host, ErrBodyTooLarge)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("reading %s: %w", host, err)
	}
	if int64(len(body)) > c.maxBody {
		return nil, fmt.Errorf("fetching %s: %w", host, ErrBodyTooLarge)
	}
	return &Result{Body: body, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}, nil
}

// HostOf returns the host of rawURL for logs and the UI, or "" if it does not
// parse. It never returns the path, query or user information.
func HostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
