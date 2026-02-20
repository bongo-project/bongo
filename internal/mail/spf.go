package mail

import (
	"context"
	"fmt"
	"net"
	"strings"
)

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

	return sc.evaluate(ctx, senderIP, senderDomain, spfRecord)
}

func (sc *SPFChecker) evaluate(ctx context.Context, senderIP net.IP, domain, record string) (string, error) {
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

		match, err := sc.matchMechanism(ctx, senderIP, domain, mechanism)
		if err != nil {
			continue
		}
		if match {
			return qualifierResult(qualifier), nil
		}
	}

	return "neutral", nil
}

func (sc *SPFChecker) matchMechanism(ctx context.Context, senderIP net.IP, domain, mechanism string) (bool, error) {
	switch {
	case mechanism == "all":
		return true, nil

	case strings.HasPrefix(mechanism, "ip4:"):
		return matchIP(senderIP, mechanism[4:])

	case strings.HasPrefix(mechanism, "ip6:"):
		return matchIP(senderIP, mechanism[4:])

	case mechanism == "a" || strings.HasPrefix(mechanism, "a:"):
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
		includeDomain := mechanism[8:]
		result, err := sc.Check(ctx, senderIP, includeDomain)
		if err != nil {
			return false, err
		}
		return result == "pass", nil
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
