package store

import (
	"fmt"
	"sync"
)

// Default limits for a MailBatcher. The byte cap bounds memory when many
// loader shards each hold a pending batch of large bodies.
const (
	DefaultBatchDocs  = 256
	DefaultBatchBytes = 8 << 20
)

type pendingMail struct {
	guid string
	doc  MailDocument
}

// MailBatcher buffers mail documents and writes them to a user's Bleve index
// as one batch, so Scorch persists and merges once per batch instead of once
// per message. It exists for the offline bulk loader only: the running server
// indexes each message on its own, and nothing outside the loader may
// construct one (a source-scan test enforces that). It assumes a single
// writer per user, which the loader guarantees by loading a user on one shard.
//
// It satisfies the FSM's search interface. Mail documents are deferred;
// deletes flush first so apply order is kept; profile calls pass through.
// Close the SearchManager only after Flush.
type MailBatcher struct {
	sm       *SearchManager
	maxDocs  int
	maxBytes int

	mu      sync.Mutex
	user    string
	pending []pendingMail
	bytes   int
	err     error // first flush error, returned by every later call
}

// NewMailBatcher returns a batcher over sm. Non-positive limits take the
// defaults.
func (sm *SearchManager) NewMailBatcher(maxDocs, maxBytes int) *MailBatcher {
	if maxDocs <= 0 {
		maxDocs = DefaultBatchDocs
	}
	if maxBytes <= 0 {
		maxBytes = DefaultBatchBytes
	}
	return &MailBatcher{sm: sm, maxDocs: maxDocs, maxBytes: maxBytes}
}

// IndexMail buffers the document and flushes when the user changes or a
// limit is reached.
func (b *MailBatcher) IndexMail(userID, guid string, subject, from string, to []string, body string, receivedAt int64, hasAttachment bool, tags []string, spamScore ...float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	if b.user != userID && len(b.pending) > 0 {
		if err := b.flushLocked(); err != nil {
			return err
		}
	}
	b.user = userID
	b.pending = append(b.pending, pendingMail{guid, newMailDocument(subject, from, to, body, receivedAt, hasAttachment, tags, spamScore)})
	b.bytes += len(body) + len(subject) + len(from)
	if len(b.pending) >= b.maxDocs || b.bytes >= b.maxBytes {
		return b.flushLocked()
	}
	return nil
}

// DeleteMailIndex flushes first so a delete never overtakes an earlier index.
func (b *MailBatcher) DeleteMailIndex(userID, guid string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.flushLocked(); err != nil {
		return err
	}
	return b.sm.DeleteMailIndex(userID, guid)
}

// IndexProfile passes through: the GAL is not batched.
func (b *MailBatcher) IndexProfile(userID, email, displayName string) error {
	return b.sm.IndexProfile(userID, email, displayName)
}

// DeleteProfile passes through.
func (b *MailBatcher) DeleteProfile(userID string) error {
	return b.sm.DeleteProfile(userID)
}

// Flush writes everything buffered. It returns the first error any flush
// hit, including an automatic one.
func (b *MailBatcher) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.flushLocked()
}

func (b *MailBatcher) flushLocked() error {
	if b.err != nil {
		return b.err
	}
	if len(b.pending) == 0 {
		return nil
	}
	// Error text names the operation only: documents hold message bodies and
	// addresses.
	idx, err := b.sm.openUser(b.user)
	if err != nil {
		b.err = fmt.Errorf("mail batch: %w", err)
		return b.err
	}
	batch := idx.NewBatch()
	for _, p := range b.pending {
		if err := batch.Index(p.guid, p.doc); err != nil {
			b.err = fmt.Errorf("mail batch: add document: %w", err)
			return b.err
		}
	}
	if err := idx.Batch(batch); err != nil {
		b.err = fmt.Errorf("mail batch: write: %w", err)
		return b.err
	}
	b.pending = b.pending[:0]
	b.bytes = 0
	return nil
}
