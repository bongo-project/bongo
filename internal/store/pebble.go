package store

import (
	"errors"
	"fmt"

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

// NewPebbleStore opens a Pebble database at the given directory.
func NewPebbleStore(dataDir string, opts ...Option) (*PebbleStore, error) {
	pebbleOpts := &pebble.Options{}
	for _, o := range opts {
		o(pebbleOpts)
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

// Close closes the Pebble database.
func (s *PebbleStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing pebble store: %w", err)
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
