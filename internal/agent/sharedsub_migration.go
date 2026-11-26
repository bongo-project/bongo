package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// sharedSubCleanupMarkerKey marks the one-time removal of the old
// /user/{id}/sharedsub/ keys as done. It is written only after a run with no
// errors, so a failure is retried by the next pass.
var sharedSubCleanupMarkerKey = []byte("/system/migrations/sharedsub-cleanup-v1")

// sharedSubCleanupInterval is how often a node that has not yet seen the
// marker checks whether it is leader and the job is due. A variable so tests
// can shorten it.
var sharedSubCleanupInterval = 30 * time.Second

// cleanupSharedSubPass deletes the /user/{id}/sharedsub/ keys, which only ever
// meant "subscribed" and are no longer read: a shared folder is subscribed by
// default and the opt-out lives under sharedunsub/. Nothing is converted.
//
// It writes only when isLeader reports true, checked before anything is read.
// Keys are collected first and the iterator is closed before the deletes are
// applied. The deletes are separate commands and not atomic together: an error
// is logged, the rest still run and the marker stays unwritten so the next
// pass finishes. It reports whether the job is finished.
func (a *Agent) cleanupSharedSubPass(isLeader func() bool) (done bool) {
	if a.Store == nil || !isLeader() {
		return false
	}
	marked, err := a.markerSet(sharedSubCleanupMarkerKey)
	if err != nil {
		a.logger.Warn("sharedsub-cleanup: marker unreadable", "error", err)
		return false
	}
	if marked {
		return true
	}
	keys, err := a.collectSharedSubKeys()
	if err != nil {
		a.logger.Warn("sharedsub-cleanup: scan failed", "error", err)
		return false
	}
	var applier store.RaftApplier = a
	if a.migrationApplier != nil {
		applier = a.migrationApplier
	}
	failed := 0
	for _, k := range keys {
		if err := applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: k}, 5*time.Second); err != nil {
			failed++
			a.logger.Warn("sharedsub-cleanup: delete failed", "error", err)
		}
	}
	if failed > 0 {
		a.logger.Warn("sharedsub-cleanup: some keys were not deleted, will retry", "failed", failed, "total", len(keys))
		return false
	}
	if err := a.setMarker(sharedSubCleanupMarkerKey); err != nil {
		a.logger.Warn("sharedsub-cleanup: marker not written", "error", err)
		return false
	}
	a.logger.Info("sharedsub-cleanup: removed legacy shared subscriptions", "count", len(keys))
	return true
}

// collectSharedSubKeys returns every /user/{id}/sharedsub/... key. The segment
// match is exact, so sharedunsub keys and user ids that contain the text are
// never caught. The iterator is closed before it returns.
func (a *Agent) collectSharedSubKeys() ([][]byte, error) {
	iter, err := a.Store.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, fmt.Errorf("scanning shared subscriptions: %w", err)
	}
	defer iter.Close()
	var keys [][]byte
	for iter.Next() {
		k := iter.Key()
		parts := strings.SplitN(strings.TrimPrefix(string(k), "/user/"), "/", 3)
		if len(parts) != 3 || parts[1] != "sharedsub" {
			continue
		}
		keys = append(keys, append([]byte(nil), k...))
	}
	return keys, nil
}

// runSharedSubCleanup retries cleanupSharedSubPass on every tick until it
// reports done or ctx ends, so a node that becomes leader after boot still
// runs the job.
func (a *Agent) runSharedSubCleanup(ctx context.Context, interval time.Duration, isLeader func() bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.cleanupSharedSubPass(isLeader) {
				return
			}
		}
	}
}
