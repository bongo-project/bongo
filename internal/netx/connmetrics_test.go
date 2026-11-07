package netx

import (
	"net"
	"sync/atomic"
	"testing"
)

type fakeGauge struct{ v atomic.Int64 }

func (g *fakeGauge) Inc() { g.v.Add(1) }
func (g *fakeGauge) Dec() { g.v.Add(-1) }

type fakeCounter struct{ v atomic.Int64 }

func (c *fakeCounter) Inc() { c.v.Add(1) }

func dialAccept(t *testing.T, ln net.Listener) net.Conn {
	t.Helper()
	cl, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCountConns(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var open fakeGauge
	var accepted fakeCounter
	ln := CountConns(raw, &open, &accepted)

	a, b := dialAccept(t, ln), dialAccept(t, ln)
	if open.v.Load() != 2 || accepted.v.Load() != 2 {
		t.Fatalf("open=%d accepted=%d, want 2 2", open.v.Load(), accepted.v.Load())
	}
	// A second Close must not decrement again.
	_ = a.Close()
	_ = a.Close()
	if open.v.Load() != 1 {
		t.Fatalf("open=%d after closing one conn twice, want 1", open.v.Load())
	}
	_ = b.Close()
	if open.v.Load() != 0 || accepted.v.Load() != 2 {
		t.Fatalf("open=%d accepted=%d, want 0 2", open.v.Load(), accepted.v.Load())
	}
}

// TestLimitPerIPCountsOnlyAdmitted checks a connection refused by the per-IP
// cap is neither accepted nor left open in the metrics.
func TestLimitPerIPCountsOnlyAdmitted(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var open fakeGauge
	var accepted fakeCounter
	ln := LimitPerIP(raw, NewPerIPLimiter(1), WithConnMetrics(&open, &accepted))

	first := dialAccept(t, ln)
	if _, err := first.Write([]byte("x")); err != nil {
		t.Fatalf("first conn refused: %v", err)
	}
	second := dialAccept(t, ln)
	if _, err := second.Write([]byte("x")); err == nil {
		t.Fatal("second conn was admitted over the cap")
	}
	_ = second.Close()
	if open.v.Load() != 1 || accepted.v.Load() != 1 {
		t.Fatalf("open=%d accepted=%d, want 1 1 (refused conn must not count)", open.v.Load(), accepted.v.Load())
	}
	_ = first.Close()
	_ = first.Close()
	if open.v.Load() != 0 {
		t.Fatalf("open=%d after close, want 0", open.v.Load())
	}
}
