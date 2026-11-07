package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync/atomic"
	"time"
)

// sourceAddrs is the --source-addr list and the pool built from it. Config is
// copied by value, so the pool is a pointer: every copy counts into the same
// connection tallies.
type sourceAddrs struct {
	Addrs []netip.Addr
	pool  *sourcePool
}

// sourceCount is one source address and the connections opened from it. The
// summaries hold a slice of these, never a map keyed by address: a dotted
// key would be split into path segments when report-compare flattens a report.
type sourceCount struct {
	Addr        string `json:"addr"`
	Connections int64  `json:"connections"`
}

// sourcePool hands out local source addresses round-robin and counts the
// connections that were actually opened from each.
type sourcePool struct {
	addrs  []netip.Addr
	cursor atomic.Int64
	opened []atomic.Int64
}

func newSourcePool(addrs []netip.Addr) *sourcePool {
	if len(addrs) == 0 {
		return nil
	}
	return &sourcePool{addrs: addrs, opened: make([]atomic.Int64, len(addrs))}
}

// next picks the index of the address for the next connection.
func (p *sourcePool) next() int {
	return int((p.cursor.Add(1) - 1) % int64(len(p.addrs)))
}

// counts is a snapshot; nil without --source-addr, which keeps the JSON of a
// run without the flag unchanged.
func (p *sourcePool) counts() []sourceCount {
	if p == nil {
		return nil
	}
	out := make([]sourceCount, len(p.addrs))
	for i, a := range p.addrs {
		out[i] = sourceCount{Addr: a.String(), Connections: p.opened[i].Load()}
	}
	return out
}

// sourceCounts returns the run's per-address tallies for a summary.
func (c Config) sourceCounts() []sourceCount { return c.Source.pool.counts() }

// dialer returns a dialler bound to the next source address and that
// address's index, or -1 and an unbound dialler for a nil pool.
func (p *sourcePool) dialer(timeout time.Duration) (net.Dialer, int) {
	d := net.Dialer{Timeout: timeout}
	if p == nil {
		return d, -1
	}
	i := p.next()
	d.LocalAddr = &net.TCPAddr{IP: p.addrs[i].AsSlice()}
	// Without this the kernel gives each source address one ephemeral-port
	// pool shared by every destination port.
	d.Control = bindWithoutPort
	return d, i
}

// dialFrom builds a dialler that binds each connection to the next source
// address. A nil pool dials from whatever address the kernel picks. Callers
// wrap the result in guardedDial so checkTarget runs before any bind.
func dialFrom(pool *sourcePool, timeout time.Duration) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		d, i := pool.dialer(timeout)
		conn, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if i >= 0 {
			pool.opened[i].Add(1)
		}
		return conn, nil
	}
}

// sourceAddrFlag parses --source-addr: IP literals only, repeatable or
// comma-separated.
type sourceAddrFlag struct{ cfg *Config }

func (f sourceAddrFlag) String() string { return "" }

func (f sourceAddrFlag) Set(s string) error {
	var items []string
	if err := (listFlag{&items}).Set(s); err != nil {
		return err
	}
	list := f.cfg.Source.Addrs
	for _, it := range items {
		a, err := parseSourceAddr(it)
		if err != nil {
			return fmt.Errorf("source-addr %q: %w", it, err)
		}
		for _, have := range list {
			if have == a {
				// A repeat would skew both the spread and the counts.
				return fmt.Errorf("source-addr %q: given twice", it)
			}
		}
		list = append(list, a)
	}
	f.cfg.Source = sourceAddrs{Addrs: list, pool: newSourcePool(list)}
	return nil
}

func parseSourceAddr(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, errors.New("not an IP literal (names are not resolved)")
	}
	if a.Zone() != "" {
		return netip.Addr{}, errors.New("zones are not supported")
	}
	a = a.Unmap()
	if a.IsUnspecified() || a.IsMulticast() || a == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return netip.Addr{}, errors.New("not a unicast address")
	}
	return a, nil
}

// interfaceAddrs lists the local interface addresses; tests replace it.
var interfaceAddrs = net.InterfaceAddrs

// checkSourceAddrs runs after Validate and does the I/O Validate may not: it
// reads the interface list. It never resolves a name, so with --source-addr
// every --target-allow entry must be an IP literal of one family, which then
// covers every target a dial can reach.
func checkSourceAddrs(cfg Config, list func() ([]net.Addr, error)) error {
	if len(cfg.Source.Addrs) == 0 {
		return nil
	}
	var family netip.Addr
	for _, t := range cfg.Allow {
		a, err := netip.ParseAddr(t.Host)
		if err != nil {
			return errors.New("--source-addr needs IP-literal --target-allow entries")
		}
		if family.IsValid() && family.Is4() != a.Is4() {
			return errors.New("--source-addr needs --target-allow entries of one address family")
		}
		family = a
	}
	have, err := list()
	if err != nil {
		return fmt.Errorf("list local interface addresses: %w", err)
	}
	local := map[netip.Addr]bool{}
	for _, a := range have {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if x, ok := netip.AddrFromSlice(ip); ok {
			local[x.Unmap()] = true
		}
	}
	for _, s := range cfg.Source.Addrs {
		if !local[s] {
			return fmt.Errorf("--source-addr %s is not assigned to a local interface", s)
		}
		if family.IsValid() && s.Is4() != family.Is4() {
			return fmt.Errorf("--source-addr %s is %s but --target-allow %s is %s", s, famName(s), family, famName(family))
		}
	}
	return nil
}

func famName(a netip.Addr) string {
	if a.Is4() {
		return "IPv4"
	}
	return "IPv6"
}

// sourceAddrUsage is the --source-addr help. It only describes the Linux
// limits; nothing here runs a command.
const sourceAddrUsage = `local address to connect from (repeatable or comma-separated IP literals; each must be assigned to a local interface and of the --target-allow family).
Connections are spread round-robin and counted per address in each summary.
Linux limits a connection test hits: net.ipv4.ip_local_port_range (default 32768-60999) leaves about 28000 ports per source address and destination;
raise it, or add source addresses, to hold more connections. The process also needs file descriptors above the connection count:
ulimit -n, systemd LimitNOFILE, and the system caps fs.nr_open and fs.file-max. Reconnect churn leaves sockets in TIME_WAIT;
net.ipv4.tcp_tw_reuse=1 lets outgoing connections reuse them. Once --source-addr is set the tool asks the kernel to pick the port at connect
time (IP_BIND_ADDRESS_NO_PORT), so the port limit applies per destination, not across all of them (Linux only; without it each source
address shares about 28000 ports across every destination port, so use at least two addresses).
To add an address to an interface: ip addr add 10.0.0.11/24 dev eth0 (as root). A VPS usually has one public IPv4 address, so extra ones
come from the provider's additional-IP or private-network feature. Nothing here runs those commands.`
