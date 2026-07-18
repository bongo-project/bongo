package queue

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/mail"
	bongosmtp "github.com/bongo-go/bongo-go/internal/smtp"
	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

type mockLeader struct {
	leader bool
}

func (m *mockLeader) IsLeader() bool { return m.leader }

type mockDeliverer struct {
	messages chan deliveredMsg
}

type deliveredMsg struct {
	from       string
	recipients []string
	data       []byte
}

func (m *mockDeliverer) Deliver(from string, recipients []string, r io.Reader) error {
	data, _ := io.ReadAll(r)
	m.messages <- deliveredMsg{from: from, recipients: recipients, data: data}
	return nil
}

func newWorkerTestEnv(t *testing.T, isLeader bool) (*Worker, *QueueStore, *store.BlobStore) {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	blobs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	qs := NewQueueStore(ps, &directApplier{store: ps})

	relay := bongosmtp.NewRelay(&config.SMTPRelay{}, slog.Default())
	leader := &mockLeader{leader: isLeader}

	deliverer := &mockDeliverer{messages: make(chan deliveredMsg, 100)}
	bouncer := mail.NewBounceGenerator(deliverer, blobs, slog.Default(), "test.local")

	worker := NewWorker(qs, relay, nil, leader, bouncer, blobs, slog.Default())

	return worker, qs, blobs
}

func TestWorkerNonLeaderIdles(t *testing.T) {
	worker, qs, blobs := newWorkerTestEnv(t, false)
	_ = blobs

	// Enqueue a message.
	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: test\r\n\r\nHello")
	hash, _ := blobs.Put(rawMsg)

	msg := &bongov1.OutboundMessage{
		Id:          "msg-worker-1",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    hash,
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Poll should do nothing since not leader.
	ctx := context.Background()
	worker.poll(ctx)

	// Message should still be pending.
	ready, _ := qs.GetReady(time.Now().Add(1*time.Second), 100)
	if len(ready) != 1 {
		t.Errorf("expected 1 pending message (non-leader idle), got %d", len(ready))
	}
}

func TestWorkerProcessesMessage(t *testing.T) {
	// This test verifies the worker picks up messages.
	// The relay will fail (no actual SMTP server), so we verify retry behavior.
	worker, qs, blobs := newWorkerTestEnv(t, true)

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: test\r\n\r\nHello")
	hash, _ := blobs.Put(rawMsg)

	msg := &bongov1.OutboundMessage{
		Id:          "msg-worker-2",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    hash,
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Poll — relay will fail since there's no SMTP server, so message should be retried.
	ctx := context.Background()
	worker.poll(ctx)

	// Message should still exist but with incremented attempt count.
	retrieved, err := qs.Get("msg-worker-2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if retrieved.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", retrieved.AttemptCount)
	}
}

func TestWorkerMaxRetriesBounces(t *testing.T) {
	worker, qs, blobs := newWorkerTestEnv(t, true)

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: test\r\n\r\nHello")
	hash, _ := blobs.Put(rawMsg)

	msg := &bongov1.OutboundMessage{
		Id:           "msg-worker-3",
		SenderEmail:  "alice@example.com",
		Recipients:   []string{"bob@external.com"},
		BlobHash:     hash,
		AttemptCount: 9, // Next failure will be attempt 10 (>= maxAttempts-1)
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	ctx := context.Background()
	worker.poll(ctx)

	// Should be gone from pending (bounced).
	ready, _ := qs.GetReady(time.Now().Add(time.Hour), 100)
	if len(ready) != 0 {
		t.Errorf("expected 0 pending after max retries, got %d", len(ready))
	}

	// Check stats - should show in bounced.
	stats, _ := qs.Stats()
	if stats.FailedCount != 1 {
		t.Errorf("bounced count = %d, want 1", stats.FailedCount)
	}
}
