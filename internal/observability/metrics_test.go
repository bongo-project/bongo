package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHandlerServesRegisteredMetrics(t *testing.T) {
	SMTPMessagesTotal.WithLabelValues("accepted").Inc()
	SMTPMessagesTotal.WithLabelValues("rejected").Inc()
	SMTPMessagesTotal.WithLabelValues("rejected").Inc()

	srv := httptest.NewServer(Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	for _, want := range []string{
		`bongo_smtp_messages_total{verdict="accepted"} 1`,
		`bongo_smtp_messages_total{verdict="rejected"} 2`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}

// TestLoadTestMetricsExported covers every unlabelled metric the load test
// reads. Unlabelled collectors are exported at zero, so no observation is needed.
func TestLoadTestMetricsExported(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	for _, tt := range []struct{ name, kind string }{
		{"bongo_user_dbs_open", "gauge"},
		{"bongo_user_db_evictions_total", "counter"},
		{"bongo_search_index_backlog", "gauge"},
		{"bongo_raft_apply_seconds", "histogram"},
		{"bongo_queue_depth", "gauge"},
		{"bongo_raft_commit_lag_entries", "gauge"},
		{"bongo_raft_fsm_apply_seconds", "histogram"},
		{"bongo_pebble_compactions_total", "counter"},
		{"bongo_pebble_compaction_seconds_total", "counter"},
		{"bongo_pebble_compacted_bytes_total", "counter"},
		{"bongo_pebble_compaction_debt_bytes", "gauge"},
		{"bongo_pebble_compactions_in_progress", "gauge"},
		{"bongo_pebble_l0_sublevels", "gauge"},
		{"bongo_pebble_wal_fsync_seconds", "histogram"},
		{"bongo_pebble_write_stalls_total", "counter"},
		{"bongo_pebble_write_stall_seconds_total", "counter"},
		{"bongo_blobs", "gauge"},
		{"bongo_blob_bytes", "gauge"},
		{"bongo_blob_gc_pass_seconds", "histogram"},
		{"bongo_blob_gc_deleted_total", "counter"},
		{"bongo_blob_gc_referenced_blobs", "gauge"},
		{"bongo_raft_snapshot_seconds", "histogram"},
		{"bongo_raft_snapshot_bytes", "gauge"},
		{"bongo_search_indexes_open", "gauge"},
		{"bongo_search_index_evictions_total", "counter"},
		{"bongo_protocol_connections", "gauge"},
		{"bongo_protocol_connections_accepted_total", "counter"},
		{"bongo_protocol_command_seconds", "histogram"},
		{"bongo_dav_requests_in_flight", "gauge"},
		{"bongo_dav_requests_total", "counter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if want := "# TYPE " + tt.name + " " + tt.kind; !strings.Contains(s, want) {
				t.Errorf("metrics output missing %q", want)
			}
		})
	}
}

func TestRaftCommitLag(t *testing.T) {
	if got := testutil.ToFloat64(RaftCommitLagEntries); got != 0 {
		t.Fatalf("lag with no source = %v, want 0", got)
	}
	for _, tt := range []struct {
		name          string
		last, applied uint64
		want          float64
	}{
		{"behind", 10, 7, 3},
		{"caught up", 10, 10, 0},
		{"applied ahead does not underflow", 5, 9, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clear := SetRaftIndexSource(func() (uint64, uint64) { return tt.last, tt.applied })
			defer clear()
			if got := testutil.ToFloat64(RaftCommitLagEntries); got != tt.want {
				t.Errorf("lag = %v, want %v", got, tt.want)
			}
		})
	}
	if got := testutil.ToFloat64(RaftCommitLagEntries); got != 0 {
		t.Errorf("lag after clear = %v, want 0", got)
	}
}

func TestRaftIndexSourceClearIsCompareAndSwap(t *testing.T) {
	clearOld := SetRaftIndexSource(func() (uint64, uint64) { return 4, 0 })
	clearNew := SetRaftIndexSource(func() (uint64, uint64) { return 9, 0 })
	defer clearNew()
	clearOld()
	if got := testutil.ToFloat64(RaftCommitLagEntries); got != 9 {
		t.Fatalf("closing an older source unhooked the newer one: lag = %v, want 9", got)
	}
}

// TestFsyncHistogramConvertsNanosecondsToSeconds pins the unit: Pebble records
// nanoseconds, and a missed conversion is off by 1e9 yet still "increases".
func TestFsyncHistogramConvertsNanosecondsToSeconds(t *testing.T) {
	h := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "x",
		Buckets: []float64{1e5, 1e6, 1e7, 1e9},
	})
	h.Observe(2e6) // 2ms
	h.Observe(5e6) // 5ms

	count, sum, buckets := fsyncHistogram(h)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if sum < 0.0069 || sum > 0.0071 {
		t.Errorf("sum = %v seconds, want about 0.007", sum)
	}
	if got := buckets[0.01]; got != 2 {
		t.Errorf("bucket le=0.01 = %d, want 2 (buckets %v)", got, buckets)
	}
	if got := buckets[0.001]; got != 0 {
		t.Errorf("bucket le=0.001 = %d, want 0", got)
	}
	if c, s, b := fsyncHistogram(nil); c != 0 || s != 0 || len(b) != 0 {
		t.Errorf("nil histogram = %d %v %v, want empty", c, s, b)
	}
}

func TestPebbleSourceClearIsCompareAndSwap(t *testing.T) {
	older := &pebble.Metrics{}
	older.Compact.Count = 1
	newer := &pebble.Metrics{}
	newer.Compact.Count = 7
	clearOld := SetPebbleMetricsSource(func() *pebble.Metrics { return older })
	clearNew := SetPebbleMetricsSource(func() *pebble.Metrics { return newer })
	defer clearNew()
	clearOld()

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(newPebbleCollector())
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == "bongo_pebble_compactions_total" {
			if got := mf.GetMetric()[0].GetCounter().GetValue(); got != 7 {
				t.Fatalf("compactions = %v, want 7 from the newer source", got)
			}
			return
		}
	}
	t.Fatal("bongo_pebble_compactions_total not gathered")
}
