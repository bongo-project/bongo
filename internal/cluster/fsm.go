package cluster

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// raftAppliedIndexKey stores the highest Raft log index already applied to
// Pebble. Pebble persists independently of the Raft log, so on restart Raft
// replays committed entries; without this watermark those entries would be
// re-applied, re-creating data that was deleted after it was first written.
const raftAppliedIndexKey = "/system/raft/applied_index"

// Command represents a legacy JSON Raft FSM command (kept for backward compat).
type Command struct {
	Op    string `json:"op"`
	Key   []byte `json:"key"`
	Value []byte `json:"value,omitempty"`
}

// SearchIndexer is the interface for full-text search indexing.
type SearchIndexer interface {
	IndexMail(userID, guid string, subject, from string, to []string, body string, receivedAt int64, hasAttachment bool, tags []string, spamScore ...float64) error
	IndexProfile(userID, email, displayName string) error
	DeleteMailIndex(userID, guid string) error
}

// BlobEnqueuer is the interface for blob sync enqueueing.
type BlobEnqueuer interface {
	Enqueue(hash string)
}

// MailboxNotifier is the interface for notifying IMAP sessions of new mail.
type MailboxNotifier interface {
	NotifyNewMail(userID, mailbox string, uid, existsCount uint32)
}

// FSM implements raft.FSM, applying committed log entries to Pebble.
type FSM struct {
	store    *store.PebbleStore
	blobs    *store.BlobStore
	metadata *store.MetadataManager
	logger   *slog.Logger

	// search, blobSync and notifier are injected by Agent.Run AFTER the FSM is
	// constructed and handed to Raft, so Raft's apply goroutine can already be
	// reading them while Run is still writing them -- a data race the race
	// detector reports as SetBlobSyncer against runDerivedIndexes. They are
	// interfaces, so an atomic.Pointer would need a wrapper per field; an
	// RWMutex read through the accessors below is simpler and the read side is
	// uncontended in practice.
	//
	// Read them ONLY via searchIndexer/blobSyncer/mailboxNotifier, never
	// directly: a direct read reintroduces the race silently.
	depsMu   sync.RWMutex
	search   SearchIndexer
	blobSync BlobEnqueuer
	notifier MailboxNotifier

	// indexQueue carries derived-index work off the Raft apply path. Guarded by
	// depsMu for the same reason as the fields above: StartIndexer assigns it
	// from Agent.Run while the apply goroutine is already reading it. Read it
	// only via indexChan. It is
	// nil until StartIndexer is called, and while nil the work runs inline —
	// so any caller that never starts the worker (every unit test, and any
	// embedder that wants synchronous indexes) keeps the original behaviour.
	indexQueue chan *bongov1.RaftCommand
	indexWG    sync.WaitGroup
	// indexInflight counts items queued but not yet processed, so WaitIdle
	// can tell "drained" from "still working".
	indexInflight atomic.Int64
}

// indexQueueDepth bounds the derived-index backlog. Sized generously: the work
// per item is milliseconds once a user's handles are open, so the queue only
// fills if indexing genuinely cannot keep up, at which point falling back to
// inline indexing (see updateDerivedIndexes) is the safe answer.
const indexQueueDepth = 4096

// slowIndexThreshold is the point at which derived indexing is considered
// anomalous and gets logged. Opening a user's SQLite and Bleve handles for the
// first time is the usual cause; it should still be well under this.
const slowIndexThreshold = time.Second

// slowApplyThreshold is the point at which a single FSM.Apply is considered
// anomalous. raft handles Apply and Snapshot on the same goroutine, so a slow
// apply blocks all writers.
const slowApplyThreshold = 2 * time.Second

