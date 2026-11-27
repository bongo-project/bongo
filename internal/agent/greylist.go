package agent

import (
	"context"
	"time"

	"github.com/bongo-project/bongo/internal/greylist"
)

// greylistSweepInterval is how often expired greylist entries are removed.
const greylistSweepInterval = time.Hour

// initGreylist builds the greylisting table and starts its hourly sweep when
// smtp.greylisting.enabled is set on a node that takes MX mail (server and dev
// roles). The table lives in memory only: it is never written to Raft or
// Pebble. It returns nil when greylisting is off.
func (a *Agent) initGreylist(ctx context.Context) *greylist.Table {
	if a.Greylist != nil {
		return a.Greylist
	}
	role := a.Config.Server.Role
	g := a.Config.SMTP.GreylistSettings()
	if !g.Enabled || !a.Config.SMTP.Enabled || (role != "server" && role != "dev") {
		return nil
	}
	delay, window, life, err := g.Durations()
	if err != nil {
		// Config.Validate rejects this at startup; stay off if it slips by.
		a.logger.Error("greylist: invalid settings, greylisting stays off", "error", err)
		return nil
	}
	table := greylist.New(greylist.Settings{Delay: delay, RetryWindow: window, PassLifetime: life},
		greylist.DefaultMaxEntries, nil)
	sweepCtx, cancel := context.WithCancel(ctx)
	a.greylistCancel = cancel
	a.goWorker("greylist-sweep", func() { table.RunSweep(sweepCtx, greylistSweepInterval, a.logger) })
	a.Greylist = table
	a.logger.Info("greylist: enabled", "delay", g.Delay, "retry_window", g.RetryWindow, "pass_lifetime", g.PassLifetime)
	return table
}
