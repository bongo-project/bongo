package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeMetrics serves Prometheus text whose CPU counter rises by 0.5 per
// scrape. fail decides, from the request number, whether to answer an error.
func fakeMetrics(t *testing.T, fail func(n int64) int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := n.Add(1)
		if fail != nil {
			if code := fail(i); code != 0 {
				http.Error(w, "nope", code)
				return
			}
		}
		fmt.Fprintf(w, "# TYPE process_cpu_seconds_total counter\nprocess_cpu_seconds_total %g\n", float64(i)*0.5)
		fmt.Fprintf(w, "# TYPE process_resident_memory_bytes gauge\nprocess_resident_memory_bytes %d\n", 1000+i*10)
		fmt.Fprintf(w, "# TYPE process_open_fds gauge\nprocess_open_fds %d\n", 20+i)
		fmt.Fprintf(w, "# TYPE bongo_blob_bytes gauge\nbongo_blob_bytes %d\n", i*4096)
		fmt.Fprintf(w, "# TYPE bongo_blobs gauge\nbongo_blobs %d\n", i)
		fmt.Fprint(w, "# TYPE bongo_pebble_wal_fsync_seconds histogram\n")
		fmt.Fprintf(w, "bongo_pebble_wal_fsync_seconds_bucket{le=\"0.001\"} %d\n", i*5)
		fmt.Fprintf(w, "bongo_pebble_wal_fsync_seconds_bucket{le=\"0.01\"} %d\n", i*9)
		fmt.Fprintf(w, "bongo_pebble_wal_fsync_seconds_bucket{le=\"+Inf\"} %d\n", i*10)
		fmt.Fprintf(w, "bongo_pebble_wal_fsync_seconds_sum %g\nbongo_pebble_wal_fsync_seconds_count %d\n", float64(i)*0.02, i*10)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func sampleConfig(t *testing.T, url string, interval, duration time.Duration) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		Allow:      []Target{{Host: "127.0.0.1", IP: true}},
		TestDomain: testDomain,
		Report: reportConfig{Sample: sampleOptions{
			URL: url, Interval: interval, Timeout: interval / 2, Duration: duration,
			Out: filepath.Join(dir, "samples.jsonl"), Raw: filepath.Join(dir, "raw.txt"), RawMaxMB: 8,
		}},
	}
}

func TestSamplerScrapesAtTheInterval(t *testing.T) {
	srv, _ := fakeMetrics(t, nil)
	const interval = 50 * time.Millisecond
	cfg := sampleConfig(t, srv.URL+"/metrics", interval, 520*time.Millisecond)
	n, failed, err := sampleRun(context.Background(), cfg, defaultDial())
	if err != nil || failed != 0 {
		t.Fatalf("scrapes %d, failed %d, err %v", n, failed, err)
	}
	// One at the start, then one per interval: about 11 in 520ms.
	if n < 8 || n > 12 {
		t.Fatalf("%d scrapes in 520ms at 50ms", n)
	}
	recs, err := readSamples(cfg.Report.Sample.Out)
	if err != nil || len(recs) != n {
		t.Fatalf("read %d records, want %d, err %v", len(recs), n, err)
	}
	for i := 1; i < len(recs); i++ {
		if gap := recs[i].Time.Sub(recs[i-1].Time); gap < interval*6/10 || gap > interval*3 {
			t.Errorf("gap %d is %v, want about %v", i, gap, interval)
		}
	}

	srvSec := analyseSamples(recs)
	first, last := recs[0], recs[len(recs)-1]
	want := (*last.CPUSeconds - *first.CPUSeconds) / last.Time.Sub(first.Time).Seconds()
	if srvSec.CPUCoresAvg == nil || *srvSec.CPUCoresAvg < want*0.999 || *srvSec.CPUCoresAvg > want*1.001 {
		t.Fatalf("cpu avg %v, want %v cores", srvSec.CPUCoresAvg, want)
	}
	if want < 0.5/0.05/3 || want > 0.5/0.05*1.5 {
		t.Errorf("0.5s per %v scrape should be near 10 cores, got %v", interval, want)
	}
	if srvSec.Samples != n || srvSec.Memory == nil || srvSec.OpenFiles == nil || srvSec.BlobBytesGrowth == nil {
		t.Errorf("section incomplete: %+v", srvSec)
	}
	if got, wantG := *srvSec.BlobBytesGrowth, float64(n-1)*4096; got != wantG {
		t.Errorf("blob growth %v, want %v", got, wantG)
	}
	// Every interval adds 10 fsyncs, 9 of them below 10ms and 5 below 1ms.
	if srvSec.FsyncP50Ms == nil || *srvSec.FsyncP50Ms != 1 || *srvSec.FsyncP99Ms != 10 {
		t.Errorf("fsync bounds p50 %v p99 %v, want 1ms and 10ms", srvSec.FsyncP50Ms, srvSec.FsyncP99Ms)
	}
	if len(srvSec.NotExposed) != 0 {
		t.Errorf("exposed series reported missing: %v", srvSec.NotExposed)
	}

	for _, p := range []string{cfg.Report.Sample.Out, cfg.Report.Sample.Raw} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v, want mode 0600", filepath.Base(p), st, err)
		}
	}
	raw, _ := os.ReadFile(cfg.Report.Sample.Raw)
	if got := strings.Count(string(raw), "# loadgen scrape"); got != n || !strings.Contains(string(raw), "process_cpu_seconds_total 0.5\n") {
		t.Errorf("raw file holds %d scrapes, want %d", got, n)
	}
}

