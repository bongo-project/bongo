package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"time"
)

func init() {
	commands["report"] = runReport
	subcommandFlags["report"] = addReportFlags
}

// maxInputBytes bounds one report input read into memory.
const maxInputBytes = 512 << 20

// maxSampleGap is the longest gap between samples a valid run may have.
const maxSampleGap = 30 * time.Second

func addReportFlags(fs *flag.FlagSet, cfg *Config) {
	o := &cfg.Report.Report
	fs.Var(listFlag{&o.SMTP}, "report-smtp", "report: smtp summary (repeatable, for several generators; counts and histograms are merged)")
	fs.StringVar(&o.Clients, "report-clients", "", "report: clients summary")
	fs.StringVar(&o.Sink, "report-sink", "", "report: sink summary")
	fs.StringVar(&o.Samples, "report-samples", "", "report: samples file written by \"sample\"")
	fs.StringVar(&o.Raw, "report-raw", "", "report: raw scrape file written by \"sample\"; only its path, size and SHA-256 are recorded")
	fs.StringVar(&o.Probe, "report-probe", "", "report: probe summary")
	fs.StringVar(&o.Meta, "report-meta", "", "report: JSON file the operator fills in: version, machine_type, disk_type, file_system, seed")
	fs.StringVar(&o.BongoConfig, "report-bongo-config", "", "report: the server's config file; only its SHA-256 is recorded, never a copy, because it holds secrets")
	fs.StringVar(&o.Out, "report-out", "loadgen-report", "report: base path; writes BASE.json and BASE.md, mode 0600")
	fs.StringVar(&o.Version, "report-version", "", "report: Bongo version (overrides the metadata file)")
	fs.StringVar(&o.Machine, "report-machine", "", "report: machine type (overrides the metadata file)")
	fs.StringVar(&o.Disk, "report-disk", "", "report: disk type (overrides the metadata file)")
	fs.StringVar(&o.FileSystem, "report-filesystem", "", "report: file system (overrides the metadata file)")
	fs.StringVar(&o.Seed, "report-seed", "", "report: seed of the run (overrides the metadata file)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), reportHelp)
		fs.PrintDefaults()
	}
}

const reportHelp = `usage: loadgen report --target-allow H:P --test-domain D [--report-* inputs] [flags]

Builds one report from the files the other subcommands wrote: smtp, clients,
sink, sample and probe. It reads files and sends nothing. It still needs
--target-allow and --test-domain, because every subcommand passes the same
safety check.

Writes BASE.json and BASE.md. An input you do not give becomes a "not
measured" section, never zeros. A summary written mid-run (partial) is used
and named as partial. The report holds no message content, no address and no
password; the Bongo config is recorded as a SHA-256 only.

Notes on the numbers:
  - SMTP latency is MAIL to the DATA reply on an open connection, from a
    histogram with about 6% resolution.
  - IMAP, POP3 and DAV times come from the clients summary, whose buckets are powers
    of two: they read up to 2x high.
  - Disk growth is bongo_blob_bytes and bongo_blobs. The server exposes no
    Pebble, SQLite or Bleve size, so those are not covered.
  - WAL fsync times are upper bounds of coarse histogram buckets. A rank
    beyond the last finite bucket is a lower bound, in the *_over_ms fields.

Exit 0 ok, 1 failed, 2 refused.

`

// reportedClientOps are the client operations the report carries. Connects
// and the cheap POP3 steps are left out: the summary file has them all.
var reportedClientOps = []string{
	opLogin, opFetchHeaders, opFetchBody, opDAVPropfind, opDAVReport, opDAVPut,
	opPOP3Login, opPOP3Retr, opPOP3Quit,
}

// reportDoc is the JSON report. A section is nil when its input was not
// given, and the flattening for report-compare then has no metrics for it.
type reportDoc struct {
	Generated time.Time  `json:"generated"`
	Meta      reportMeta `json:"meta"`

	Throughput  *throughputSection `json:"throughput,omitempty"`
	SMTPLatency *latencySection    `json:"smtp_latency,omitempty"`
	Errors      *errorsSection     `json:"errors,omitempty"`
	Delivery    *deliverySection   `json:"delivery_delay,omitempty"`
	Search      *searchSection     `json:"search_lag,omitempty"`
	Clients     *clientsSection    `json:"clients,omitempty"`
	Server      *serverSection     `json:"server,omitempty"`
	Validity    validitySection    `json:"validity"`
}

