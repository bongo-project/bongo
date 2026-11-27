package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUTCDay(t *testing.T) {
	zone := time.FixedZone("x", 5*3600)
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"utc", time.Date(2026, 11, 27, 23, 59, 59, 0, time.UTC), "2026-11-27"},
		{"local ahead of utc", time.Date(2026, 11, 28, 2, 0, 0, 0, zone), "2026-11-27"},
		{"midnight", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026-01-01"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := utcDay(tc.in); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRetentionCutoff(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 10, 0, 0, 0, time.UTC) }
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"mid month", d(2026, 11, 15), "2025-10-15"},
		{"31 January clamps to 31 December", d(2026, 1, 31), "2024-12-31"},
		{"31 March clamps to 29 February (leap)", d(2025, 3, 31), "2024-02-29"},
		{"31 March clamps to 28 February", d(2026, 3, 31), "2025-02-28"},
		{"29 February", d(2028, 2, 29), "2027-01-29"},
		{"1 January", d(2026, 1, 1), "2024-12-01"},
		{"31 December", d(2026, 12, 31), "2025-11-30"},
		{"30 April", d(2026, 4, 30), "2025-03-30"},
		{"31 May to 30 April", d(2026, 5, 31), "2025-04-30"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := retentionCutoff(tc.in); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRollupLoop(t *testing.T) {
	st := newFakeStore()
	lc := &logCapture{}
	var mu sync.Mutex
	clock := time.Date(2026, 11, 27, 0, 30, 0, 0, time.UTC)
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rollupLoop(ctx, st, now, ticks, lc.logf)
		close(done)
	}()

	ticks <- time.Time{}
	mu.Lock()
	clock = clock.Add(24 * time.Hour)
	mu.Unlock()
	ticks <- time.Time{}
	ticks <- time.Time{} // taken only after the second run finished
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop on cancel")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	// One run at start, then one per tick (the third tick may or may not
	// have run before the cancel).
	if len(st.rollups) < 3 {
		t.Fatalf("rollups: %v", st.rollups)
	}
	if st.rollups[0] != [2]string{"2026-11-27", "2025-10-27"} {
		t.Errorf("first rollup %v", st.rollups[0])
	}
	if st.rollups[2] != [2]string{"2026-11-28", "2025-10-28"} {
		t.Errorf("third rollup %v", st.rollups[2])
	}
}

func TestRollupFailureLogsNoRequestData(t *testing.T) {
	st := newFakeStore()
	st.err = errors.New("connection to secret-host failed")
	lc := &logCapture{}
	rollupOnce(context.Background(), st, func() time.Time { return testNow }, lc.logf)
	if len(lc.lines) != 1 || strings.Contains(lc.lines[0], "secret-host") {
		t.Errorf("log: %v", lc.lines)
	}
}

type blockingStore struct{ fakeStore }

func (b *blockingStore) Rollup(ctx context.Context, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRollupOnceTimesOut(t *testing.T) {
	old := rollupTimeout
	rollupTimeout = 20 * time.Millisecond
	defer func() { rollupTimeout = old }()
	var lines []string
	done := make(chan struct{})
	go func() {
		rollupOnce(context.Background(), &blockingStore{}, func() time.Time { return time.Now() },
			func(f string, a ...any) { lines = append(lines, f) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rollup did not time out")
	}
	if len(lines) != 1 {
		t.Errorf("want one log line, got %d", len(lines))
	}
}
