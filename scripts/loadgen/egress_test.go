package main

import (
	"bytes"
	"net"
	"strings"
	"sync/atomic"
	"testing"
)

// liveListener accepts and counts connections.
func liveListener(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var n atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), &n
}

// closedAddr is a loopback port that was bound and then released.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := ln.Addr().String()
	_ = ln.Close()
	return a
}

func egressArgs(sink string, probes ...string) []string {
	args := []string{"egress-check", "--target-allow", "127.0.0.1", "--test-domain", testDomain, "--egress-timeout", "1s"}
	if sink != "" {
		args = append(args, "--egress-sink", sink)
	}
	for _, p := range probes {
		args = append(args, "--egress-probe", p)
	}
	return args
}

func TestEgressCheck(t *testing.T) {
	sink, _ := liveListener(t)
	reachable, _ := liveListener(t)
	closed := closedAddr(t)
	tests := []struct {
		name string
		args []string
		code int
		out  string
	}{
		{"probe blocked", egressArgs(sink, closed), exitOK, "blocked"},
		{"two probes blocked", egressArgs(sink, closed, closedAddr(t)), exitOK, "blocked"},
		{"probe reachable", egressArgs(sink, reachable), exitFail, "FAIL probe"},
		{"one of two reachable", egressArgs(sink, closed, reachable), exitFail, "FAIL probe"},
		{"sink down", egressArgs(closedAddr(t), closed), exitFail, "FAIL sink"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb); code != tc.code {
				t.Fatalf("exit %d, want %d; out %s err %s", code, tc.code, out.String(), errb.String())
			}
			if !strings.Contains(out.String(), tc.out) {
				t.Errorf("stdout %q lacks %q", out.String(), tc.out)
			}
		})
	}
}

func TestEgressCheckWarnsOnNon25AndLoopback(t *testing.T) {
	sink, _ := liveListener(t)
	var out, errb bytes.Buffer
	if code := run(egressArgs(sink, closedAddr(t)), &out, &errb); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"not on port 25", "not an internet address"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr %q lacks %q", errb.String(), want)
		}
	}
}

func TestEgressCheckRefusals(t *testing.T) {
	sink, sinkHits := liveListener(t)
	probe, probeHits := liveListener(t)
	_, port, _ := net.SplitHostPort(probe)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no sink", egressArgs("", probe), "--egress-sink is required"},
		{"no probe", egressArgs(sink), "--egress-probe is required"},
		{"sink without port", egressArgs("127.0.0.1", probe), "host:port"},
		// A probe outside --target-allow is refused before any dial.
		{"probe not allowed", append(egressArgs(sink, "127.0.0.2:"+port), "--target-allow", "127.0.0.1:1"), "not in --target-allow"},
		{"probe is a name", egressArgs(sink, "localhost:"+port), "IP literal"},
		{"probe without port", egressArgs(sink, "127.0.0.1"), "missing port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb); code != exitRefuse {
				t.Fatalf("exit %d, want %d; err %s", code, exitRefuse, errb.String())
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Errorf("stderr %q lacks %q", errb.String(), tc.want)
			}
		})
	}
	if sinkHits.Load() != 0 || probeHits.Load() != 0 {
		t.Errorf("a refused run dialled: sink %d probe %d", sinkHits.Load(), probeHits.Load())
	}
}

func TestEgressCheckSendsNothing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan int, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 64)
			n, _ := c.Read(buf)
			got <- n
			_ = c.Close()
		}
	}()
	var out, errb bytes.Buffer
	run(egressArgs(ln.Addr().String(), ln.Addr().String()), &out, &errb)
	for range 2 {
		if n := <-got; n != 0 {
			t.Errorf("a connection sent %d bytes", n)
		}
	}
}
