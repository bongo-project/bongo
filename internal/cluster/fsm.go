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

	"github.com/bongo-go/bongo-go/internal/store"
)

// Command represents a Raft FSM command.
// JSON-encoded for now; will switch to protobuf in a later sprint.
type Command struct {
	Op    string `json:"op"`    // "put" or "delete"
	Key   []byte `json:"key"`
	Value []byte `json:"value,omitempty"`
}

// FSM implements raft.FSM, applying committed log entries to Pebble.
type FSM struct {
	store    *store.PebbleStore
	blobs    *store.BlobStore
	metadata *store.MetadataManager
	logger   *slog.Logger
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

// Apply applies a committed Raft log entry to the Pebble store.
func (f *FSM) Apply(log *raft.Log) interface{} {
	var cmd Command
	if err := json.Unmarshal(log.Data, &cmd); err != nil {
		f.logger.Error("fsm: failed to unmarshal command", "error", err)
		return fmt.Errorf("unmarshal command: %w", err)
	}

	var err error
	switch cmd.Op {
	case "put":
		err = f.store.Set(cmd.Key, cmd.Value)
	case "delete":
		err = f.store.Delete(cmd.Key)
	default:
		err = fmt.Errorf("unknown op: %q", cmd.Op)
	}

	if err != nil {
		f.logger.Error("fsm: apply failed", "op", cmd.Op, "key", string(cmd.Key), "error", err)
		return err
	}

	f.logger.Debug("fsm: applied", "op", cmd.Op, "key", string(cmd.Key))

	// Trigger derived index updates.
	f.updateDerivedIndexes(cmd)

	return nil
}

// updateDerivedIndexes updates SQLite indexes based on the applied key.
func (f *FSM) updateDerivedIndexes(cmd Command) {
	key := string(cmd.Key)

	// Parse user mail/event keys: /user/{id}/mail/{guid} or /user/{id}/event/{guid}
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

		// Check for profile updates: /user/{id}/profile
		if strings.HasSuffix(key, "/profile") {
			// Profile updates would refresh system.db users table.
			// Actual implementation deferred until we have the profile protobuf format.
			f.logger.Debug("fsm: profile key updated, system.db refresh deferred", "key", key)
		}
	}

	// Check blob references in the value.
	if cmd.Op == "put" && f.blobs != nil {
		f.checkBlobReferences(cmd)
	}
}

// indexStoreObject updates the per-user SQLite index for a mail or event.
func (f *FSM) indexStoreObject(userID, guid string, docType int, cmd Command) {
	if f.metadata == nil {
		return
	}

	if cmd.Op == "delete" {
		// Deletion from derived index — we'd need a DeleteObject method.
		// For now, log it. Full implementation comes with protocol handlers.
		f.logger.Debug("fsm: derived index delete deferred", "user", userID, "guid", guid)
		return
	}

	// For put operations, we'd parse the protobuf value to extract metadata.
	// Since we're using JSON commands for now and don't have protobuf yet,
	// we log the intent. Full parsing comes with protocol handlers.
	f.logger.Debug("fsm: derived index update deferred until protobuf", "user", userID, "guid", guid, "type", docType)
}

// checkBlobReferences checks if any blob hashes referenced in the value exist locally.
func (f *FSM) checkBlobReferences(cmd Command) {
	// Blob references will be embedded in protobuf values (BlobRef fields).
	// Until we have protobuf, we can't parse them. This is a stub that will
	// be filled in when protocol handlers create real blob-referencing entries.
}

// Snapshot returns an FSMSnapshot that captures the current Pebble state.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	// Create a Pebble checkpoint — this is a point-in-time consistent copy.
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

// Restore restores the FSM from a snapshot. All previous state is discarded.
func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	// Read the snapshot into a temp directory.
	restoreDir, err := os.MkdirTemp("", "bongo-restore-*")
	if err != nil {
		return fmt.Errorf("creating restore temp dir: %w", err)
	}
	defer os.RemoveAll(restoreDir)

	pebbleDir := filepath.Join(restoreDir, "pebble")
	if err := os.MkdirAll(pebbleDir, 0o750); err != nil {
		return fmt.Errorf("creating restore pebble dir: %w", err)
	}

	// The snapshot is a tar stream of the Pebble checkpoint.
	if err := untar(rc, pebbleDir); err != nil {
		return fmt.Errorf("extracting snapshot: %w", err)
	}

	// Open the restored Pebble to read all keys.
	restored, err := store.NewPebbleStore(pebbleDir)
	if err != nil {
		return fmt.Errorf("opening restored pebble: %w", err)
	}
	defer restored.Close()

	// Replay all application keys into the live store.
	// Skip _raft/ prefixed keys (those belong to the raft log/stable store).
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
	return nil
}

// fsmSnapshot implements raft.FSMSnapshot using a Pebble checkpoint.
type fsmSnapshot struct {
	checkpointDir string
	pebbleDir     string
	logger        *slog.Logger
}

var _ raft.FSMSnapshot = (*fsmSnapshot)(nil)

// Persist writes the snapshot to the given sink as a tar archive.
func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	if err := tarDir(s.pebbleDir, sink); err != nil {
		sink.Cancel()
		return fmt.Errorf("persisting snapshot: %w", err)
	}
	return sink.Close()
}

// Release cleans up the checkpoint directory.
func (s *fsmSnapshot) Release() {
	if s.checkpointDir != "" {
		os.RemoveAll(s.checkpointDir)
		s.logger.Debug("fsm: snapshot checkpoint cleaned up", "dir", s.checkpointDir)
	}
}
