package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// The summary holds counts and timings only. It never holds message content,
// addresses or credentials.
type smtpSummary struct {
	RunID string    `json:"run_id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Partial is true for the periodic flush written while the run is still
	// going, so a SIGKILL leaves counts behind.
	Partial     bool          `json:"partial"`
	Mode        string        `json:"mode"`
	Params      summaryParams `json:"params"`
	Interrupted bool          `json:"interrupted"`

	Due int64 `json:"due"`
	// SourceAddrs is bookkeeping for --source-addr, not a measurement.
	SourceAddrs []sourceCount `json:"source_addrs,omitempty"`
	Counts      summaryCounts `json:"counts"`
	Reconciled  bool          `json:"reconciled"`
	Started     int64         `json:"started"`

	ElapsedSeconds float64      `json:"elapsed_seconds"`
	AchievedRate   float64      `json:"achieved_rate"`
	Steps          []stepResult `json:"steps"`
	RateHeld       bool         `json:"rate_held"`

	// Valid is false when the generator saturated: the numbers then describe
	// the generator, not the server.
	Valid                bool     `json:"valid"`
	GeneratorSaturated   bool     `json:"generator_saturated"`
	SaturationReasons    []string `json:"saturation_reasons"`
	PoolExhausted        bool     `json:"pool_exhausted"`
	PoolExhaustedSeconds float64  `json:"pool_exhausted_seconds"`

	// Latency times each accepted message from MAIL to the reply to the end
	// of DATA on an open connection. Dialling and EHLO are left out: a
	// worker keeps its connection, so they are not part of a transaction.
	Latency latencyHist `json:"latency"`

	CPU          cpuReport `json:"cpu"`
	MaxBacklog   int64     `json:"max_backlog"`
	MaxInFlight  int64     `json:"max_in_flight"`
	SchedulerLag lagReport `json:"scheduler_lag"`
	QueueWait    lagReport `json:"queue_wait"`
}

type summaryParams struct {
	Rate            float64 `json:"rate,omitempty"`
	Ramp            string  `json:"ramp,omitempty"`
	DurationSeconds float64 `json:"duration_seconds"`
	Workers         int     `json:"workers"`
	TimeoutSeconds  float64 `json:"timeout_seconds"`
	SizeBytes       int     `json:"size_bytes"`
	Targets         int     `json:"targets"`
}

type summaryCounts struct {
	OK          int64 `json:"ok"`
	Rejected5xx int64 `json:"rejected_5xx"`
	Deferred4xx int64 `json:"deferred_4xx"`
	TimedOut    int64 `json:"timed_out"`
	ConnErrors  int64 `json:"conn_errors"`
	OtherErrors int64 `json:"other_errors"`
	Unsent      int64 `json:"unsent"`
}

type stepResult struct {
	TargetRate   float64 `json:"target_rate"`
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	Expected     int64   `json:"expected"`
	Started      int64   `json:"started"`
	AchievedRate float64 `json:"achieved_rate"`
	// Full is false for a window the run ended inside; it is not judged.
	Full bool `json:"full"`
	Held bool `json:"held"`
}

type cpuReport struct {
	// Available is false where the platform cannot measure process CPU.
	Available bool    `json:"available"`
	AvgCores  float64 `json:"avg_cores"`
	AvgShare  float64 `json:"avg_share"`
	MaxShare  float64 `json:"max_share"`
	Samples   int     `json:"samples"`
}

type lagReport struct {
	P99Millis float64 `json:"p99_ms"`
	MaxMillis float64 `json:"max_ms"`
}

func millis(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// writeSummaryFile writes v (any JSON-encodable summary) to path atomically.
func writeSummaryFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode summary: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}
