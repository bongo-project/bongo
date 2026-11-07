package main

import "time"

// clientsSummary holds counts and timings only. It never holds an address,
// a password, a URL or error text.
type clientsSummary struct {
	RunID          string    `json:"run_id"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	ElapsedSeconds float64   `json:"elapsed_seconds"`
	Interrupted    bool      `json:"interrupted"`

	Params clientsParams        `json:"params"`
	Ops    map[string]opSummary `json:"ops"`
	Idle   idleSummary          `json:"idle"`
	Active activeSummary        `json:"active"`
	DAV    davSummary           `json:"dav"`
}

type clientsParams struct {
	IMAPIdle       int     `json:"imap_idle"`
	IMAPActive     int     `json:"imap_active"`
	DAV            int     `json:"dav"`
	Accounts       int     `json:"accounts"`
	DurationSecs   float64 `json:"duration_seconds"`
	TimeoutSecs    float64 `json:"timeout_seconds"`
	RampSecs       float64 `json:"ramp_seconds"`
	ActiveCycleSec float64 `json:"active_cycle_seconds"`
	FetchCount     int     `json:"fetch_count"`
	BodyShare      float64 `json:"body_share"`
	FlagEvery      int     `json:"flag_every"`
	DAVIntervalSec float64 `json:"dav_interval_seconds"`
	DAVWriteShare  float64 `json:"dav_write_share"`
	IdleRestartSec float64 `json:"idle_restart_seconds"`
	IMAPTLS        string  `json:"imap_tls,omitempty"`
}

// opSummary counts successes and times them; Errors holds a count per class
// and ErrorTotal their sum.
type opSummary struct {
	Count      int64            `json:"count"`
	P50Ms      float64          `json:"p50_ms"`
	P95Ms      float64          `json:"p95_ms"`
	P99Ms      float64          `json:"p99_ms"`
	MaxMs      float64          `json:"max_ms"`
	Errors     map[string]int64 `json:"errors"`
	ErrorTotal int64            `json:"error_total"`
}

type idleSummary struct {
	Open        int64 `json:"open"`
	MaxOpen     int64 `json:"max_open"`
	Reconnects  int64 `json:"reconnects"`
	Restarts    int64 `json:"restarts"`
	StoppedAuth int64 `json:"clients_stopped_after_auth_failures"`
}

type activeSummary struct {
	Cycles            int64 `json:"cycles"`
	Completed         int64 `json:"completed"`
	FetchSkippedEmpty int64 `json:"fetch_skipped_empty"`
	StoppedAuth       int64 `json:"clients_stopped_after_auth_failures"`
}

type davSummary struct {
	Cycles      int64 `json:"cycles"`
	Writes      int64 `json:"writes"`
	StoppedAuth int64 `json:"clients_stopped_after_auth_failures"`
}

// totals sums successes and errors. A completed connection is not counted as
// a success: a server that accepts every connection and refuses every login
// has not served the load.
func (s *clientsSummary) totals() (okN, errN int64) {
	for name, o := range s.Ops {
		if name != opConnect {
			okN += o.Count
		}
		errN += o.ErrorTotal
	}
	return okN, errN
}

func (r *clientsRunner) summary(runID string, start, end time.Time, interrupted bool) *clientsSummary {
	s := r.st
	o := r.cfg
	sum := &clientsSummary{
		RunID: runID, Start: start, End: end, ElapsedSeconds: end.Sub(start).Seconds(),
		Interrupted: interrupted,
		Params: clientsParams{
			IMAPIdle: o.IMAPIdle, IMAPActive: o.IMAPActive, DAV: o.DAV, Accounts: len(r.accts),
			DurationSecs: o.Duration.Seconds(), TimeoutSecs: o.Timeout.Seconds(), RampSecs: o.Ramp.Seconds(),
			ActiveCycleSec: o.ActiveCycle.Seconds(), FetchCount: o.FetchCount, BodyShare: o.BodyShare,
			FlagEvery: o.FlagEvery, DAVIntervalSec: o.DAVInterval.Seconds(), DAVWriteShare: o.DAVWriteShare,
			IdleRestartSec: o.IdleRestart.Seconds(), IMAPTLS: o.IMAPTLS,
		},
		Ops: make(map[string]opSummary, len(s.ops)),
		Idle: idleSummary{
			Open: s.idleOpen.Load(), MaxOpen: s.idleMaxOpen.Load(),
			Reconnects: s.idleReconnects.Load(), Restarts: s.idleRestart.Load(), StoppedAuth: s.idleStoppedAuth.Load(),
		},
		Active: activeSummary{
			Cycles: s.activeCycles.Load(), Completed: s.activeDone.Load(),
			FetchSkippedEmpty: s.fetchSkippedEmpty.Load(), StoppedAuth: s.activeStoppedAuth.Load(),
		},
		DAV: davSummary{Cycles: s.davCycles.Load(), Writes: s.davWrites.Load(), StoppedAuth: s.davStoppedAuth.Load()},
	}
	for name, op := range s.ops {
		sm := opSummary{
			Count: op.h.n.Load(), P50Ms: millis(op.h.quantile(0.50)), P95Ms: millis(op.h.quantile(0.95)),
			P99Ms: millis(op.h.quantile(0.99)), MaxMs: millis(op.h.maximum()),
			Errors: map[string]int64{},
		}
		for c := range op.errs {
			n := op.errs[c].Load()
			sm.Errors[classNames[c]] = n
			sm.ErrorTotal += n
		}
		sum.Ops[name] = sm
	}
	return sum
}
