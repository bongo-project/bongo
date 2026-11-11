package cluster

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hashicorp/raft"

	"github.com/bongo-project/bongo/internal/store"
)

// DerivedIndexMarkerName is the file, in the data directory, that says the
// per-user SQLite and Bleve indexes were complete up to the applied index it
// holds when the node last shut down cleanly.
//
// Derived indexing is asynchronous (FSM.StartIndexer), so a crash loses queued
// work and the indexes then miss entries Pebble has. The marker is written
// only after the index worker has drained and the index stores have closed,
// and it is removed at the next start before anything is applied, so its
// presence at start proves the previous run ended cleanly. Without it the
// start rebuilds the indexes from Pebble.
//
// It is a file rather than a Pebble key because Pebble is what a Raft snapshot
// carries: a key would travel to another node, whose indexes it says nothing
// about, and would come back with every restore. Deleting the file by hand
// forces a rebuild at the next start.
const DerivedIndexMarkerName = "derived-indexes.clean"

// AppliedIndex returns the highest Raft log index recorded as applied to
// Pebble, or 0 when none is recorded (a fresh store, or one filled only with
// index-0 applies such as the bulk loader's).
func AppliedIndex(ps *store.PebbleStore) uint64 {
	v, err := ps.Get([]byte(raftAppliedIndexKey))
	if err != nil || len(v) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}

// WriteDerivedIndexMarker records that the derived indexes in dataDir are
// complete up to applied index index. Call it only once nothing can apply or
// index any more: after Raft has shut down, the index worker has drained and
// the search and metadata stores have closed without error.
//
// The write goes through a temporary file, fsync and rename, and the directory
// is synced after, so a crash leaves either no marker or a whole one.
func WriteDerivedIndexMarker(dataDir string, index uint64) error {
	tmp, err := os.CreateTemp(dataDir, "."+DerivedIndexMarkerName+"-*")
	if err != nil {
		return fmt.Errorf("creating derived-index marker: %w", err)
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.WriteString(strconv.FormatUint(index, 10) + "\n"); err != nil {
		return fail(fmt.Errorf("writing derived-index marker: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return fail(fmt.Errorf("syncing derived-index marker: %w", err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("closing derived-index marker: %w", err)
	}
	if err := os.Rename(name, filepath.Join(dataDir, DerivedIndexMarkerName)); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("placing derived-index marker: %w", err)
	}
	return syncDir(dataDir)
}

// errMarkerUnreadable marks a marker file that exists but does not parse.
var errMarkerUnreadable = errors.New("derived-index marker unreadable")

// ReadDerivedIndexMarker returns the applied index in dataDir's marker and
// whether a marker exists. A marker that exists but does not parse is
// reported as present with an error wrapping errMarkerUnreadable.
func ReadDerivedIndexMarker(dataDir string) (uint64, bool, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, DerivedIndexMarkerName)) // #nosec G304 -- fixed name in the operator's data directory
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("reading derived-index marker: %w", err)
	}
	idx, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("%w: %w", errMarkerUnreadable, err)
	}
	return idx, true, nil
}

