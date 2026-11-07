package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reportInputs writes one of every input by hand, so the report's own
// arithmetic is tested apart from the tools that feed it.
func reportInputs(t *testing.T) reportOptions {
	t.Helper()
	dir := t.TempDir()
	put := func(name string, v any) string {
		p := filepath.Join(dir, name)
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	var h latHist
	for _, ms := range []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		h.add(time.Duration(ms) * time.Millisecond)
	}
	smtp := smtpSummary{RunID: "aaaa", Valid: true, ElapsedSeconds: 10, Latency: h.snapshot(),
		Counts: summaryCounts{OK: 1000, Rejected5xx: 3, Deferred4xx: 4, TimedOut: 5, ConnErrors: 6}}
	clients := clientsSummary{RunID: "bbbb", Ops: map[string]opSummary{
		opLogin:       {Count: 50, P50Ms: 4, P95Ms: 16, P99Ms: 32, MaxMs: 40},
		opDAVPropfind: {Count: 20, P50Ms: 8, P95Ms: 8, P99Ms: 64, MaxMs: 70, ErrorTotal: 2},
	}}
	probe := probeSummary{RunID: "cccc", Sent: 4, Seen: 4, Found: 3, SearchLost: 1,
		DelayMs: []float64{100, 110, 120, 130}, SearchLagMs: []float64{200, 300, 400},
		Params: probeParams{ResolutionMs: 5000}}
	sink := sinkSummary{Messages: 998}

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var lines []string
	for i := range 4 {
		cpu, rss := float64(i)*5, float64(1000*(i+1))
		line, _ := json.Marshal(sampleRecord{Time: t0.Add(time.Duration(i) * 10 * time.Second), OK: true, CPUSeconds: &cpu, RSSBytes: &rss})
		lines = append(lines, string(line))
	}
	samples := filepath.Join(dir, "samples.jsonl")
	if err := os.WriteFile(samples, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(dir, "raw.txt")
	if err := os.WriteFile(raw, []byte("raw scrape text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "bongo.hcl")
	if err := os.WriteFile(conf, []byte(`session_secret = "must-never-be-copied-into-a-report"`), 0o600); err != nil {
		t.Fatal(err)
	}
	return reportOptions{
		SMTP: []string{put("smtp.json", smtp)}, Clients: put("clients.json", clients), Sink: put("sink.json", sink),
		Probe: put("probe.json", probe), Samples: samples, Raw: raw, BongoConfig: conf,
		Meta: put("meta.json", map[string]any{"version": "v0.15.4", "machine_type": "4 vCPU", "disk_type": "nvme", "file_system": "ext4", "seed": 42}),
		Out:  filepath.Join(dir, "report"),
	}
}

func TestBuildReportAllSections(t *testing.T) {
	o := reportInputs(t)
	d, err := buildReport(o, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if d.Throughput.AcceptedPerSecond != 100 || *d.Throughput.SinkMessages != 998 {
		t.Errorf("throughput %+v", d.Throughput)
	}
	// Ten values 10..100ms: nearest rank p50 is the 5th, p95 and p99 the 10th,
	// each as the upper edge of a 6% bucket.
	l := d.SMTPLatency
	if *l.P50Ms < 50 || *l.P50Ms > 53.2 || *l.P99Ms < 100 || *l.P99Ms > 100.001 {
		t.Errorf("latency %+v %v", l.quantiles, *l.P50Ms)
	}
	if e := d.Errors; e.Rejected5xx != 3 || e.Deferred4xx != 4 || e.TimedOut != 5 || e.ConnErrors != 6 {
		t.Errorf("errors %+v", e)
	}
	if *d.Delivery.P99Ms != 130 || *d.Delivery.P50Ms != 110 || d.Delivery.Seen != 4 {
		t.Errorf("delivery %+v", d.Delivery)
	}
	if d.Search.Found != 3 || d.Search.NotFound != 1 || *d.Search.P50Ms != 300 || d.Search.ResolutionMs != 5000 {
		t.Errorf("search %+v", d.Search)
	}
	if d.Clients.Ops[opLogin].quantiles.Count != 50 || d.Clients.Ops[opDAVPropfind].Errors != 2 {
		t.Errorf("clients %+v", d.Clients)
	}
	if _, ok := d.Clients.Ops[opFetchBody]; ok {
		t.Error("an operation with no data became an entry")
	}
	if s := d.Server; *s.CPUCoresAvg != 0.5 || s.Memory.Max != 4000 || s.RawFile.SHA256 == "" || s.SamplesFile.SHA256 == "" {
		t.Errorf("server %+v", s)
	}
	m := d.Meta
	if m.Version != "v0.15.4" || m.Seed != "42" || m.DiskType != "nvme" || len(m.ConfigSHA256) != 64 || len(m.Missing) != 0 {
		t.Errorf("meta %+v", m)
	}
	if !d.Validity.Valid || len(d.Validity.NotMeasured) != 0 {
		t.Errorf("validity %+v", d.Validity)
	}
}

func TestBuildReportNotMeasuredAndPartial(t *testing.T) {
	o := reportInputs(t)
	// Only a partial SMTP summary, saturated, and nothing else.
	p := filepath.Join(t.TempDir(), "smtp.json")
	data, _ := json.Marshal(smtpSummary{Partial: true, GeneratorSaturated: true, SaturationReasons: []string{"cpu"}, ElapsedSeconds: 4, Counts: summaryCounts{OK: 40}})
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := buildReport(reportOptions{SMTP: []string{p}, Out: o.Out}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if d.Delivery != nil || d.Search != nil || d.Clients != nil || d.Server != nil {
		t.Fatal("a section without an input was filled in")
	}
	if len(d.Validity.NotMeasured) != 3 || d.Validity.Valid || len(d.Validity.PartialInputs) != 1 {
		t.Errorf("validity %+v", d.Validity)
	}
	if len(d.Meta.Missing) != 5 {
		t.Errorf("missing metadata %v", d.Meta.Missing)
	}
	// Not measured is absent from the JSON, never a block of zeros.
	js, _ := json.Marshal(d)
	for _, key := range []string{`"delivery_delay"`, `"search_lag"`, `"clients"`, `"server"`} {
		if strings.Contains(string(js), key) {
			t.Errorf("JSON holds %s for an input that was not given", key)
		}
	}
	md := renderMarkdown(d)
	if !strings.Contains(md, "Partial inputs: smtp #1") || !strings.Contains(md, "**invalid**") {
		t.Errorf("markdown does not say partial and invalid:\n%s", md)
	}
}

func TestMarkdownHoldsEverySection(t *testing.T) {
	headings := []string{
		"## Run metadata", "## Throughput", "## SMTP latency", "## Errors", "## Delivery delay",
		"## Search lag", "## IMAP, POP3 and DAV", "## Server resources", "## Validity",
	}
	full, err := buildReport(reportInputs(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	md := renderMarkdown(full)
	for _, h := range headings {
		if !strings.Contains(md, h+"\n") {
			t.Errorf("full report lacks %q", h)
		}
	}
	if strings.Contains(md, "Not measured: no") {
		t.Errorf("a measured section says not measured:\n%s", md)
	}
	for _, want := range []string{"messages per second", "5xx rejected", "NOOP poll", "Resolution: 5000 ms", "upper bounds", "no Pebble, SQLite or Bleve"} {
		if !strings.Contains(md, want) {
			t.Errorf("full report lacks %q", want)
		}
	}

	empty := renderMarkdown(&reportDoc{Validity: validitySection{Valid: true}})
	for _, h := range headings {
		if !strings.Contains(empty, h+"\n") {
			t.Errorf("empty report lacks %q", h)
		}
	}
	if got := strings.Count(empty, "Not measured: no "); got != 7 {
		t.Errorf("%d sections say not measured, want 7:\n%s", got, empty)
	}
}

func TestRunReportWritesBothFilesAtMode0600(t *testing.T) {
	o := reportInputs(t)
	cfg := Config{Report: reportConfig{Report: o}}
	var so, se bytes.Buffer
	if code := runReport(cfg, &so, &se); code != exitOK {
		t.Fatalf("exit %d, stderr %s", code, se.String())
	}
	for _, ext := range []string{".json", ".md"} {
		st, err := os.Stat(o.Out + ext)
		if err != nil || st.Mode().Perm() != 0o600 || st.Size() == 0 {
			t.Errorf("%s: %v %v", ext, st, err)
		}
	}
	js, _ := os.ReadFile(o.Out + ".json")
	if strings.Contains(string(js), "must-never-be-copied") || strings.Contains(string(js), "session_secret") {
		t.Error("the Bongo config was copied into the report")
	}
	if code := runReport(Config{}, &so, &se); code != exitRefuse {
		t.Errorf("no input: exit %d, want %d", code, exitRefuse)
	}
	cfg.Report.Report.SMTP = []string{filepath.Join(t.TempDir(), "absent.json")}
	if code := runReport(cfg, &so, &se); code != exitFail {
		t.Errorf("missing input: exit %d, want %d", code, exitFail)
	}
}

func TestReadSamplesIgnoresATornLastLine(t *testing.T) {
	good := `{"time":"2026-01-01T00:00:00Z","ok":true}` + "\n"
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(good+good+`{"time":"2026-01-01T00:0`), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, err := readSamples(p)
	if err != nil || len(recs) != 2 {
		t.Fatalf("%d records, err %v: a killed sampler's torn line must not lose the run", len(recs), err)
	}
	if err := os.WriteFile(p, []byte(good+"garbage\n"+good), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSamples(p); err == nil {
		t.Error("a bad line in the middle was accepted")
	}
}

func metricsOf(t *testing.T, v map[string]any) map[string]float64 {
	t.Helper()
	raw, _ := json.Marshal(v)
	m, err := flattenMetrics(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCompareMetrics(t *testing.T) {
	tests := []struct {
		name        string
		base, cand  map[string]float64
		threshold   float64
		wantVerdict string
		wantAgree   bool
	}{
		{"11 percent up is flagged", map[string]float64{"m": 100}, map[string]float64{"m": 111}, 10, "differs", false},
		{"9 percent up passes", map[string]float64{"m": 100}, map[string]float64{"m": 109}, 10, "agree", true},
		{"11 percent down is flagged", map[string]float64{"m": 100}, map[string]float64{"m": 89}, 10, "differs", false},
		{"9 percent down passes", map[string]float64{"m": 100}, map[string]float64{"m": 91}, 10, "agree", true},
		{"exactly at the threshold agrees", map[string]float64{"m": 100}, map[string]float64{"m": 110}, 10, "agree", true},
		{"zero against zero agrees", map[string]float64{"m": 0}, map[string]float64{"m": 0}, 10, "agree", true},
		{"zero against non-zero differs", map[string]float64{"m": 0}, map[string]float64{"m": 0.001}, 10, "differs", false},
		{"non-zero against zero differs", map[string]float64{"m": 5}, map[string]float64{"m": 0}, 10, "differs", false},
		{"only in the baseline is missing", map[string]float64{"m": 5}, nil, 10, "missing", false},
		{"only in the candidate is missing", nil, map[string]float64{"m": 5}, 10, "missing", false},
		{"a wider threshold lets 11 percent pass", map[string]float64{"m": 100}, map[string]float64{"m": 111}, 15, "agree", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := compareMetrics(tt.base, tt.cand, tt.threshold)
			if len(rows) != 1 || rows[0].Verdict != tt.wantVerdict || rows[0].Agree != tt.wantAgree {
				t.Fatalf("rows %+v", rows)
			}
		})
	}
}

func TestReportCompareCommand(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, doc map[string]any) string {
		p := filepath.Join(dir, name)
		data, _ := json.Marshal(doc)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rep := func(rate, p99 float64, machine string) map[string]any {
		return map[string]any{
			"generated":    "2026-01-01T00:00:00Z",
			"meta":         map[string]any{"machine_type": machine, "disk_type": "nvme", "seed": "1"},
			"throughput":   map[string]any{"accepted_per_second": rate},
			"smtp_latency": map[string]any{"p99_ms": p99, "note": "text is not a metric"},
			"validity":     map[string]any{"valid": true},
		}
	}
	run := func(base, cand string, thr float64) (int, string, string) {
		cfg := Config{Report: reportConfig{Compare: compareOptions{Baseline: base, Candidate: cand, Threshold: thr}}}
		var so, se bytes.Buffer
		return runReportCompare(cfg, &so, &se), so.String(), se.String()
	}
	a := write("a.json", rep(100, 50, "4 vCPU"))

	if code, out, _ := run(a, write("b.json", rep(109, 54, "4 vCPU")), 10); code != exitOK || !strings.Contains(out, "throughput.accepted_per_second") {
		t.Errorf("9 percent: exit %d\n%s", code, out)
	}
	code, out, _ := run(a, write("c.json", rep(111, 50, "4 vCPU")), 10)
	if code != exitCompareDiffers || !strings.Contains(out, "differs") || !strings.Contains(out, "11.0") {
		t.Errorf("11 percent: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "note") || strings.Contains(out, "generated") || strings.Contains(out, "machine_type") {
		t.Errorf("a non-metric was compared:\n%s", out)
	}
	code, _, errOut := run(a, write("d.json", rep(100, 50, "8 vCPU")), 10)
	if code != exitOK || !strings.Contains(errOut, "machine type differs") {
		t.Errorf("machine change: exit %d, stderr %q", code, errOut)
	}
	missing := rep(100, 50, "4 vCPU")
	delete(missing, "smtp_latency")
	if code, out, _ := run(a, write("e.json", missing), 10); code != exitCompareDiffers || !strings.Contains(out, "missing") {
		t.Errorf("missing metric: exit %d\n%s", code, out)
	}
	if code, _, _ := run(a, filepath.Join(dir, "nope.json"), 10); code != exitFail {
		t.Errorf("absent file: exit %d", code)
	}
	if code, _, _ := run("", "", 10); code != exitRefuse {
		t.Errorf("no inputs: exit %d", code)
	}
}

// TestReportsHoldNoContentOrPassword runs the tools end to end against
// loopback fakes and searches everything they print or write for the probe
// account's password, its address, any address at all, and the message text.
func TestReportsHoldNoContentOrPassword(t *testing.T) {
	e := newProbeEnv(t, 30*time.Millisecond, 0)
	metrics, _ := fakeMetrics(t, nil)

	var outputs = map[string]string{}
	capture := func(name string, so, se *bytes.Buffer) {
		outputs[name+" stdout"], outputs[name+" stderr"] = so.String(), se.String()
	}

	// smtp, to the probe address, so the message and address are both in play.
	smtpCfg := testConfig(t, e.smtpAddr, "ok", smtpConfig{Rate: 20, Duration: 500 * time.Millisecond, Size: 2000})
	smtpCfg.Recipients = []string{probeEmail}
	sched, err := validateSMTP(smtpCfg)
	if err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if code := smtpMain(context.Background(), smtpCfg, sched, looseOpts(), &so, &se); code != exitOK {
		t.Fatalf("smtp exit %d: %s", code, se.String())
	}
	capture("smtp", &so, &se)

	// probe
	pcfg := e.config(t, func(o *probeOptions) { o.Duration = 500 * time.Millisecond })
	psum, err := probeRun(context.Background(), pcfg, defaultDial(), fastTune)
	if err != nil || psum.Sent == 0 {
		t.Fatalf("probe: %v %+v", err, psum)
	}

	// sampler
	scfg := sampleConfig(t, metrics.URL+"/metrics", 40*time.Millisecond, 200*time.Millisecond)
	if _, _, err := sampleRun(context.Background(), scfg, defaultDial(), io.Discard); err != nil {
		t.Fatal(err)
	}

	// report
	dir := t.TempDir()
	rcfg := Config{Report: reportConfig{Report: reportOptions{
		SMTP: []string{smtpCfg.SMTP.Summary}, Probe: pcfg.Report.Probe.Summary,
		Samples: scfg.Report.Sample.Out, Raw: scfg.Report.Sample.Raw, Out: filepath.Join(dir, "rep"),
	}}}
	so.Reset()
	se.Reset()
	if code := runReport(rcfg, &so, &se); code != exitOK {
		t.Fatalf("report exit %d: %s", code, se.String())
	}
	capture("report", &so, &se)

	files := map[string]string{
		"smtp summary": smtpCfg.SMTP.Summary, "probe summary": pcfg.Report.Probe.Summary,
		"samples": scfg.Report.Sample.Out, "raw scrapes": scfg.Report.Sample.Raw,
		"report json": rcfg.Report.Report.Out + ".json", "report markdown": rcfg.Report.Report.Out + ".md",
	}
	for name, p := range files {
		data, err := os.ReadFile(p)
		if err != nil || len(data) == 0 {
			t.Fatalf("%s: %v", name, err)
		}
		outputs[name] = string(data)
	}

	// The body is fillerBody text; the address and password come from the
	// state file.
	for _, bad := range []string{probePW, "probe-account", "@" + testDomain, "filler line", "sender@"} {
		for name, text := range outputs {
			if strings.Contains(text, bad) {
				t.Errorf("%s holds %q", name, bad)
			}
		}
	}
}
