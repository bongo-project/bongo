package pipeline

import (
	"context"
	"log/slog"
	"time"
)

// errorDir is the spool directory that holds envelopes whose pipeline
// processing failed.
const errorDir = "error"

// RunErrorPurge periodically removes error/ envelopes older than retention.
// It blocks until ctx is cancelled; intended to run as a goroutine.
//
// It takes no blob store on purpose: blobs are shared and content-addressed,
// so only the blob GC may delete them. The GC counts error/ envelopes in its
// reference set, so a blob becomes collectable once its envelope is purged.
func RunErrorPurge(ctx context.Context, spool *SpoolStore, retention, interval time.Duration, logger *slog.Logger) {
	if spool == nil {
		return
	}
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	if interval <= 0 {
		interval = time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purgeErrorSpool(spool, retention, time.Now(), logger)
		}
	}
}

// purgeErrorSpool does one pass. The rule matches the quarantine purge: an
// envelope is removed when its ReceivedAt (receipt time, not the time it
// entered error/) is older than retention; ReceivedAt == 0 is kept. Each
// removal logs identity (id, sender, recipients) and never content.
func purgeErrorSpool(spool *SpoolStore, retention time.Duration, now time.Time, logger *slog.Logger) {
	envs, err := spool.List(errorDir)
	if err != nil {
		logger.Error("pipeline: error-spool purge list failed", "error", err)
		return
	}

	cutoff := now.Add(-retention)
	purged, failed := 0, 0

	for _, env := range envs {
		if env.ReceivedAt == 0 {
			continue
		}
		received := time.Unix(env.ReceivedAt, 0)
		if !received.Before(cutoff) {
			continue
		}
		if err := spool.Remove(errorDir, env.Id); err != nil {
			failed++
			logger.Error("pipeline: error envelope purge failed",
				append(envIdentity(env), "error", err)...)
			continue
		}
		purged++
		logger.Info("pipeline: error envelope purged",
			append(envIdentity(env),
				"received_at", received.UTC(),
				"retention", retention.String())...)
	}

	if purged+failed > 0 {
		logger.Info("pipeline: error-spool purge complete",
			"purged", purged, "failed", failed, "retention", retention.String())
	}
}