func TestSamplerRecordsFailuresAsClasses(t *testing.T) {
	srv, _ := fakeMetrics(t, func(n int64) int {
		switch n {
		case 2:
			return http.StatusForbidden
		case 3:
			return http.StatusInternalServerError
		}
		return 0
	})
	cfg := sampleConfig(t, srv.URL+"/metrics?token=hunter2-url-secret", 40*time.Millisecond, 250*time.Millisecond)
	_, failed, err := sampleRun(context.Background(), cfg, defaultDial())
	if err != nil || failed != 2 {
		t.Fatalf("failed %d, err %v", failed, err)
	}
	recs, _ := readSamples(cfg.Report.Sample.Out)
	if recs[1].OK || recs[1].Error != "http_403" || recs[2].Error != "http_5xx" {
		t.Errorf("failures not classed: %+v %+v", recs[1], recs[2])
	}
	data, _ := os.ReadFile(cfg.Report.Sample.Out)
	if strings.Contains(string(data), "hunter2") || strings.Contains(string(data), "127.0.0.1") {
		t.Error("the samples file holds the URL")
	}
	if s := analyseSamples(recs); s.FailedScrapes != 2 || s.Samples != len(recs)-2 {
		t.Errorf("section counts %d failed %d ok", s.FailedScrapes, s.Samples)
	}
}

func TestSamplerNeverFollowsRedirects(t *testing.T) {
	var other atomic.Int64
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { other.Add(1) }))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer srv.Close()
	cfg := sampleConfig(t, srv.URL, 40*time.Millisecond, 100*time.Millisecond)
	if _, failed, err := sampleRun(context.Background(), cfg, defaultDial()); err != nil || failed == 0 {
		t.Fatalf("failed %d, err %v: a redirect is not a metrics page", failed, err)
	}
	if other.Load() != 0 {
		t.Fatal("the sampler followed a redirect")
	}
}

func TestSamplerRawFileIsCapped(t *testing.T) {
	srv, _ := fakeMetrics(t, nil)
	cfg := sampleConfig(t, srv.URL, 40*time.Millisecond, 100*time.Millisecond)
	cfg.Report.Sample.RawMaxMB = 0
	if _, _, err := sampleRun(context.Background(), cfg, defaultDial()); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(cfg.Report.Sample.Raw); len(raw) != 0 {
		t.Errorf("raw file holds %d bytes past a cap of 0", len(raw))
	}
	recs, _ := readSamples(cfg.Report.Sample.Out)
	if len(recs) == 0 || !recs[0].RawSkipped || !recs[0].OK {
		t.Errorf("a skipped raw scrape must still be a good sample: %+v", recs)
	}
}