type reportMeta struct {
	Version      string   `json:"version,omitempty"`
	MachineType  string   `json:"machine_type,omitempty"`
	DiskType     string   `json:"disk_type,omitempty"`
	FileSystem   string   `json:"file_system,omitempty"`
	Seed         string   `json:"seed,omitempty"`
	ConfigSHA256 string   `json:"bongo_config_sha256,omitempty"`
	RunIDs       []string `json:"run_ids,omitempty"`
	// SourceAddrs lists, per generator input, the local addresses its
	// connections came from. Bookkeeping, kept under meta so it is never
	// compared as a metric.
	SourceAddrs []generatorSources `json:"source_addrs,omitempty"`
	// Missing names the metadata the operator did not give.
	Missing []string `json:"missing,omitempty"`
}

// generatorSources names one input summary and its per-address connection counts.
type generatorSources struct {
	Generator string        `json:"generator"`
	Addrs     []sourceCount `json:"addrs"`
}

func (m *reportMeta) addSources(generator string, s []sourceCount) {
	if len(s) > 0 {
		m.SourceAddrs = append(m.SourceAddrs, generatorSources{Generator: generator, Addrs: s})
	}
}

type quantiles struct {
	Count int64    `json:"count"`
	P50Ms *float64 `json:"p50_ms,omitempty"`
	P95Ms *float64 `json:"p95_ms,omitempty"`
	P99Ms *float64 `json:"p99_ms,omitempty"`
	MaxMs *float64 `json:"max_ms,omitempty"`
}

type throughputSection struct {
	AcceptedMessages  int64   `json:"accepted_messages"`
	ElapsedSeconds    float64 `json:"elapsed_seconds"`
	AcceptedPerSecond float64 `json:"accepted_per_second"`
	SinkMessages      *int64  `json:"sink_messages,omitempty"`
}

type latencySection struct {
	quantiles
	Note string `json:"note"`
}

type errorsSection struct {
	Rejected5xx int64 `json:"rejected_5xx"`
	Deferred4xx int64 `json:"deferred_4xx"`
	TimedOut    int64 `json:"timed_out"`
	ConnErrors  int64 `json:"conn_errors"`
	OtherErrors int64 `json:"other_errors"`
	Unsent      int64 `json:"unsent"`
}

type deliverySection struct {
	quantiles
	Sent                int64  `json:"sent"`
	Seen                int64  `json:"seen"`
	SeenByNoop          int64  `json:"seen_by_noop"`
	Lost                int64  `json:"lost"`
	VisibleBeforeAccept int64  `json:"visible_before_accept"`
	Unresolved          int64  `json:"unresolved"`
	Note                string `json:"note"`
}

type searchSection struct {
	quantiles
	Found        int64   `json:"found"`
	NotFound     int64   `json:"not_found"`
	ResolutionMs float64 `json:"resolution_ms"`
	Note         string  `json:"note"`
}

type opQuantiles struct {
	quantiles
	Errors int64 `json:"errors"`
}

type clientsSection struct {
	Ops  map[string]opQuantiles `json:"ops"`
	Note string                 `json:"note"`
}

type resourceStat struct {
	Max float64 `json:"max"`
	Avg float64 `json:"avg"`
}

