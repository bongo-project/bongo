package main

import (
	"context"
	"time"
)

// rollupTimeout bounds one rollup so a stalled database cannot hold the
// loop until shutdown and make later ticks pile up unseen.
var rollupTimeout = 5 * time.Minute

const dayLayout = "2006-01-02"

// utcDay is the UTC calendar day of t as YYYY-MM-DD. Days are always
// computed here, never by the database, so the server's time zone cannot
// move a count to another day.
func utcDay(t time.Time) string { return t.UTC().Format(dayLayout) }

// retentionCutoff is the same day of the month 13 months earlier, clamped to
// that month's last day. Days strictly before it are deleted. time.AddDate
// is not used: it normalises an overflowing day into the next month.
func retentionCutoff(t time.Time) string {
	t = t.UTC()
	y, m, d := t.Year(), int(t.Month())-1-13, t.Day()
	for m < 0 {
		m += 12
		y--
	}
	// Day 0 of the following month is the last day of this one.
	last := time.Date(y, time.Month(m+2), 0, 0, 0, 0, 0, time.UTC).Day()
	d = min(d, last)
	return time.Date(y, time.Month(m+1), d, 0, 0, 0, 0, time.UTC).Format(dayLayout)
}

// rollupOnce runs one rollup for the time now reports.
func rollupOnce(ctx context.Context, st store, now func() time.Time, logf func(string, ...any)) {
	t := now()
	ctx, cancel := context.WithTimeout(ctx, rollupTimeout)
	defer cancel()
	if err := st.Rollup(ctx, utcDay(t), retentionCutoff(t)); err != nil {
		logf("rollup failed: %s", describeErr(err))
	}
}

// rollupLoop rolls up once at once and again on every tick until ctx ends.
func rollupLoop(ctx context.Context, st store, now func() time.Time, ticks <-chan time.Time, logf func(string, ...any)) {
	rollupOnce(ctx, st, now, logf)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			rollupOnce(ctx, st, now, logf)
		}
	}
}
