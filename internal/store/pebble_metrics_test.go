package store

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/bongo-project/bongo/internal/observability"
)

// scrape returns the value of one unlabelled series from the real /metrics
// handler, so the test covers the collector and its registration, not just the
// store's own state.
func scrape(t *testing.T, name string) float64 {
	t.Helper()
	srv := httptest.NewServer(observability.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(line, name+" "); ok {
			v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
			if err != nil {
				t.Fatalf("parsing %q: %v", line, err)
			}
			return v
		}
	}
	t.Fatalf("series %s not exported", name)
	return 0
}

// TestPebbleStallAndCompactionMetricsMove drives a real database into a write
// stall and out of it with a manual compaction. All series are process-wide,
// so every assertion is a delta.
func TestPebbleStallAndCompactionMetricsMove(t *testing.T) {
	small := Option(func(o *pebble.Options) {
		o.MemTableSize = 64 << 10
		o.DisableAutomaticCompactions = true
		o.L0StopWritesThreshold = 2
		o.L0CompactionThreshold = 1
	})
	s, err := NewPebbleStore(t.TempDir(), small, WithMetrics())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	stalls0 := testutil.ToFloat64(observability.PebbleWriteStalls)
	stallSecs0 := testutil.ToFloat64(observability.PebbleWriteStallSeconds)
	compactions0 := scrape(t, "bongo_pebble_compactions_total")
	compactedBytes0 := scrape(t, "bongo_pebble_compacted_bytes_total")
	compactSecs0 := scrape(t, "bongo_pebble_compaction_seconds_total")
	fsync0 := scrape(t, "bongo_pebble_wal_fsync_seconds_count")
	fsyncSum0 := scrape(t, "bongo_pebble_wal_fsync_seconds_sum")

	// Sync writes drive the WAL fsync histogram.
	for i := range 20 {
		if err := s.Set([]byte("k"), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	// Pebble wakes the writer (syncQ.pop in flushPending) before the flush loop
	// records the fsync latency, so the last Set can return before its fsync is
	// counted. Wait for the count rather than reading it once.
	var fsyncGrowth float64
	for deadline := time.Now().Add(5 * time.Second); ; {
		fsyncGrowth = scrape(t, "bongo_pebble_wal_fsync_seconds_count") - fsync0
		if fsyncGrowth >= 20 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fsyncGrowth < 20 {
		t.Errorf("fsync count grew by %v, want at least 20", fsyncGrowth)
	}
	// Nanosecond to second: a real fsync is far below 20s each. An unconverted
	// sum would be in the millions.
	if d := scrape(t, "bongo_pebble_wal_fsync_seconds_sum") - fsyncSum0; d <= 0 || d > 20*20 {
		t.Errorf("fsync sum grew by %v seconds, want a small positive number", d)
	}

	// Two flushes of one key leave two overlapping L0 files, which meets the
	// stop threshold, so the third flush stalls in makeRoomForWrite. That flush
	// carries a key outside the compaction range below: Compact waits on any
	// overlapping memtable, and the stalled flush holds the lock it needs.
	done := make(chan error, 1)
	go func() {
		for _, key := range []string{"a1", "a1", "zz"} {
			if err := s.db.Set([]byte(key), []byte("v"), pebble.Sync); err != nil {
				done <- err
				return
			}
			if err := s.db.Flush(); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	deadline := time.After(20 * time.Second)
	for testutil.ToFloat64(observability.PebbleWriteStalls) < stalls0+1 {
		select {
		case err := <-done:
			t.Fatalf("writer finished without stalling (err %v)", err)
		case <-deadline:
			t.Fatal("no write stall observed within 20s")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if got := scrape(t, "bongo_pebble_l0_sublevels"); got < 2 {
		t.Errorf("l0 sublevels = %v while stalled, want at least 2", got)
	}

	if err := s.db.Compact([]byte("a"), []byte("b"), false); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("writer: %v", err)
		}
	case <-deadline:
		t.Fatal("writer still stalled after compaction")
	}

	if d := testutil.ToFloat64(observability.PebbleWriteStallSeconds) - stallSecs0; d <= 0 {
		t.Errorf("stall seconds grew by %v, want above 0", d)
	}
	if d := scrape(t, "bongo_pebble_compactions_total") - compactions0; d < 1 {
		t.Errorf("compactions grew by %v, want at least 1", d)
	}
	if d := scrape(t, "bongo_pebble_compacted_bytes_total") - compactedBytes0; d <= 0 {
		t.Errorf("compacted bytes grew by %v, want above 0", d)
	}
	if d := scrape(t, "bongo_pebble_compaction_seconds_total") - compactSecs0; d <= 0 {
		t.Errorf("compaction seconds grew by %v, want above 0", d)
	}
}

// TestPebbleMetricsAreOptIn guards the restore path, which opens a short-lived
// store: without WithMetrics it must neither take over the source nor count.
func TestPebbleMetricsAreOptIn(t *testing.T) {
	main, err := NewPebbleStore(t.TempDir(), WithMetrics())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = main.Close() })
	for i := range 10 {
		if err := main.Set([]byte("m"), []byte{byte(i)}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	before := scrape(t, "bongo_pebble_wal_fsync_seconds_count")

	plain, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	for i := range 10 {
		if err := plain.Set([]byte("p"), []byte{byte(i)}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	if got := scrape(t, "bongo_pebble_wal_fsync_seconds_count"); got != before {
		t.Errorf("fsync count moved from %v to %v after writes to a store without WithMetrics", before, got)
	}
	if err := plain.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := scrape(t, "bongo_pebble_wal_fsync_seconds_count"); got != before {
		t.Errorf("closing a store without WithMetrics changed the source: %v -> %v", before, got)
	}
}

// TestPebbleMetricsAfterClose checks a scrape after Close neither panics nor
// reports the closed database.
func TestPebbleMetricsAfterClose(t *testing.T) {
	s, err := NewPebbleStore(t.TempDir(), WithMetrics())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	if s.metrics() == nil {
		t.Fatal("metrics nil on an open store")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if s.metrics() != nil {
		t.Error("metrics returned a value after Close")
	}
	if got := scrape(t, "bongo_pebble_compactions_total"); got != 0 {
		t.Errorf("compactions after close = %v, want 0 with no source", got)
	}
}