type serverSection struct {
	Samples       int     `json:"samples"`
	FailedScrapes int     `json:"failed_scrapes"`
	MaxGapSeconds float64 `json:"max_gap_seconds"`
	Restarts      int     `json:"restarts_seen"`

	CPUCoresAvg *float64      `json:"cpu_cores_avg,omitempty"`
	CPUCoresMax *float64      `json:"cpu_cores_max,omitempty"`
	Memory      *resourceStat `json:"resident_memory_bytes,omitempty"`
	OpenFiles   *resourceStat `json:"open_fds,omitempty"`

	BlobBytesGrowth *float64 `json:"blob_bytes_growth,omitempty"`
	BlobsGrowth     *float64 `json:"blobs_growth,omitempty"`

	FsyncCount       int64    `json:"wal_fsync_count"`
	FsyncP50Ms       *float64 `json:"wal_fsync_p50_upper_ms,omitempty"`
	FsyncP95Ms       *float64 `json:"wal_fsync_p95_upper_ms,omitempty"`
	FsyncP99Ms       *float64 `json:"wal_fsync_p99_upper_ms,omitempty"`
	FsyncMaxIntP99Ms *float64 `json:"wal_fsync_interval_p99_max_upper_ms,omitempty"`
	// The Over fields hold a rank that fell in the +Inf bucket: the true
	// value exceeds the last finite bound. They are separate keys so a lower
	// bound is never compared with an upper bound as one quantity.
	FsyncP50OverMs       *float64 `json:"wal_fsync_p50_over_ms,omitempty"`
	FsyncP95OverMs       *float64 `json:"wal_fsync_p95_over_ms,omitempty"`
	FsyncP99OverMs       *float64 `json:"wal_fsync_p99_over_ms,omitempty"`
	FsyncMaxIntP99OverMs *float64 `json:"wal_fsync_interval_p99_max_over_ms,omitempty"`

	// NotExposed names series the server did not serve in any sample.
	NotExposed  []string `json:"not_exposed,omitempty"`
	SamplesFile *fileRef `json:"samples_file,omitempty"`
	RawFile     *fileRef `json:"raw_scrapes_file,omitempty"`
	DiskNote    string   `json:"disk_note"`
}

type fileRef struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type validitySection struct {
	Valid              bool     `json:"valid"`
	GeneratorSaturated bool     `json:"generator_saturated"`
	SaturationReasons  []string `json:"saturation_reasons,omitempty"`
	PartialInputs      []string `json:"partial_inputs,omitempty"`
	SampleGapOver30s   bool     `json:"sample_gap_over_30s"`
	NotMeasured        []string `json:"not_measured,omitempty"`
	Warnings           []string `json:"warnings,omitempty"`
}

func ptr[T any](v T) *T { return &v }

// quantilesOf summarises raw millisecond values by nearest rank.
func quantilesOf(v []float64) quantiles {
	q := quantiles{Count: int64(len(v))}
	if len(v) == 0 {
		return q
	}
	q.P50Ms = ptrOK(percentile(v, 0.50))
	q.P95Ms = ptrOK(percentile(v, 0.95))
	q.P99Ms = ptrOK(percentile(v, 0.99))
	q.MaxMs = ptr(slices.Max(v))
	return q
}

// quantilesOfHist summarises a mergeable histogram.
func quantilesOfHist(h latencyHist) quantiles {
	q := quantiles{Count: h.Count}
	if h.Count == 0 {
		return q
	}
	q.P50Ms = ptrOK(h.quantile(0.50))
	q.P95Ms = ptrOK(h.quantile(0.95))
	q.P99Ms = ptrOK(h.quantile(0.99))
	q.MaxMs = ptr(float64(h.MaxUs) / 1000)
	return q
}

func ptrOK(v float64, ok bool) *float64 {
	if !ok {
		return nil
	}
	return &v
}

// readJSONFile decodes a summary. Errors name the flag, never file content.
func readJSONFile(flagName, path string, v any) error {
	data, err := readCapped(path)
	if err != nil {
		return fmt.Errorf("%s: %w", flagName, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: not a valid summary", flagName)
	}
	return nil
}

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- operator-chosen input file
	if err != nil {
		return nil, errors.New(errKind(err))
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return nil, errors.New("read failed")
	}
	if len(data) > maxInputBytes {
		return nil, errors.New("file is too large")
	}
	return data, nil
}

// hashFile streams path through SHA-256.
func hashFile(path string) (*fileRef, error) {
	f, err := os.Open(path) // #nosec G304 -- operator-chosen input file
	if err != nil {
		return nil, errors.New(errKind(err))
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return nil, errors.New("read failed")
	}
	return &fileRef{Path: path, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// flexString reads a JSON string or number, so "seed": 42 works.
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = flexString(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return errors.New("must be a string or a number")
	}
	*s = flexString(n.String())
	return nil
}

type metaFile struct {
	Version     flexString `json:"version"`
	MachineType flexString `json:"machine_type"`
	DiskType    flexString `json:"disk_type"`
	FileSystem  flexString `json:"file_system"`
	Seed        flexString `json:"seed"`
}

func validateReport(cfg Config) error {
	o := cfg.Report.Report
	if o.Out == "" {
		return errors.New("--report-out must not be empty")
	}
	if len(o.SMTP) == 0 && o.Clients == "" && o.Sink == "" && o.Samples == "" && o.Probe == "" {
		return errors.New("report needs at least one input: --report-smtp, --report-clients, --report-sink, --report-samples or --report-probe")
	}
	return nil
}

func runReport(cfg Config, stdout, stderr io.Writer) int {
	if err := validateReport(cfg); err != nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	doc, err := buildReport(cfg.Report.Report, time.Now().UTC())
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitFail
	}
	js, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: encode report: %v\n", err)
		return exitFail
	}
	out := cfg.Report.Report.Out
	if err := writeFileAtomic(out+".json", append(js, '\n')); err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitFail
	}
	if err := writeFileAtomic(out+".md", []byte(renderMarkdown(doc))); err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitFail
	}
	fmt.Fprintf(stdout, "report: %s.json and %s.md; valid: %v\n", out, out, doc.Validity.Valid)
	for _, w := range doc.Validity.Warnings {
		fmt.Fprintf(stderr, "loadgen: warning: %s\n", w)
	}
	return exitOK
}

