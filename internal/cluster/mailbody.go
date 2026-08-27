package cluster

import (
	"bytes"
	"log/slog"

	"github.com/jhillyerd/enmime"

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

// mailBodyText returns the plain-text body of a stored message for full-text
// indexing, or "" when it cannot be recovered.
//
// Body text is not carried in MailMeta -- it lives only in the blob -- so this
// reads and MIME-parses the message. That is why it belongs on the background
// indexer and the rebuild path, and must never be called from FSM.Apply:
// raft serialises Apply, so a blob read there stalls every writer.
//
// Every failure degrades to "" rather than propagating. A message that will not
// parse should still be searchable by its subject and correspondents, and an
// unreadable blob is a problem for the blob store to report, not something to
// fail an index update over.
func mailBodyText(blobs *store.BlobStore, meta *bongov1.MailMeta, logger *slog.Logger) string {
	if blobs == nil || meta.GetRawMessage().GetHash() == "" {
		return ""
	}

	raw, err := store.RawMessageBytes(blobs, meta)
	if err != nil {
		logger.Warn("index: could not read message body for indexing",
			"guid", meta.GetGuid(), "error", err)
		return ""
	}

	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		logger.Warn("index: could not parse message for body indexing",
			"guid", meta.GetGuid(), "error", err)
		return ""
	}

	text := env.Text
	if len(text) > maxIndexedBodyBytes {
		text = text[:maxIndexedBodyBytes]
	}
	return text
}
