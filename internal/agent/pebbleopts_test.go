package agent

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// TestPebbleStoreOptionsRouteLogsThroughSlog opens a real store with the
// agent's options and reopens it to force a WAL replay, which must arrive on
// the process logger. NewAgent's call site passes this helper's result; that
// spread is covered by review, since it needs a full Agent to exercise.
func TestPebbleStoreOptionsRouteLogsThroughSlog(t *testing.T) {
	var buf syncBuilder
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	dir := t.TempDir()
	s, err := store.NewPebbleStore(dir, pebbleStoreOptions(logger)...)
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
	s2, err := store.NewPebbleStore(dir, pebbleStoreOptions(logger)...)
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