// buildReport reads every input given and assembles the report.
func buildReport(o reportOptions, now time.Time) (*reportDoc, error) {
	doc := &reportDoc{Generated: now}
	v := &doc.Validity
	v.Valid = true
	note := func(name string) { v.NotMeasured = append(v.NotMeasured, name) }

	if err := fillMeta(&doc.Meta, o); err != nil {
		return nil, err
	}

	// SMTP: merge every generator's counts and histograms.
	if len(o.SMTP) > 0 {
		var merged smtpSummary
		var lat latencyHist
		for i, path := range o.SMTP {
			var s smtpSummary
			if err := readJSONFile("--report-smtp", path, &s); err != nil {
				return nil, err
			}
			if s.Partial {
				v.PartialInputs = append(v.PartialInputs, "smtp #"+strconv.Itoa(i+1))
			}
			doc.Meta.RunIDs = append(doc.Meta.RunIDs, s.RunID)
			doc.Meta.addSources("smtp #"+strconv.Itoa(i+1), s.SourceAddrs)
			merged.Counts.OK += s.Counts.OK
			merged.Counts.Rejected5xx += s.Counts.Rejected5xx
			merged.Counts.Deferred4xx += s.Counts.Deferred4xx
			merged.Counts.TimedOut += s.Counts.TimedOut
			merged.Counts.ConnErrors += s.Counts.ConnErrors
			merged.Counts.OtherErrors += s.Counts.OtherErrors
			merged.Counts.Unsent += s.Counts.Unsent
			merged.ElapsedSeconds = math.Max(merged.ElapsedSeconds, s.ElapsedSeconds)
			lat.merge(s.Latency)
			if s.GeneratorSaturated {
				v.GeneratorSaturated = true
				for _, r := range s.SaturationReasons {
					if !slices.Contains(v.SaturationReasons, r) {
						v.SaturationReasons = append(v.SaturationReasons, r)
					}
				}
			}
		}
		t := &throughputSection{AcceptedMessages: merged.Counts.OK, ElapsedSeconds: merged.ElapsedSeconds}
		if t.ElapsedSeconds > 0 {
			t.AcceptedPerSecond = float64(t.AcceptedMessages) / t.ElapsedSeconds
		}
		doc.Throughput = t
		doc.SMTPLatency = &latencySection{quantiles: quantilesOfHist(lat),
			Note: "MAIL to the reply after DATA on an open connection; dial and EHLO excluded; histogram, about 6% resolution"}
		c := merged.Counts
		doc.Errors = &errorsSection{
			Rejected5xx: c.Rejected5xx, Deferred4xx: c.Deferred4xx, TimedOut: c.TimedOut,
			ConnErrors: c.ConnErrors, OtherErrors: c.OtherErrors, Unsent: c.Unsent,
		}
		if v.GeneratorSaturated {
			v.Valid = false
		}
	} else {
		note("throughput, smtp latency and errors (no --report-smtp)")
	}

	if o.Sink != "" {
		var s sinkSummary
		if err := readJSONFile("--report-sink", o.Sink, &s); err != nil {
			return nil, err
		}
		if doc.Throughput == nil {
			doc.Throughput = &throughputSection{}
		}
		doc.Throughput.SinkMessages = ptr(s.Messages)
	}

	if o.Probe != "" {
		var s probeSummary
		if err := readJSONFile("--report-probe", o.Probe, &s); err != nil {
			return nil, err
		}
		if s.Partial {
			v.PartialInputs = append(v.PartialInputs, "probe")
		}
		doc.Meta.RunIDs = append(doc.Meta.RunIDs, s.RunID)
		doc.Meta.addSources("probe", s.SourceAddrs)
		doc.Delivery = &deliverySection{
			quantiles: quantilesOf(s.DelayMs), Sent: s.Sent, Seen: s.Seen, SeenByNoop: s.SeenByNoop, Lost: s.Lost,
			VisibleBeforeAccept: s.VisibleBeforeAccept, Unresolved: s.Unresolved,
			Note: "SMTP accept to the message showing over IMAP IDLE; lost = not seen within the probe timeout",
		}
		doc.Search = &searchSection{
			quantiles: quantilesOf(s.SearchLagMs), Found: s.Found, NotFound: s.SearchLost, ResolutionMs: s.Params.ResolutionMs,
			Note: "SMTP accept to a UID SEARCH on X-Loadgen-Id returning the message's own UID; resolution is the backoff cap",
		}
		if s.Error != "" {
			v.Warnings = append(v.Warnings, "the probe stopped on a connection failure ("+s.Error+")")
		}
	} else {
		note("delivery delay and search lag (no --report-probe)")
	}

	if o.Clients != "" {
		var s clientsSummary
		if err := readJSONFile("--report-clients", o.Clients, &s); err != nil {
			return nil, err
		}
		doc.Meta.RunIDs = append(doc.Meta.RunIDs, s.RunID)
		doc.Meta.addSources("clients", s.SourceAddrs)
		cs := &clientsSection{Ops: map[string]opQuantiles{},
			Note: "log2 histogram buckets: each percentile is a bucket upper bound and can read up to 2x high; pop3_login includes the server's INBOX snapshot, which grows with mailbox size, and pop3_quit holds the cost of moving deleted messages to Trash"}
		for _, name := range reportedClientOps {
			op, ok := s.Ops[name]
			if !ok || (op.Count == 0 && op.ErrorTotal == 0) {
				continue
			}
			q := quantiles{Count: op.Count}
			if op.Count > 0 {
				q.P50Ms, q.P95Ms, q.P99Ms, q.MaxMs = ptr(op.P50Ms), ptr(op.P95Ms), ptr(op.P99Ms), ptr(op.MaxMs)
			}
			cs.Ops[name] = opQuantiles{quantiles: q, Errors: op.ErrorTotal}
		}
		doc.Clients = cs
	} else {
		note("IMAP, POP3 and DAV times (no --report-clients)")
	}

	if o.Samples != "" {
		recs, err := readSamples(o.Samples)
		if err != nil {
			return nil, err
		}
		srv := analyseSamples(recs)
		if srv.SamplesFile, err = hashFile(o.Samples); err != nil {
			return nil, fmt.Errorf("--report-samples: %w", err)
		}
		if o.Raw != "" {
			if srv.RawFile, err = hashFile(o.Raw); err != nil {
				return nil, fmt.Errorf("--report-raw: %w", err)
			}
		}
		doc.Server = srv
		if srv.MaxGapSeconds > maxSampleGap.Seconds() {
			v.SampleGapOver30s = true
			v.Warnings = append(v.Warnings, fmt.Sprintf("the largest gap between samples was %.0fs, over 30s", srv.MaxGapSeconds))
		}
		if srv.Samples == 0 {
			v.Warnings = append(v.Warnings, "no scrape succeeded")
		}
		if srv.Restarts > 0 {
			v.Warnings = append(v.Warnings, "a counter went backwards: the server restarted during the run")
		}
		for _, n := range srv.NotExposed {
			v.Warnings = append(v.Warnings, n+" was not exposed by the server")
		}
	} else {
		note("server resources (no --report-samples)")
	}

	if v.GeneratorSaturated {
		v.Warnings = append(v.Warnings, "the generator saturated: the numbers describe the generator, not the server")
	}
	for _, p := range v.PartialInputs {
		v.Warnings = append(v.Warnings, p+" is a partial summary: the run did not finish writing it")
	}
	return doc, nil
}

