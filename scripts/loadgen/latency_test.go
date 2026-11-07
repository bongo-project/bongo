package main

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	hundred := make([]float64, 100)
	for i := range hundred {
		hundred[i] = float64(i + 1)
	}
	tests := []struct {
		name string
		in   []float64
		q    float64
		want float64
		ok   bool
	}{
		{"empty", nil, 0.5, 0, false},
		{"one value p50", []float64{7}, 0.5, 7, true},
		{"one value p99", []float64{7}, 0.99, 7, true},
		{"100 values p50", hundred, 0.50, 50, true},
		{"100 values p95", hundred, 0.95, 95, true},
		{"100 values p99", hundred, 0.99, 99, true},
		{"100 values p100", hundred, 1, 100, true},
		{"duplicates", []float64{5, 5, 5, 5, 9}, 0.80, 5, true},
		{"duplicates p99", []float64{5, 5, 5, 5, 9}, 0.99, 9, true},
		{"unsorted", []float64{9, 1, 5, 3, 7}, 0.5, 5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := percentile(tt.in, tt.q)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("percentile = %v, %v; want %v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestLatIndexBounds(t *testing.T) {
	prev := -1
	for us := int64(0); us < 5_000_000; us += 1 + us/40 {
		i := latIndex(us)
		if i < prev {
			t.Fatalf("index fell from %d to %d at %dus", prev, i, us)
		}
		prev = i
		if up := latUpper(i); us > up || (us >= latSubCount && float64(up-us) > float64(us)/latSubCount) {
			t.Fatalf("%dus lands in a bucket ending at %dus: more than 1/%d wide", us, up, latSubCount)
		}
	}
	if got := latIndex(math.MaxInt64); got != latBuckets-1 {
		t.Fatalf("huge value lands in %d, want the top bucket %d", got, latBuckets-1)
	}
}

func TestLatencyQuantileAccuracy(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var h latHist
	var exact []float64
	for range 20000 {
		d := time.Duration(rng.Int64N(int64(900*time.Millisecond))) + time.Millisecond
		h.add(d)
		exact = append(exact, millis(d))
	}
	snap := h.snapshot()
	for _, q := range []float64{0.5, 0.95, 0.99} {
		want, _ := percentile(exact, q)
		got, ok := snap.quantile(q)
		if !ok || got < want || got > want*(1+1.0/latSubCount)+0.002 {
			t.Errorf("p%.0f = %.3fms, exact %.3fms: outside the bucket error", q*100, got, want)
		}
	}
}

// TestLatencyMergeEqualsSingle: summaries from two generators add up to the
// histogram of one generator that saw all the values.
func TestLatencyMergeEqualsSingle(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	var a, b, all latHist
	for i := range 5000 {
		d := time.Duration(rng.Int64N(int64(2 * time.Second)))
		all.add(d)
		if i%3 == 0 {
			a.add(d)
		} else {
			b.add(d)
		}
	}
	var merged latencyHist
	merged.merge(a.snapshot())
	merged.merge(b.snapshot())
	want := all.snapshot()
	if merged.Count != want.Count || merged.MaxUs != want.MaxUs || len(merged.Buckets) != len(want.Buckets) {
		t.Fatalf("merged %d/%d/%d, single %d/%d/%d", merged.Count, merged.MaxUs, len(merged.Buckets), want.Count, want.MaxUs, len(want.Buckets))
	}
	for i, v := range want.Buckets {
		if merged.Buckets[i] != v {
			t.Fatalf("bucket %d: merged %d, single %d", i, merged.Buckets[i], v)
		}
	}
	for _, q := range []float64{0.5, 0.95, 0.99} {
		m, _ := merged.quantile(q)
		s, _ := want.quantile(q)
		if m != s {
			t.Errorf("p%.0f merged %v, single %v", q*100, m, s)
		}
	}
	if _, ok := (latencyHist{}).quantile(0.5); ok {
		t.Error("an empty histogram reported a quantile")
	}
}

// TestSMTPSummaryCarriesLatency: a run records one sample per accepted
// message, and a partial flush carries the field too.
func TestSMTPSummaryCarriesLatency(t *testing.T) {
	ts := newTestServer(t)
	cfg := testConfig(t, ts.addr, "lag", smtpConfig{Rate: 50, Duration: time.Second})
	sum := runTest(t, cfg, looseOpts())
	if sum.Counts.OK == 0 {
		t.Fatal("nothing was accepted")
	}
	if sum.Latency.Count != sum.Counts.OK {
		t.Errorf("latency samples %d, accepted %d", sum.Latency.Count, sum.Counts.OK)
	}
	// The "lag" recipient stalls DATA for 100ms.
	if p50, ok := sum.Latency.quantile(0.5); !ok || p50 < 100 {
		t.Errorf("p50 %.1fms ignores the server's 100ms stall", p50)
	}
	saved := readSummary(t, cfg.SMTP.Summary)
	if saved.Latency.Count != sum.Latency.Count || !slices.Equal(mapKeys(saved.Latency.Buckets), mapKeys(sum.Latency.Buckets)) {
		t.Error("the written summary lost the histogram")
	}
}

func mapKeys(m map[int]int64) []int {
	k := make([]int, 0, len(m))
	for i := range m {
		k = append(k, i)
	}
	slices.Sort(k)
	return k
}