// StartIndexer moves derived-index maintenance (per-user SQLite and Bleve)
// onto a background worker so it cannot block Raft's apply loop.
//
// The apply path looks bounded and is not: the 5s timeout callers pass via
// store.DeliverMail -> ApplyCommand bounds only ENQUEUEING the log entry (see
// ApplyCommand in forward.go), while the subsequent future.Error() waits with no
// deadline until the FSM has applied it. Indexing inline therefore let a slow
// index open stall mail delivery indefinitely — observed in production as an 85s
// delay on the first delivery after a restart, against 63ms for the same message
// once the user's handles were open.
//
// A single worker preserves apply order: a delete followed by a re-index of the
// same GUID must not be reordered. Safe to skip — with no worker started,
// indexing stays inline.
func (f *FSM) StartIndexer(ctx context.Context) {
	// Check-and-set under the lock: Agent.Run calls this while Raft's apply
	// goroutine is already reading indexQueue on every command, and the bare
	// check-then-assign was both a data race and a double-start window.
	f.depsMu.Lock()
	if f.indexQueue != nil {
		f.depsMu.Unlock()
		return
	}
	queue := make(chan *bongov1.RaftCommand, indexQueueDepth)
	f.indexQueue = queue
	f.depsMu.Unlock()

	// The worker closes over `queue` rather than reading f.indexQueue, so it
	// never touches the guarded field at all.
	f.indexWG.Add(1)
	go func() {
		defer f.indexWG.Done()
		for {
			select {
			case <-ctx.Done():
				// Drain what is already queued before returning: the entries
				// are committed in Raft, so dropping them would leave the
				// derived indexes behind until the next rebuild.
				for {
					select {
					case cmd := <-queue:
						f.runDerivedIndexes(cmd)
						f.indexInflight.Add(-1)
					default:
						return
					}
				}
			case cmd := <-queue:
				f.runDerivedIndexes(cmd)
				f.indexInflight.Add(-1)
			}
		}
	}()
}

// StopIndexer waits for the background indexer to finish draining. Call after
// cancelling the context passed to StartIndexer.
func (f *FSM) StopIndexer() {
	f.indexWG.Wait()
}

// WaitIdle blocks until the derived-index backlog is empty or timeout expires,
// and reports whether it drained. Intended for tests and shutdown, not for the
// delivery path.
func (f *FSM) WaitIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f.indexInflight.Load() == 0 {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return f.indexInflight.Load() == 0
}

// Compile-time interface check.
var _ raft.FSM = (*FSM)(nil)

// NewFSM creates a new FSM backed by the given stores.
func NewFSM(s *store.PebbleStore, blobs *store.BlobStore, meta *store.MetadataManager, logger *slog.Logger) *FSM {
	return &FSM{
		store:    s,
		blobs:    blobs,
		metadata: meta,
		logger:   logger,
	}
}

// SetSearchManager sets the search indexer (initialized after FSM).
func (f *FSM) SetSearchManager(s SearchIndexer) {
	f.depsMu.Lock()
	defer f.depsMu.Unlock()
	f.search = s
}

// SetBlobSyncer sets the blob syncer (initialized after FSM).
func (f *FSM) SetBlobSyncer(bs BlobEnqueuer) {
	f.depsMu.Lock()
	defer f.depsMu.Unlock()
	f.blobSync = bs
}

// SetNotifier sets the mailbox notifier (initialized after FSM).
func (f *FSM) SetNotifier(n MailboxNotifier) {
	f.depsMu.Lock()
	defer f.depsMu.Unlock()
	f.notifier = n
}

// searchIndexer returns the current search indexer, or nil if none is set.
// Callers snapshot once and use the returned value: re-reading mid-operation
// could observe a different indexer partway through.
func (f *FSM) searchIndexer() SearchIndexer {
	f.depsMu.RLock()
	defer f.depsMu.RUnlock()
	return f.search
}

// blobSyncer returns the current blob syncer, or nil if none is set.
func (f *FSM) blobSyncer() BlobEnqueuer {
	f.depsMu.RLock()
	defer f.depsMu.RUnlock()
	return f.blobSync
}

// indexChan returns the derived-index queue, or nil while the indexer has not
// been started (in which case callers index inline).
func (f *FSM) indexChan() chan *bongov1.RaftCommand {
	f.depsMu.RLock()
	defer f.depsMu.RUnlock()
	return f.indexQueue
}