func fillMeta(m *reportMeta, o reportOptions) error {
	var f metaFile
	if o.Meta != "" {
		if err := readJSONFile("--report-meta", o.Meta, &f); err != nil {
			return err
		}
	}
	pick := func(flagVal string, fileVal flexString) string {
		if flagVal != "" {
			return flagVal
		}
		return string(fileVal)
	}
	m.Version = pick(o.Version, f.Version)
	m.MachineType = pick(o.Machine, f.MachineType)
	m.DiskType = pick(o.Disk, f.DiskType)
	m.FileSystem = pick(o.FileSystem, f.FileSystem)
	m.Seed = pick(o.Seed, f.Seed)
	for _, c := range []struct{ name, val string }{
		{"version", m.Version}, {"machine_type", m.MachineType}, {"disk_type", m.DiskType},
		{"file_system", m.FileSystem}, {"seed", m.Seed},
	} {
		if c.val == "" {
			m.Missing = append(m.Missing, c.name)
		}
	}
	if o.BongoConfig != "" {
		ref, err := hashFile(o.BongoConfig)
		if err != nil {
			return fmt.Errorf("--report-bongo-config: %w", err)
		}
		m.ConfigSHA256 = ref.SHA256
	}
	return nil
}

// readSamples reads the JSONL file. A last line cut short by a killed sampler
// is ignored; a bad line elsewhere is an error.
func readSamples(path string) ([]sampleRecord, error) {
	data, err := readCapped(path)
	if err != nil {
		return nil, fmt.Errorf("--report-samples: %w", err)
	}
	var recs []sampleRecord
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var bad int
	for line := 1; sc.Scan(); line++ {
		if bad != 0 {
			return nil, fmt.Errorf("--report-samples: line %d is not valid", bad)
		}
		var r sampleRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			bad = line
			continue
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return nil, errors.New("--report-samples: read failed")
	}
	return recs, nil
}

