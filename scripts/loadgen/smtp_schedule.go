package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// maxRate caps --rate and --ramp max. A single generator process cannot
// honestly hold more, and a typo of a few zeros should be refused rather than
// run.
const maxRate = 100000

// maxRampSteps bounds the number of segments a ramp may expand to.
const maxRampSteps = 10000

// rampSpec is the parsed --ramp start:step:interval:max.
type rampSpec struct {
	Start, Step float64
	Interval    time.Duration
	Max         float64
}

// parseRamp parses start:step:interval:max, for example 50:50:30s:500.
func parseRamp(s string) (rampSpec, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 4 {
		return rampSpec{}, errors.New("--ramp wants start:step:interval:max, for example 50:50:30s:500")
	}
	var r rampSpec
	var err error
	if r.Start, err = parseRate(parts[0]); err != nil {
		return rampSpec{}, fmt.Errorf("--ramp start: %w", err)
	}
	if r.Step, err = strconv.ParseFloat(parts[1], 64); err != nil || !(r.Step > 0) || math.IsInf(r.Step, 0) {
		return rampSpec{}, fmt.Errorf("--ramp step %q must be a number above 0", parts[1])
	}
	if r.Interval, err = time.ParseDuration(parts[2]); err != nil {
		return rampSpec{}, fmt.Errorf("--ramp interval: %w", err)
	}
	if r.Interval < time.Second {
		return rampSpec{}, fmt.Errorf("--ramp interval %s is below 1s", r.Interval)
	}
	if r.Max, err = parseRate(parts[3]); err != nil {
		return rampSpec{}, fmt.Errorf("--ramp max: %w", err)
	}
	if r.Start > r.Max {
		return rampSpec{}, fmt.Errorf("--ramp start %g is above max %g", r.Start, r.Max)
	}
	if (r.Max-r.Start)/r.Step > maxRampSteps {
		return rampSpec{}, fmt.Errorf("--ramp would take more than %d steps", maxRampSteps)
	}
	return r, nil
}

func parseRate(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	return v, checkRate(v)
}

func checkRate(v float64) error {
	if math.IsNaN(v) || !(v > 0) {
		return fmt.Errorf("rate %g must be above 0", v)
	}
	if v > maxRate {
		return fmt.Errorf("rate %g is above the cap of %d per second", v, maxRate)
	}
	return nil
}

// segment is one stretch of constant rate. cum is the number of messages due
// when the segment starts.
type segment struct {
	start time.Duration
	rate  float64
	cum   float64
	// length is zero for the last segment, which holds until the run ends.
	length time.Duration
}

// schedule is a piecewise-constant rate. Every query is a pure function of
// elapsed time since the run started, so the scheduler never accumulates
// error: a late wakeup finds more messages due and catches up.
type schedule struct {
	segs []segment
}

func constSchedule(rate float64) *schedule {
	return &schedule{segs: []segment{{rate: rate}}}
}

// rampSchedule steps from Start by Step every Interval. Once the rate reaches
// Max it holds Max until the run ends.
func rampSchedule(r rampSpec) *schedule {
	s := &schedule{}
	var start time.Duration
	cum := 0.0
	for k := 0; ; k++ {
		rate := math.Min(r.Start+float64(k)*r.Step, r.Max)
		seg := segment{start: start, rate: rate, cum: cum}
		if rate >= r.Max {
			s.segs = append(s.segs, seg)
			return s
		}
		seg.length = r.Interval
		s.segs = append(s.segs, seg)
		start += r.Interval
		cum += rate * r.Interval.Seconds()
	}
}

// segAt returns the index of the segment holding elapsed.
func (s *schedule) segAt(elapsed time.Duration) int {
	i := 0
	for i+1 < len(s.segs) && elapsed >= s.segs[i+1].start {
		i++
	}
	return i
}

func (s *schedule) rateAt(elapsed time.Duration) float64 {
	return s.segs[s.segAt(elapsed)].rate
}

// dueCount is how many messages are due after elapsed.
func (s *schedule) dueCount(elapsed time.Duration) int64 {
	if elapsed <= 0 {
		return 0
	}
	g := s.segs[s.segAt(elapsed)]
	// The epsilon keeps 100/s over exactly 1s from reading 99 after float
	// rounding.
	return int64(math.Floor(g.cum + g.rate*(elapsed-g.start).Seconds() + 1e-9))
}

// dueTime is when message n (1-based) becomes due.
func (s *schedule) dueTime(n int64) time.Duration {
	if n <= 0 {
		return 0
	}
	f := float64(n)
	i := 0
	for i+1 < len(s.segs) && f > s.segs[i+1].cum {
		i++
	}
	g := s.segs[i]
	return g.start + time.Duration((f-g.cum)/g.rate*float64(time.Second))
}

// window is the span of one rate step. end is zero for the last step.
type window struct {
	start, end time.Duration
	rate       float64
}

func (s *schedule) windows() []window {
	w := make([]window, len(s.segs))
	for i, g := range s.segs {
		w[i] = window{start: g.start, rate: g.rate}
		if g.length > 0 {
			w[i].end = g.start + g.length
		}
	}
	return w
}
