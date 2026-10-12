package store

import (
	"errors"
	"fmt"
	"log/slog"
	"math"

	"github.com/cockroachdb/pebble"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("key not found")

// Iterator allows iterating over key-value pairs from a prefix scan.
type Iterator interface {
	// Next advances to the next key-value pair. Returns false when exhausted.
	Next() bool
	// Key returns the current key. Only valid after Next returns true.
	Key() []byte
	// Value returns the current value. Only valid after Next returns true.
	Value() ([]byte, error)
	// Close releases the iterator resources.
	Close() error
}

// PebbleStore wraps a Pebble database for key-value storage.
type PebbleStore struct {
	db *pebble.DB
}

// Option configures a PebbleStore.
type Option func(*pebble.Options)

// WithCacheSize sets the Pebble block cache size in bytes.
func WithCacheSize(size int64) Option {
	return func(opts *pebble.Options) {
		opts.Cache = pebble.NewCache(size)
	}
}

// WithLogger routes Pebble's log output through the given slog logger. A nil
// logger is a programming error; it is ignored and Pebble keeps its default.
func WithLogger(l *slog.Logger) Option {
	return func(opts *pebble.Options) {
		if l == nil {
			return
		}
		opts.Logger = newPebbleLogger(l)
	}
}

// NewPebbleStore opens a Pebble database at the given directory.
func NewPebbleStore(dataDir string, opts ...Option) (*PebbleStore, error) {
	pebbleOpts := &pebble.Options{}
	for _, o := range opts {
		o(pebbleOpts)
	}

	// pebble.NewCache hands the creator one reference. Open takes its own
	// (and Close drops it), so release ours once Open returns, on success or
	// failure. Dropping it earlier would free the cache before Open's Ref.
	if pebbleOpts.Cache != nil {
		defer pebbleOpts.Cache.Unref()
	}

	db, err := pebble.Open(dataDir, pebbleOpts)
	if err != nil {
		return nil, fmt.Errorf("opening pebble store: %w", err)
	}

	return &PebbleStore{db: db}, nil
}

// Get retrieves the value for a key. Returns ErrNotFound if the key does not exist.
func (s *PebbleStore) Get(key []byte) ([]byte, error) {
	val, closer, err := s.db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("pebble get: %w", err)
	}
	defer closer.Close()

	// Copy the value since it's only valid until closer is closed.
	result := make([]byte, len(val))
	copy(result, val)
	return result, nil
}

// Set stores a key-value pair.
func (s *PebbleStore) Set(key, value []byte) error {
	if err := s.db.Set(key, value, pebble.Sync); err != nil {
		return fmt.Errorf("pebble set: %w", err)
	}
	return nil
}

// Delete removes a key.
func (s *PebbleStore) Delete(key []byte) error {
	if err := s.db.Delete(key, pebble.Sync); err != nil {
		return fmt.Errorf("pebble delete: %w", err)
	}
	return nil
}

// PrefixScan returns an iterator over all keys with the given prefix.
func (s *PebbleStore) PrefixScan(prefix []byte) (Iterator, error) {
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, fmt.Errorf("pebble prefix scan: %w", err)
	}

	return &pebbleIterator{iter: iter, started: false}, nil
}

// LastKeyWithPrefix returns the greatest key under prefix, or ok=false when
// the prefix is empty.
//
// This exists so callers that only want the highest key do not have to iterate
// every key under the prefix to find it. Pebble can seek straight to the end of
// the range, so this is O(log n) where the equivalent forward scan is O(n) —
// the difference between a constant-time lookup and reading the whole raft log
// on every call (see RaftStore.LastIndex).
func (s *PebbleStore) LastKeyWithPrefix(prefix []byte) ([]byte, bool, error) {
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return nil, false, fmt.Errorf("pebble last key scan: %w", err)
	}
	defer func() { _ = iter.Close() }()

	if !iter.Last() {
		return nil, false, nil
	}
	// The key is only valid until the iterator moves or closes.
	return append([]byte(nil), iter.Key()...), true, nil
}

// DeleteRange deletes all keys in the range [start, end] (inclusive).
func (s *PebbleStore) DeleteRange(start, end []byte) error {
	// Pebble's DeleteRange is [start, end) exclusive on end,
	// so we need the successor of end to make it inclusive.
	upper := keySuccessor(end)
	if upper != nil {
		if err := s.db.DeleteRange(start, upper, pebble.Sync); err != nil {
			return fmt.Errorf("pebble delete range: %w", err)
		}
		return nil
	}

	// All-0xFF end key: no successor exists. Scan from start with no upper bound.
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: start,
	})
	if err != nil {
		return fmt.Errorf("pebble delete range scan: %w", err)
	}
	defer iter.Close()

	var keys [][]byte
	for iter.First(); iter.Valid(); iter.Next() {
		k := make([]byte, len(iter.Key()))
		copy(k, iter.Key())
		keys = append(keys, k)
	}
	for _, k := range keys {
		if err := s.db.Delete(k, pebble.Sync); err != nil {
			return fmt.Errorf("pebble delete range key: %w", err)
		}
	}
	return nil
}

// DB returns the underlying pebble.DB for advanced operations like checkpoints.
func (s *PebbleStore) DB() *pebble.DB {
	return s.db
}

// EstimateDiskUsage returns an estimate of the disk usage of the Pebble database.
func (s *PebbleStore) EstimateDiskUsage() int64 {
	est, err := s.db.EstimateDiskUsage([]byte{0x00}, []byte{0xFF})
	if err != nil {
		return 0
	}
	if est > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(est)
}

// Close closes the Pebble database.
func (s *PebbleStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing pebble store: %w", err)
	}
	return nil
}

// keySuccessor returns the lexicographically next key after key by incrementing
// the last non-0xFF byte and truncating trailing 0xFF bytes.
// Returns nil if all bytes are 0xFF (no successor exists).
func keySuccessor(key []byte) []byte {
	if len(key) == 0 {
		return nil
	}
	succ := make([]byte, len(key))
	copy(succ, key)
	for i := len(succ) - 1; i >= 0; i-- {
		if succ[i] < 0xFF {
			succ[i]++
			return succ[:i+1]
		}
	}
	return nil
}

// prefixUpperBound returns the upper bound for a prefix scan by incrementing
// the last byte of the prefix. Returns nil if all bytes are 0xFF (full range).
func prefixUpperBound(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	for i := len(upper) - 1; i >= 0; i-- {
		if upper[i] < 0xFF {
			upper[i]++
			return upper[:i+1]
		}
	}
	return nil
}

type pebbleIterator struct {
	iter    *pebble.Iterator
	started bool
}

func (it *pebbleIterator) Next() bool {
	if !it.started {
		it.started = true
		return it.iter.First()
	}
	return it.iter.Next()
}

func (it *pebbleIterator) Key() []byte {
	k := it.iter.Key()
	out := make([]byte, len(k))
	copy(out, k)
	return out
}

func (it *pebbleIterator) Value() ([]byte, error) {
	v, err := it.iter.ValueAndErr()
	if err != nil {
		return nil, fmt.Errorf("pebble iterator value: %w", err)
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (it *pebbleIterator) Close() error {
	return it.iter.Close()
}
