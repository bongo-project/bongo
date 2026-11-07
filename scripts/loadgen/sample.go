package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

func init() {
	commands["sample"] = runSample
	subcommandFlags["sample"] = addSampleFlags
}

const (
	// maxSampleInterval is the longest gap the requirements allow between
	// two scrapes.
	maxSampleInterval = 30 * time.Second
	// maxScrapeBytes caps one response, so a server that streams without end
	// cannot fill memory.
	maxScrapeBytes = 8 << 20
)

// Metric names the sampler reads.
const (
	metricCPU       = "process_cpu_seconds_total"
	metricRSS       = "process_resident_memory_bytes"
	metricFDs       = "process_open_fds"
	metricBlobBytes = "bongo_blob_bytes"
	metricBlobs     = "bongo_blobs"
	metricFsync     = "bongo_pebble_wal_fsync_seconds"
)

func addSampleFlags(fs *flag.FlagSet, cfg *Config) {
	o := &cfg.Report.Sample
	fs.StringVar(&o.URL, "sample-url", "", "sample: URL of the server's /metrics; its host:port must be in --target-allow (required)")
	fs.DurationVar(&o.Interval, "sample-interval", 15*time.Second, "sample: time between scrapes; at most 30s")
	fs.DurationVar(&o.Timeout, "sample-timeout", 10*time.Second, "sample: per-scrape timeout; must be below --sample-interval")
	fs.DurationVar(&o.Duration, "sample-duration", 0, "sample: stop after this long (0 runs until SIGINT or SIGTERM)")
	fs.StringVar(&o.Out, "sample-out", "loadgen-samples.jsonl", "sample: file the parsed samples are appended to, one JSON line per scrape, mode 0600")
	fs.StringVar(&o.Raw, "sample-raw", "loadgen-metrics-raw.txt", "sample: file the raw scrape text is appended to, mode 0600")
	fs.IntVar(&o.RawMaxMB, "sample-raw-max-mb", 512, "sample: stop keeping raw text after this many megabytes")
	fs.BoolVar(&o.InsecureTLS, "sample-insecure-tls", false, "sample: accept a certificate that does not match the host; for an allow-listed test target only")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), sampleHelp)
		fs.PrintDefaults()
	}
}

const sampleHelp = `usage: loadgen sample --target-allow H:P --test-domain D --sample-url URL [flags]

Scrapes the server's /metrics every --sample-interval (at most 30s) and writes
one JSON line per scrape as it goes, so a killed run keeps what it measured. A
failed scrape is a line too, with an error class, so gaps show in the report.
Raw scrape text goes to a separate file.

Run it for the whole test, beside "smtp" and "clients". Then give its output to
"report" with --report-samples and --report-raw.

Reaching /metrics. The route is on the web listener behind PrivateOnly: it
answers only clients whose address is loopback, private or link-local, so
the public address always answers 403. Two ways in:

  - an SSH tunnel to the server's loopback:
      ssh -N -L 9443:127.0.0.1:<web port> user@server
    then --target-allow 127.0.0.1:9443 --sample-url https://127.0.0.1:9443/metrics
    and --sample-insecure-tls, because the certificate will not match 127.0.0.1;
  - a private network address of the server (VPN or the same VPC), allow-listed
    the same way.

If web.behind_tls_proxy is set and server.trusted_proxies is empty the route
answers 403 to everyone, whatever the route to it; set trusted_proxies first.

The target allow-list and --test-domain are required by every subcommand, this
one included. The URL is never printed or written to any output.

`

