package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"
)

func init() {
	commands["egress-check"] = runEgressCheck
	subcommandFlags["egress-check"] = addEgressFlags
}

// egressConfig holds the egress-check flags.
type egressConfig struct {
	Sink    string
	Probes  []string
	Timeout time.Duration
}

func addEgressFlags(fs *flag.FlagSet, cfg *Config) {
	c := &cfg.Egress
	fs.StringVar(&c.Sink, "egress-sink", "", "egress-check: host:port of the sink; the connect must succeed (required; must be in --target-allow)")
	fs.Var(listFlag{&c.Probes}, "egress-probe", "egress-check: IP:port that must NOT be reachable; repeatable or comma-separated; each must be in --target-allow")
	fs.DurationVar(&c.Timeout, "egress-timeout", 3*time.Second, "egress-check: connect timeout for the sink and each probe")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), egressHelp)
		fs.PrintDefaults()
	}
}

const egressHelp = `usage: loadgen egress-check --target-allow LIST --test-domain D --egress-sink H:P --egress-probe IP:25 [--egress-probe ...] [flags]

Runbook pre-flight, run on the test server machine. It TCP-connects to the
sink (must succeed) and to each probe (must fail or time out), then closes
each connection at once. It sends no SMTP commands and no bytes. Exit 0 when
the sink is reachable and no probe is, 1 when the sink is down or any probe
connects (the server could deliver direct to MX), 2 on refusal.

The operator supplies the probes; the tool has no built-in internet
addresses. Use port 25, which is what direct delivery uses, and give both an
IPv4 and an IPv6 address, because firewalls often filter only one family.
Probes must be IP literals so an unresolvable name cannot count as blocked,
and the sink and every probe must be listed in --target-allow. A probe on a
loopback, private or link-local address proves nothing about internet egress,
so it draws a warning. Only TCP 25 is checked: HTTPS and DNS egress are not.

`

// egressProbe validates one --egress-probe: an IP literal with an explicit
// port.
func egressProbe(cfg Config, s string) (netip.AddrPort, error) {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("probe %q: %w", s, err)
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("probe %q: the host must be an IP literal, not a name", s)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return netip.AddrPort{}, fmt.Errorf("probe %q: port out of range 1-65535", s)
	}
	if err := checkTarget(cfg, s); err != nil {
		return netip.AddrPort{}, err
	}
	return netip.AddrPortFrom(a, uint16(p)), nil
}

func validateEgress(cfg Config) ([]netip.AddrPort, error) {
	c := cfg.Egress
	if c.Sink == "" {
		return nil, errors.New("--egress-sink is required")
	}
	if len(c.Probes) == 0 {
		return nil, errors.New("at least one --egress-probe is required")
	}
	if c.Timeout <= 0 {
		return nil, errors.New("--egress-timeout must be positive")
	}
	if _, port, err := net.SplitHostPort(c.Sink); err != nil || port == "" {
		return nil, fmt.Errorf("--egress-sink %q must be host:port", c.Sink)
	}
	if err := checkTarget(cfg, c.Sink); err != nil {
		return nil, err
	}
	probes := make([]netip.AddrPort, 0, len(c.Probes))
	for _, s := range c.Probes {
		ap, err := egressProbe(cfg, s)
		if err != nil {
			return nil, err
		}
		probes = append(probes, ap)
	}
	return probes, nil
}

// connectClose dials and closes at once, sending nothing.
func connectClose(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func runEgressCheck(cfg Config, stdout, stderr io.Writer) int {
	probes, err := validateEgress(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	c := cfg.Egress
	for _, ap := range probes {
		if ap.Port() != 25 {
			fmt.Fprintf(stderr, "loadgen: warning: probe %s is not on port 25, which is what direct delivery uses\n", ap)
		}
		if a := ap.Addr(); a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() {
			fmt.Fprintf(stderr, "loadgen: warning: probe %s is not an internet address, so it proves nothing about internet egress\n", ap)
		}
	}
	code := exitOK
	if err := connectClose(c.Sink, c.Timeout); err != nil {
		fmt.Fprintf(stdout, "FAIL sink %s: %v\n", c.Sink, err)
		code = exitFail
	} else {
		fmt.Fprintf(stdout, "ok   sink %s: connected\n", c.Sink)
	}
	for _, ap := range probes {
		if err := connectClose(ap.String(), c.Timeout); err != nil {
			fmt.Fprintf(stdout, "ok   probe %s: blocked (%v)\n", ap, err)
			continue
		}
		fmt.Fprintf(stdout, "FAIL probe %s: connected; the server could reach the internet\n", ap)
		code = exitFail
	}
	return code
}