// analyseSamples turns the scrapes into resource figures. CPU and the fsync
// histogram are counters: a step backwards means the server restarted, and
// that interval is skipped, not counted as negative work.
func analyseSamples(recs []sampleRecord) *serverSection {
	s := &serverSection{DiskNote: "disk growth is bongo_blob_bytes and bongo_blobs only; the server exposes no Pebble, SQLite or Bleve size"}
	var ok []sampleRecord
	for _, r := range recs {
		if r.OK {
			ok = append(ok, r)
		} else {
			s.FailedScrapes++
		}
	}
	sort.SliceStable(ok, func(i, j int) bool { return ok[i].Time.Before(ok[j].Time) })
	s.Samples = len(ok)
	for i := 1; i < len(ok); i++ {
		s.MaxGapSeconds = math.Max(s.MaxGapSeconds, ok[i].Time.Sub(ok[i-1].Time).Seconds())
	}

	// CPU: sum of deltas over sum of intervals.
	var cpuDelta, cpuWall, cpuMax float64
	for i := 1; i < len(ok); i++ {
		a, b := ok[i-1], ok[i]
		if a.CPUSeconds == nil || b.CPUSeconds == nil {
			continue
		}
		d, dt := *b.CPUSeconds-*a.CPUSeconds, b.Time.Sub(a.Time).Seconds()
		if d < 0 {
			s.Restarts++
			continue
		}
		if dt <= 0 {
			continue
		}
		cpuDelta += d
		cpuWall += dt
		cpuMax = math.Max(cpuMax, d/dt)
	}
	if cpuWall > 0 {
		s.CPUCoresAvg, s.CPUCoresMax = ptr(cpuDelta/cpuWall), ptr(cpuMax)
	}

	stat := func(get func(sampleRecord) *float64) *resourceStat {
		var sum, mx float64
		var n int
		for _, r := range ok {
			if p := get(r); p != nil {
				sum += *p
				mx = math.Max(mx, *p)
				n++
			}
		}
		if n == 0 {
			return nil
		}
		return &resourceStat{Max: mx, Avg: sum / float64(n)}
	}
	s.Memory = stat(func(r sampleRecord) *float64 { return r.RSSBytes })
	s.OpenFiles = stat(func(r sampleRecord) *float64 { return r.OpenFDs })
	growth := func(get func(sampleRecord) *float64) *float64 {
		var first, last *float64
		for _, r := range ok {
			if p := get(r); p != nil {
				if first == nil {
					first = p
				}
				last = p
			}
		}
		if first == nil {
			return nil
		}
		return ptr(*last - *first)
	}
	s.BlobBytesGrowth = growth(func(r sampleRecord) *float64 { return r.BlobBytes })
	s.BlobsGrowth = growth(func(r sampleRecord) *float64 { return r.Blobs })

	analyseFsync(ok, s)

	for _, c := range []struct {
		name string
		has  func(sampleRecord) bool
	}{
		{metricCPU, func(r sampleRecord) bool { return r.CPUSeconds != nil }},
		{metricRSS, func(r sampleRecord) bool { return r.RSSBytes != nil }},
		{metricFDs, func(r sampleRecord) bool { return r.OpenFDs != nil }},
		{metricBlobBytes, func(r sampleRecord) bool { return r.BlobBytes != nil }},
		{metricFsync, func(r sampleRecord) bool { return r.Fsync != nil }},
	} {
		if len(ok) > 0 && !slices.ContainsFunc(ok, c.has) {
			s.NotExposed = append(s.NotExposed, c.name)
		}
	}
	return s
}