func TestValidateSample(t *testing.T) {
	good := func(mut func(*Config)) error {
		cfg := sampleConfig(t, "http://127.0.0.1:9443/metrics", 15*time.Second, 0)
		cfg.Report.Sample.Timeout = 10 * time.Second
		mut(&cfg)
		return validateSample(cfg)
	}
	tests := []struct {
		name string
		mut  func(*Config)
		bad  bool
	}{
		{"defaults", func(*Config) {}, false},
		{"30s is the longest interval", func(c *Config) { c.Report.Sample.Interval = 30 * time.Second }, false},
		{"over 30s", func(c *Config) { c.Report.Sample.Interval = 31 * time.Second }, true},
		{"timeout not below interval", func(c *Config) { c.Report.Sample.Timeout = 15 * time.Second }, true},
		{"no url", func(c *Config) { c.Report.Sample.URL = "" }, true},
		{"host outside the allow list", func(c *Config) { c.Report.Sample.URL = "http://10.9.9.9:9/metrics" }, true},
		{"credentials in the url", func(c *Config) { c.Report.Sample.URL = "http://u:p@127.0.0.1:9/metrics" }, true},
		{"not http", func(c *Config) { c.Report.Sample.URL = "ftp://127.0.0.1/metrics" }, true},
		{"same file twice", func(c *Config) { c.Report.Sample.Raw = c.Report.Sample.Out }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := good(tt.mut); (err != nil) != tt.bad {
				t.Fatalf("err = %v, want failure %v", err, tt.bad)
			}
		})
	}
}

// TestAnalyseSamplesCounterReset: a server restart drops CPU and the fsync
// histogram back to zero. The interval is skipped, never counted as negative
// work, and the restart is reported.
func TestAnalyseSamplesCounterReset(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := func(sec int, cpu float64) sampleRecord {
		return sampleRecord{Time: t0.Add(time.Duration(sec) * time.Second), OK: true, CPUSeconds: &cpu}
	}
	s := analyseSamples([]sampleRecord{rec(0, 10), rec(10, 20), rec(20, 1), rec(30, 6)})
	if s.Restarts != 1 {
		t.Errorf("restarts = %d, want 1", s.Restarts)
	}
	// 10s of CPU over 10s and 5s over 10s; the interval across the reset is out.
	if s.CPUCoresAvg == nil || *s.CPUCoresAvg != 0.75 || *s.CPUCoresMax != 1 {
		t.Errorf("cpu avg %v max %v, want 0.75 and 1", s.CPUCoresAvg, s.CPUCoresMax)
	}
	if s.MaxGapSeconds != 10 {
		t.Errorf("gap = %v", s.MaxGapSeconds)
	}
	if len(s.NotExposed) == 0 {
		t.Error("series absent from every sample were not reported as not exposed")
	}
}

// TestAnalyseSamplesFsyncReset: bucket differences across a restart are
// skipped, and what remains is bounded by the buckets' upper edges.
func TestAnalyseSamplesFsyncReset(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := func(sec int, le1, le2, total uint64) sampleRecord {
		return sampleRecord{Time: t0.Add(time.Duration(sec) * time.Second), OK: true, Fsync: &fsyncSample{
			Count: total, Buckets: []fsyncBucket{{Le: 0.001, Count: le1}, {Le: 0.01, Count: le2}},
		}}
	}
	// 100 more fsyncs, 90 under 1ms; a restart; then 10 more, all over 1ms.
	s := analyseSamples([]sampleRecord{rec(0, 0, 0, 0), rec(10, 90, 100, 100), rec(20, 0, 0, 0), rec(30, 0, 10, 10)})
	if s.FsyncCount != 110 {
		t.Fatalf("fsync count %d, want 110: the interval across the restart must be skipped", s.FsyncCount)
	}
	if s.FsyncP50Ms == nil || *s.FsyncP50Ms != 1 || *s.FsyncP99Ms != 10 || *s.FsyncMaxIntP99Ms != 10 {
		t.Errorf("bounds p50 %v p99 %v max-interval p99 %v", s.FsyncP50Ms, s.FsyncP99Ms, s.FsyncMaxIntP99Ms)
	}
}

func TestAnalyseSamplesLargestGapIgnoresFailures(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ok := func(sec int) sampleRecord {
		return sampleRecord{Time: t0.Add(time.Duration(sec) * time.Second), OK: true}
	}
	bad := func(sec int) sampleRecord {
		return sampleRecord{Time: t0.Add(time.Duration(sec) * time.Second), Error: "timeout"}
	}
	s := analyseSamples([]sampleRecord{ok(0), ok(15), bad(30), bad(45), ok(60)})
	if s.MaxGapSeconds != 45 || s.FailedScrapes != 2 {
		t.Errorf("gap %v failed %d, want 45 and 2: failed scrapes are gaps in the data", s.MaxGapSeconds, s.FailedScrapes)
	}
}
