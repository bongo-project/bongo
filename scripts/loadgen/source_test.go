package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustAddrs(t *testing.T, ss ...string) []netip.Addr {
	t.Helper()
	var out []netip.Addr
	for _, s := range ss {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func cfgWithSources(t *testing.T, allow string, sources ...string) Config {
	t.Helper()
	tg, err := parseTarget(allow)
	if err != nil {
		t.Fatal(err)
	}
	addrs := mustAddrs(t, sources...)
	return Config{Allow: []Target{tg}, Source: sourceAddrs{Addrs: addrs, pool: newSourcePool(addrs)}}
}

func lister(addrs ...string) func() ([]net.Addr, error) {
	return func() ([]net.Addr, error) {
		var out []net.Addr
		for _, a := range addrs {
			out = append(out, &net.IPNet{IP: net.ParseIP(a), Mask: net.CIDRMask(8, 32)})
		}
		return out, nil
	}
}

func TestSourcePoolSpreadsEvenly(t *testing.T) {
	p := newSourcePool(mustAddrs(t, "10.0.0.1", "10.0.0.2", "10.0.0.3"))
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				p.opened[p.next()].Add(1)
			}
		}()
	}
	wg.Wait()
	for _, c := range p.counts() {
		if c.Connections != 100 {
			t.Errorf("%s got %d connections, want 100", c.Addr, c.Connections)
		}
	}
	if newSourcePool(nil).counts() != nil {
		t.Error("a nil pool must report no counts")
	}
}

// TestPoolDialerBindsEachAddressInTurn checks the requested local address
// without a real bind: on macOS a loopback dial reports 127.0.0.1 whether or
// not LocalAddr was set, so the dial tests alone cannot catch its removal.
func TestPoolDialerBindsEachAddressInTurn(t *testing.T) {
	p := newSourcePool(mustAddrs(t, "10.0.0.1", "10.0.0.2", "10.0.0.3"))
	for n := range 6 {
		d, i := p.dialer(time.Second)
		want := p.addrs[n%3].String()
		if i != n%3 || d.LocalAddr == nil || d.LocalAddr.(*net.TCPAddr).IP.String() != want {
			t.Fatalf("dial %d: index %d, local %v, want %s", n, i, d.LocalAddr, want)
		}
	}
	if d, i := (*sourcePool)(nil).dialer(time.Second); i != -1 || d.LocalAddr != nil {
		t.Errorf("a nil pool bound %v", d.LocalAddr)
	}
}

// listen serves loopback and reports the remote address of every accept.
func listen(t *testing.T, network, addr string) (string, chan net.Addr) {
	t.Helper()
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Skipf("cannot listen on %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	seen := make(chan net.Addr, 64)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			seen <- c.RemoteAddr()
			_ = c.Close()
		}
	}()
	return ln.Addr().String(), seen
}

func TestDialFromBindsAndCounts(t *testing.T) {
	for _, tt := range []struct{ name, network, listen, source string }{
		{"ipv4", "tcp4", "127.0.0.1:0", "127.0.0.1"},
		{"ipv6", "tcp6", "[::1]:0", "::1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			addr, seen := listen(t, tt.network, tt.listen)
			pool := newSourcePool(mustAddrs(t, tt.source))
			conn, err := dialFrom(pool, time.Second)(context.Background(), "tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if got := conn.LocalAddr().(*net.TCPAddr).IP.String(); got != tt.source {
				t.Errorf("local address %s, want %s", got, tt.source)
			}
			if got := (<-seen).(*net.TCPAddr).IP.String(); got != tt.source {
				t.Errorf("server saw %s, want %s", got, tt.source)
			}
			if c := pool.counts(); c[0].Connections != 1 {
				t.Errorf("counts %+v", c)
			}
		})
	}
}

func TestDialFromFailedDialIsNotCounted(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	pool := newSourcePool(mustAddrs(t, "127.0.0.1"))
	if _, err := dialFrom(pool, time.Second)(context.Background(), "tcp", addr); err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
	if c := pool.counts(); c[0].Connections != 0 {
		t.Errorf("a failed dial was counted: %+v", c)
	}
}

