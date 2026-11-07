package main

import (
	"math"
	"math/bits"
	"sort"
	"sync/atomic"
	"time"
)

// The latency histogram is log-linear: exact below latSubCount microseconds,
// then latSubCount buckets per power of two, so a quantile is off by at most
// 1/latSubCount (about 6%). The log2 hist in smtp_stats.go can be 2x off,
// which is too coarse for a p99 that a report compares between two runs.
const (
	latSubShift = 4
	latSubCount = 1 << latSubShift
	// latMaxExp bounds the top bucket at about 2^40 microseconds (12 days).
	latMaxExp  = 36
	latBuckets = latSubCount + latMaxExp*latSubCount
)

// latIndex maps a duration in microseconds to its bucket.
func latIndex(us int64) int {
	if us < latSubCount {
		return int(max(us, 0))
	}
	e := bits.Len64(uint64(us)) - latSubShift - 1
	if e >= latMaxExp {
		return latBuckets - 1
	}
	return latSubCount + e*latSubCount + int(us>>e) - latSubCount
}

// latUpper is the largest microsecond value in a bucket.
func latUpper(idx int) int64 {
	if idx < latSubCount {
		return int64(idx)
	}
	e := (idx - latSubCount) / latSubCount
	sub := (idx - latSubCount) % latSubCount
	return int64(latSubCount+sub+1)<<e - 1
}

// latHist is the live form: lock-free, so the periodic flush can read it
// while workers write.
type latHist struct {
	b   [latBuckets]atomic.Int64
	max atomic.Int64 // microseconds
}

func (h *latHist) add(d time.Duration) {
	us := d.Microseconds()
	if us < 0 {
		us = 0
	}
	h.b[latIndex(us)].Add(1)
	storeMax(&h.max, us)
}

// snapshot copies the histogram into its serialisable form. Count is summed
// from the buckets read, so the two always agree even during writes.
func (h *latHist) snapshot() latencyHist {
	s := latencyHist{Buckets: map[int]int64{}, MaxUs: h.max.Load()}
	for i := range h.b {
		if v := h.b[i].Load(); v > 0 {
			s.Buckets[i] = v
			s.Count += v
		}
	}
	return s
}

// latencyHist is the form written to a summary and merged by the report:
// sparse bucket counts, so two runs of the same generator add up exactly.
type latencyHist struct {
	Count   int64         `json:"count"`
	MaxUs   int64         `json:"max_us"`
	Buckets map[int]int64 `json:"buckets,omitempty"`
}

// merge adds o into h.
func (h *latencyHist) merge(o latencyHist) {
	if h.Buckets == nil {
		h.Buckets = map[int]int64{}
	}
	for i, v := range o.Buckets {
		h.Buckets[i] += v
	}
	h.Count += o.Count
	h.MaxUs = max(h.MaxUs, o.MaxUs)
}

// quantile returns the q-th quantile in milliseconds by nearest rank, as the
// upper edge of the bucket holding that rank, never above the recorded
// maximum. ok is false for an empty histogram.
func (h latencyHist) quantile(q float64) (ms float64, ok bool) {
	if h.Count <= 0 {
		return 0, false
	}
	idx := make([]int, 0, len(h.Buckets))
	for i := range h.Buckets {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	rank := rankOf(h.Count, q)
	var seen int64
	for _, i := range idx {
		seen += h.Buckets[i]
		if seen >= rank {
			return float64(min(latUpper(i), h.MaxUs)) / 1000, true
		}
	}
	return float64(h.MaxUs) / 1000, true
}

// rankOf is the nearest-rank position, 1-based, of the q-th quantile among n
// ordered samples. Every percentile in a report goes through it. The small
// subtraction keeps 0.95*100 from rounding up to 96.
func rankOf(n int64, q float64) int64 {
	return min(max(int64(math.Ceil(q*float64(n)-1e-9)), 1), n)
}

// percentile returns the q-th quantile of v by nearest rank; v need not be
// sorted. ok is false for an empty slice.
func percentile(v []float64, q float64) (float64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[rankOf(int64(len(s)), q)-1], true
}
