package smtp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MTA-STS policy modes (RFC 8461 §3.2).
const (
	MTASTSModeEnforce = "enforce"
	MTASTSModeTesting = "testing"
	MTASTSModeNone    = "none"
)

const (
	// mtaSTSMaxPolicyBytes bounds the fetched policy body.
	mtaSTSMaxPolicyBytes = 64 * 1024
	// mtaSTSMaxCacheTTL caps how long a policy is trusted before re-validation,
	// regardless of a larger published max_age (defence-in-depth revalidation).
	mtaSTSMaxCacheTTL = 7 * 24 * time.Hour
)

// MTASTSPolicy is a parsed MTA-STS policy for a recipient domain.
type MTASTSPolicy struct {
	Mode   string   // enforce | testing | none
	MX     []string // MX host patterns (may use a leading "*." wildcard)
	MaxAge int      // seconds
}

// MXMatches reports whether an MX hostname is authorised by the policy's mx
// patterns. A "*." wildcard matches exactly one leftmost label (RFC 8461 §4.1).
func (p *MTASTSPolicy) MXMatches(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, pat := range p.MX {
		pat = strings.ToLower(strings.TrimSuffix(pat, "."))
		if pat == host {
			return true
		}
		if strings.HasPrefix(pat, "*.") {
			suffix := pat[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) {
				label := host[:len(host)-len(suffix)]
				if label != "" && !strings.Contains(label, ".") {
					return true
				}
			}
		}
	}
	return false
}

type mtaSTSEntry struct {
	policy  *MTASTSPolicy
	id      string
	expires time.Time
}

// MTASTSCache discovers, fetches, and caches MTA-STS policies per recipient
// domain. It is safe for concurrent use. The DNS TXT lookup and HTTPS policy
// fetch are injectable for testing.
type MTASTSCache struct {
	mu        sync.Mutex
	entries   map[string]mtaSTSEntry
	lookupTXT func(ctx context.Context, name string) ([]string, error)
	fetch     func(ctx context.Context, url string) (string, error)
	now       func() time.Time
}

// NewMTASTSCache creates a cache backed by the system resolver and a
// WebPKI-validating HTTPS client.
func NewMTASTSCache() *MTASTSCache {
	return &MTASTSCache{
		entries: make(map[string]mtaSTSEntry),
		lookupTXT: func(ctx context.Context, name string) ([]string, error) {
			return net.DefaultResolver.LookupTXT(ctx, name)
		},
		fetch: httpsFetchMTASTSPolicy,
		now:   time.Now,
	}
}

// PolicyFor returns the recipient domain's active MTA-STS policy, or nil if the
// domain publishes none. A domain that advertises MTA-STS (via the _mta-sts TXT
// record) but whose policy cannot be fetched or parsed returns an error so the
// caller can decide how strict to be; a domain with no TXT record returns
// (nil, nil).
func (c *MTASTSCache) PolicyFor(ctx context.Context, domain string) (*MTASTSPolicy, error) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))

	id := c.stsRecordID(ctx, domain)
	if id == "" {
		return nil, nil // domain does not advertise MTA-STS
	}

	c.mu.Lock()
	ent, ok := c.entries[domain]
	fresh := ok && ent.id == id && c.now().Before(ent.expires)
	c.mu.Unlock()
	if fresh {
		return ent.policy, nil
	}

	body, err := c.fetch(ctx, "https://mta-sts."+domain+"/.well-known/mta-sts.txt")
	if err != nil {
		return nil, fmt.Errorf("mta-sts: fetch policy for %s: %w", domain, err)
	}
	policy, err := parseMTASTSPolicy(body)
	if err != nil {
		return nil, fmt.Errorf("mta-sts: parse policy for %s: %w", domain, err)
	}

	ttl := time.Duration(policy.MaxAge) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	if ttl > mtaSTSMaxCacheTTL {
		ttl = mtaSTSMaxCacheTTL
	}
	c.mu.Lock()
	c.entries[domain] = mtaSTSEntry{policy: policy, id: id, expires: c.now().Add(ttl)}
	c.mu.Unlock()
	return policy, nil
}

// stsRecordID returns the id= value from the _mta-sts TXT record, or "" if the
// domain publishes no valid MTA-STS record. The id changes when the policy is
// updated, which invalidates the cache.
func (c *MTASTSCache) stsRecordID(ctx context.Context, domain string) string {
	txts, err := c.lookupTXT(ctx, "_mta-sts."+domain)
	if err != nil {
		return ""
	}
	for _, txt := range txts {
		if !strings.Contains(txt, "v=STSv1") {
			continue
		}
		for _, part := range strings.Split(txt, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "id=") {
				return strings.TrimSpace(part[3:])
			}
		}
	}
	return ""
}

// parseMTASTSPolicy parses an mta-sts.txt policy body (RFC 8461 §3.2).
func parseMTASTSPolicy(body string) (*MTASTSPolicy, error) {
	p := &MTASTSPolicy{}
	var sawVersion bool
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		switch key {
		case "version":
			if strings.EqualFold(val, "STSv1") {
				sawVersion = true
			}
		case "mode":
			p.Mode = strings.ToLower(val)
		case "mx":
			if val != "" {
				p.MX = append(p.MX, val)
			}
		case "max_age":
			if n, err := strconv.Atoi(val); err == nil {
				p.MaxAge = n
			}
		}
	}
	if !sawVersion {
		return nil, fmt.Errorf("missing STSv1 version")
	}
	switch p.Mode {
	case MTASTSModeEnforce, MTASTSModeTesting, MTASTSModeNone:
	default:
		return nil, fmt.Errorf("invalid mode %q", p.Mode)
	}
	return p, nil
}

// httpsFetchMTASTSPolicy fetches the policy over HTTPS with WebPKI validation.
// Per RFC 8461 §3.3 redirects are not followed and the body is size-bounded.
func httpsFetchMTASTSPolicy(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("policy fetch status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, mtaSTSMaxPolicyBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