// TestDialFromSpreadsOverTwoLoopbackAddresses needs 127.0.0.2 assigned, which
// Linux does and macOS normally does not.
func TestDialFromSpreadsOverTwoLoopbackAddresses(t *testing.T) {
	have, _ := net.InterfaceAddrs()
	assigned := false
	for _, a := range have {
		if n, ok := a.(*net.IPNet); ok && n.IP.String() == "127.0.0.2" {
			assigned = true
		}
	}
	if !assigned {
		t.Skip("127.0.0.2 is not assigned to an interface")
	}
	addr, seen := listen(t, "tcp4", "127.0.0.1:0")
	pool := newSourcePool(mustAddrs(t, "127.0.0.1", "127.0.0.2"))
	dial := dialFrom(pool, time.Second)
	for range 10 {
		c, err := dial(context.Background(), "tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Close()
	}
	per := map[string]int{}
	for range 10 {
		per[(<-seen).(*net.TCPAddr).IP.String()]++
	}
	if per["127.0.0.1"] != 5 || per["127.0.0.2"] != 5 {
		t.Errorf("server saw %v", per)
	}
	for _, c := range pool.counts() {
		if c.Connections != 5 {
			t.Errorf("counts %+v", pool.counts())
		}
	}
}

func TestNilPoolDialsLikeDefault(t *testing.T) {
	addr, _ := listen(t, "tcp4", "127.0.0.1:0")
	c, err := dialFrom(nil, time.Second)(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestCheckSourceAddrs(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		list    []string
		wantErr string
	}{
		{"no flag", Config{}, nil, ""},
		{"assigned and matching", cfgWithSources(t, "127.0.0.1", "127.0.0.1"), []string{"127.0.0.1"}, ""},
		{"unassigned", cfgWithSources(t, "127.0.0.1", "192.0.2.1"), []string{"127.0.0.1"}, "192.0.2.1 is not assigned to a local interface"},
		{"family mismatch", cfgWithSources(t, "127.0.0.1", "::1"), []string{"127.0.0.1", "::1"}, "::1 is IPv6 but --target-allow 127.0.0.1 is IPv4"},
		{"name target", func() Config {
			c := cfgWithSources(t, "127.0.0.1", "127.0.0.1")
			c.Allow = []Target{{Host: "mail.bongo.test"}}
			return c
		}(), []string{"127.0.0.1"}, "needs IP-literal --target-allow entries"},
		{"mixed target families", func() Config {
			c := cfgWithSources(t, "127.0.0.1", "127.0.0.1")
			c.Allow = append(c.Allow, Target{Host: "::1", IP: true})
			return c
		}(), []string{"127.0.0.1"}, "one address family"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSourceAddrs(tt.cfg, lister(tt.list...))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected refusal: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestRunRefusesUnassignedSourceBeforeAnyCommand feeds an address that is
// accepted once it is on an interface, so the refusal cannot come from the
// flag parser or the family rule.
func TestRunRefusesUnassignedSourceBeforeAnyCommand(t *testing.T) {
	var ran atomic.Int64
	table := map[string]command{"check": func(Config, io.Writer, io.Writer) int { ran.Add(1); return exitOK }}
	args := []string{"check", "--target-allow", "192.0.2.9", "--test-domain", "bongo.test", "--source-addr", "192.0.2.1"}

	old := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = old })

	interfaceAddrs = func() ([]net.Addr, error) { return lister("192.0.2.1")() }
	if code := runWith(table, args, io.Discard, io.Discard); code != exitOK || ran.Load() != 1 {
		t.Fatalf("assigned address: exit %d, ran %d", code, ran.Load())
	}

	ran.Store(0)
	interfaceAddrs = func() ([]net.Addr, error) { return lister("127.0.0.1")() }
	var stderr bytes.Buffer
	if code := runWith(table, args, io.Discard, &stderr); code != exitRefuse || ran.Load() != 0 {
		t.Fatalf("unassigned address: exit %d, ran %d", code, ran.Load())
	}
	if !strings.Contains(stderr.String(), "not assigned to a local interface") {
		t.Errorf("stderr %q", stderr.String())
	}
}

func TestSourceAddrFlagParsing(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr string
	}{
		{"repeatable and comma-separated", []string{"--source-addr", "10.0.0.1,10.0.0.2", "--source-addr", "10.0.0.3"}, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, ""},
		{"mapped form is unmapped", []string{"--source-addr", "::ffff:10.0.0.1"}, []string{"10.0.0.1"}, ""},
		{"name", []string{"--source-addr", "localhost"}, nil, "not an IP literal"},
		{"zone", []string{"--source-addr", "fe80::1%eth0"}, nil, "zones"},
		{"unspecified v4", []string{"--source-addr", "0.0.0.0"}, nil, "unicast"},
		{"unspecified v6", []string{"--source-addr", "::"}, nil, "unicast"},
		{"multicast", []string{"--source-addr", "224.0.0.1"}, nil, "unicast"},
		{"broadcast", []string{"--source-addr", "255.255.255.255"}, nil, "unicast"},
		{"duplicate", []string{"--source-addr", "10.0.0.1,10.0.0.1"}, nil, "given twice"},
		{"duplicate across flags", []string{"--source-addr", "10.0.0.1", "--source-addr", "::ffff:10.0.0.1"}, nil, "given twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			var out bytes.Buffer
			err := newFlagSet("check", &cfg, &out).Parse(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(out.String()+err.Error(), tt.wantErr) {
					t.Fatalf("err %v, output %q, want %q", err, out.String(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, a := range cfg.Source.Addrs {
				got = append(got, a.String())
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if cfg.Source.pool == nil || len(cfg.Source.pool.addrs) != len(tt.want) {
				t.Errorf("pool does not match the list")
			}
		})
	}
}

func TestSMTPRunThroughSourcePool(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 50, Duration: time.Second})
	sum := runTest(t, cfg, looseOpts())
	data, err := json.Marshal(sum)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("source_addrs")) {
		t.Errorf("a run without --source-addr wrote source_addrs: %s", data)
	}

	cfg = testConfig(t, ts.addr, "ok", smtpConfig{Rate: 50, Duration: time.Second})
	addrs := mustAddrs(t, "127.0.0.1")
	cfg.Source = sourceAddrs{Addrs: addrs, pool: newSourcePool(addrs)}
	sum = runTest(t, cfg, looseOpts())
	if len(sum.SourceAddrs) != 1 || sum.SourceAddrs[0].Addr != "127.0.0.1" || sum.SourceAddrs[0].Connections == 0 {
		t.Fatalf("source_addrs = %+v", sum.SourceAddrs)
	}
}

func TestSenderDialsThroughRunnerDial(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 1, Duration: time.Second})
	var reached atomic.Int64
	r := &runner{cfg: cfg, domain: testDomain, runCtx: context.Background(),
		dial: guardedDial(cfg, func(context.Context, string, string) (net.Conn, error) {
			reached.Add(1)
			return nil, io.ErrClosedPipe
		})}
	if err := (&sender{r: r, addr: ts.addr}).dial(); err == nil || reached.Load() != 1 {
		t.Fatalf("dial err %v, injected dial reached %d times", err, reached.Load())
	}
	// Outside the allow list the injected dialler must not be reached.
	reached.Store(0)
	if err := (&sender{r: r, addr: "10.9.9.9:25"}).dial(); err == nil || reached.Load() != 0 {
		t.Fatalf("dial err %v, reached %d", err, reached.Load())
	}
	if ts.accepts.Load() != 0 {
		t.Fatal("server accepted a connection")
	}
}

func TestReportListsSourcesAsBookkeeping(t *testing.T) {
	m := reportMeta{}
	m.addSources("smtp #1", nil)
	if len(m.SourceAddrs) != 0 {
		t.Fatal("an empty list must add nothing")
	}
	m.addSources("smtp #1", []sourceCount{{"10.0.0.1", 3}, {"10.0.0.2", 4}})
	data, _ := json.Marshal(reportDoc{Meta: m})
	flat, err := flattenMetrics(data)
	if err != nil {
		t.Fatal(err)
	}
	for k := range flat {
		if strings.Contains(k, "source") || strings.Contains(k, "10.0.0") {
			t.Errorf("source bookkeeping leaked into metric %q", k)
		}
	}
}
