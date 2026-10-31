package diskspace

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/bongo-project/bongo/internal/observability"
)

// recordHandler captures log records so tests can count transitions.
type recordHandler struct {
	mu   sync.Mutex
	msgs []string
	attr map[string]any
}

func (h *recordHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, r.Level.String()+" "+r.Message)
	h.attr = map[string]any{}
	r.Attrs(func(a slog.Attr) bool { h.attr[a.Key] = a.Value.Any(); return true })
	return nil
}
func (h *recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordHandler) WithGroup(string) slog.Handler      { return h }
func (h *recordHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.msgs...)
}

func newTestMonitor(minFree uint64, free map[string]uint64, errs map[string]error) (*Monitor, *recordHandler) {
	h := &recordHandler{}
	dirs := make([]string, 0, len(free)+len(errs))
	for d := range free {
		dirs = append(dirs, d)
	}
	for d := range errs {
		dirs = append(dirs, d)
	}
	m := New(dirs, minFree, slog.New(h))
	m.freeFn = func(dir string) (uint64, error) {
		if err, ok := errs[dir]; ok {
			return 0, err
		}
		return free[dir], nil
	}
	return m, h
}

func TestNilMonitorNeverLow(t *testing.T) {
	var m *Monitor
	if m.Low() {
		t.Fatal("nil monitor must not be low")
	}
}

func TestSampleThresholdEdges(t *testing.T) {
	const min = 1_048_577 // not a round number
	tests := []struct {
		name string
		free uint64
		low  bool
	}{
		{"one below", min - 1, true},
		{"exactly at", min, false},
		{"one above", min + 1, false},
		{"zero", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newTestMonitor(min, map[string]uint64{"/d": tt.free}, nil)
			m.sample()
			if m.Low() != tt.low {
				t.Errorf("Low() = %v, want %v", m.Low(), tt.low)
			}
		})
	}
}

func TestSampleSmallestDirWins(t *testing.T) {
	m, _ := newTestMonitor(1000, map[string]uint64{"/a": 5000, "/b": 999}, nil)
	m.sample()
	if !m.Low() {
		t.Fatal("the smaller of two directories must decide")
	}
}

func TestSampleErrorFailsOpen(t *testing.T) {
	m, h := newTestMonitor(1000, nil, map[string]error{"/a": errors.New("boom")})
	m.sample()
	m.sample()
	if m.Low() {
		t.Fatal("a statfs error must never mean low")
	}
	if n := len(h.messages()); n != 1 {
		t.Errorf("error logged %d times over two samples, want once: %v", n, h.messages())
	}
}

func TestSampleErrorKeepsLowStateOfOtherDir(t *testing.T) {
	m, _ := newTestMonitor(1000, map[string]uint64{"/a": 10}, map[string]error{"/b": errors.New("boom")})
	m.sample()
	if !m.Low() {
		t.Fatal("a readable low directory must still count when another errors")
	}
}

func TestSampleLogsOncePerTransition(t *testing.T) {
	free := uint64(10)
	h := &recordHandler{}
	m := New([]string{"/d"}, 1000, slog.New(h))
	m.freeFn = func(string) (uint64, error) { return free, nil }

	m.sample()
	m.sample()
	m.sample()
	free = 2000
	m.sample()
	m.sample()

	got := h.messages()
	want := []string{"WARN storage low", "INFO storage recovered"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("log records = %v, want %v", got, want)
	}
}

func TestSampleLowLogCarriesFigures(t *testing.T) {
	h := &recordHandler{}
	m := New([]string{"/d"}, 5<<20, slog.New(h))
	m.freeFn = func(string) (uint64, error) { return 3 << 20, nil }
	m.sample()
	if h.attr["free_mb"] != uint64(3) && h.attr["free_mb"] != int64(3) {
		t.Errorf("free_mb = %v", h.attr["free_mb"])
	}
	if h.attr["dir"] != "/d" {
		t.Errorf("dir = %v", h.attr["dir"])
	}
}

func TestSampleSetsGauge(t *testing.T) {
	free := uint64(1)
	m := New([]string{"/d"}, 1000, slog.New(&recordHandler{}))
	m.freeFn = func(string) (uint64, error) { return free, nil }
	m.sample()
	if got := testutil.ToFloat64(observability.StorageLow); got != 1 {
		t.Errorf("gauge = %v, want 1", got)
	}
	free = 1000
	m.sample()
	if got := testutil.ToFloat64(observability.StorageLow); got != 0 {
		t.Errorf("gauge = %v, want 0", got)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	m, _ := newTestMonitor(1000, map[string]uint64{"/d": 1}, nil)
	m.interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()

	// The first sample happens before any tick.
	deadline := time.After(5 * time.Second)
	for !m.Low() {
		select {
		case <-deadline:
			t.Fatal("Run did not sample")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestStatfsFreeBytesRealDir(t *testing.T) {
	free, err := FreeBytes(t.TempDir())
	if err != nil {
		t.Skipf("statfs unsupported here: %v", err)
	}
	if free == 0 {
		t.Error("a temp dir reporting zero free bytes is implausible")
	}
}

// Leaving the low state because every check failed is not a recovery; the log
// must say the check failed.
func TestSampleAllErrorsWhileLowIsNotRecovery(t *testing.T) {
	var fail bool
	h := &recordHandler{}
	m := New([]string{"/d"}, 1000, slog.New(h))
	m.freeFn = func(string) (uint64, error) {
		if fail {
			return 0, errors.New("boom")
		}
		return 10, nil
	}
	m.sample()
	fail = true
	m.sample()
	for _, msg := range h.messages() {
		if strings.Contains(msg, "recovered") {
			t.Fatalf("logged %q after a failed check, want no recovery: %v", msg, h.messages())
		}
	}
	if m.Low() {
		t.Error("a failed check must not keep refusing work")
	}
}
