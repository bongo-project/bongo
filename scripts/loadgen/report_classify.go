package main

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// metricKind says how report-compare treats a flattened report key.
type metricKind int

const (
	// kindMeasured is a result: gated by the threshold. An unclassified key is
	// measured too, so a field added later fails safe instead of escaping.
	kindMeasured metricKind = iota
	// kindShown is printed with the measured results and never gated.
	kindShown
	// kindError is an error count, judged as a rate against its attempts.
	kindError
	// kindBookkeeping describes the run, not the server, and is not compared.
	kindBookkeeping
)

// metricClasses classifies every numeric key a report can hold. A "*" matches
// exactly one path segment. The completeness test flattens a fully populated
// report and fails on a key no entry matches, so a new field must be placed
// here when it is added.
var metricClasses = []struct {
	pattern string
	kind    metricKind
}{
	{"throughput.accepted_per_second", kindMeasured},
	{"throughput.accepted_messages", kindBookkeeping},
	{"throughput.elapsed_seconds", kindBookkeeping},
	{"throughput.sink_messages", kindBookkeeping},

	{"smtp_latency.p50_ms", kindMeasured},
	{"smtp_latency.p95_ms", kindMeasured},
	{"smtp_latency.p99_ms", kindMeasured},
	{"smtp_latency.max_ms", kindShown},
	{"smtp_latency.count", kindBookkeeping},

	{"errors.*", kindError},

	{"delivery_delay.p50_ms", kindMeasured},
	{"delivery_delay.p95_ms", kindMeasured},
	{"delivery_delay.p99_ms", kindMeasured},
	{"delivery_delay.max_ms", kindShown},
	{"delivery_delay.lost", kindError},
	{"delivery_delay.count", kindBookkeeping},
	{"delivery_delay.sent", kindBookkeeping},
	{"delivery_delay.seen", kindBookkeeping},
	{"delivery_delay.seen_by_noop", kindBookkeeping},
	{"delivery_delay.visible_before_accept", kindBookkeeping},
	{"delivery_delay.unresolved", kindBookkeeping},

	{"search_lag.p50_ms", kindMeasured},
	{"search_lag.p95_ms", kindMeasured},
	{"search_lag.p99_ms", kindMeasured},
	{"search_lag.max_ms", kindShown},
	{"search_lag.not_found", kindError},
	{"search_lag.count", kindBookkeeping},
	{"search_lag.found", kindBookkeeping},
	{"search_lag.resolution_ms", kindBookkeeping},

	{"clients.ops.*.p50_ms", kindMeasured},
	{"clients.ops.*.p95_ms", kindMeasured},
	{"clients.ops.*.p99_ms", kindMeasured},
	{"clients.ops.*.max_ms", kindShown},
	{"clients.ops.*.errors", kindError},
	{"clients.ops.*.count", kindBookkeeping},

	{"server.cpu_cores_avg", kindMeasured},
	{"server.cpu_cores_max", kindMeasured},
	{"server.resident_memory_bytes.avg", kindMeasured},
	{"server.resident_memory_bytes.max", kindMeasured},
	{"server.open_fds.avg", kindMeasured},
	{"server.open_fds.max", kindMeasured},
	{"server.blob_bytes_growth", kindMeasured},
	{"server.blobs_growth", kindMeasured},
	{"server.wal_fsync_p50_upper_ms", kindMeasured},
	{"server.wal_fsync_p95_upper_ms", kindMeasured},
	{"server.wal_fsync_p99_upper_ms", kindMeasured},
	{"server.wal_fsync_interval_p99_max_upper_ms", kindMeasured},
	{"server.wal_fsync_p50_over_ms", kindMeasured},
	{"server.wal_fsync_p95_over_ms", kindMeasured},
	{"server.wal_fsync_p99_over_ms", kindMeasured},
	{"server.wal_fsync_interval_p99_max_over_ms", kindMeasured},
	{"server.samples", kindBookkeeping},
	{"server.failed_scrapes", kindBookkeeping},
	{"server.max_gap_seconds", kindBookkeeping},
	{"server.restarts_seen", kindBookkeeping},
	{"server.wal_fsync_count", kindBookkeeping},
	{"server.samples_file.*", kindBookkeeping},
	{"server.raw_scrapes_file.*", kindBookkeeping},
}

