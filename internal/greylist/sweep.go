package greylist

import (
	"context"
	"log/slog"
	"time"
)

// RunSweep sweeps expired entries every interval until ctx is cancelled.
func (t *Table) RunSweep(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if n := t.Sweep(); n > 0 && logger != nil {
				logger.Debug("greylist: swept expired entries", "removed", n)
			}
		}
	}
}