// mailboxNotifier returns the current notifier, or nil if none is set.
func (f *FSM) mailboxNotifier() MailboxNotifier {
	f.depsMu.RLock()
	defer f.depsMu.RUnlock()
	return f.notifier
}

// appliedIndex returns the highest Raft index already applied to Pebble, or 0.
func (f *FSM) appliedIndex() uint64 {
	v, err := f.store.Get([]byte(raftAppliedIndexKey))
	if err != nil || len(v) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}

// setAppliedIndex records the highest Raft index applied to Pebble.
func (f *FSM) setAppliedIndex(idx uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], idx)
	if err := f.store.Set([]byte(raftAppliedIndexKey), buf[:]); err != nil {
		f.logger.Warn("fsm: failed to persist applied index", "index", idx, "error", err)
	}
}

// Apply applies a committed Raft log entry to the Pebble store. Entries at or
// below the persisted applied index are skipped so that log replay on restart
// does not re-apply (and thereby resurrect deleted) data.
func (f *FSM) Apply(log *raft.Log) interface{} {
	// Timed so a slow apply names itself. raft serialises Apply and Snapshot on
	// one goroutine, so anything slow here stalls every writer — and raft's own
	// logs say nothing about time spent inside the FSM.
	start := time.Now()
	defer func() {
		if elapsed := time.Since(start); elapsed >= slowApplyThreshold {
			f.logger.Warn("fsm: apply was slow",
				"index", log.Index,
				"duration_ms", elapsed.Milliseconds(),
			)
		}
	}()

	if log.Index != 0 && log.Index <= f.appliedIndex() {
		return nil
	}
	// applyEntryFn is indirected so a test can simulate a crash inside apply and
	// observe that the watermark has NOT advanced. The ordering below is the
	// whole guarantee: persist the entry first, record it as applied second.
	// Reversed, a crash in the gap leaves a watermark claiming an entry is
	// durable that was never written, and the replay-skip at the top of this
	// function then drops it silently. See TestFSMAppliedIndexNotAdvancedOnCrash.
	result := applyEntryFn(f, log)
	if log.Index != 0 {
		f.setAppliedIndex(log.Index)
	}
	return result
}

// applyEntryFn indirects FSM.applyEntry so tests can substitute it. Production
// always uses the method; see the call site in Apply for why the seam exists.
var applyEntryFn = (*FSM).applyEntry

// applyEntry applies a single committed Raft log entry to the Pebble store,
// decoding it as a protobuf RaftCommand and falling back to the legacy JSON
// format.
func (f *FSM) applyEntry(log *raft.Log) interface{} {
	var cmd bongov1.RaftCommand
	if err := proto.Unmarshal(log.Data, &cmd); err == nil && cmd.Op != bongov1.Op_OP_UNSPECIFIED {
		return f.applyProto(&cmd)
	}

	var legacyCmd Command
	if err := json.Unmarshal(log.Data, &legacyCmd); err != nil {
		f.logger.Error("fsm: failed to unmarshal command", "error", err)
		return fmt.Errorf("unmarshal command: %w", err)
	}

	var err error
	switch legacyCmd.Op {
	case "put":
		err = f.store.Set(legacyCmd.Key, legacyCmd.Value)
	case "delete":
		err = f.store.Delete(legacyCmd.Key)
	default:
		err = fmt.Errorf("unknown op: %q", legacyCmd.Op)
	}

	if err != nil {
		f.logger.Error("fsm: apply failed", "op", legacyCmd.Op, "key", string(legacyCmd.Key), "error", err)
		return err
	}

	f.logger.Debug("fsm: applied (json)", "op", legacyCmd.Op, "key", string(legacyCmd.Key))
	f.updateDerivedIndexes(&bongov1.RaftCommand{
		Op:    opStringToProto(legacyCmd.Op),
		Key:   legacyCmd.Key,
		Value: legacyCmd.Value,
	})
	return nil
}

func opStringToProto(op string) bongov1.Op {
	switch op {
	case "put":
		return bongov1.Op_OP_PUT
	case "delete":
		return bongov1.Op_OP_DELETE
	default:
		return bongov1.Op_OP_UNSPECIFIED
	}
}

