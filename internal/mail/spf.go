package mail

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// maxSPFDNSLookups is the RFC 7208 §4.6.4 ceiling on DNS-querying mechanisms
// (a, mx, include, and the redirect modifier) evaluated for a single SPF check.
// Without it, an attacker who controls the MAIL FROM domain's DNS can craft a
// deep/branching or cyclic include tree that makes the public MX issue an
// unbounded burst of DNS queries per connection (resolver/CPU exhaustion and
// outbound DNS amplification).
const maxSPFDNSLookups = 10

// errSPFAbort aborts evaluation and forces a permerror result at the top level.
// It is raised when the DNS-lookup budget is exhausted or an include/redirect
// loop is detected, per RFC 7208.
var errSPFAbort = errors.New("spf: evaluation aborted (lookup limit or loop)")

// spfState carries the per-evaluation DNS-lookup budget and the set of already
// visited domains (include-loop / redirect-loop guard) across recursion.
type spfState struct {
	lookups int
	visited map[string]struct{}
}

// count charges one DNS-querying mechanism against the budget.
func (st *spfState) count() error {
	st.lookups++
	if st.lookups > maxSPFDNSLookups {
		return errSPFAbort
	}
	return nil
}

// DNSResolver provides DNS lookups for SPF checking.
type DNSResolver interface {
	LookupTXT(ctx context.Context, domain string) ([]string, error)
	LookupIP(ctx context.Context, host string) ([]net.IP, error)
	LookupMX(ctx context.Context, domain string) ([]*net.MX, error)
}

// defaultResolver uses the system DNS resolver.
type defaultResolver struct{}

func (d *defaultResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, domain)
}

func (d *defaultResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func (d *defaultResolver) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, domain)
}

// SPFChecker verifies sender SPF records.
type SPFChecker struct {
	resolver DNSResolver
}

// NewSPFChecker creates a new SPF checker with the default DNS resolver.
func NewSPFChecker() *SPFChecker {
	return &SPFChecker{resolver: &defaultResolver{}}
}

// NewSPFCheckerWithResolver creates a new SPF checker with a custom resolver.
func NewSPFCheckerWithResolver(resolver DNSResolver) *SPFChecker {
	return &SPFChecker{resolver: resolver}
}

// Check evaluates the SPF policy for a sender IP and domain.
// Returns one of: pass, fail, softfail, neutral, none, temperror, permerror.
func (sc *SPFChecker) Check(ctx context.Context, senderIP net.IP, senderDomain string) (string, error) {
	result, err := sc.checkWithState(ctx, senderIP, senderDomain, &spfState{visited: make(map[string]struct{})})
	if errors.Is(err, errSPFAbort) {
		// Exceeding the DNS-lookup limit is a permanent error per RFC 7208.
		return "permerror", nil
	}
	return result, err
}

func (sc *SPFChecker) checkWithState(ctx context.Context, senderIP net.IP, senderDomain string, st *spfState) (string, error) {
	// Guard against include/redirect loops: a domain already on the *current*
	// recursion path aborts to permerror. Tracking the path (add on entry,
	// remove on exit) rather than a global visited set means a legitimate
	// diamond include (same domain reached via two branches) is not penalised;
	// the shared lookup budget still bounds total work.
	key := strings.ToLower(strings.TrimSuffix(senderDomain, "."))
	if _, onPath := st.visited[key]; onPath {
		return "permerror", errSPFAbort
	}
	st.visited[key] = struct{}{}
	defer delete(st.visited, key)

	records, err := sc.resolver.LookupTXT(ctx, senderDomain)
	if err != nil {
		return "temperror", fmt.Errorf("DNS lookup: %w", err)
	}

	var spfRecord string
	for _, r := range records {
		if strings.HasPrefix(r, "v=spf1 ") || r == "v=spf1" {
			spfRecord = r
			break
		}
	}

	if spfRecord == "" {
		return "none", nil
	}

	return sc.evaluate(ctx, senderIP, senderDomain, spfRecord, st)
}

