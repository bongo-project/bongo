package cluster

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// seedUnfilledMail stores n messages for userID with a Pebble MailMeta, a blob
// and a maildocument row whose preview is NULL. It returns guid -> blob hash.
func seedUnfilledMail(t *testing.T, env *fsmTestEnv, userID string, n int) map[string]string {
	t.Helper()
	if _, err := env.metadata.System.DB().Exec(
		`INSERT OR IGNORE INTO users (id, username) VALUES (?, ?)`, userID, userID); err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	for i := range n {
		guid := fmt.Sprintf("m%03d", i)
		raw := fmt.Sprintf("From: a@example.com\r\nSubject: s\r\n\r\nbody number %d\r\n", i)
		hash, err := env.blobs.Put([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		meta := &bongov1.MailMeta{Guid: guid, RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(raw))}}
		val, _ := proto.Marshal(meta)
		if err := env.pebble.Set([]byte("/user/"+userID+"/mail/"+guid), val); err != nil {
			t.Fatal(err)
		}
		if err := env.metadata.IndexMail(userID, guid, "s", "a@example.com", 1, int64(len(raw))); err != nil {
			t.Fatal(err)
		}
		hashes[guid] = hash
	}
	return hashes
}

func newTestBackfill(env *fsmTestEnv, batch int) *PreviewBackfill {
	return &PreviewBackfill{
		ps: env.pebble, blobs: env.blobs, metadata: env.metadata, logger: testLogger(),
		batch: batch, pause: time.Millisecond, interval: time.Hour,
	}
}

