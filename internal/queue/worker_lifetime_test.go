package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// failingRelay fails every delivery with a transient error.
type failingRelay struct{ calls int }

func (r *failingRelay) Deliver(context.Context, string, []string, []byte) ([]string, error) {
	r.calls++
	return nil, errors.New("connection refused")
}

func newLifetimeEnv(t *testing.T, clock *time.Time) (*Worker, *QueueStore, *failingRelay, *mockDeliverer, string) {
	t.Helper()
	worker, qs, blobs, deliverer := newWorkerTestEnvWithDeliverer(t, true)
	relay := &failingRelay{}
	worker.relay = relay
	worker.now = func() time.Time { return *clock }
	hash, err := blobs.Put([]byte("From: alice@example.com\r\nTo: bob@external.com\r\n\r\nHi"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return worker, qs, relay, deliverer, hash
}

// TestWorkerLifetimeWalk walks a message through a relay that always fails with
// a 1h lifetime and 10m retry cap: it keeps retrying while the next attempt
// falls within created_at + lifetime and is bounced at the first failure where
// it would not, with exactly one DSN.
func TestWorkerLifetimeWalk(t *testing.T) {
	created := time.Unix(1_800_000_000, 0)
	clock := created
	worker, qs, relay, deliverer, hash := newLifetimeEnv(t, &clock)
	worker.maxLifetime = time.Hour
	worker.maxRetryInterval = 10 * time.Minute

	if err := qs.Enqueue(&bongov1.OutboundMessage{
		Id: "walk", SenderEmail: "alice@example.com", Recipients: []string{"bob@external.com"},
		BlobHash: hash, CreatedAt: created.Unix(), NextRetryAt: created.Unix(),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	deadline := created.Add(time.Hour)
	var lastAttempt int32
	for step := 0; step < 100; step++ {
		worker.poll(context.Background())
		stats, _ := qs.Stats()
		if stats.PendingCount == 0 {
			break
		}
		msg, err := qs.Get("walk")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		lastAttempt = msg.AttemptCount
		next := time.Unix(msg.NextRetryAt, 0)
		if next.After(deadline) {
			t.Fatalf("scheduled retry %v after deadline %v", next, deadline)
		}
		clock = next
	}

	stats, _ := qs.Stats()
	if stats.PendingCount != 0 || stats.FailedCount != 1 {
		t.Fatalf("pending=%d bounced=%d, want 0 and 1", stats.PendingCount, stats.FailedCount)
	}
	// 2m,4m,8m,10m... within an hour: the attempt count keeps climbing.
	if lastAttempt < 5 {
		t.Errorf("attempt count %d, want it to keep counting", lastAttempt)
	}
	// Bounced at the first failure whose next retry passes the deadline.
	if !clock.Add(retryDelay(lastAttempt+1, 10*time.Minute)).After(deadline) {
		t.Errorf("bounced early: now=%v last=%d", clock, lastAttempt)
	}
	if relay.calls != int(lastAttempt)+1 {
		t.Errorf("relay calls = %d, want %d", relay.calls, lastAttempt+1)
	}
	if n := len(deliverer.messages); n != 1 {
		t.Errorf("DSNs generated = %d, want 1", n)
	}
}

// TestWorkerLifetimeBoundary pins the comparison: a next attempt landing
// exactly on created_at + lifetime is still allowed; one second later bounces.
func TestWorkerLifetimeBoundary(t *testing.T) {
	tests := []struct {
		name       string
		lifetime   time.Duration
		wantBounce bool
	}{
		{"next retry exactly at deadline retries", 2 * time.Minute, false},
		{"next retry one second past deadline bounces", 2*time.Minute - time.Second, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := time.Unix(1_800_000_000, 0)
			clock := created
			worker, qs, _, _, hash := newLifetimeEnv(t, &clock)
			worker.maxLifetime = tt.lifetime
			if err := qs.Enqueue(&bongov1.OutboundMessage{
				Id: "edge", SenderEmail: "alice@example.com", Recipients: []string{"bob@external.com"},
				BlobHash: hash, CreatedAt: created.Unix(), NextRetryAt: created.Unix(),
			}); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			worker.poll(context.Background())
			stats, _ := qs.Stats()
			if got := stats.FailedCount == 1; got != tt.wantBounce {
				t.Errorf("bounced = %v, want %v", got, tt.wantBounce)
			}
		})
	}
}

// TestWorkerLegacyCreatedAtZero: a record with no created_at gets a full
// lifetime from now rather than being judged against epoch 0.
func TestWorkerLegacyCreatedAtZero(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	worker, qs, _, _, hash := newLifetimeEnv(t, &clock)
	msg := &bongov1.OutboundMessage{
		Id: "legacy", SenderEmail: "alice@example.com", Recipients: []string{"bob@external.com"},
		BlobHash: hash, NextRetryAt: now.Unix(), AttemptCount: 2,
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Enqueue stamps the wall clock; clear it in memory to model a record
	// written before created_at was tracked.
	msg.CreatedAt = 0
	worker.handleFailure(msg, "boom")
	stats, _ := qs.Stats()
	if stats.FailedCount != 0 {
		t.Errorf("legacy message was bounced")
	}
	// The stamp must be persisted, or every later retry would start the
	// lifetime again.
	got, err := qs.Get("legacy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CreatedAt != now.Unix() {
		t.Errorf("stored CreatedAt = %d, want stamped %d", got.CreatedAt, now.Unix())
	}
}

// TestWorkerPreUpgradeMessageJudgedByCreatedAt: a message with few attempts but
// an old created_at bounces on its next failure.
func TestWorkerPreUpgradeMessageJudgedByCreatedAt(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	worker, qs, _, deliverer, hash := newLifetimeEnv(t, &clock)
	if err := qs.Enqueue(&bongov1.OutboundMessage{
		Id: "old", SenderEmail: "alice@example.com", Recipients: []string{"bob@external.com"},
		BlobHash: hash, AttemptCount: 1, CreatedAt: now.Add(-6 * 24 * time.Hour).Unix(), NextRetryAt: now.Unix(),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	worker.poll(context.Background())
	stats, _ := qs.Stats()
	if stats.FailedCount != 1 || stats.PendingCount != 0 {
		t.Errorf("pending=%d bounced=%d, want 0 and 1", stats.PendingCount, stats.FailedCount)
	}
	if len(deliverer.messages) != 1 {
		t.Errorf("DSNs = %d, want 1", len(deliverer.messages))
	}
}

func TestWorkerOptionsIgnoreInvalid(t *testing.T) {
	w := NewWorker(nil, nil, nil, nil, nil, nil, nil,
		WithMaxLifetime(0), WithMaxRetryInterval(-1), WithConcurrency(65))
	if w.maxLifetime != defaultMaxLifetime || w.maxRetryInterval != defaultMaxRetryInterval || w.concurrency != defaultConcurrency {
		t.Errorf("invalid options changed defaults: %v %v %d", w.maxLifetime, w.maxRetryInterval, w.concurrency)
	}
	w = NewWorker(nil, nil, nil, nil, nil, nil, nil,
		WithMaxLifetime(time.Hour), WithMaxRetryInterval(time.Minute), WithConcurrency(64))
	if w.maxLifetime != time.Hour || w.maxRetryInterval != time.Minute || w.concurrency != 64 {
		t.Errorf("valid options not applied")
	}
}
