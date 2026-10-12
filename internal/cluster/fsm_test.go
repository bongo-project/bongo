package cluster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
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

func TestFSMSkipsAlreadyAppliedEntriesOnReplay(t *testing.T) {
	env := newFSMTestEnv(t)
	key := []byte("/user/u1/mail/m1")

	// Deliver a message (index 1), then delete it (index 2).
	env.fsm.Apply(makeProtoLog(1, &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: []byte("body")}))
	env.fsm.Apply(makeProtoLog(2, &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: key}))

	if _, err := env.pebble.Get(key); err == nil {
		t.Fatal("key should be deleted after the delete entry")
	}

	// Simulate a restart replaying the log: re-applying the already-applied PUT
	// (index 1) must be skipped so the deleted message is not resurrected.
	env.fsm.Apply(makeProtoLog(1, &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: []byte("body")}))

	if _, err := env.pebble.Get(key); err == nil {
		t.Error("deleted key was resurrected by replaying an already-applied entry (index <= applied)")
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

	idx := uint64(1)
	for key, val := range testData {
		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(key),
			Value: []byte(val),
		}
		env.fsm.Apply(makeProtoLog(idx, cmd))
		idx++
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

// TestFSMRestoreReplacesStaleState verifies Restore REPLACES rather than merges:
// keys present in the target store but absent from the snapshot must be removed,
// and keys present in both take the snapshot's value.
func TestFSMRestoreReplacesStaleState(t *testing.T) {
	env := newFSMTestEnv(t)

	// Snapshot contains only /keep/a.
	env.fsm.Apply(makeProtoLog(1, &bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/keep/a"), Value: []byte("snap"),
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

	// Target store seeded with a stale key (not in snapshot) and a divergent
	// value for /keep/a.
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

	_ = ps2.Set([]byte("/stale/x"), []byte("old"))
	_ = ps2.Set([]byte("/keep/a"), []byte("stale-version"))

	fsm2 := NewFSM(ps2, bs2, mm2, testLogger())
	if err := fsm2.Restore(io.NopCloser(&buf)); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if v, err := ps2.Get([]byte("/keep/a")); err != nil || string(v) != "snap" {
		t.Errorf("/keep/a = %q (err %v), want %q", v, err, "snap")
	}
	if _, err := ps2.Get([]byte("/stale/x")); err == nil {
		t.Error("/stale/x survived Restore: state was merged, not replaced")
	}
}

type bufferSink struct {
	buf *bytes.Buffer
}

func (s *bufferSink) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s *bufferSink) Close() error                { return nil }
func (s *bufferSink) Cancel() error               { return nil }
func (s *bufferSink) ID() string                  { return "test" }

func TestFSMMailMetaWriteDoesNotAutoPlace(t *testing.T) {
	env := newFSMTestEnv(t)

	meta := &bongov1.MailMeta{
		Guid:       "mail-1",
		Subject:    "Test Mail",
		From:       "alice@example.com",
		To:         []string{"bob@example.com"},
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: "abc123", Size: 100},
	}
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/u1/mail/mail-1"),
		Value: metaBytes,
	}
	result := env.fsm.Apply(makeProtoLog(1, cmd))
	if result != nil {
		t.Fatalf("Apply: %v", result)
	}

	if _, err := env.pebble.Get([]byte("/user/u1/mailbox/INBOX/meta")); err == nil {
		t.Fatal("mail meta write unexpectedly created INBOX meta")
	}
	if _, err := env.pebble.Get([]byte("/user/u1/mailbox/INBOX/msg/0000000001")); err == nil {
		t.Fatal("mail meta write unexpectedly created mailbox message entry")
	}
}

func TestFSMMailboxDeliveryCommand(t *testing.T) {
	env := newFSMTestEnv(t)

	meta := &bongov1.MailMeta{
		Guid:       "mail-1",
		Subject:    "Test Mail",
		From:       "alice@example.com",
		To:         []string{"bob@example.com"},
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: "abc123", Size: 100},
	}
	cmd, err := store.BuildDeliverMailCommand("u1", "INBOX", meta, 0)
	if err != nil {
		t.Fatalf("build deliver command: %v", err)
	}
	result := env.fsm.Apply(makeProtoLog(1, cmd))
	if result != nil {
		t.Fatalf("Apply: %v", result)
	}

	mbMetaBytes, err := env.pebble.Get([]byte("/user/u1/mailbox/INBOX/meta"))
	if err != nil {
		t.Fatalf("INBOX meta not found: %v", err)
	}
	var mbMeta bongov1.MailboxMeta
	if err := proto.Unmarshal(mbMetaBytes, &mbMeta); err != nil {
		t.Fatalf("unmarshal mailbox meta: %v", err)
	}
	if mbMeta.UidNext != 2 {
		t.Errorf("uid_next = %d, want 2", mbMeta.UidNext)
	}

	msgBytes, err := env.pebble.Get([]byte("/user/u1/mailbox/INBOX/msg/0000000001"))
	if err != nil {
		t.Fatalf("mailbox msg not found: %v", err)
	}
	var mbMsg bongov1.MailboxMessage
	if err := proto.Unmarshal(msgBytes, &mbMsg); err != nil {
		t.Fatalf("unmarshal mailbox msg: %v", err)
	}
	if mbMsg.MailGuid != "mail-1" {
		t.Errorf("mail_guid = %q, want %q", mbMsg.MailGuid, "mail-1")
	}
}

