package cluster

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	previewBackfillBatch    = 50
	previewBackfillPause    = 200 * time.Millisecond
	previewBackfillInterval = 30 * time.Minute
)

// PreviewBackfill fills the list preview for mail indexed before the preview
// columns existed, and for mail whose blob had not reached this node when it
// was first indexed.
//
// It runs on every node, not just the leader: the per-user SQLite indexes are
// node-local and the job writes no Raft state. It is a background job rather
// than a fill-on-listing so that the mailbox list never reads blobs.
//
// State lives in the index itself (preview IS NULL), so it is resumable across
// restarts and never rereads a filled row. A single goroutine, small batches
// and a pause between them keep it gentle on a live node.
type PreviewBackfill struct {
	ps       *store.PebbleStore
	blobs    *store.BlobStore
	metadata *store.MetadataManager
	logger   *slog.Logger

	batch    int
	pause    time.Duration
	interval time.Duration
	// unreadable counts rows skipped for an unreadable blob in the current
	// pass. Only the single Run goroutine touches it.
	unreadable int
	// afterBatch, when set, runs after each batch (tests).
	afterBatch func()
	// afterFill, when set, runs after each row (tests).
	afterFill func()
}

// NewPreviewBackfill builds a backfill with production batch size and pacing.
func NewPreviewBackfill(ps *store.PebbleStore, blobs *store.BlobStore, metadata *store.MetadataManager, logger *slog.Logger) *PreviewBackfill {
	return &PreviewBackfill{
		ps: ps, blobs: blobs, metadata: metadata, logger: logger,
		batch: previewBackfillBatch, pause: previewBackfillPause, interval: previewBackfillInterval,
	}
}

// Run does one pass immediately, then repeats every b.interval
// until ctx is cancelled. The repeat picks up rows whose blob arrived later
// through blob sync.
func (b *PreviewBackfill) Run(ctx context.Context) {
	for {
		b.RunPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.interval):
		}
	}
}

// RunPass walks every user once and returns the number of rows filled. Within
// a pass the guid cursor means a row whose blob is unreadable is tried once.
func (b *PreviewBackfill) RunPass(ctx context.Context) int {
	if b.metadata == nil || b.metadata.System == nil {
		return 0
	}
	ids, err := b.metadata.System.ListUserIDs()
	if err != nil {
		b.logger.Warn("preview-backfill: listing users failed", "error", err)
		return 0
	}
	filled := 0
	b.unreadable = 0
	for _, userID := range ids {
		n, ok := b.backfillUser(ctx, userID)
		filled += n
		if !ok {
			break
		}
	}
	if filled > 0 {
		b.logger.Info("preview-backfill: pass complete", "filled", filled)
	}
	if b.unreadable > 0 {
		b.logger.Warn("preview-backfill: messages with unreadable blobs left pending", "count", b.unreadable)
	}
	return filled
}

// backfillUser returns the rows filled and false if ctx was cancelled.
func (b *PreviewBackfill) backfillUser(ctx context.Context, userID string) (int, bool) {
	filled := 0
	cursor := ""
	for {
		if ctx.Err() != nil {
			return filled, false
		}
		guids, err := b.metadata.PendingPreviews(userID, cursor, b.batch)
		if err != nil {
			b.logger.Warn("preview-backfill: listing pending failed", "user", userID, "error", err)
			return filled, true
		}
		if len(guids) == 0 {
			return filled, true
		}
		for _, guid := range guids {
			if ctx.Err() != nil {
				return filled, false
			}
			cursor = guid
			if b.fill(userID, guid) {
				filled++
			}
			if b.afterFill != nil {
				b.afterFill()
			}
		}
		if b.afterBatch != nil {
			b.afterBatch()
		}
		select {
		case <-ctx.Done():
			return filled, false
		case <-time.After(b.pause):
		}
	}
}

func (b *PreviewBackfill) fill(userID, guid string) bool {
	data, err := b.ps.Get([]byte("/user/" + userID + "/mail/" + guid))
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			b.logger.Warn("preview-backfill: reading MailMeta failed", "user", userID, "guid", guid, "error", err)
			return false
		}
		// The message is gone; an empty preview stops the row being pending.
		return b.set(userID, guid, "", false)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		b.logger.Warn("preview-backfill: bad MailMeta", "user", userID, "guid", guid, "error", err)
		return false
	}
	_, preview, hasAtt, ok := mailContentAt(b.blobs, &meta, b.logger, slog.LevelDebug)
	if !ok {
		b.unreadable++
		return false
	}
	return b.set(userID, guid, preview, hasAtt)
}

func (b *PreviewBackfill) set(userID, guid, preview string, hasAtt bool) bool {
	if err := b.metadata.SetMailPreview(userID, guid, preview, hasAtt); err != nil {
		b.logger.Warn("preview-backfill: storing preview failed", "user", userID, "guid", guid, "error", err)
		return false
	}
	return true
}
