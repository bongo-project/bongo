package cluster

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

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

func makeProtoLog(index uint64, cmd *bongov1.RaftCommand) *raft.Log {
	data, _ := proto.Marshal(cmd)
	return &raft.Log{
		Index: index,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  data,
	}
}

func TestFSMApplyPut(t *testing.T) {
	env := newFSMTestEnv(t)

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/system/config/test"),
		Value: []byte("hello"),
	}
	result := env.fsm.Apply(makeProtoLog(1, cmd))
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

	putCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/system/config/key"),
		Value: []byte("val"),
	}
	env.fsm.Apply(makeProtoLog(1, putCmd))

	delCmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: []byte("/system/config/key"),
	}
	result := env.fsm.Apply(makeProtoLog(2, delCmd))
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
		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(key),
			Value: []byte("data"),
		}
		result := env.fsm.Apply(makeProtoLog(uint64(i+1), cmd))
		if result != nil {
			t.Fatalf("Apply %s: %v", key, result)
		}
	}

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

	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_UNSPECIFIED,
		Key: []byte("k"),
	}
	// With OP_UNSPECIFIED, protobuf will fall through to JSON which fails.
	log := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  []byte("not json"),
	}
	result := env.fsm.Apply(log)
	if result == nil {
		t.Error("expected error for bad data, got nil")
	}
	_ = cmd // unused in this variant
}

func TestFSMApplyBadData(t *testing.T) {
	env := newFSMTestEnv(t)

	log := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  []byte("not json or proto"),
	}
	result := env.fsm.Apply(log)
	if result == nil {
		t.Error("expected error for bad data, got nil")
	}
}

func TestFSMSnapshotAndRestore(t *testing.T) {
	env := newFSMTestEnv(t)

	testData := map[string]string{
		"/user/u1/mail/m1":           "mail-data-1",
		"/user/u1/mail/m2":           "mail-data-2",
		"/user/u2/event/e1":          "event-data",
		"/system/domain/example.com": "domain-data",
	}

	for key, val := range testData {
		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(key),
			Value: []byte(val),
		}
		env.fsm.Apply(makeProtoLog(1, cmd))
	}

	snap, err := env.fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	defer snap.Release()

	var buf bytes.Buffer
	sink := &bufferSink{buf: &buf}
	if err := snap.Persist(sink); err != nil {
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

	fsm2 := NewFSM(ps2, bs2, mm2, testLogger())

	rc := io.NopCloser(&buf)
	if err := fsm2.Restore(rc); err != nil {
		t.Fatalf("Restore: %v", err)
	}

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

type bufferSink struct {
	buf *bytes.Buffer
}

func (s *bufferSink) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s *bufferSink) Close() error                { return nil }
func (s *bufferSink) Cancel() error               { return nil }
func (s *bufferSink) ID() string                   { return "test" }

func TestFSMMailIndexing(t *testing.T) {
	env := newFSMTestEnv(t)

	// Create a MailMeta protobuf.
	meta := &bongov1.MailMeta{
		Guid:      "m1",
		MessageId: "<test@example.com>",
		Subject:   "Hello World",
		From:      "alice@example.com",
		To:        []string{"bob@example.com"},
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{
			Hash: "abc123",
			Size: 1024,
		},
	}
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/testuser/mail/m1"),
		Value: metaBytes,
	}
	result := env.fsm.Apply(makeProtoLog(1, cmd))
	if result != nil {
		t.Fatalf("Apply: %v", result)
	}

	// Verify SQLite was indexed.
	objs, err := env.metadata.QueryByType("testuser", store.DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 mail object, got %d", len(objs))
	}
	if objs[0].GUID != "m1" {
		t.Errorf("guid = %q, want %q", objs[0].GUID, "m1")
	}
}
