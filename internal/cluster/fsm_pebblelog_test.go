package cluster

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

type syncLogBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncLogBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncLogBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestFSMRestoreRoutesPebbleLogsThroughSlog restores a snapshot (whose
// checkpoint carries a WAL) and expects Pebble's replay line on the FSM logger.
func TestFSMRestoreRoutesPebbleLogsThroughSlog(t *testing.T) {
	env := newFSMTestEnv(t)
	env.fsm.Apply(makeProtoLog(1, &bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/user/u1/mail/m1"), Value: []byte("x"),
	}))

	snap, err := env.fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	defer snap.Release()
	var buf bytes.Buffer
	if err := snap.Persist(&bufferSink{buf: &buf}); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	dir2 := t.TempDir()
	ps2, err := store.NewPebbleStore(dir2 + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	defer ps2.Close()
	bs2, err := store.NewBlobStore(dir2)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	mm2, err := store.NewMetadataManager(dir2)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	defer mm2.Close()

	var logs syncLogBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fsm2 := NewFSM(ps2, bs2, mm2, logger)

	if err := fsm2.Restore(io.NopCloser(&buf)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !strings.Contains(logs.String(), `"msg":"pebble.wal_replay"`) {
		t.Errorf("no pebble.wal_replay record after Restore; got: %s", logs.String())
	}
}