func patternMatches(pattern, key string) bool {
	ps, ks := strings.Split(pattern, "."), strings.Split(key, ".")
	if len(ps) != len(ks) {
		return false
	}
	for i := range ps {
		if ps[i] != "*" && ps[i] != ks[i] {
			return false
		}
	}
	return true
}

// classifyMetric returns the kind of key and whether the table names it.
func classifyMetric(key string) (metricKind, bool) {
	for _, c := range metricClasses {
		if patternMatches(c.pattern, key) {
			return c.kind, true
		}
	}
	return kindMeasured, false
}

// errorRow is one error count of both reports with its rate.
type errorRow struct {
	Name               string
	Base, Cand         *float64
	BaseRate, CandRate *float64
	Verdict            string
	Agree              bool
}

type reportComparison struct {
	Measured    []metricRow
	Errors      []errorRow
	Bookkeeping int
	BadMeasured int
	BadErrors   int
}

// attemptsFor returns the number of attempts an error count is a share of,
// from the flattened report holding it. The unsent count is not an attempt.
func attemptsFor(key string, flat map[string]float64) float64 {
	switch {
	case strings.HasPrefix(key, "errors."):
		n := flat["throughput.accepted_messages"]
		for k, v := range flat {
			if strings.HasPrefix(k, "errors.") && k != "errors.unsent" {
				n += v
			}
		}
		return n
	case key == "delivery_delay.lost":
		return flat["delivery_delay.sent"]
	case key == "search_lag.not_found":
		return flat["search_lag.found"] + flat["search_lag.not_found"]
	case strings.HasPrefix(key, "clients.ops."):
		prefix := strings.TrimSuffix(key, ".errors")
		return flat[prefix+".count"] + flat[key]
	}
	return 0
}

func rateOf(key string, flat map[string]float64) (rate, count float64, ok bool) {
	count, ok = flat[key]
	if !ok {
		return 0, 0, false
	}
	if n := attemptsFor(key, flat); n > 0 {
		rate = count / n
	}
	return rate, count, true
}

// compareReports splits two flattened reports by metricClasses and judges
// each part by its own rule. threshold is a percent, and for error rates a
// number of percentage points: a relative change would fail 3 against 4
// errors in a million.
func compareReports(base, cand map[string]float64, threshold float64) reportComparison {
	var out reportComparison
	measuredBase, measuredCand := map[string]float64{}, map[string]float64{}
	shown := map[string]bool{}
	errKeys := map[string]struct{}{}
	bookkeeping := map[string]struct{}{}
	split := func(flat, measured map[string]float64) {
		for k, v := range flat {
			kind, _ := classifyMetric(k)
			switch kind {
			case kindMeasured, kindShown:
				measured[k] = v
				shown[k] = kind == kindShown
			case kindError:
				errKeys[k] = struct{}{}
			case kindBookkeeping:
				bookkeeping[k] = struct{}{}
			}
		}
	}
	split(base, measuredBase)
	split(cand, measuredCand)
	out.Bookkeeping = len(bookkeeping)

	for _, r := range compareMetrics(measuredBase, measuredCand, threshold) {
		if shown[r.Name] {
			r.Agree, r.Verdict = true, "not gated"
		} else if !r.Agree {
			out.BadMeasured++
		}
		out.Measured = append(out.Measured, r)
	}

	names := make([]string, 0, len(errKeys))
	for k := range errKeys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		row := errorRow{Name: k}
		br, bc, bok := rateOf(k, base)
		cr, cc, cok := rateOf(k, cand)
		if bok {
			row.Base, row.BaseRate = &bc, &br
		}
		if cok {
			row.Cand, row.CandRate = &cc, &cr
		}
		switch {
		case !bok || !cok:
			row.Verdict = "missing"
		case math.Abs(cr-br)*100 > threshold:
			row.Verdict = "differs"
		default:
			row.Agree, row.Verdict = true, "agree"
		}
		if !row.Agree {
			out.BadErrors++
		}
		out.Errors = append(out.Errors, row)
	}
	return out
}

func fmtRate(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*v*100, 'f', 4, 64)
}
