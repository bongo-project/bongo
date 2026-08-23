// Package dnsbl implements DNS blocklist (RBL) lookups: given a connecting
// IP and a set of configured zones, it answers "which of these zones list
// this IP" without ever failing the caller. A DNSBL is queried by encoding
// the IP into a hostname under the zone (reversed octets for IPv4, reversed
// nibbles for IPv6) and looking up its A record; an answer in 127.0.0.0/8
// (excluding the reserved policy-error sub-range, see query.go) means
// listed, NXDOMAIN means not listed.
//
// This package is self-contained and offline-testable: it has no knowledge
// of HCL config, the antispam pipeline, or SMTP. It answers one question
// safely; callers decide what a listing means.
//
// Fail-open is a hard guarantee: Check never returns an error. Every
// failure mode -- timeout, SERVFAIL, malformed answer, a zone with no IPv6
// support, a resolver error -- is recorded as "not listed" on the affected
// zone (see ZoneResult.Err, which is informational only) and logged. There
// is no path by which this package can turn a DNS problem into a delivery
// failure.
package dnsbl

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"time"
)

// Resolver is the DNS seam for this package, injected so tests never touch
// the network.
//
// Defined here at the consumer, per project convention, rather than reusing
// internal/mail.DNSResolver: that interface is unexported at its default
// implementation (mail.defaultResolver), so the concrete default cannot be
// reused directly, and it also declares LookupTXT/LookupMX, which DNSBL
// never calls. The method signature below is deliberately identical to
// mail.DNSResolver's LookupIP, so any existing mail.DNSResolver value
// already satisfies this interface with no adapter needed.
type Resolver interface {
	LookupIP(ctx context.Context, host string) ([]net.IP, error)
}

// defaultResolver is the production Resolver, backed by the system resolver.
type defaultResolver struct{}

func (defaultResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	// "ip4" rather than mail.defaultResolver's "ip": DNSBL answers are
	// only ever A records, and "ip" additionally issues an AAAA query
	// for every lookup. Against zones that rate-limit, that doubles
	// query volume for an answer type that will never come back
	// listed. This is the one place this package deliberately departs
	// from the spf.go resolver shape.
	return net.DefaultResolver.LookupIP(ctx, "ip4", host)
}

// ZoneResult is the verdict for one configured zone. Err is informational
// only -- callers must not treat a non-nil Err as message failure; it
// exists purely so operators can see *why* a zone was skipped.
type ZoneResult struct {
	Zone   string
	Listed bool
	Code   string // the returned address, e.g. "127.0.0.2"; empty when not listed
	Err    error  // informational only: timeout, resolver error, or context deadline
}

// Result is the outcome of checking one IP against every configured zone.
type Result struct {
	IP    string
	Zones []ZoneResult
}

// ListedZones returns the subset of Zones with Listed set.
func (r Result) ListedZones() []ZoneResult {
	var out []ZoneResult
	for _, z := range r.Zones {
		if z.Listed {
			out = append(out, z)
		}
	}
	return out
}

// Fallbacks applied when a caller supplies a non-positive timeout or cache
// TTL. See newChecker for why these are clamped here rather than trusted.
const (
	defaultTimeout  = 5 * time.Second
	defaultCacheTTL = 10 * time.Minute
)

// Checker looks up an IP against a fixed set of DNSBL zones, concurrently,
// under one shared timeout, with per-IP result caching.
type Checker struct {
	resolver Resolver
	zones    []string
	timeout  time.Duration
	cache    *cache
	logger   *slog.Logger
}

// NewChecker creates a Checker using the system DNS resolver.
//
// zones, timeout and cacheTTL are plain Go values -- this package has no
// knowledge of HCL config; the caller is responsible for parsing config into
// these arguments. logger may be nil, in which case slog.Default() is used.
func NewChecker(zones []string, timeout, cacheTTL time.Duration, logger *slog.Logger) *Checker {
	return newChecker(defaultResolver{}, zones, timeout, cacheTTL, logger)
}

// NewCheckerWithResolver creates a Checker using a caller-supplied resolver,
// so tests run without network access.
func NewCheckerWithResolver(resolver Resolver, zones []string, timeout, cacheTTL time.Duration, logger *slog.Logger) *Checker {
	return newChecker(resolver, zones, timeout, cacheTTL, logger)
}

