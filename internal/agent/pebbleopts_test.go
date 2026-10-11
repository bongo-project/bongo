package agent

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/bongo-project/bongo/internal/store"
)

func TestPebbleStoreOptionsApplyCacheSize(t *testing.T) {
	tests := []struct {
		name      string
		cacheSize int64
		wantCache bool
	}{
		{"256MB", 256 << 20, true},
		{"unset", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &pebble.Options{}
			for _, o := range pebbleStoreOptions(slog.New(slog.DiscardHandler), tt.cacheSize) {
				o(opts)
			}
			if !tt.wantCache {
				if opts.Cache != nil {
					t.Fatal("Cache set, want nil")
				}
				return
			}
			if opts.Cache == nil {
				t.Fatal("Cache not set")
			}
			if got := opts.Cache.MaxSize(); got != tt.cacheSize {
				t.Fatalf("MaxSize = %d, want %d", got, tt.cacheSize)
			}
			opts.Cache.Unref() // release the creator reference; no DB holds it
		})
	}
}

// TestPebbleStoreOptionsRouteLogsThroughSlog opens a real store with the
// agent's options and reopens it to force a WAL replay, which must arrive on
// the process logger. NewAgent's call site passes this helper's result; that
// spread is covered by review, since it needs a full Agent to exercise.
func TestPebbleStoreOptionsRouteLogsThroughSlog(t *testing.T) {
	var buf syncBuilder
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	dir := t.TempDir()
	s, err := store.NewPebbleStore(dir, pebbleStoreOptions(logger, 0)...)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set([]byte("/k"), []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Pebble logs synchronously during Open, so the builder is only touched
	// from this goroutine until the store is reopened.
	buf.Reset()
	s2, err := store.NewPebbleStore(dir, pebbleStoreOptions(logger, 0)...)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec["msg"] == "pebble.wal_replay" && rec["component"] == "pebble" {
			found = true
		}
	}
	if !found {
		t.Errorf("no pebble.wal_replay record with component=pebble; got: %s", buf.String())
	}
}

// syncBuilder is safe for Pebble background goroutines to log into.
type syncBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuilder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuilder) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}