func pendingCount(t *testing.T, env *fsmTestEnv, userID string) int {
	t.Helper()
	g, err := env.metadata.PendingPreviews(userID, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	return len(g)
}

func TestPreviewBackfillFillsAll(t *testing.T) {
	env := newFSMTestEnv(t)
	seedUnfilledMail(t, env, "u1", 7)
	b := newTestBackfill(env, 3)
	if got := b.RunPass(context.Background()); got != 7 {
		t.Fatalf("filled = %d, want 7", got)
	}
	rows, err := env.metadata.MailPreviews("u1", []string{"m000", "m006"})
	if err != nil {
		t.Fatal(err)
	}
	if rows["m006"].Preview != "body number 6" {
		t.Fatalf("preview = %+v", rows)
	}
	if n := pendingCount(t, env, "u1"); n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestPreviewBackfillStopsOnCancelAndResumes(t *testing.T) {
	env := newFSMTestEnv(t)
	seedUnfilledMail(t, env, "u1", 7)

	ctx, cancel := context.WithCancel(context.Background())
	b := newTestBackfill(env, 3)
	b.pause = time.Hour // a cancel must interrupt the pause
	b.afterBatch = cancel

	done := make(chan int, 1)
	go func() { done <- b.RunPass(ctx) }()
	select {
	case got := <-done:
		if got != 3 {
			t.Fatalf("filled before cancel = %d, want 3 (one batch)", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunPass did not return after cancel")
	}
	if n := pendingCount(t, env, "u1"); n != 4 {
		t.Fatalf("pending after cancel = %d, want 4", n)
	}

	// A fresh run continues with the remainder only.
	b2 := newTestBackfill(env, 3)
	if got := b2.RunPass(context.Background()); got != 4 {
		t.Fatalf("resumed fill = %d, want 4", got)
	}
}

func TestPreviewBackfillCancelStopsMidBatchAndBeforeStart(t *testing.T) {
	env := newFSMTestEnv(t)
	seedUnfilledMail(t, env, "u1", 6)

	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if got := newTestBackfill(env, 3).RunPass(cancelled); got != 0 {
		t.Fatalf("pre-cancelled pass filled %d rows", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	b := newTestBackfill(env, 5)
	b.afterFill = cancel
	if got := b.RunPass(ctx); got != 1 {
		t.Fatalf("filled = %d, want 1: cancel mid-batch must stop before the next row", got)
	}
}

func TestPreviewBackfillDoesNotRereadFilledRows(t *testing.T) {
	env := newFSMTestEnv(t)
	hashes := seedUnfilledMail(t, env, "u1", 3)
	if err := env.metadata.SetMailPreview("u1", "m000", "kept", true); err != nil {
		t.Fatal(err)
	}
	// If m000 were reread its blob is gone and the preview could not be rebuilt.
	if err := env.blobs.Delete(hashes["m000"]); err != nil {
		t.Fatal(err)
	}
	b := newTestBackfill(env, 10)
	if got := b.RunPass(context.Background()); got != 2 {
		t.Fatalf("filled = %d, want 2", got)
	}
	rows, _ := env.metadata.MailPreviews("u1", []string{"m000"})
	if rows["m000"].Preview != "kept" || !rows["m000"].HasAttachments {
		t.Fatalf("filled row changed: %+v", rows["m000"])
	}
}

func TestPreviewBackfillMissingBlobStaysPendingAndMissingMetaIsClosed(t *testing.T) {
	env := newFSMTestEnv(t)
	hashes := seedUnfilledMail(t, env, "u1", 3)
	if err := env.blobs.Delete(hashes["m001"]); err != nil {
		t.Fatal(err)
	}
	if err := env.pebble.Delete([]byte("/user/u1/mail/m002")); err != nil {
		t.Fatal(err)
	}
	b := newTestBackfill(env, 10)
	if got := b.RunPass(context.Background()); got != 2 {
		t.Fatalf("filled = %d, want 2 (m000 and the deleted m002)", got)
	}
	pending, _ := env.metadata.PendingPreviews("u1", "", 10)
	if len(pending) != 1 || pending[0] != "m001" {
		t.Fatalf("pending = %v, want [m001]: unreadable blob must stay NULL so a later pass retries", pending)
	}
}

func TestIndexStoreObjectStoresPreviewWithoutSearch(t *testing.T) {
	env := newFSMTestEnv(t)
	hash, err := env.blobs.Put([]byte(rawTestMessage))
	if err != nil {
		t.Fatal(err)
	}
	mail := &bongov1.MailMeta{
		Guid: "m1", Subject: "Hello", From: "sender@example.com", ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(rawTestMessage))},
	}
	val, _ := proto.Marshal(mail)
	env.fsm.Apply(makeProtoLog(1, &bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/user/u1/mail/m1"), Value: val,
	}))
	env.fsm.WaitIdle(5 * time.Second)

	rows, err := env.metadata.MailPreviews("u1", []string{"m1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rows["m1"].Preview, "the quick brown pumpernickel") {
		t.Fatalf("preview = %+v", rows)
	}
}

func TestRebuildStoresPreview(t *testing.T) {
	env := newFSMTestEnv(t)
	seedUnfilledMail(t, env, "u1", 2)
	if err := rebuildDerivedIndexes(env.fsm.store, env.fsm.blobs, env.fsm.metadata, nil, testLogger()); err != nil {
		t.Fatal(err)
	}
	if n := pendingCount(t, env, "u1"); n != 0 {
		t.Fatalf("pending after rebuild = %d", n)
	}
}

// applyMail applies a MailMeta PUT and waits for the background indexer.
func applyMail(t *testing.T, env *fsmTestEnv, guid, hash string, size int) {
	t.Helper()
	mail := &bongov1.MailMeta{
		Guid: guid, Subject: "Hello", From: "sender@example.com", ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(size)},
	}
	val, _ := proto.Marshal(mail)
	env.fsm.Apply(makeProtoLog(1, &bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/user/u1/mail/" + guid), Value: val,
	}))
	env.fsm.WaitIdle(5 * time.Second)
}

func TestIndexStoreObjectStoresPreviewWithSearch(t *testing.T) {
	env := newFSMTestEnv(t)
	env.fsm.SetSearchManager(&mockSearchIndexer{})
	hash, err := env.blobs.Put([]byte(rawTestMessage))
	if err != nil {
		t.Fatal(err)
	}
	applyMail(t, env, "m1", hash, len(rawTestMessage))

	rows, err := env.metadata.MailPreviews("u1", []string{"m1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rows["m1"].Preview, "the quick brown pumpernickel") {
		t.Fatalf("preview = %+v", rows)
	}
}

// An unreadable blob (follower blob sync lag) must leave the row pending, not
// record an empty preview that nothing would ever revisit.
func TestIndexStoreObjectUnreadableBlobStaysPending(t *testing.T) {
	for _, withSearch := range []bool{false, true} {
		t.Run(fmt.Sprintf("search=%v", withSearch), func(t *testing.T) {
			env := newFSMTestEnv(t)
			if withSearch {
				env.fsm.SetSearchManager(&mockSearchIndexer{})
			}
			applyMail(t, env, "m1", strings.Repeat("a", 64), 10)

			pending, err := env.metadata.PendingPreviews("u1", "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 || pending[0] != "m1" {
				t.Fatalf("pending = %v, want [m1]", pending)
			}
		})
	}
}

func TestPreviewBackfillRunReturnsAfterCancel(t *testing.T) {
	env := newFSMTestEnv(t)
	seedUnfilledMail(t, env, "u1", 2)

	b := newTestBackfill(env, 10)
	b.interval = time.Hour // only the cancel arm can end the wait between passes
	passDone := make(chan struct{}, 1)
	b.afterBatch = func() {
		select {
		case passDone <- struct{}{}:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan struct{})
	go func() { b.Run(ctx); close(returned) }()

	select {
	case <-passDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first pass did not run")
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel; agent shutdown would hang on it")
	}
}

// Run repeats: a message whose blob only becomes readable after the first pass
// (late blob sync) is filled by a later pass.
func TestPreviewBackfillRunRepeatsToFillLateBlobs(t *testing.T) {
	env := newFSMTestEnv(t)
	hashes := seedUnfilledMail(t, env, "u1", 1)
	raw := []byte("From: a@example.com\r\nSubject: s\r\n\r\nbody number 0\r\n")
	if err := env.blobs.Delete(hashes["m000"]); err != nil {
		t.Fatal(err)
	}

	b := newTestBackfill(env, 10)
	b.interval = 5 * time.Millisecond
	var once sync.Once
	b.afterBatch = func() {
		// The first pass has tried and skipped the row; now the blob "syncs".
		once.Do(func() {
			if _, err := env.blobs.Put(raw); err != nil {
				t.Error(err)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() { b.Run(ctx); close(returned) }()
	defer func() {
		cancel()
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for pendingCount(t, env, "u1") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("a later pass never filled the late-arriving blob")
		}
		time.Sleep(5 * time.Millisecond)
	}
	rows, _ := env.metadata.MailPreviews("u1", []string{"m000"})
	if rows["m000"].Preview != "body number 0" {
		t.Fatalf("preview = %+v", rows["m000"])
	}
}