// applyProto handles a protobuf-encoded RaftCommand.
func (f *FSM) applyProto(cmd *bongov1.RaftCommand) interface{} {
	if cmd.Op == bongov1.Op_OP_PUT && store.IsMailboxSyntheticCommand(cmd.Key) {
		result, err := store.ApplyMailboxCommand(f.store, cmd)
		if err != nil {
			f.logger.Error("fsm: mailbox synthetic command failed", "key", string(cmd.Key), "error", err)
			return err
		}
		f.logger.Debug("fsm: mailbox synthetic command applied", "key", string(cmd.Key))
		f.handleMailboxCommandResult(result)
		return nil
	}

	// Queue transitions (retry backoff, bounce, retry-now) arrive as a single
	// composite command so the delete of the old key and the write of the new
	// one commit together. Done as two separate commands, a crash between them
	// removed the message from the queue without writing it back: silent mail
	// loss with no error to act on.
	if cmd.Op == bongov1.Op_OP_PUT && store.IsQueueSyntheticCommand(cmd.Key) {
		if err := store.ApplyQueueCommand(f.store, cmd); err != nil {
			f.logger.Error("fsm: queue synthetic command failed", "key", string(cmd.Key), "error", err)
			return err
		}
		f.logger.Debug("fsm: queue synthetic command applied", "key", string(cmd.Key))
		return nil
	}

	var err error
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		err = f.store.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		err = f.store.Delete(cmd.Key)
	default:
		err = fmt.Errorf("unknown op: %v", cmd.Op)
	}

	if err != nil {
		f.logger.Error("fsm: apply failed", "op", cmd.Op.String(), "key", string(cmd.Key), "error", err)
		return err
	}

	f.logger.Debug("fsm: applied", "op", cmd.Op.String(), "key", string(cmd.Key))

	f.updateDerivedIndexes(cmd)
	return nil
}

func (f *FSM) handleMailboxCommandResult(result *store.MailboxCommandResult) {
	if result == nil || result.NewMail == nil {
		return
	}
	newMail := result.NewMail

	if newMail.Meta != nil {
		metaBytes, err := proto.Marshal(newMail.Meta)
		if err != nil {
			f.logger.Warn("fsm: failed to marshal delivered MailMeta for indexing", "error", err, "user", newMail.UserID, "guid", newMail.GUID)
		} else {
			f.updateDerivedIndexes(&bongov1.RaftCommand{
				Op:    bongov1.Op_OP_PUT,
				Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", newMail.UserID, newMail.GUID)),
				Value: metaBytes,
			})
		}
	}

	if notifier := f.mailboxNotifier(); notifier != nil {
		// Count total messages for the IMAP EXISTS response.
		var totalMsgs uint32
		scanStart := time.Now()
		prefix := []byte(fmt.Sprintf("/user/%s/mailbox/%s/msg/", newMail.UserID, newMail.Mailbox))
		iter, err := f.store.PrefixScan(prefix)
		if err == nil {
			for iter.Next() {
				totalMsgs++
			}
			if err := iter.Close(); err != nil {
				f.logger.Warn("fsm: failed to close mailbox count iterator", "user", newMail.UserID, "mailbox", newMail.Mailbox, "error", err)
			}
		}
		if elapsed := time.Since(scanStart); elapsed >= slowApplyThreshold {
			// This scan is O(mailbox size) and runs inside the apply path for
			// every delivery, purely to produce an IMAP EXISTS count.
			f.logger.Warn("fsm: mailbox message count scan was slow",
				"user", newMail.UserID,
				"mailbox", newMail.Mailbox,
				"messages", totalMsgs,
				"duration_ms", elapsed.Milliseconds(),
			)
		}
		notifier.NotifyNewMail(newMail.UserID, newMail.Mailbox, newMail.UID, totalMsgs)
	}
}

