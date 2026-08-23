package queue

import (
	"fmt"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// directApplier applies Raft commands directly to Pebble (no Raft, for testing).
type directApplier struct {
	store *store.PebbleStore
}

func (a *directApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	// Mirror the FSM's dispatch (internal/cluster/fsm.go applyProto): a queue
	// transition arrives as one composite command and must be interpreted, not
	// written through as a literal key. Without this the tests would exercise a
	// different code path from production.
	if cmd.Op == bongov1.Op_OP_PUT && store.IsQueueSyntheticCommand(cmd.Key) {
		return store.ApplyQueueCommand(a.store, cmd)
	}
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.store.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.store.Delete(cmd.Key)
	}
	return nil
}

func newTestQueueStore(t *testing.T) *QueueStore {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	return NewQueueStore(ps, &directApplier{store: ps})
}

func TestEnqueueAndGetReady(t *testing.T) {
	qs := newTestQueueStore(t)

	msg := &bongov1.OutboundMessage{
		Id:          "msg-001",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    "abc123",
	}

	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Should be ready immediately.
	ready, err := qs.GetReady(time.Now().Add(1*time.Second), 100)
	if err != nil {
		t.Fatalf("GetReady: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready message, got %d", len(ready))
	}
	if ready[0].Id != "msg-001" {
		t.Errorf("id = %q, want %q", ready[0].Id, "msg-001")
	}
	if ready[0].Status != bongov1.MessageStatus_MESSAGE_STATUS_PENDING {
		t.Errorf("status = %v, want PENDING", ready[0].Status)
	}
}

func TestMarkDelivered(t *testing.T) {
	qs := newTestQueueStore(t)

	msg := &bongov1.OutboundMessage{
		Id:          "msg-002",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    "abc123",
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := qs.MarkDelivered("msg-002"); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	// Should be gone.
	ready, err := qs.GetReady(time.Now().Add(1*time.Second), 100)
	if err != nil {
		t.Fatalf("GetReady: %v", err)
	}
	if len(ready) != 0 {
		t.Errorf("expected 0 ready messages after delivery, got %d", len(ready))
	}
}

func TestMarkFailed(t *testing.T) {
	qs := newTestQueueStore(t)

	msg := &bongov1.OutboundMessage{
		Id:          "msg-003",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    "abc123",
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Get the message and mark failed.
	ready, _ := qs.GetReady(time.Now().Add(1*time.Second), 100)
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready, got %d", len(ready))
	}

	if err := qs.MarkFailed(ready[0]); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	// Should not be ready now (backoff pushes it into the future).
	nowReady, _ := qs.GetReady(time.Now(), 100)
	if len(nowReady) != 0 {
		t.Errorf("expected 0 ready after failure, got %d", len(nowReady))
	}

	// Verify attempt count incremented.
	retrieved, err := qs.Get("msg-003")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if retrieved.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", retrieved.AttemptCount)
	}
	if retrieved.Status != bongov1.MessageStatus_MESSAGE_STATUS_FAILED {
		t.Errorf("status = %v, want FAILED", retrieved.Status)
	}
}

func TestMarkBounced(t *testing.T) {
	qs := newTestQueueStore(t)

	msg := &bongov1.OutboundMessage{
		Id:          "msg-004",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    "abc123",
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	ready, _ := qs.GetReady(time.Now().Add(1*time.Second), 100)
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready, got %d", len(ready))
	}

	if err := qs.MarkBounced(ready[0]); err != nil {
		t.Fatalf("MarkBounced: %v", err)
	}

	// Should be gone from pending.
	nowReady, _ := qs.GetReady(time.Now().Add(1*time.Second), 100)
	if len(nowReady) != 0 {
		t.Errorf("expected 0 pending after bounce, got %d", len(nowReady))
	}

	// Should appear in stats as bounced.
	stats, err := qs.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.FailedCount != 1 {
		t.Errorf("bounced count = %d, want 1", stats.FailedCount)
	}
}

func TestBackoffMath(t *testing.T) {
	tests := []struct {
		attempt    int32
		wantMinSec int64
		wantMaxSec int64
	}{
		{1, 120, 120},      // 2^1 * 60 = 120s = 2min
		{5, 1920, 1920},    // 2^5 * 60 = 1920s = 32min
		{10, 61440, 86400}, // 2^10 * 60 = 61440 > 86400, capped at 24h
		{15, 86400, 86400}, // way past cap
	}

	for _, tt := range tests {
		now := time.Now().Unix()
		got := backoff(tt.attempt)
		diff := got - now

		wantSec := tt.wantMinSec
		if wantSec > 86400 {
			wantSec = 86400
		}

		// Allow 2s tolerance for test execution time.
		if diff < wantSec-2 || diff > wantSec+2 {
			t.Errorf("backoff(%d) = %ds from now, want ~%ds", tt.attempt, diff, wantSec)
		}
	}
}

func TestStats(t *testing.T) {
	qs := newTestQueueStore(t)

	// Enqueue 3 messages.
	for i := 0; i < 3; i++ {
		msg := &bongov1.OutboundMessage{
			Id:          fmt.Sprintf("msg-%d", i),
			SenderEmail: "alice@example.com",
			Recipients:  []string{"bob@external.com"},
			BlobHash:    "abc123",
		}
		if err := qs.Enqueue(msg); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	stats, err := qs.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.PendingCount != 3 {
		t.Errorf("pending = %d, want 3", stats.PendingCount)
	}
	if stats.FailedCount != 0 {
		t.Errorf("bounced = %d, want 0", stats.FailedCount)
	}
}

func TestRetryNow(t *testing.T) {
	qs := newTestQueueStore(t)

	msg := &bongov1.OutboundMessage{
		Id:          "msg-retry",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    "abc123",
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Mark failed to push into the future.
	ready, _ := qs.GetReady(time.Now().Add(1*time.Second), 100)
	if err := qs.MarkFailed(ready[0]); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	// Not ready now.
	nowReady, _ := qs.GetReady(time.Now(), 100)
	if len(nowReady) != 0 {
		t.Fatalf("expected 0 ready after fail, got %d", len(nowReady))
	}

	// RetryNow should make it ready again.
	if err := qs.RetryNow("msg-retry"); err != nil {
		t.Fatalf("RetryNow: %v", err)
	}

	nowReady, _ = qs.GetReady(time.Now().Add(1*time.Second), 100)
	if len(nowReady) != 1 {
		t.Errorf("expected 1 ready after retry, got %d", len(nowReady))
	}
}

func TestDeleteFromQueue(t *testing.T) {
	qs := newTestQueueStore(t)

	msg := &bongov1.OutboundMessage{
		Id:          "msg-del",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    "abc123",
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := qs.Delete("msg-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	ready, _ := qs.GetReady(time.Now().Add(1*time.Second), 100)
	if len(ready) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(ready))
	}
}
