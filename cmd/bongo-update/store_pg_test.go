package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// pgTestStore opens the database named by BONGO_UPDATE_TEST_DSN with empty
// tables, or skips the test when it is not set.
func pgTestStore(t *testing.T) *pgStore {
	t.Helper()
	dsn := os.Getenv("BONGO_UPDATE_TEST_DSN")
	if dsn == "" {
		t.Skip("BONGO_UPDATE_TEST_DSN is not set")
	}
	st, err := openStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ctx := context.Background()
	if err := st.EnsureSchema(ctx); err != nil {
		t.Fatal("schema:", describeErr(err))
	}
	for _, q := range []string{"TRUNCATE seen", "TRUNCATE daily_counts"} {
		if _, err := st.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func (s *pgStore) countSeen(t *testing.T, day string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM seen WHERE day = $1::date`, day).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (s *pgStore) instances(t *testing.T, day, version string) int {
	t.Helper()
	var n int
	err := s.pool.QueryRow(context.Background(),
		`SELECT coalesce(sum(instances), 0) FROM daily_counts WHERE day = $1::date AND version = $2`, day, version).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (s *pgStore) countDays(t *testing.T) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM daily_counts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func id(n int) string { return fmt.Sprintf("%032x", n) }

func TestPGSchemaIdempotent(t *testing.T) {
	st := pgTestStore(t)
	for range 2 {
		if err := st.EnsureSchema(context.Background()); err != nil {
			t.Fatal(describeErr(err))
		}
	}
}

func TestPGRecordDistinct(t *testing.T) {
	st := pgTestStore(t)
	ctx := context.Background()
	for _, r := range [][3]string{
		{"2026-11-01", "1.0.0", id(1)},
		{"2026-11-01", "1.0.0", id(1)},
		{"2026-11-01", "1.0.1", id(1)},
		{"2026-11-01", "1.0.0", id(2)},
	} {
		if err := st.Record(ctx, r[0], r[1], r[2]); err != nil {
			t.Fatal(describeErr(err))
		}
	}
	if got := st.countSeen(t, "2026-11-01"); got != 3 {
		t.Errorf("seen rows = %d, want 3", got)
	}
}

func TestPGRollup(t *testing.T) {
	st := pgTestStore(t)
	ctx := context.Background()
	rec := func(day, v string, n int) {
		t.Helper()
		for i := range n {
			if err := st.Record(ctx, day, v, id(i)); err != nil {
				t.Fatal(describeErr(err))
			}
		}
	}
	rec("2026-11-25", "1.0.0", 3)
	rec("2026-11-25", "1.0.1", 1)
	rec("2026-11-26", "1.0.0", 2)
	rec("2026-11-27", "1.0.0", 5) // today

	if err := st.Rollup(ctx, "2026-11-27", "2025-10-27"); err != nil {
		t.Fatal(describeErr(err))
	}
	if got := st.instances(t, "2026-11-25", "1.0.0"); got != 3 {
		t.Errorf("25th 1.0.0 = %d", got)
	}
	if got := st.instances(t, "2026-11-25", "1.0.1"); got != 1 {
		t.Errorf("25th 1.0.1 = %d", got)
	}
	if got := st.instances(t, "2026-11-26", "1.0.0"); got != 2 {
		t.Errorf("26th = %d", got)
	}
	if got := st.countSeen(t, "2026-11-25") + st.countSeen(t, "2026-11-26"); got != 0 {
		t.Errorf("past seen rows remain: %d", got)
	}
	if got := st.countSeen(t, "2026-11-27"); got != 5 {
		t.Errorf("today's rows = %d, want 5", got)
	}
	if got := st.instances(t, "2026-11-27", "1.0.0"); got != 0 {
		t.Errorf("today was rolled up: %d", got)
	}

	// Idempotent: a second run changes nothing.
	if err := st.Rollup(ctx, "2026-11-27", "2025-10-27"); err != nil {
		t.Fatal(describeErr(err))
	}
	if got := st.instances(t, "2026-11-25", "1.0.0"); got != 3 {
		t.Errorf("second run changed the count: %d", got)
	}

	// A late row for a rolled-up day is added to it.
	if err := st.Record(ctx, "2026-11-25", "1.0.0", id(99)); err != nil {
		t.Fatal(describeErr(err))
	}
	if err := st.Rollup(ctx, "2026-11-27", "2025-10-27"); err != nil {
		t.Fatal(describeErr(err))
	}
	if got := st.instances(t, "2026-11-25", "1.0.0"); got != 4 {
		t.Errorf("late row: %d, want 4", got)
	}
}

func TestPGRetention(t *testing.T) {
	st := pgTestStore(t)
	ctx := context.Background()
	for _, day := range []string{"2025-10-26", "2025-10-27", "2025-10-28"} {
		if err := st.Record(ctx, day, "1.0.0", id(1)); err != nil {
			t.Fatal(describeErr(err))
		}
	}
	// Roll all three up, keeping the cutoff far in the past.
	if err := st.Rollup(ctx, "2026-11-27", "2000-01-01"); err != nil {
		t.Fatal(describeErr(err))
	}
	if got := st.countDays(t); got != 3 {
		t.Fatalf("counts = %d, want 3", got)
	}
	if err := st.Rollup(ctx, "2026-11-27", "2025-10-27"); err != nil {
		t.Fatal(describeErr(err))
	}
	if st.instances(t, "2025-10-26", "1.0.0") != 0 || st.instances(t, "2025-10-27", "1.0.0") != 1 || st.instances(t, "2025-10-28", "1.0.0") != 1 {
		t.Errorf("retention removed the wrong days; %d remain", st.countDays(t))
	}
}

func TestPGConcurrentRollupCountsOnce(t *testing.T) {
	st := pgTestStore(t)
	ctx := context.Background()
	const rows = 5000
	if _, err := st.pool.Exec(ctx, `INSERT INTO seen (day, version, instance_id)
		SELECT '2026-11-20', '1.0.0', lpad(to_hex(g), 32, '0') FROM generate_series(1, $1::int) g`, rows); err != nil {
		t.Fatal(describeErr(err))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			errs <- st.Rollup(cctx, "2026-11-27", "2025-10-27")
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(describeErr(err))
		}
	}
	if got := st.instances(t, "2026-11-20", "1.0.0"); got != rows {
		t.Errorf("instances = %d, want %d", got, rows)
	}
}