// updateDerivedIndexes updates SQLite and Bleve indexes based on the applied
// key. Only per-user mail and event indexing is deferred to the background
// worker: it is the hot path and touches only per-user databases, so it is both
// the part worth moving and the part that is safe to move.
//
// Profile indexing stays inline because it writes the SHARED system.db, which
// callers also write directly (user creation inserts a row there). While
// indexing ran inside Apply those two could not overlap, because the caller was
// blocked on the apply; deferring it introduced real write-write contention and
// SQLITE_BUSY failures on user creation. Profile updates are rare, so inline
// costs nothing and keeps that serialisation intact. Blob-ref enqueueing stays
// inline too: it is a non-blocking channel send, already fast, with nothing to
// gain from deferral.
func (f *FSM) updateDerivedIndexes(cmd *bongov1.RaftCommand) {
	queue := f.indexChan()
	if queue == nil || !isDeferrableIndexKey(cmd.Key) {
		f.runDerivedIndexes(cmd)
		return
	}

	// The command is retained by the queue, so hand the worker its own copy
	// rather than a value the caller may reuse or mutate after Apply returns.
	queued := &bongov1.RaftCommand{
		Op:    cmd.Op,
		Key:   append([]byte(nil), cmd.Key...),
		Value: append([]byte(nil), cmd.Value...),
	}

	f.indexInflight.Add(1)
	select {
	case queue <- queued:
	default:
		// Backlog full. Index inline rather than drop: a dropped update would
		// leave a message unsearchable until the next rebuild, and the entry
		// is already committed. This reintroduces the original blocking
		// behaviour, so it is logged as the capacity signal it is.
		f.indexInflight.Add(-1)
		f.logger.Warn("fsm: derived index queue full, indexing inline",
			"depth", indexQueueDepth,
			"key", string(cmd.Key),
		)
		f.runDerivedIndexes(cmd)
	}
}

// isDeferrableIndexKey reports whether a key's derived-index work may run on
// the background worker. Only per-user mail and event objects qualify — see
// updateDerivedIndexes for why profile keys must not.
func isDeferrableIndexKey(key []byte) bool {
	k := string(key)
	if !strings.HasPrefix(k, "/user/") || strings.HasSuffix(k, "/profile") {
		return false
	}
	parts := strings.SplitN(k, "/", 5) // ["", "user", "{id}", "{type}", "{guid}"]
	if len(parts) < 5 {
		return false
	}
	return parts[3] == "mail" || parts[3] == "event"
}

// runDerivedIndexes performs the actual derived-index maintenance. It runs on
// the background worker when one is started, and inline otherwise.
func (f *FSM) runDerivedIndexes(cmd *bongov1.RaftCommand) {
	start := time.Now()
	defer func() {
		// Derived indexing used to be invisible, which is why an 85s stall in
		// here could only be attributed by decoding a spool envelope after the
		// fact. Anything above the threshold now says so at the time.
		if elapsed := time.Since(start); elapsed >= slowIndexThreshold {
			f.logger.Warn("fsm: derived index update was slow",
				"key", string(cmd.Key),
				"op", cmd.Op.String(),
				"duration_ms", elapsed.Milliseconds(),
			)
		}
	}()

	key := string(cmd.Key)

	if strings.HasPrefix(key, "/user/") {
		parts := strings.SplitN(key, "/", 5) // ["", "user", "{id}", "{type}", "{guid}"]
		if len(parts) >= 5 {
			userID := parts[2]
			objType := parts[3]
			guid := parts[4]

			switch objType {
			case "mail":
				f.indexStoreObject(userID, guid, store.DocTypeMail, cmd)
			case "event":
				f.indexStoreObject(userID, guid, store.DocTypeEvent, cmd)
			}
		}

		if strings.HasSuffix(key, "/profile") && len(parts) >= 4 {
			userID := parts[2]
			f.indexProfile(userID, cmd)
		}
	}

	if cmd.Op == bongov1.Op_OP_PUT && f.blobSyncer() != nil {
		f.enqueueBlobRefs(cmd)
	}
}