func newChecker(resolver Resolver, zones []string, timeout, cacheTTL time.Duration, logger *slog.Logger) *Checker {
	normalized := make([]string, 0, len(zones))
	for _, z := range zones {
		if nz, ok := normalizeZone(z); ok {
			normalized = append(normalized, nz)
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	// Clamp rather than trust the caller. A zero or negative timeout makes
	// every context expire before it is used, so every zone is recorded as
	// timed out and DNSBL is silently disabled -- an easy mistake for a
	// missing HCL value to make, and one that shows up only as log noise.
	if timeout <= 0 {
		logger.Warn("dnsbl: non-positive lookup timeout, using default",
			"configured", timeout, "using", defaultTimeout)
		timeout = defaultTimeout
	}
	if cacheTTL <= 0 {
		logger.Warn("dnsbl: non-positive cache TTL, using default",
			"configured", cacheTTL, "using", defaultCacheTTL)
		cacheTTL = defaultCacheTTL
	}
	return &Checker{
		resolver: resolver,
		zones:    normalized,
		timeout:  timeout,
		cache:    newCache(normalized, cacheTTL, nil),
		logger:   logger,
	}
}

// Check reports which of the Checker's configured zones list ip. It never
// returns an error: every failure mode is folded into a "not listed"
// ZoneResult with Err set for diagnostics. A nil ip or an empty configured
// zone set returns a Result with no Zones.
//
// Check is safe for concurrent use.
func (c *Checker) Check(ctx context.Context, ip net.IP) Result {
	if ip == nil {
		return Result{}
	}
	if len(c.zones) == 0 {
		return Result{IP: ip.String()}
	}

	key := c.cache.key(ip)
	if res, ok := c.cache.get(key); ok {
		return res
	}

	res, cacheable := c.lookup(ctx, ip)
	if cacheable {
		c.cache.set(key, res)
	}
	return res
}

// lookup queries every configured zone concurrently, bounded by one shared
// timeout derived from parent. Zones not collected before the deadline are
// recorded as timed-out rather than left unreported.
//
// cacheable is false when the caller's own context ended the lookup. Such a
// Result is all-timeouts and says nothing about the zones, so storing it
// would silence DNSBL for this IP for a full TTL on the strength of one
// cancelled request.
func (c *Checker) lookup(parent context.Context, ip net.IP) (res Result, cacheable bool) {
	ipStr := ip.String()

	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()

	// Buffered to len(c.zones): every goroutine can always write its
	// result and exit, even if the collector below has already
	// returned on deadline. This is what makes the timeout bound
	// unconditional -- it does not depend on every resolver honouring
	// ctx, only on the collector giving up at the deadline.
	ch := make(chan ZoneResult, len(c.zones))
	for _, zone := range c.zones {
		go func(zone string) {
			ch <- c.queryZone(ctx, ip, zone)
		}(zone)
	}

	pending := make(map[string]struct{}, len(c.zones))
	for _, zone := range c.zones {
		pending[zone] = struct{}{}
	}

	results := make([]ZoneResult, 0, len(c.zones))
collect:
	for len(pending) > 0 {
		select {
		case r := <-ch:
			delete(pending, r.Zone)
			results = append(results, r)
		case <-ctx.Done():
			break collect
		}
	}

	// A select picks pseudo-randomly when both cases are ready, so a real
	// verdict that landed in the buffer just before the deadline can lose
	// the race to ctx.Done(). Take whatever is already waiting before
	// writing any zone off as timed out.
drain:
	for len(pending) > 0 {
		select {
		case r := <-ch:
			delete(pending, r.Zone)
			results = append(results, r)
		default:
			break drain
		}
	}

	for zone := range pending {
		c.logger.Warn("dnsbl: zone lookup did not complete before shared timeout, treating as not listed",
			"ip", ipStr, "zone", zone)
		results = append(results, ZoneResult{Zone: zone, Listed: false, Err: ctx.Err()})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Zone < results[j].Zone })
	return Result{IP: ipStr, Zones: results}, parent.Err() == nil
}

// queryZone performs and interprets one zone lookup. It never returns an
// error itself -- every failure is folded into ZoneResult.Err with
// Listed=false.
func (c *Checker) queryZone(ctx context.Context, ip net.IP, zone string) ZoneResult {
	ipStr := ip.String()

	name, ok := queryName(ip, zone)
	if !ok {
		// Effectively unreachable -- Check rejects a nil ip and
		// normalizeZone drops empty zones -- but every fail-open path in
		// this package logs, and a silent one is the hole an operator
		// cannot diagnose.
		err := fmt.Errorf("cannot build query name for %s under zone %s", ipStr, zone)
		c.logger.Warn("dnsbl: unusable address or zone, treating as not listed",
			"ip", ipStr, "zone", zone)
		return ZoneResult{Zone: zone, Listed: false, Err: err}
	}

	answers, err := c.resolver.LookupIP(ctx, name)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			// NXDOMAIN is the overwhelmingly common case (every zone,
			// every non-listed message) -- Debug only, or this floods
			// the log at Info/Warn volume.
			c.logger.Debug("dnsbl: not listed (nxdomain)", "ip", ipStr, "zone", zone)
		} else {
			c.logger.Warn("dnsbl: lookup error, treating as not listed", "ip", ipStr, "zone", zone, "error", err)
		}
		return ZoneResult{Zone: zone, Listed: false, Err: err}
	}

	for _, a := range answers {
		class, code := classifyAnswer(a)
		switch class {
		case classListed:
			c.logger.Info("dnsbl: listed", "ip", ipStr, "zone", zone, "code", code)
			return ZoneResult{Zone: zone, Listed: true, Code: code}
		case classPolicyError:
			c.logger.Warn("dnsbl: zone returned a policy-error code, treating as not listed",
				"ip", ipStr, "zone", zone, "code", code)
		case classUninterpretable:
			c.logger.Warn("dnsbl: uninterpretable answer outside 127.0.0.0/8, treating as not listed",
				"ip", ipStr, "zone", zone, "code", code)
		}
	}
	return ZoneResult{Zone: zone, Listed: false}
}
