package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// productionDomains and productionHosts are the runtime source of what the
// tool refuses without --i-own-this-target; deploy/ is read only by tests, so
// the tool behaves the same on a machine without the repository. A name is
// production when it equals an entry or is a subdomain of one.
var (
	productionDomains = []string{"tydewhatmay.eu", "example.com", "example.org"}
	productionHosts   = []string{"mail.tydewhatmay.eu", "mail.example.com"}
)

// Target is a host (lower-case name or canonical IP literal) and an optional
// port; port 0 means unspecified.
type Target struct {
	Host string
	Port int
	IP   bool
}

func (t Target) String() string {
	if t.Port == 0 {
		return t.Host
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// normaliseHost lower-cases a DNS name and drops one trailing dot. It refuses
// anything that is not plain ASCII with non-empty labels, rather than half
// supporting IDN.
func normaliseHost(s string) (string, error) {
	s = strings.ToLower(strings.TrimSuffix(s, "."))
	if s == "" {
		return "", errors.New("empty host")
	}
	if len(s) > 253 {
		return "", errors.New("host too long")
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" {
			return "", fmt.Errorf("host %q has an empty label", s)
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_'
		if !ok {
			return "", fmt.Errorf("host %q has a character outside a-z, 0-9, '-', '_' and '.'", s)
		}
	}
	return s, nil
}

// inDomain reports whether name equals domain or is a subdomain of it, on a
// label boundary. Both must already be normalised.
func inDomain(name, domain string) bool {
	return name == domain || strings.HasSuffix(name, "."+domain)
}

// parseTarget parses host, host:port, an IP literal or a bracketed IPv6
// literal with or without a port. Names and addresses are compared as given;
// DNS is never consulted, so a name that resolves to a production address is
// not caught.
func parseTarget(s string) (Target, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Target{}, errors.New("empty target")
	}
	host, portStr := s, ""
	switch {
	case strings.HasPrefix(s, "["):
		if h, p, err := net.SplitHostPort(s); err == nil {
			host, portStr = h, p
		} else if strings.HasSuffix(s, "]") {
			host = s[1 : len(s)-1]
		} else {
			return Target{}, fmt.Errorf("bad target: %w", err)
		}
	case strings.Count(s, ":") == 1:
		h, p, err := net.SplitHostPort(s)
		if err != nil {
			return Target{}, fmt.Errorf("bad target: %w", err)
		}
		host, portStr = h, p
		if p == "" {
			return Target{}, errors.New("empty port")
		}
	}
	var t Target
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return Target{}, fmt.Errorf("port %q out of range 1-65535", portStr)
		}
		t.Port = p
	}
	if a, err := netip.ParseAddr(host); err == nil {
		t.Host = a.WithZone("").Unmap().String()
		t.IP = true
		return t, nil
	}
	h, err := normaliseHost(host)
	if err != nil {
		return Target{}, err
	}
	t.Host = h
	return t, nil
}

func isProductionName(name string) bool {
	for _, d := range productionDomains {
		if inDomain(name, d) {
			return true
		}
	}
	for _, h := range productionHosts {
		if inDomain(name, h) {
			return true
		}
	}
	return false
}

// allowed reports whether t is covered by the allow list. An entry without a
// port matches any port; an entry with a port matches only that port. An IP
// literal matches only an identical literal entry.
func allowed(allow []Target, t Target) bool {
	for _, a := range allow {
		if a.Host != t.Host || a.IP != t.IP {
			continue
		}
		if a.Port == 0 || a.Port == t.Port {
			return true
		}
	}
	return false
}

// checkTarget is the gate for one connection. Every network dial must call it
// with the address it is about to use.
func checkTarget(cfg Config, hostport string) error {
	t, err := parseTarget(hostport)
	if err != nil {
		return fmt.Errorf("target %q: %w", hostport, err)
	}
	if !allowed(cfg.Allow, t) {
		return fmt.Errorf("target %q is not in --target-allow", hostport)
	}
	if !t.IP && isProductionName(t.Host) && !cfg.IOwnThisTarget {
		return fmt.Errorf("target %q is a production host; --i-own-this-target is required", hostport)
	}
	return nil
}

// checkTestDomain validates the --test-domain value itself and returns it
// normalised.
func checkTestDomain(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("--test-domain is required")
	}
	d, err := normaliseHost(raw)
	if err != nil {
		return "", fmt.Errorf("--test-domain: %w", err)
	}
	if !strings.Contains(d, ".") {
		return "", fmt.Errorf("--test-domain %q needs at least two labels", raw)
	}
	// The ancestor rule stops "eu" or "com" admitting production addresses.
	// --i-own-this-target does not lift it.
	all := append(append([]string{}, productionDomains...), productionHosts...)
	for _, p := range all {
		if inDomain(d, p) || inDomain(p, d) {
			return "", fmt.Errorf("--test-domain %q overlaps production domain %q", raw, p)
		}
	}
	return d, nil
}

// checkAddress requires the domain of an envelope address to be the test
// domain or a subdomain. Every sender and recipient must pass it, including
// ones a later subcommand generates.
func checkAddress(addr, testDomain string) error {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return fmt.Errorf("address %q has no '@'", addr)
	}
	local, domain := addr[:at], addr[at+1:]
	if local == "" {
		return fmt.Errorf("address %q has an empty local part", addr)
	}
	for _, r := range local {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("address %q has a control or space character", addr)
		}
	}
	d, err := normaliseHost(domain)
	if err != nil {
		return fmt.Errorf("address %q: %w", addr, err)
	}
	td, err := normaliseHost(testDomain)
	if err != nil {
		return fmt.Errorf("test domain: %w", err)
	}
	if !inDomain(d, td) {
		return fmt.Errorf("address %q is outside test domain %q", addr, testDomain)
	}
	return nil
}

// Validate is the only gate. run calls it before any subcommand; it does no
// network I/O and never resolves names.
func Validate(cfg Config) error {
	if len(cfg.Allow) == 0 {
		return errors.New("no --target-allow given; nothing may be contacted")
	}
	for _, a := range cfg.Allow {
		if !a.IP && isProductionName(a.Host) && !cfg.IOwnThisTarget {
			return fmt.Errorf("--target-allow %q is a production host; --i-own-this-target is required", a)
		}
	}
	td, err := checkTestDomain(cfg.TestDomain)
	if err != nil {
		return err
	}
	for _, t := range cfg.Targets {
		if err := checkTarget(cfg, t); err != nil {
			return err
		}
	}
	for _, s := range cfg.Senders {
		if err := checkAddress(s, td); err != nil {
			return fmt.Errorf("sender: %w", err)
		}
	}
	for _, r := range cfg.Recipients {
		if err := checkAddress(r, td); err != nil {
			return fmt.Errorf("recipient: %w", err)
		}
	}
	return nil
}
