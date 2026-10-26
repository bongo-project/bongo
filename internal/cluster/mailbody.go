package cluster

import (
	"context"
	"log/slog"

	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// maxIndexedBodyBytes caps how much body text is handed to Bleve for a single
// message.
//
// This is a guard against pathological input, not an indexing strategy: at 1MB
// of extracted text a message is already far past anything a human wrote, and
// the alternative is letting one mail dictate the size of a user's index and
// the memory used to build it. Ordinary mail is nowhere near the cap.
const maxIndexedBodyBytes = 1 << 20

// mailContent reads and MIME-parses a stored message once and returns what the
// derived indexes need from it: the body text for full-text indexing, and the
// list-view preview and attachment flag for the SQLite index.
//
// Body text is not carried in MailMeta -- it lives only in the blob -- so this
// reads and MIME-parses the message. That is why it belongs on the background
// indexer, the rebuild path and the preview backfill, and must never be called
// from FSM.Apply: raft serialises Apply, so a blob read there stalls every
// writer.
//
// ok is false only when the content may yet become readable: no blob store is
// configured, or the blob could not be read (absent, or not yet synced to this
// node). The caller must then leave the preview unset so the backfill retries,
// rather than record an empty one. ok is true with empty results when nothing
// will ever be readable: the message has no raw-message hash (there is no blob
// to wait for, so record an empty preview), or it reads but will not parse
// (still searchable by subject and correspondents; a retry cannot succeed).
func mailContent(blobs *store.BlobStore, meta *bongov1.MailMeta, logger *slog.Logger) (body, preview string, hasAttachments, ok bool) {
	return mailContentAt(blobs, meta, logger, slog.LevelWarn)
}

// mailContentAt is mailContent with the level used for an unreadable blob. The
// backfill passes Debug and reports a per-pass count instead, so a blob that
// never arrives does not log on every pass.
func mailContentAt(blobs *store.BlobStore, meta *bongov1.MailMeta, logger *slog.Logger, unreadable slog.Level) (body, preview string, hasAttachments, ok bool) {
	if blobs == nil {
		return "", "", false, false
	}
	if meta.GetRawMessage().GetHash() == "" {
		return "", "", false, true
	}

	raw, err := store.RawMessageBytes(blobs, meta)
	if err != nil {
		logger.Log(context.Background(), unreadable, "index: could not read message body for indexing",
			"guid", meta.GetGuid(), "error", err)
		return "", "", false, false
	}

	env, _, err := mimesafe.ReadEnvelope(raw)
	if err != nil {
		logger.Warn("index: could not parse message for body indexing",
			"guid", meta.GetGuid(), "error", err)
		return "", "", false, true
	}

	body = env.Text
	if len(body) > maxIndexedBodyBytes {
		body = body[:maxIndexedBodyBytes]
	}
	preview, hasAttachments = store.MailPreview(env)
	return body, preview, hasAttachments, true
}
