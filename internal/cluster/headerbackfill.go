package cluster

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	headerBackfillBatch    = 50
	headerBackfillPause    = 200 * time.Millisecond
	headerBackfillInterval = 30 * time.Minute
)

// indexMailHeaders fills the header-derived view columns (to_me, sender_addr,
// list_id, list_name) of an already indexed mail from its metadata. mine are
// the user's own addresses. It reads no blob, so the live indexer and a
// rebuild can call it for every message.
func indexMailHeaders(metadata *store.MetadataManager, userID, guid string, meta *bongov1.MailMeta, mine map[string]bool) error {
	return metadata.SetMailHeaders(userID, guid, store.MailHeaderFields(meta, mine))
}

// HeaderBackfill fills the header-derived view columns for mail indexed before
// they existed. It has the shape of PreviewBackfill: it runs on every node
// (the SQLite indexes are node-local and nothing here writes Raft state), a
// single goroutine works in small batches with a pause between them, and the
// state lives in the index itself (to_me IS NULL), so it resumes across
// restarts and never revisits a filled row.
//
// to_me is judged against the user's addresses as they are when the backfill
// reaches the row, not as they were at delivery: the addresses index is itself
// derived.
//
// Mail delivered before the List-Id header was recorded in MailMeta has an
// empty ListId; the backfill then reads the header from the blob. A blob that
// cannot be read does not leave the row pending: the columns that MailMeta can
// answer are written, so one lost blob cannot make every pass retry the row.
type HeaderBackfill struct {
	ps       *store.PebbleStore
	blobs    *store.BlobStore
	metadata *store.MetadataManager
	logger   *slog.Logger

	batch    int
	pause    time.Duration
	interval time.Duration
	// afterBatch, when set, runs after each batch (tests).
	afterBatch func()
}

// NewHeaderBackfill builds a backfill with production batch size and pacing.
func NewHeaderBackfill(ps *store.PebbleStore, blobs *store.BlobStore, metadata *store.MetadataManager, logger *slog.Logger) *HeaderBackfill {
	return &HeaderBackfill{
		ps: ps, blobs: blobs, metadata: metadata, logger: logger,
		batch: headerBackfillBatch, pause: headerBackfillPause, interval: headerBackfillInterval,
	}
}

// Run does one pass immediately, then repeats every b.interval until ctx is
// cancelled. The repeat picks up mail indexed by a node that lagged behind.
func (b *HeaderBackfill) Run(ctx context.Context) {
	for {
		b.RunPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.interval):
		}
	}
}

// RunPass walks every user once and returns the number of rows filled.
func (b *HeaderBackfill) RunPass(ctx context.Context) int {
	if b.metadata == nil || b.metadata.System == nil {
		return 0
	}
	ids, err := b.metadata.System.ListUserIDs()
	if err != nil {
		b.logger.Warn("header-backfill: listing users failed", "error", err)
		return 0
	}
	filled := 0
	for _, userID := range ids {
		n, ok := b.backfillUser(ctx, userID)
		filled += n
		if !ok {
			break
		}
	}
	if filled > 0 {
		b.logger.Info("header-backfill: pass complete", "filled", filled)
	}
	return filled
}

// backfillUser returns the rows filled and false if ctx was cancelled.
func (b *HeaderBackfill) backfillUser(ctx context.Context, userID string) (int, bool) {
	mine, err := b.metadata.UserAddresses(userID)
	if err != nil {
		b.logger.Warn("header-backfill: reading user addresses failed", "user", userID, "error", err)
		return 0, true
	}
	filled := 0
	cursor := ""
	for {
		if ctx.Err() != nil {
			return filled, false
		}
		guids, err := b.metadata.PendingHeaders(userID, cursor, b.batch)
		if err != nil {
			b.logger.Warn("header-backfill: listing pending failed", "user", userID, "error", err)
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
			if b.fill(userID, guid, mine) {
				filled++
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

func (b *HeaderBackfill) fill(userID, guid string, mine map[string]bool) bool {
	var meta bongov1.MailMeta
	data, err := b.ps.Get([]byte("/user/" + userID + "/mail/" + guid))
	switch {
	case errors.Is(err, store.ErrNotFound):
		// The message is gone; a derived row with to_me = 0 stops it being pending.
	case err != nil:
		b.logger.Warn("header-backfill: reading MailMeta failed", "user", userID, "guid", guid, "error", err)
		return false
	default:
		if err := proto.Unmarshal(data, &meta); err != nil {
			// Unreadable metadata will not heal on retry; record the row as
			// derived rather than loop on it.
			b.logger.Warn("header-backfill: bad MailMeta", "user", userID, "guid", guid, "error", err)
			meta.Reset()
		}
	}
	if meta.ListId == "" {
		meta.ListId = b.listIDFromBlob(&meta)
	}
	if err := indexMailHeaders(b.metadata, userID, guid, &meta, mine); err != nil {
		b.logger.Warn("header-backfill: storing headers failed", "user", userID, "guid", guid, "error", err)
		return false
	}
	return true
}

// listIDFromBlob reads the List-Id header of a message delivered before
// MailMeta recorded it. It returns "" when the blob is missing or unreadable.
func (b *HeaderBackfill) listIDFromBlob(meta *bongov1.MailMeta) string {
	if b.blobs == nil || meta.GetRawMessage().GetHash() == "" {
		return ""
	}
	raw, err := store.RawMessageBytes(b.blobs, meta)
	if err != nil {
		b.logger.Debug("header-backfill: blob unreadable", "guid", meta.GetGuid(), "error", err)
		return ""
	}
	env, _, err := mimesafe.ReadEnvelope(raw)
	if err != nil {
		return ""
	}
	return env.GetHeader("List-Id")
}