func (sc *SPFChecker) evaluate(ctx context.Context, senderIP net.IP, domain, record string, st *spfState) (string, error) {
	parts := strings.Fields(record)
	if len(parts) == 0 || parts[0] != "v=spf1" {
		return "permerror", nil
	}

	for _, part := range parts[1:] {
		qualifier := "+"
		mechanism := part

		if len(mechanism) > 0 && (mechanism[0] == '+' || mechanism[0] == '-' || mechanism[0] == '~' || mechanism[0] == '?') {
			qualifier = string(mechanism[0])
			mechanism = mechanism[1:]
		}

		match, err := sc.matchMechanism(ctx, senderIP, domain, mechanism, st)
		if err != nil {
			if errors.Is(err, errSPFAbort) {
				// Propagate the budget exhaustion up to Check, which maps it
				// to permerror; do not silently continue past it.
				return "", err
			}
			continue
		}
		if match {
			return qualifierResult(qualifier), nil
		}
	}

	return "neutral", nil
}

func (sc *SPFChecker) matchMechanism(ctx context.Context, senderIP net.IP, domain, mechanism string, st *spfState) (bool, error) {
	switch {
	case mechanism == "all":
		return true, nil

	case strings.HasPrefix(mechanism, "ip4:"):
		return matchIP(senderIP, mechanism[4:])

	case strings.HasPrefix(mechanism, "ip6:"):
		return matchIP(senderIP, mechanism[4:])

	case mechanism == "a" || strings.HasPrefix(mechanism, "a:"):
		if err := st.count(); err != nil {
			return false, err
		}
		host := domain
		if strings.HasPrefix(mechanism, "a:") {
			host = mechanism[2:]
		}
		ips, err := sc.resolver.LookupIP(ctx, host)
		if err != nil {
			return false, err
		}
		for _, ip := range ips {
			if ip.Equal(senderIP) {
				return true, nil
			}
		}
		return false, nil

	case mechanism == "mx" || strings.HasPrefix(mechanism, "mx:"):
		if err := st.count(); err != nil {
			return false, err
		}
		lookupDomain := domain
		if strings.HasPrefix(mechanism, "mx:") {
			lookupDomain = mechanism[3:]
		}
		mxRecords, err := sc.resolver.LookupMX(ctx, lookupDomain)
		if err != nil {
			return false, err
		}
		for _, mx := range mxRecords {
			host := strings.TrimSuffix(mx.Host, ".")
			ips, err := sc.resolver.LookupIP(ctx, host)
			if err != nil {
				continue
			}
			for _, ip := range ips {
				if ip.Equal(senderIP) {
					return true, nil
				}
			}
		}
		return false, nil

	case strings.HasPrefix(mechanism, "include:"):
		if err := st.count(); err != nil {
			return false, err
		}
		includeDomain := mechanism[8:]
		result, err := sc.checkWithState(ctx, senderIP, includeDomain, st)
		if err != nil {
			return false, err
		}
		switch result {
		case "pass":
			return true, nil
		case "permerror", "none":
			// RFC 7208 §5.2: an include whose target yields permerror or none
			// makes the including record a permerror.
			return false, errSPFAbort
		default: // fail, softfail, neutral, temperror -> not a match
			return false, nil
		}
	}

	return false, nil
}

func matchIP(senderIP net.IP, cidr string) (bool, error) {
	if !strings.Contains(cidr, "/") {
		// Single IP.
		ip := net.ParseIP(cidr)
		if ip == nil {
			return false, fmt.Errorf("invalid IP: %s", cidr)
		}
		return ip.Equal(senderIP), nil
	}

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, fmt.Errorf("invalid CIDR: %w", err)
	}
	return network.Contains(senderIP), nil
}

func qualifierResult(q string) string {
	switch q {
	case "+":
		return "pass"
	case "-":
		return "fail"
	case "~":
		return "softfail"
	case "?":
		return "neutral"
	}
	return "neutral"
}