func TestFSMMailIndexing(t *testing.T) {
	env := newFSMTestEnv(t)

	meta := &bongov1.MailMeta{
		Guid:       "m1",
		MessageId:  "<test@example.com>",
		Subject:    "Hello World",
		From:       "alice@example.com",
		To:         []string{"bob@example.com"},
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{
			Hash: "abc123",
			Size: 1024,
		},
	}
	cmd, err := store.BuildDeliverMailCommand("testuser", "INBOX", meta, 0)
	if err != nil {
		t.Fatalf("build deliver command: %v", err)
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

// slowIndexer is a SearchIndexer whose IndexMail blocks until released, so a
// test can hold derived indexing open and observe whether Apply waits for it.
type slowIndexer struct {
	release chan struct{}
	calls   atomic.Int32
}

func newSlowIndexer() *slowIndexer {
	return &slowIndexer{release: make(chan struct{})}
}

func (s *slowIndexer) IndexMail(_, _ string, _, _ string, _ []string, _ string, _ int64, _ bool, _ []string, _ ...float64) error {
	s.calls.Add(1)
	<-s.release
	return nil
}
func (s *slowIndexer) IndexProfile(_, _, _ string) error { return nil }
func (s *slowIndexer) DeleteProfile(_ string) error      { return nil }
func (s *slowIndexer) DeleteMailIndex(_, _ string) error { return nil }

// mailPutLog builds a committed log entry that delivers a MailMeta for userID,
// which is what drives the derived-index path.
func mailPutLog(t *testing.T, index uint64, userID, guid string) *raft.Log {
	t.Helper()
	meta := &bongov1.MailMeta{Guid: guid, Subject: "s", From: "a@example.com"}
	value, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal MailMeta: %v", err)
	}
	return makeProtoLog(index, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/" + userID + "/mail/" + guid),
		Value: value,
	})
}

// TestFSMApplyDoesNotBlockOnSlowIndexing is the regression test for the
// production stall: the first delivery after a restart took 85s because
// FSM.Apply performed per-user SQLite and Bleve index work inline, and the
// Raft apply path has no real deadline (the 5s at the store.DeliverMail call
// site bounds only enqueueing — see cluster.ApplyCommand).
//
// With the background indexer started, Apply must return while indexing is
// still in flight. The paired sub-test below proves this is not vacuous: with
// no indexer started, the same Apply blocks.
func TestFSMApplyDoesNotBlockOnSlowIndexing(t *testing.T) {
	t.Run("with indexer: Apply returns while indexing is blocked", func(t *testing.T) {
		env := newFSMTestEnv(t)
		idx := newSlowIndexer()
		env.fsm.SetSearchManager(idx)

		ctx, cancel := context.WithCancel(context.Background())
		env.fsm.StartIndexer(ctx)
		t.Cleanup(func() {
			close(idx.release)
			cancel()
			env.fsm.StopIndexer()
		})

		done := make(chan struct{})
		go func() {
			env.fsm.Apply(mailPutLog(t, 1, "user-a", "guid-a"))
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Apply blocked on derived indexing; it must not wait for SQLite/Bleve work")
		}

		// The write itself must still be durable in Pebble before Apply returns
		// — only the DERIVED index work is deferred.
		if _, err := env.pebble.Get([]byte("/user/user-a/mail/guid-a")); err != nil {
			t.Errorf("mail key not in Pebble after Apply returned: %v", err)
		}
	})

	t.Run("without indexer: Apply blocks (proves the test is real)", func(t *testing.T) {
		env := newFSMTestEnv(t)
		idx := newSlowIndexer()
		env.fsm.SetSearchManager(idx)
		// Deliberately no StartIndexer: indexing stays inline.

		done := make(chan struct{})
		go func() {
			env.fsm.Apply(mailPutLog(t, 1, "user-b", "guid-b"))
			close(done)
		}()

		select {
		case <-done:
			t.Fatal("expected inline indexing to block Apply; if this passes, the sub-test above proves nothing")
		case <-time.After(300 * time.Millisecond):
			// Still blocked, as expected.
		}
		close(idx.release)
		<-done
	})
}

// TestFSMIndexerPreservesOrderAndDrains checks the two properties the single
// worker exists to provide: apply order is preserved (a delete must not
// overtake the put it follows), and cancelling the worker drains what is
// already queued rather than dropping committed work.
func TestFSMIndexerPreservesOrderAndDrains(t *testing.T) {
	env := newFSMTestEnv(t)
	rec := &orderRecorder{}
	env.fsm.SetSearchManager(rec)

	ctx, cancel := context.WithCancel(context.Background())
	env.fsm.StartIndexer(ctx)

	const n = 50
	for i := range n {
		env.fsm.Apply(mailPutLog(t, uint64(i+1), "user-c", fmt.Sprintf("guid-%02d", i)))
	}

	if !env.fsm.WaitIdle(10 * time.Second) {
		t.Fatal("derived index backlog did not drain")
	}
	cancel()
	env.fsm.StopIndexer()

	got := rec.seen()
	if len(got) != n {
		t.Fatalf("indexed %d items, want %d — committed index work was dropped", len(got), n)
	}
	for i, guid := range got {
		if want := fmt.Sprintf("guid-%02d", i); guid != want {
			t.Fatalf("index order diverged at %d: got %q want %q — a single worker must preserve apply order", i, guid, want)
		}
	}
}

// orderRecorder records the order in which mail GUIDs were indexed.
type orderRecorder struct {
	mu    sync.Mutex
	guids []string
}

func (o *orderRecorder) IndexMail(_, guid string, _, _ string, _ []string, _ string, _ int64, _ bool, _ []string, _ ...float64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.guids = append(o.guids, guid)
	return nil
}
func (o *orderRecorder) IndexProfile(_, _, _ string) error { return nil }
func (o *orderRecorder) DeleteProfile(_ string) error      { return nil }
func (o *orderRecorder) DeleteMailIndex(_, _ string) error { return nil }

func (o *orderRecorder) seen() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.guids...)
}
