package cluster

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/store"
)

// Command represents a legacy JSON Raft FSM command (kept for backward compat).
type Command struct {
	Op    string `json:"op"`
	Key   []byte `json:"key"`
	Value []byte `json:"value,omitempty"`
}

// SearchIndexer is the interface for full-text search indexing.
type SearchIndexer interface {
	IndexMail(userID, guid string, subject, from string, to []string, body string, receivedAt int64, hasAttachment bool) error
	IndexProfile(userID, email, displayName string) error
}

// BlobEnqueuer is the interface for blob sync enqueueing.
type BlobEnqueuer interface {
	Enqueue(hash string)
}

// FSM implements raft.FSM, applying committed log entries to Pebble.
type FSM struct {
	store    *store.PebbleStore
	blobs    *store.BlobStore
	metadata *store.MetadataManager
	logger   *slog.Logger

	search   SearchIndexer
	blobSync BlobEnqueuer
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
	f.search = s
}

// SetBlobSyncer sets the blob syncer (initialized after FSM).
func (f *FSM) SetBlobSyncer(bs BlobEnqueuer) {
	f.blobSync = bs
}

// Apply applies a committed Raft log entry to the Pebble store.
func (f *FSM) Apply(log *raft.Log) interface{} {
	// Try protobuf first.
	var cmd bongov1.RaftCommand
	if err := proto.Unmarshal(log.Data, &cmd); err == nil && cmd.Op != bongov1.Op_OP_UNSPECIFIED {
		return f.applyProto(&cmd)
	}

	// Fall back to legacy JSON.
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

// updateDerivedIndexes updates SQLite and Bleve indexes based on the applied key.
func (f *FSM) updateDerivedIndexes(cmd *bongov1.RaftCommand) {
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

		// Profile updates: refresh system.db + GAL index.
		if strings.HasSuffix(key, "/profile") && len(parts) >= 4 {
			userID := parts[2]
			f.indexProfile(userID, cmd)
		}
	}

	// Enqueue blob references for sync.
	if cmd.Op == bongov1.Op_OP_PUT && f.blobSync != nil {
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
		return
	}

	if docType == store.DocTypeMail {
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(cmd.Value, &meta); err != nil {
			f.logger.Warn("fsm: failed to parse MailMeta", "error", err, "user", userID, "guid", guid)
			return
		}

		// Index into per-user SQLite.
		if err := f.metadata.IndexMail(userID, guid, meta.Subject, meta.From, meta.ReceivedAt, meta.GetRawMessage().GetSize()); err != nil {
			f.logger.Warn("fsm: failed to index mail in sqlite", "error", err, "user", userID, "guid", guid)
		}

		// Index into Bleve.
		if f.search != nil {
			hasAttachment := len(meta.Attachments) > 0
			if err := f.search.IndexMail(userID, guid, meta.Subject, meta.From, meta.To, "", meta.ReceivedAt, hasAttachment); err != nil {
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

	// Update system.db users table.
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

	// Update GAL index.
	if f.search != nil {
		if err := f.search.IndexProfile(userID, profile.Email, profile.DisplayName); err != nil {
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

	if meta.RawMessage != nil && meta.RawMessage.Hash != "" {
		f.blobSync.Enqueue(meta.RawMessage.Hash)
	}
	for _, att := range meta.Attachments {
		if att.Hash != "" {
			f.blobSync.Enqueue(att.Hash)
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
		os.RemoveAll(checkpointDir)
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

	count := 0
	for iter.Next() {
		key := iter.Key()
		val, err := iter.Value()
		if err != nil {
			return fmt.Errorf("reading restored value: %w", err)
		}
		if err := f.store.Set(key, val); err != nil {
			return fmt.Errorf("restoring key %s: %w", key, err)
		}
		count++
	}

	f.logger.Info("fsm: restored from snapshot", "keys", count)

	// Rebuild derived indexes (SQLite + Bleve) from restored data.
	if err := rebuildDerivedIndexes(f.store, f.metadata, f.search, f.logger); err != nil {
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
		sink.Cancel()
		return fmt.Errorf("persisting snapshot: %w", err)
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {
	if s.checkpointDir != "" {
		os.RemoveAll(s.checkpointDir)
		s.logger.Debug("fsm: snapshot checkpoint cleaned up", "dir", s.checkpointDir)
	}
}