// indexStoreObject updates the per-user SQLite and Bleve indexes for a mail or event.
func (f *FSM) indexStoreObject(userID, guid string, docType int, cmd *bongov1.RaftCommand) {
	if f.metadata == nil {
		return
	}

	if cmd.Op == bongov1.Op_OP_DELETE {
		f.logger.Debug("fsm: derived index delete", "user", userID, "guid", guid)
		if docType == store.DocTypeMail {
			if err := f.metadata.DeleteMail(userID, guid); err != nil {
				f.logger.Warn("fsm: failed to delete mail from sqlite index", "error", err, "user", userID, "guid", guid)
			}
			if search := f.searchIndexer(); search != nil {
				if err := search.DeleteMailIndex(userID, guid); err != nil {
					f.logger.Warn("fsm: failed to delete mail from bleve", "error", err, "user", userID, "guid", guid)
				}
			}
		}
		return
	}

	if docType == store.DocTypeMail {
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(cmd.Value, &meta); err != nil {
			f.logger.Warn("fsm: failed to parse MailMeta", "error", err, "user", userID, "guid", guid)
			return
		}

		if err := f.metadata.IndexMail(userID, guid, meta.Subject, meta.From, meta.ReceivedAt, meta.GetRawMessage().GetSize()); err != nil {
			f.logger.Warn("fsm: failed to index mail in sqlite", "error", err, "user", userID, "guid", guid)
		}

		if search := f.searchIndexer(); search != nil {
			hasAttachment := len(meta.Attachments) > 0
			// Body text lives only in the blob, so indexing it costs a read
			// and a MIME parse. That is affordable here and nowhere else:
			// runDerivedIndexes is the background indexer, off the Raft apply
			// path (see StartIndexer), so this cannot stall delivery.
			body := mailBodyText(f.blobs, &meta, f.logger)
			if err := search.IndexMail(userID, guid, meta.Subject, meta.From, meta.To, body, meta.ReceivedAt, hasAttachment, meta.Tags, float64(meta.SpamScore)); err != nil {
				f.logger.Warn("fsm: failed to index mail in bleve", "error", err, "user", userID, "guid", guid)
			}
		}
	} else {
		f.logger.Debug("fsm: derived index update deferred for non-mail type", "user", userID, "guid", guid, "type", docType)
	}
}

// indexProfile updates system.db and GAL index on profile changes.
func (f *FSM) indexProfile(userID string, cmd *bongov1.RaftCommand) {
	if cmd.Op == bongov1.Op_OP_DELETE {
		return
	}

	var profile bongov1.UserProfile
	if err := proto.Unmarshal(cmd.Value, &profile); err != nil {
		f.logger.Warn("fsm: failed to parse UserProfile", "error", err, "user", userID)
		return
	}

	if f.metadata != nil && f.metadata.System != nil {
		_, err := f.metadata.System.DB().Exec(`
			INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
			VALUES (?, ?, ?, 1, unixepoch(), unixepoch())
			ON CONFLICT(id) DO UPDATE SET
				display_name = excluded.display_name,
				modified_at = unixepoch()
		`, userID, profile.Email, profile.DisplayName)
		if err != nil {
			f.logger.Warn("fsm: failed to update system.db user", "error", err, "user", userID)
		}
	}

	if search := f.searchIndexer(); search != nil {
		if err := search.IndexProfile(userID, profile.Email, profile.DisplayName); err != nil {
			f.logger.Warn("fsm: failed to index profile in GAL", "error", err, "user", userID)
		}
	}
}

// enqueueBlobRefs parses blob references from the value and enqueues them for sync.
func (f *FSM) enqueueBlobRefs(cmd *bongov1.RaftCommand) {
	key := string(cmd.Key)

	// Only mail keys have blob refs for now.
	if !strings.Contains(key, "/mail/") {
		return
	}

	var meta bongov1.MailMeta
	if err := proto.Unmarshal(cmd.Value, &meta); err != nil {
		return
	}

	blobSync := f.blobSyncer()
	if blobSync == nil {
		return
	}
	if meta.RawMessage != nil && meta.RawMessage.Hash != "" {
		blobSync.Enqueue(meta.RawMessage.Hash)
	}
	for _, att := range meta.Attachments {
		if att.Hash != "" {
			blobSync.Enqueue(att.Hash)
		}
	}
}

