package main

import (
	"fmt"
	"strings"
)

// renderMarkdown writes the human summary. Every section always has its
// heading; one whose input was not given says "not measured".
func renderMarkdown(d *reportDoc) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	notMeasured := func(why string) { w("Not measured: %s.\n\n", why) }

	w("# Load test report\n\nGenerated %s.\n\n", d.Generated.Format("2006-01-02 15:04:05 MST"))

	w("## Run metadata\n\n")
	w("| Item | Value |\n|---|---|\n")
	for _, row := range [][2]string{
		{"Bongo version", d.Meta.Version}, {"Machine type", d.Meta.MachineType}, {"Disk type", d.Meta.DiskType},
		{"File system", d.Meta.FileSystem}, {"Seed", d.Meta.Seed}, {"Bongo config SHA-256", d.Meta.ConfigSHA256},
		{"Run IDs", strings.Join(d.Meta.RunIDs, ", ")},
	} {
		v := row[1]
		if v == "" {
			v = "not given"
		}
		w("| %s | %s |\n", row[0], v)
	}
	w("\nThe Bongo config is recorded as a hash only; it holds secrets and is never copied.\n\n")

	w("## Throughput\n\n")
	if t := d.Throughput; t == nil {
		notMeasured("no SMTP summary was given")
	} else {
		if t.ElapsedSeconds > 0 || t.AcceptedMessages > 0 {
			w("Accepted %d messages in %.1fs: **%.1f messages per second**.\n\n", t.AcceptedMessages, t.ElapsedSeconds, t.AcceptedPerSecond)
		}
		if t.SinkMessages != nil {
			w("The sink received %d messages.\n\n", *t.SinkMessages)
		}
	}

	w("## SMTP latency\n\n")
	if l := d.SMTPLatency; l == nil {
		notMeasured("no SMTP summary was given")
	} else {
		w("%s\n\n%s\n", l.Note, quantileTable(l.quantiles))
	}

	w("## Errors\n\n")
	if e := d.Errors; e == nil {
		notMeasured("no SMTP summary was given")
	} else {
		w("| Class | Count |\n|---|---|\n| 5xx rejected | %d |\n| 4xx deferred | %d |\n| Timeouts | %d |\n| Connection errors | %d |\n| Other | %d |\n| Unsent | %d |\n\n",
			e.Rejected5xx, e.Deferred4xx, e.TimedOut, e.ConnErrors, e.OtherErrors, e.Unsent)
	}

	w("## Delivery delay\n\n")
	if p := d.Delivery; p == nil {
		notMeasured("no probe summary was given")
	} else {
		w("%s\n\n%s\n", p.Note, quantileTable(p.quantiles))
		w("Sent %d, seen %d (%d found only by the NOOP poll), lost %d, unresolved %d, visible before the accept reply %d.\n\n",
			p.Sent, p.Seen, p.SeenByNoop, p.Lost, p.Unresolved, p.VisibleBeforeAccept)
	}

	w("## Search lag\n\n")
	if s := d.Search; s == nil {
		notMeasured("no probe summary was given")
	} else {
		w("%s\n\n%s\n", s.Note, quantileTable(s.quantiles))
		w("Found %d, not found %d. Resolution: %.0f ms.\n\n", s.Found, s.NotFound, s.ResolutionMs)
	}

	w("## IMAP and DAV\n\n")
	if c := d.Clients; c == nil {
		notMeasured("no clients summary was given")
	} else {
		w("%s.\n\n| Operation | Count | p50 ms | p95 ms | p99 ms | Errors |\n|---|---|---|---|---|---|\n", c.Note)
		for _, name := range []string{opLogin, opFetchHeaders, opFetchBody, opDAVPropfind, opDAVReport, opDAVPut} {
			if o, ok := c.Ops[name]; ok {
				w("| %s | %d | %s | %s | %s | %d |\n", name, o.Count, num(o.P50Ms), num(o.P95Ms), num(o.P99Ms), o.Errors)
			}
		}
		w("\n")
	}

	w("## Server resources\n\n")
	if s := d.Server; s == nil {
		notMeasured("no samples file was given")
	} else {
		w("%d successful scrapes, %d failed, largest gap %.0fs.\n\n", s.Samples, s.FailedScrapes, s.MaxGapSeconds)
		w("| Resource | Value |\n|---|---|\n")
		w("| CPU, cores (average / maximum) | %s / %s |\n", num(s.CPUCoresAvg), num(s.CPUCoresMax))
		w("| Resident memory, bytes (average / maximum) | %s |\n", stat(s.Memory))
		w("| Open files (average / maximum) | %s |\n", stat(s.OpenFiles))
		w("| Blob bytes grown | %s |\n| Blobs grown | %s |\n", num(s.BlobBytesGrowth), num(s.BlobsGrowth))
		w("| WAL fsync, upper bound of p50 / p95 / p99, ms | %s / %s / %s (%d fsyncs) |\n", num(s.FsyncP50Ms), num(s.FsyncP95Ms), num(s.FsyncP99Ms), s.FsyncCount)
		w("| WAL fsync, worst interval p99, upper bound, ms | %s |\n\n", num(s.FsyncMaxIntP99Ms))
		w("%s. The fsync buckets are coarse, so those figures are upper bounds.\n\n", capitalise(s.DiskNote))
		if len(s.NotExposed) > 0 {
			w("Not exposed by the server: %s.\n\n", strings.Join(s.NotExposed, ", "))
		}
		if s.SamplesFile != nil {
			w("Samples file: `%s`, %d bytes, SHA-256 `%s`.\n\n", s.SamplesFile.Path, s.SamplesFile.Bytes, s.SamplesFile.SHA256)
		}
		if s.RawFile != nil {
			w("Raw scrapes: `%s`, %d bytes, SHA-256 `%s`.\n\n", s.RawFile.Path, s.RawFile.Bytes, s.RawFile.SHA256)
		}
	}

	w("## Validity\n\n")
	v := d.Validity
	if v.Valid {
		w("The run is **valid**: the generator did not saturate.\n\n")
	} else {
		w("The run is **invalid**: the generator saturated (%s), so the numbers describe the generator, not the server.\n\n", strings.Join(v.SaturationReasons, ", "))
	}
	if v.SampleGapOver30s {
		w("A gap between server samples exceeded 30s.\n\n")
	}
	if len(v.PartialInputs) > 0 {
		w("Partial inputs: %s.\n\n", strings.Join(v.PartialInputs, ", "))
	}
	for _, x := range v.Warnings {
		w("- Warning: %s\n", x)
	}
	if len(v.Warnings) > 0 {
		w("\n")
	}
	if len(v.NotMeasured) > 0 {
		w("Not measured: %s.\n", strings.Join(v.NotMeasured, "; "))
	}
	return b.String()
}

func quantileTable(q quantiles) string {
	if q.Count == 0 {
		return "No samples.\n"
	}
	return fmt.Sprintf("| Samples | p50 ms | p95 ms | p99 ms | max ms |\n|---|---|---|---|---|\n| %d | %s | %s | %s | %s |\n",
		q.Count, num(q.P50Ms), num(q.P95Ms), num(q.P99Ms), num(q.MaxMs))
}

// num formats a float or float pointer; an absent value reads "n/a", not 0.
func num[T float64 | *float64](v T) string {
	switch x := any(v).(type) {
	case float64:
		return fmt.Sprintf("%.1f", x)
	case *float64:
		if x == nil {
			return "n/a"
		}
		return fmt.Sprintf("%.1f", *x)
	}
	return "n/a"
}

func stat(r *resourceStat) string {
	if r == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0f / %.0f", r.Avg, r.Max)
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
