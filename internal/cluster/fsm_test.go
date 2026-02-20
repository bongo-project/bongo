package cluster

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/hashicorp/raft"

	"github.com/bongo-go/bongo-go/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type fsmTestEnv struct {
	pebble   *store.PebbleStore
	blobs    *store.BlobStore
	metadata *store.MetadataManager
	fsm      *FSM
}

func newFSMTestEnv(t *testing.T) *fsmTestEnv {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}

	bs, err := store.NewBlobStore(dir)
	if err != nil {
		ps.Close()
		t.Fatalf("NewBlobStore: %v", err)
	}

	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		ps.Close()
		t.Fatalf("NewMetadataManager: %v", err)
	}

	fsm := NewFSM(ps, bs, mm, testLogger())

	t.Cleanup(func() {
		mm.Close()
		ps.Close()
	})

	return &fsmTestEnv{pebble: ps, blobs: bs, metadata: mm, fsm: fsm}
}

func makeLog(index uint64, cmd Command) *raft.Log {
	data, _ := json.Marshal(cmd)
	return &raft.Log{
		Index: index,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  data,
	}
}

func TestFSMApplyPut(t *testing.T) {
	env := newFSMTestEnv(t)

	cmd := Command{Op: "put", Key: []byte("/system/config/test"), Value: []byte("hello")}
	result := env.fsm.Apply(makeLog(1, cmd))
	if result != nil {
		t.Fatalf("Apply returned error: %v", result)
	}

	val, err := env.pebble.Get([]byte("/system/config/test"))
	if err != nil {
		t.Fatalf("Get after apply: %v", err)
	}
	if string(val) != "hello" {
		t.Errorf("value = %q, want %q", val, "hello")
	}
}

func TestFSMApplyDelete(t *testing.T) {
	env := newFSMTestEnv(t)

	// Put a value first.
	putCmd := Command{Op: "put", Key: []byte("/system/config/key"), Value: []byte("val")}
	env.fsm.Apply(makeLog(1, putCmd))

	// Delete it.
	delCmd := Command{Op: "delete", Key: []byte("/system/config/key")}
	result := env.fsm.Apply(makeLog(2, delCmd))
	if result != nil {
		t.Fatalf("Apply delete returned error: %v", result)
	}

	_, err := env.pebble.Get([]byte("/system/config/key"))
	if err == nil {
		t.Error("expected ErrNotFound after delete, got nil")
	}
}

func TestFSMApplyMultiple(t *testing.T) {
	env := newFSMTestEnv(t)

	keys := []string{
		"/user/u1/mail/m1",
		"/user/u1/mail/m2",
		"/user/u2/event/e1",
		"/system/domain/example.com",
	}

	for i, key := range keys {
		cmd := Command{Op: "put", Key: []byte(key), Value: []byte("data")}
		result := env.fsm.Apply(makeLog(uint64(i+1), cmd))
		if result != nil {
			t.Fatalf("Apply %s: %v", key, result)
		}
	}

	// Verify all keys exist.
	for _, key := range keys {
		val, err := env.pebble.Get([]byte(key))
		if err != nil {
			t.Errorf("Get %s: %v", key, err)
		}
		if string(val) != "data" {
			t.Errorf("Get %s = %q, want %q", key, val, "data")
		}
	}
}

func TestFSMApplyUnknownOp(t *testing.T) {
	env := newFSMTestEnv(t)

	cmd := Command{Op: "unknown", Key: []byte("k")}
	result := env.fsm.Apply(makeLog(1, cmd))
	if result == nil {
		t.Error("expected error for unknown op, got nil")
	}
}

func TestFSMApplyBadJSON(t *testing.T) {
	env := newFSMTestEnv(t)

	log := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  []byte("not json"),
	}
	result := env.fsm.Apply(log)
	if result == nil {
		t.Error("expected error for bad JSON, got nil")
	}
}

func TestFSMSnapshotAndRestore(t *testing.T) {
	env := newFSMTestEnv(t)

	// Write some application data.
	testData := map[string]string{
		"/user/u1/mail/m1":           "mail-data-1",
		"/user/u1/mail/m2":           "mail-data-2",
		"/user/u2/event/e1":          "event-data",
		"/system/domain/example.com": "domain-data",
	}

	for key, val := range testData {
		cmd := Command{Op: "put", Key: []byte(key), Value: []byte(val)}
		env.fsm.Apply(makeLog(1, cmd))
	}

	// Take a snapshot.
	snap, err := env.fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	defer snap.Release()

	// Persist snapshot to a buffer.
	var buf bytes.Buffer
	sink := &bufferSink{buf: &buf}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// Create a new FSM (simulating a fresh node).
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

	fsm2 := NewFSM(ps2, bs2, mm2, testLogger())

	// Restore from snapshot.
	rc := io.NopCloser(&buf)
	if err := fsm2.Restore(rc); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Verify all data is present in the restored store.
	for key, want := range testData {
		val, err := ps2.Get([]byte(key))
		if err != nil {
			t.Errorf("restored Get %s: %v", key, err)
			continue
		}
		if string(val) != want {
			t.Errorf("restored %s = %q, want %q", key, val, want)
		}
	}
}

// bufferSink implements raft.SnapshotSink for testing.
type bufferSink struct {
	buf *bytes.Buffer
}

func (s *bufferSink) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s *bufferSink) Close() error                { return nil }
func (s *bufferSink) Cancel() error               { return nil }
func (s *bufferSink) ID() string                   { return "test" }