// Snapshot returns an FSMSnapshot that captures the current Pebble state.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	checkpointDir, err := os.MkdirTemp("", "bongo-snapshot-*")
	if err != nil {
		return nil, fmt.Errorf("creating snapshot temp dir: %w", err)
	}

	destDir := filepath.Join(checkpointDir, "pebble")
	if err := f.store.DB().Checkpoint(destDir); err != nil {
		if removeErr := os.RemoveAll(checkpointDir); removeErr != nil {
			f.logger.Warn("fsm: failed to remove failed snapshot checkpoint", "dir", checkpointDir, "error", removeErr)
		}
		return nil, fmt.Errorf("creating pebble checkpoint: %w", err)
	}

	f.logger.Info("fsm: snapshot checkpoint created", "dir", destDir)

	return &fsmSnapshot{
		checkpointDir: checkpointDir,
		pebbleDir:     destDir,
		logger:        f.logger,
	}, nil
}

// Restore restores the FSM from a snapshot.
func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	restoreDir, err := os.MkdirTemp("", "bongo-restore-*")
	if err != nil {
		return fmt.Errorf("creating restore temp dir: %w", err)
	}
	defer os.RemoveAll(restoreDir)

	pebbleDir := filepath.Join(restoreDir, "pebble")
	if err := os.MkdirAll(pebbleDir, 0o750); err != nil {
		return fmt.Errorf("creating restore pebble dir: %w", err)
	}

	if err := untar(rc, pebbleDir); err != nil {
		return fmt.Errorf("extracting snapshot: %w", err)
	}

	restored, err := store.NewPebbleStore(pebbleDir)
	if err != nil {
		return fmt.Errorf("opening restored pebble: %w", err)
	}
	defer restored.Close()

	iter, err := restored.PrefixScan([]byte("/"))
	if err != nil {
		return fmt.Errorf("scanning restored data: %w", err)
	}
	defer iter.Close()

	// Restore must REPLACE the FSM state with the snapshot (raft.FSM contract),
	// not merge into it. Clear the existing keyspace and load the snapshot keys
	// in one atomic batch: merging would let keys deleted after the snapshot
	// survive (resurrecting them) and leave live-only keys behind.
	db := f.store.DB()
	batch := db.NewBatch()
	defer batch.Close()

	// Every key lives under the "/" prefix, so ["/", "0") spans all of them.
	if err := batch.DeleteRange([]byte("/"), []byte("0"), nil); err != nil {
		return fmt.Errorf("clearing store before restore: %w", err)
	}

	count := 0
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			return fmt.Errorf("reading restored value: %w", err)
		}
		// Batch.Set copies key and value, so iterator-owned buffers are safe.
		if err := batch.Set(iter.Key(), val, nil); err != nil {
			return fmt.Errorf("staging restored key: %w", err)
		}
		count++
	}

	if err := db.Apply(batch, pebble.Sync); err != nil {
		return fmt.Errorf("applying restore batch: %w", err)
	}

	f.logger.Info("fsm: restored from snapshot", "keys", count)

	if err := rebuildDerivedIndexes(f.store, f.blobs, f.metadata, f.searchIndexer(), f.logger); err != nil {
		f.logger.Warn("fsm: rebuild derived indexes after restore failed", "error", err)
	}

	return nil
}

// fsmSnapshot implements raft.FSMSnapshot using a Pebble checkpoint.
type fsmSnapshot struct {
	checkpointDir string
	pebbleDir     string
	logger        *slog.Logger
}

var _ raft.FSMSnapshot = (*fsmSnapshot)(nil)

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	if err := tarDir(s.pebbleDir, sink); err != nil {
		_ = sink.Cancel()
		return fmt.Errorf("persisting snapshot: %w", err)
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {
	if s.checkpointDir != "" {
		if err := os.RemoveAll(s.checkpointDir); err != nil {
			s.logger.Warn("fsm: failed to clean up snapshot checkpoint", "dir", s.checkpointDir, "error", err)
			return
		}
		s.logger.Debug("fsm: snapshot checkpoint cleaned up", "dir", s.checkpointDir)
	}
}
