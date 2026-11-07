package main

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/emersion/go-smtp"
)

// outcome is the one counter a failed send lands in.
type outcome int

const (
	outcomeOK outcome = iota
	outcomeRejected
	outcomeDeferred
	outcomeTimedOut
	outcomeConn
	outcomeOther
)

// counters hold one tally per outcome. Every due message ends in exactly one
// of them, so due == ok + rejected + deferred + timedOut + conn + other +
// unsent once the workers have stopped.
type counters struct {
	ok, rejected5xx, deferred4xx, timedOut, connErrors, otherErrors atomic.Int64
	// unsent counts messages due but never attempted when the run ended.
	unsent atomic.Int64
}

func (c *counters) add(o outcome) {
	switch o {
	case outcomeOK:
		c.ok.Add(1)
	case outcomeRejected:
		c.rejected5xx.Add(1)
	case outcomeDeferred:
		c.deferred4xx.Add(1)
	case outcomeTimedOut:
		c.timedOut.Add(1)
	case outcomeConn:
		c.connErrors.Add(1)
	default:
		c.otherErrors.Add(1)
	}
}

func (c *counters) total() int64 {
	return c.ok.Load() + c.rejected5xx.Load() + c.deferred4xx.Load() + c.timedOut.Load() +
		c.connErrors.Load() + c.otherErrors.Load() + c.unsent.Load()
}

// dialError marks a failure while connecting, greeting or sending EHLO.
type dialError struct{ err error }

func (e *dialError) Error() string { return "connect: " + e.err.Error() }
func (e *dialError) Unwrap() error { return e.err }

// classify puts an error in one outcome. The rules, in order:
//
//   - a dialError is a connection error whatever its cause, so a dial timeout
//     or a 554 greeting is a conn error and not a timeout or a rejection;
//   - an SMTP reply of 4xx is deferred and of 5xx is rejected;
//   - a network timeout during a transaction is timedOut;
//   - a closed or reset connection is a conn error;
//   - anything else is other.
func classify(err error) outcome {
	var de *dialError
	if errors.As(err, &de) {
		return outcomeConn
	}
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		switch se.Code / 100 {
		case 4:
			return outcomeDeferred
		case 5:
			return outcomeRejected
		}
		return outcomeOther
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return outcomeTimedOut
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, net.ErrClosed) {
		return outcomeConn
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		return outcomeConn
	}
	return outcomeOther
}

// hist is a lock-free log2 histogram of durations in microseconds, cheap
// enough to update on every scheduler wakeup and safe to read while it runs.
type hist struct {
	b   [48]atomic.Int64
	n   atomic.Int64
	max atomic.Int64 // nanoseconds
}

func (h *hist) add(d time.Duration) {
	if d < 0 {
		d = 0
	}
	idx := 0
	for us := d.Microseconds(); us > 0 && idx < len(h.b)-1; us >>= 1 {
		idx++
	}
	h.b[idx].Add(1)
	h.n.Add(1)
	for {
		m := h.max.Load()
		if int64(d) <= m || h.max.CompareAndSwap(m, int64(d)) {
			return
		}
	}
}

// quantile returns the upper bound of the bucket holding the q-th sample.
func (h *hist) quantile(q float64) time.Duration {
	n := h.n.Load()
	if n == 0 {
		return 0
	}
	target := int64(float64(n)*q + 0.999999)
	var seen int64
	for i := range h.b {
		seen += h.b[i].Load()
		if seen >= target {
			return min(time.Duration(1<<i)*time.Microsecond, time.Duration(h.max.Load()))
		}
	}
	return time.Duration(h.max.Load())
}

func (h *hist) maximum() time.Duration { return time.Duration(h.max.Load()) }