// removeDerivedIndexMarker deletes the marker and syncs the directory, so the
// removal is durable before this run applies anything: a crash later in the
// run must find no marker at the next start.
func removeDerivedIndexMarker(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, DerivedIndexMarkerName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("removing derived-index marker: %w", err)
	}
	return syncDir(dataDir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- the operator's data directory
	if err != nil {
		return fmt.Errorf("opening %s to sync: %w", dir, err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return fmt.Errorf("syncing %s: %w", dir, err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", dir, err)
	}
	return nil
}

// checkDerivedIndexes runs before Raft starts and decides whether the derived
// indexes need rebuilding: they do unless the previous run left a marker at
// exactly the current applied index. The marker is consumed either way.
//
// restoreOnStart says raft will restore a snapshot into the FSM as it starts;
// FSM.Restore rebuilds the indexes itself, so rebuilding here as well would
// only double the work.
//
// The rebuild is synchronous and runs before any listener opens: serving with
// incomplete indexes would hide mail from lists and search.
func checkDerivedIndexes(dataDir string, ps *store.PebbleStore, blobs *store.BlobStore, metadata *store.MetadataManager, search SearchIndexer, logger *slog.Logger, restoreOnStart bool) error {
	markerIdx, present, readErr := ReadDerivedIndexMarker(dataDir)
	if readErr != nil && !errors.Is(readErr, errMarkerUnreadable) {
		return readErr
	}
	// Removed before the decision, so even a skipped rebuild leaves this run
	// unmarked until it shuts down cleanly.
	if err := removeDerivedIndexMarker(dataDir); err != nil {
		return err
	}

	applied := AppliedIndex(ps)
	if restoreOnStart {
		logger.Info("fsm: derived indexes will be rebuilt by the snapshot restore", "applied_index", applied)
		return nil
	}

	switch {
	case readErr != nil:
		logger.Warn("fsm: rebuilding derived indexes: clean-shutdown marker unreadable", "applied_index", applied, "error", readErr)
	case !present:
		logger.Info("fsm: rebuilding derived indexes: no clean-shutdown marker (first start, upgrade from a version without the marker, or the last run did not shut down cleanly)",
			"applied_index", applied)
	case markerIdx != applied:
		logger.Warn("fsm: rebuilding derived indexes: clean-shutdown marker does not match the applied index",
			"marker_index", markerIdx, "applied_index", applied)
	default:
		logger.Info("fsm: derived indexes complete, skipping rebuild", "applied_index", applied)
		return nil
	}

	if err := rebuildDerivedIndexes(ps, blobs, metadata, search, logger); err != nil {
		return fmt.Errorf("rebuilding derived indexes: %w", err)
	}
	return nil
}

// snapshotRestoreNeeded reports whether raft must restore its newest snapshot
// into the FSM at start, given the applied index Pebble has recorded.
//
// Pebble is the FSM state and is persistent, so a restore is needed only when
// Pebble lacks something the snapshot holds. raft passes the FSM command
// entries only; no-op and configuration entries advance raft's applied index
// but never Pebble's watermark. So Pebble below the snapshot index is still
// complete when every entry in the gap is a non-command, which is the normal
// state after a new leader's no-op. An entry in the gap that is a command, or
// that the log no longer holds, means Pebble may be behind: restore.
func snapshotRestoreNeeded(applied uint64, logs raft.LogStore, snaps raft.SnapshotStore) (restore bool, snapIndex uint64, err error) {
	list, err := snaps.List()
	if err != nil {
		return false, 0, fmt.Errorf("listing raft snapshots: %w", err)
	}
	if len(list) == 0 {
		return false, 0, nil
	}
	// raft tries the newest first (List is newest first) and, with
	// NoSnapshotRestoreOnStart, takes its indexes from that one.
	snapIndex = list[0].Index
	for idx := applied + 1; idx <= snapIndex; idx++ {
		var entry raft.Log
		if err := logs.GetLog(idx, &entry); err != nil {
			if errors.Is(err, raft.ErrLogNotFound) {
				return true, snapIndex, nil
			}
			return false, snapIndex, fmt.Errorf("reading raft log %d: %w", idx, err)
		}
		if entry.Type == raft.LogCommand {
			return true, snapIndex, nil
		}
	}
	return false, snapIndex, nil
}

// RaftNodeOption adjusts NewRaftNode.
type RaftNodeOption func(*raftNodeOptions)

type raftNodeOptions struct {
	indexCheck bool
	dataDir    string
	search     SearchIndexer
}

// WithDerivedIndexCheck makes NewRaftNode check, before raft starts applying,
// whether the derived indexes under dataDir survived the previous run (see
// DerivedIndexMarkerName), and rebuild them from Pebble when they may not
// have. search is given to the FSM before raft starts, so the rebuild here
// and a snapshot restore on start both re-index Bleve as well as SQLite.
func WithDerivedIndexCheck(dataDir string, search SearchIndexer) RaftNodeOption {
	return func(o *raftNodeOptions) {
		o.indexCheck = true
		o.dataDir = dataDir
		o.search = search
	}
}
