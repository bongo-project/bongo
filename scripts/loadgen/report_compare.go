package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"text/tabwriter"
)

// exitCompareDiffers: report-compare found a metric outside the threshold or
// on one side only.
const exitCompareDiffers = 5

func init() {
	commands["report-compare"] = runReportCompare
	subcommandFlags["report-compare"] = addCompareFlags
}

func addCompareFlags(fs *flag.FlagSet, cfg *Config) {
	o := &cfg.Report.Compare
	fs.StringVar(&o.Baseline, "report-compare-baseline", "", "report-compare: the baseline report JSON (required)")
	fs.StringVar(&o.Candidate, "report-compare-candidate", "", "report-compare: the candidate report JSON (required)")
	fs.Float64Var(&o.Threshold, "report-compare-threshold", 10, "report-compare: percent difference within which two values agree")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), compareHelp)
		fs.PrintDefaults()
	}
}

const compareHelp = `usage: loadgen report-compare --target-allow H:P --test-domain D --report-compare-baseline A.json --report-compare-candidate B.json [flags]

Compares the measured results of two reports written by "report": both values,
the difference in percent of the baseline, and whether they agree within the
threshold (default 10). Measured results are throughput, the latency
percentiles (SMTP, delivery delay, search lag, IMAP, POP3 and DAV), the server
resource figures and the WAL fsync percentiles. A measured result present in
one report only is "missing" and does not agree. 0 against 0 agrees; 0 against
anything else does not. max_ms is shown but never gated: one outlier moves it.

Bookkeeping is not compared: file references with their sizes and hashes,
counts of messages and samples, failed scrapes, gaps, probe sent and
unresolved counts and the histogram resolution. Two runs of different length
differ in those without differing in performance.

Error counts have their own table with both counts, both rates (errors
divided by attempts) and a verdict. A rate differs when it moves by more than
the threshold in percentage points, so 3 against 4 errors in a million agree
and 0% against 11% does not. A different error count alone never fails.

Warns on stderr when the machine type, disk type, file system or seed differ,
since a difference then may be the hardware. It reads files and sends nothing,
but needs --target-allow and --test-domain like every subcommand.

Exit 0 all agree, 1 failed, 2 refused, 5 a measured result or an error rate
differs, or a measured result is missing.

`

// flattenMetrics returns every numeric value of a report keyed by its dotted
// JSON path. Strings, booleans, arrays and the metadata are not metrics.
func flattenMetrics(raw []byte) (map[string]float64, error) {
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, errors.New("not a report")
	}
	out := map[string]float64{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case float64:
			out[prefix] = x
		case map[string]any:
			for k, c := range x {
				if prefix == "" && (k == "generated" || k == "meta") {
					continue
				}
				name := k
				if prefix != "" {
					name = prefix + "." + k
				}
				walk(name, c)
			}
		}
	}
	walk("", tree)
	return out, nil
}

type metricRow struct {
	Name       string
	Base, Cand *float64
	DiffPct    *float64
	Agree      bool
	Verdict    string
}

// compareMetrics lines up two flattened reports. threshold is a percent.
func compareMetrics(base, cand map[string]float64, threshold float64) []metricRow {
	names := map[string]struct{}{}
	for k := range base {
		names[k] = struct{}{}
	}
	for k := range cand {
		names[k] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	rows := make([]metricRow, 0, len(sorted))
	for _, k := range sorted {
		row := metricRow{Name: k}
		b, bok := base[k]
		c, cok := cand[k]
		if bok {
			row.Base = &b
		}
		if cok {
			row.Cand = &c
		}
		switch {
		case !bok || !cok:
			row.Verdict = "missing"
		case b == 0 && c == 0:
			row.DiffPct, row.Agree, row.Verdict = ptr(0.0), true, "agree"
		case b == 0:
			// No percentage of zero exists; any change from nothing differs.
			row.Verdict = "differs"
		default:
			d := (c - b) / math.Abs(b) * 100
			row.DiffPct = &d
			if math.Abs(d) <= threshold {
				row.Agree, row.Verdict = true, "agree"
			} else {
				row.Verdict = "differs"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func runReportCompare(cfg Config, stdout, stderr io.Writer) int {
	o := cfg.Report.Compare
	if o.Baseline == "" || o.Candidate == "" {
		fmt.Fprintln(stderr, "loadgen: refused: --report-compare-baseline and --report-compare-candidate are required")
		return exitRefuse
	}
	if o.Threshold < 0 || math.IsNaN(o.Threshold) || math.IsInf(o.Threshold, 0) {
		fmt.Fprintln(stderr, "loadgen: refused: --report-compare-threshold must be a non-negative number")
		return exitRefuse
	}
	var raws [2][]byte
	var metas [2]reportMeta
	for i, c := range []struct{ flagName, path string }{
		{"--report-compare-baseline", o.Baseline}, {"--report-compare-candidate", o.Candidate},
	} {
		data, err := readCapped(c.path)
		if err != nil {
			fmt.Fprintf(stderr, "loadgen: %s: %v\n", c.flagName, err)
			return exitFail
		}
		var doc struct {
			Meta reportMeta `json:"meta"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			fmt.Fprintf(stderr, "loadgen: %s: not a report\n", c.flagName)
			return exitFail
		}
		raws[i], metas[i] = data, doc.Meta
	}
	base, err := flattenMetrics(raws[0])
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: baseline: %v\n", err)
		return exitFail
	}
	cand, err := flattenMetrics(raws[1])
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: candidate: %v\n", err)
		return exitFail
	}
	for _, m := range []struct{ name, a, b string }{
		{"machine type", metas[0].MachineType, metas[1].MachineType},
		{"disk type", metas[0].DiskType, metas[1].DiskType},
		{"file system", metas[0].FileSystem, metas[1].FileSystem},
		{"seed", metas[0].Seed, metas[1].Seed},
	} {
		if m.a != m.b {
			fmt.Fprintf(stderr, "loadgen: warning: the %s differs (%q against %q); a difference below may be the environment\n", m.name, m.a, m.b)
		}
	}

	cmp := compareReports(base, cand, o.Threshold)
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "metric\tbaseline\tcandidate\tdiff %\tverdict")
	for _, r := range cmp.Measured {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, fmtVal(r.Base), fmtVal(r.Cand), fmtPct(r.DiffPct), r.Verdict)
	}
	_ = tw.Flush()
	fmt.Fprintf(stdout, "%d measured results, %d outside the %.4g%% threshold or missing\n", len(cmp.Measured), cmp.BadMeasured, o.Threshold)
	if len(cmp.Errors) > 0 {
		fmt.Fprintln(stdout, "\nerrors (rate = errors / attempts; differs when the rate moves by more than the threshold in percentage points)")
		tw = tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "error count\tbaseline\tcandidate\tbaseline rate %\tcandidate rate %\tverdict")
		for _, r := range cmp.Errors {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, fmtVal(r.Base), fmtVal(r.Cand), fmtRate(r.BaseRate), fmtRate(r.CandRate), r.Verdict)
		}
		_ = tw.Flush()
		fmt.Fprintf(stdout, "%d error counts, %d with a differing rate\n", len(cmp.Errors), cmp.BadErrors)
	}
	fmt.Fprintf(stdout, "%d bookkeeping figures not compared\n", cmp.Bookkeeping)
	if cmp.BadMeasured+cmp.BadErrors > 0 {
		return exitCompareDiffers
	}
	return exitOK
}

func fmtVal(v *float64) string {
	if v == nil {
		return "-"
	}
	return strconv.FormatFloat(*v, 'g', 6, 64)
}

func fmtPct(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*v, 'f', 1, 64)
}
