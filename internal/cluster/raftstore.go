package cluster

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/hashicorp/raft"

	"github.com/bongo-project/bongo/internal/store"
)

const (
	// raftLogPrefix is the key prefix for Raft log entries in Pebble.
	raftLogPrefix = "_raft/log/"
	// raftStablePrefix is the key prefix for Raft stable store entries in Pebble.
	raftStablePrefix = "_raft/stable/"
)

// RaftStore implements raft.LogStore and raft.StableStore backed by Pebble.
type RaftStore struct {
	store *store.PebbleStore
}

// Compile-time interface checks.
var (
	_ raft.LogStore    = (*RaftStore)(nil)
	_ raft.StableStore = (*RaftStore)(nil)
)

// NewRaftStore creates a RaftStore backed by the given PebbleStore.
func NewRaftStore(s *store.PebbleStore) *RaftStore {
	return &RaftStore{store: s}
}

// logKey returns the Pebble key for a Raft log entry at the given index.
// The index is zero-padded to 20 digits for correct lexicographic sort order.
func logKey(index uint64) []byte {
	return []byte(fmt.Sprintf("%s%020d", raftLogPrefix, index))
}

// stableKey returns the Pebble key for a stable store entry.
func stableKey(key []byte) []byte {
	k := make([]byte, len(raftStablePrefix)+len(key))
	copy(k, raftStablePrefix)
	copy(k[len(raftStablePrefix):], key)
	return k
}

// --- LogStore implementation ---

// FirstIndex returns the first log index written. 0 for no entries.
func (r *RaftStore) FirstIndex() (uint64, error) {
	iter, err := r.store.PrefixScan([]byte(raftLogPrefix))
	if err != nil {
		return 0, fmt.Errorf("first index scan: %w", err)
	}
	defer iter.Close()

	if !iter.Next() {
		return 0, nil
	}
	return parseLogIndex(iter.Key())
}

// LastIndex returns the last log index written. 0 for no entries.
//
// This seeks directly to the highest key rather than iterating the whole log.
// It used to scan every entry and keep the last one, which made the call O(n) in
// the size of the raft log — and hashicorp/raft calls LastIndex constantly (on
// the leader loop, on every append, when dispatching entries). That caused an
// 80s stall on the first delivery after a restart: with ~8,200 log entries and a
// cold Pebble block cache, every one of those repeated full scans went to disk.
// Once the cache warmed the same delivery took 63ms, which is why it looked like
// a mysterious startup-only problem.
//
// Seeking to the greatest key is only equivalent to the greatest index because
// logKey zero-pads the index to a fixed 20 digits, so lexicographic order
// matches numeric order across the whole uint64 range. Do not change that
// encoding without revisiting this.
func (r *RaftStore) LastIndex() (uint64, error) {
	key, ok, err := r.store.LastKeyWithPrefix([]byte(raftLogPrefix))
	if err != nil {
		return 0, fmt.Errorf("last index lookup: %w", err)
	}
	if !ok {
		return 0, nil
	}
	return parseLogIndex(key)
}

// GetLog gets a log entry at a given index.
func (r *RaftStore) GetLog(index uint64, log *raft.Log) error {
	val, err := r.store.Get(logKey(index))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return raft.ErrLogNotFound
		}
		return fmt.Errorf("get log %d: %w", index, err)
	}
	return decodeMsgPack(val, log)
}

// StoreLog stores a single log entry.
func (r *RaftStore) StoreLog(log *raft.Log) error {
	return r.StoreLogs([]*raft.Log{log})
}

// StoreLogs stores multiple log entries.
func (r *RaftStore) StoreLogs(logs []*raft.Log) error {
	for _, l := range logs {
		val, err := encodeMsgPack(l)
		if err != nil {
			return fmt.Errorf("encoding log %d: %w", l.Index, err)
		}
		if err := r.store.Set(logKey(l.Index), val); err != nil {
			return fmt.Errorf("storing log %d: %w", l.Index, err)
		}
	}
	return nil
}

// DeleteRange deletes a range of log entries. The range is inclusive.
func (r *RaftStore) DeleteRange(min, max uint64) error {
	return r.store.DeleteRange(logKey(min), logKey(max))
}

// --- StableStore implementation ---

// Set stores a key-value pair in the stable store.
func (r *RaftStore) Set(key []byte, val []byte) error {
	return r.store.Set(stableKey(key), val)
}

// Get returns the value for key, or an empty byte slice if not found.
func (r *RaftStore) Get(key []byte) ([]byte, error) {
	val, err := r.store.Get(stableKey(key))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("stable get: %w", err)
	}
	return val, nil
}

// SetUint64 stores a uint64 value in the stable store.
func (r *RaftStore) SetUint64(key []byte, val uint64) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, val)
	return r.Set(key, buf)
}

// GetUint64 returns the uint64 value for key, or 0 if not found.
func (r *RaftStore) GetUint64(key []byte) (uint64, error) {
	val, err := r.Get(key)
	if err != nil {
		return 0, err
	}
	if len(val) == 0 {
		return 0, nil
	}
	if len(val) != 8 {
		return 0, fmt.Errorf("invalid uint64 value length: %d", len(val))
	}
	return binary.BigEndian.Uint64(val), nil
}

// parseLogIndex extracts the log index from a Pebble key like "_raft/log/00000000000000000001".
func parseLogIndex(key []byte) (uint64, error) {
	s := string(key)
	if len(s) <= len(raftLogPrefix) {
		return 0, fmt.Errorf("invalid log key: %q", s)
	}
	var idx uint64
	_, err := fmt.Sscanf(s[len(raftLogPrefix):], "%d", &idx)
	if err != nil {
		return 0, fmt.Errorf("parsing log index from %q: %w", s, err)
	}
	return idx, nil
}