// sampleRecord is one line of the samples file. A series the server did not
// expose is absent, not zero, so a report can say "not exposed".
type sampleRecord struct {
	Time       time.Time `json:"time"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	DurationMs float64   `json:"duration_ms"`
	Bytes      int       `json:"bytes,omitempty"`

	CPUSeconds *float64     `json:"process_cpu_seconds_total,omitempty"`
	RSSBytes   *float64     `json:"process_resident_memory_bytes,omitempty"`
	OpenFDs    *float64     `json:"process_open_fds,omitempty"`
	BlobBytes  *float64     `json:"bongo_blob_bytes,omitempty"`
	Blobs      *float64     `json:"bongo_blobs,omitempty"`
	Fsync      *fsyncSample `json:"wal_fsync,omitempty"`

	// RawSkipped is set when the raw file reached its size cap.
	RawSkipped bool `json:"raw_skipped,omitempty"`
}

// fsyncSample is the cumulative WAL fsync histogram. The +Inf bucket is left
// out (JSON cannot hold it); Count covers it.
type fsyncSample struct {
	Count      uint64        `json:"count"`
	SumSeconds float64       `json:"sum_seconds"`
	Buckets    []fsyncBucket `json:"buckets"`
}

type fsyncBucket struct {
	Le    float64 `json:"le"`
	Count uint64  `json:"count"`
}

// sampleTarget validates --sample-url and returns the URL and its host:port.
// Errors never include the URL, which could carry a token.
func sampleTarget(cfg Config) (*url.URL, string, error) {
	o := cfg.Report.Sample
	if o.URL == "" {
		return nil, "", errors.New("--sample-url is required")
	}
	u, err := url.Parse(o.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, "", errors.New("--sample-url must be an http or https URL with a host")
	}
	if u.User != nil {
		return nil, "", errors.New("--sample-url must not carry credentials")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	hostport := net.JoinHostPort(u.Hostname(), port)
	if err := checkTarget(cfg, hostport); err != nil {
		return nil, "", fmt.Errorf("--sample-url: %w", err)
	}
	return u, hostport, nil
}

func validateSample(cfg Config) error {
	o := cfg.Report.Sample
	if _, _, err := sampleTarget(cfg); err != nil {
		return err
	}
	switch {
	case o.Interval <= 0 || o.Interval > maxSampleInterval:
		return errors.New("--sample-interval must be above 0 and at most 30s")
	case o.Timeout <= 0 || o.Timeout >= o.Interval:
		return errors.New("--sample-timeout must be above 0 and below --sample-interval")
	case o.Duration < 0:
		return errors.New("--sample-duration must not be negative")
	case o.Out == "" || o.Raw == "" || o.Out == o.Raw:
		return errors.New("--sample-out and --sample-raw must be two different non-empty paths")
	case o.RawMaxMB < 0:
		return errors.New("--sample-raw-max-mb must not be negative")
	}
	return nil
}

func runSample(cfg Config, stdout, stderr io.Writer) int {
	if err := validateSample(cfg); err != nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	n, failed, err := sampleRun(ctx, cfg, defaultDial())
	fmt.Fprintf(stdout, "sample: %d scrapes, %d failed; samples: %s, raw: %s\n", n, failed, cfg.Report.Sample.Out, cfg.Report.Sample.Raw)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitFail
	}
	if ctx.Err() != nil {
		return exitInterrupted
	}
	return exitOK
}

// openAppend opens path for writing, truncated, with mode 0600 even where the
// file existed with a wider one.
func openAppend(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- operator-chosen path
	if err != nil {
		return nil, errors.New("open " + errKind(err))
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, errors.New("chmod output")
	}
	return f, nil
}

// sampleRun scrapes until ctx ends or --sample-duration passes. It returns the
// scrapes made and the number that failed. The client uses the guarded dialer,
// ignores proxies and does not follow redirects.
func sampleRun(ctx context.Context, cfg Config, base dialFunc) (scrapes, failed int, err error) {
	if err := validateSample(cfg); err != nil {
		return 0, 0, err
	}
	o := cfg.Report.Sample
	u, _, _ := sampleTarget(cfg)
	out, err := openAppend(o.Out)
	if err != nil {
		return 0, 0, fmt.Errorf("samples file: %w", err)
	}
	defer out.Close()
	raw, err := openAppend(o.Raw)
	if err != nil {
		return 0, 0, fmt.Errorf("raw file: %w", err)
	}
	defer raw.Close()

	hc, tr := newHTTPClient(guardedDial(cfg, base), o.InsecureTLS, o.Timeout, nil)
	defer tr.CloseIdleConnections()

	if o.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Duration)
		defer cancel()
	}
	rawBudget := int64(o.RawMaxMB) << 20
	var rawUsed int64

	scrape := func() error {
		rec, body := scrapeOnce(ctx, hc, u.String(), o.Timeout)
		if ctx.Err() != nil && !rec.OK {
			// The scrape was cut off by our own shutdown, not the server.
			return nil
		}
		scrapes++
		if !rec.OK {
			failed++
		}
		if len(body) > 0 {
			header := "# loadgen scrape " + rec.Time.Format(time.RFC3339Nano) + "\n"
			if need := int64(len(header) + len(body) + 1); rawUsed+need > rawBudget {
				rec.RawSkipped = true
			} else {
				if _, err := raw.WriteString(header + string(body) + "\n"); err != nil {
					return errors.New("write raw file")
				}
				rawUsed += need
			}
		}
		line, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("encode sample: %w", err)
		}
		if _, err := out.Write(append(line, '\n')); err != nil {
			return errors.New("write samples file")
		}
		return nil
	}

	if err := scrape(); err != nil {
		return scrapes, failed, err
	}
	tick := time.NewTicker(o.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return scrapes, failed, nil
		case <-tick.C:
			if err := scrape(); err != nil {
				return scrapes, failed, err
			}
		}
	}
}

// scrapeOnce fetches and parses /metrics. The returned error text is a class,
// never the raw error, which would carry the URL.
func scrapeOnce(ctx context.Context, hc *http.Client, target string, timeout time.Duration) (sampleRecord, []byte) {
	rec := sampleRecord{Time: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	defer func() { rec.DurationMs = millis(time.Since(start)) }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		rec.Error = "request"
		return rec, nil
	}
	resp, err := hc.Do(req) // #nosec G107 G704 -- target passed checkTarget and the client dials through guardedDial
	if err != nil {
		rec.Error = scrapeErrClass(err)
		return rec, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		rec.Error = fmt.Sprintf("http_%dxx", resp.StatusCode/100)
		if resp.StatusCode == http.StatusForbidden {
			// The usual cause is the PrivateOnly guard, so say so.
			rec.Error = "http_403"
		}
		return rec, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxScrapeBytes+1))
	if err != nil {
		rec.Error = scrapeErrClass(err)
		return rec, nil
	}
	if len(body) > maxScrapeBytes {
		rec.Error = "too_large"
		return rec, nil
	}
	rec.Bytes = len(body)
	if err := parseScrape(body, &rec); err != nil {
		rec.Error = "parse"
		return rec, body
	}
	rec.OK = true
	return rec, body
}

func scrapeErrClass(err error) string {
	var cve *tls.CertificateVerificationError
	var ua x509.UnknownAuthorityError
	var hn x509.HostnameError
	if errors.As(err, &cve) || errors.As(err, &ua) || errors.As(err, &hn) {
		return "tls"
	}
	return classNames[classifyTransport(err)]
}

// parseScrape fills rec from Prometheus text. The validation scheme is named
// because the parser refuses to run without one.
func parseScrape(body []byte, rec *sampleRecord) error {
	p := expfmt.NewTextParser(model.LegacyValidation)
	fams, err := p.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return err
	}
	rec.CPUSeconds = scalar(fams[metricCPU])
	rec.RSSBytes = scalar(fams[metricRSS])
	rec.OpenFDs = scalar(fams[metricFDs])
	rec.BlobBytes = scalar(fams[metricBlobBytes])
	rec.Blobs = scalar(fams[metricBlobs])
	if f := fams[metricFsync]; f != nil && len(f.Metric) > 0 && f.Metric[0].Histogram != nil {
		h := f.Metric[0].Histogram
		fs := &fsyncSample{Count: h.GetSampleCount(), SumSeconds: h.GetSampleSum()}
		for _, b := range h.Bucket {
			if b.GetUpperBound() < 1e300 {
				fs.Buckets = append(fs.Buckets, fsyncBucket{Le: b.GetUpperBound(), Count: b.GetCumulativeCount()})
			}
		}
		rec.Fsync = fs
	}
	return nil
}

// scalar returns the first sample of a counter, gauge or untyped family.
func scalar(f *dto.MetricFamily) *float64 {
	if f == nil || len(f.Metric) == 0 {
		return nil
	}
	m := f.Metric[0]
	var v float64
	switch {
	case m.Counter != nil:
		v = m.Counter.GetValue()
	case m.Gauge != nil:
		v = m.Gauge.GetValue()
	case m.Untyped != nil:
		v = m.Untyped.GetValue()
	default:
		return nil
	}
	return &v
}