// analyseFsync adds the per-interval bucket differences of the WAL fsync
// histogram. The buckets are coarse, so every figure is the upper bound of the
// bucket holding the rank.
func analyseFsync(ok []sampleRecord, s *serverSection) {
	var total []uint64
	var totalLes []float64
	var count uint64
	var maxP99 float64
	var anyOver bool
	for i := 1; i < len(ok); i++ {
		a, b := ok[i-1].Fsync, ok[i].Fsync
		if a == nil || b == nil || len(a.Buckets) != len(b.Buckets) || len(b.Buckets) == 0 {
			continue
		}
		if b.Count < a.Count {
			// A restart reset the histogram; the interval is unusable.
			continue
		}
		diff := make([]uint64, len(b.Buckets))
		les := make([]float64, 0, len(b.Buckets))
		okInt := true
		for k := range b.Buckets {
			if b.Buckets[k].Le != a.Buckets[k].Le || b.Buckets[k].Count < a.Buckets[k].Count {
				okInt = false
				break
			}
			diff[k] = b.Buckets[k].Count - a.Buckets[k].Count
			les = append(les, b.Buckets[k].Le)
		}
		if !okInt {
			continue
		}
		n := b.Count - a.Count
		if n == 0 {
			continue
		}
		if total == nil {
			total, totalLes = make([]uint64, len(diff)), les
		}
		if len(total) != len(diff) {
			continue
		}
		for k := range diff {
			total[k] += diff[k]
		}
		count += n
		if v, over, ok := fsyncRank(diff, les, n, 0.99); ok {
			maxP99 = math.Max(maxP99, v)
			anyOver = anyOver || over
		}
	}
	s.FsyncCount = int64(count)
	if count == 0 {
		return
	}
	s.FsyncP50Ms, s.FsyncP50OverMs = fsyncFigure(total, totalLes, count, 0.50)
	s.FsyncP95Ms, s.FsyncP95OverMs = fsyncFigure(total, totalLes, count, 0.95)
	s.FsyncP99Ms, s.FsyncP99OverMs = fsyncFigure(total, totalLes, count, 0.99)
	if anyOver {
		s.FsyncMaxIntP99OverMs = ptr(maxP99)
	} else {
		s.FsyncMaxIntP99Ms = ptr(maxP99)
	}
}

// fsyncFigure returns the rank as an upper bound or, when it fell in +Inf, as
// a lower bound; the other result is nil.
func fsyncFigure(cum []uint64, les []float64, n uint64, q float64) (upper, over *float64) {
	v, isOver, ok := fsyncRank(cum, les, n, q)
	switch {
	case !ok:
		return nil, nil
	case isOver:
		return nil, &v
	}
	return &v, nil
}

// fsyncRank returns, in milliseconds, the upper bound of the bucket holding
// the q-th of n observations. cum holds cumulative counts per upper bound. A
// rank above the last finite bucket falls in +Inf: the value is then the last
// finite bound and lowerBound is true, since the real time is longer.
func fsyncRank(cum []uint64, les []float64, n uint64, q float64) (value float64, lowerBound, ok bool) {
	if n == 0 || len(cum) == 0 {
		return 0, false, false
	}
	// #nosec G115 -- an observation count never nears 2^63
	rank := uint64(rankOf(int64(n), q))
	for k, c := range cum {
		if c >= rank {
			return les[k] * 1000, false, true
		}
	}
	return les[len(les)-1] * 1000, true, true
}
