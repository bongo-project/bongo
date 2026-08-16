package queue

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/mail"
	"github.com/bongo-go/bongo-go/internal/observability"
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

	relay := bongosmtp.NewRelay(&config.SMTPRelay{}, "mail.example.test", slog.Default())
	leader := &mockLeader{leader: isLeader}

	deliverer := &mockDeliverer{messages: make(chan deliveredMsg, 100)}
	bouncer := mail.NewBounceGenerator(deliverer, blobs, slog.Default(), "test.local")

	worker := NewWorker(qs, relay, nil, leader, bouncer, blobs, slog.Default())

	return worker, qs, blobs
}

func TestNewWorkerPollIntervalDefault(t *testing.T) {
	worker, _, _ := newWorkerTestEnv(t, false)
	if worker.pollInterval != defaultPollInterval {
		t.Fatalf("default poll interval = %v, want %v", worker.pollInterval, defaultPollInterval)
	}
}

func TestWithPollIntervalOverridesDefault(t *testing.T) {
	relay := bongosmtp.NewRelay(&config.SMTPRelay{}, "mail.example.test", slog.Default())
	leader := &mockLeader{leader: false}

	// A positive interval is honoured.
	w := NewWorker(nil, relay, nil, leader, nil, nil, slog.Default(), WithPollInterval(250*time.Millisecond))
	if w.pollInterval != 250*time.Millisecond {
		t.Fatalf("poll interval = %v, want 250ms", w.pollInterval)
	}

	// A non-positive interval is ignored, keeping the default.
	w2 := NewWorker(nil, relay, nil, leader, nil, nil, slog.Default(), WithPollInterval(0))
	if w2.pollInterval != defaultPollInterval {
		t.Fatalf("zero override should keep default, got %v", w2.pollInterval)
	}
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

func TestWorkerRecordsMetrics(t *testing.T) {
	worker, qs, blobs := newWorkerTestEnv(t, true)

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: test\r\n\r\nHello")
	hash, _ := blobs.Put(rawMsg)

	// A pending message with CreatedAt in the past so depth/oldest gauges move.
	if err := qs.Enqueue(&bongov1.OutboundMessage{
		Id:          "msg-metrics-1",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    hash,
		CreatedAt:   time.Now().Add(-30 * time.Second).Unix(),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	failedBefore := testutil.ToFloat64(observability.QueueMessagesTotal.WithLabelValues("failed"))

	// Relay fails (no SMTP server) -> transient failure -> retry scheduled.
	worker.poll(context.Background())

	// Depth gauge reflects the one pending message; oldest age is > 0.
	if got := testutil.ToFloat64(observability.QueueDepth); got < 1 {
		t.Errorf("QueueDepth = %v, want >= 1", got)
	}
	if got := testutil.ToFloat64(observability.QueueOldestPendingSeconds); got <= 0 {
		t.Errorf("QueueOldestPendingSeconds = %v, want > 0", got)
	}
	failedAfter := testutil.ToFloat64(observability.QueueMessagesTotal.WithLabelValues("failed"))
	if failedAfter-failedBefore != 1 {
		t.Errorf("failed counter delta = %v, want 1", failedAfter-failedBefore)
	}
}

// TestWorkerReconstructsSplitMessage: a split OutboundMessage (skeleton + layout
// + attachment blobs) is reconstructed byte-exact before relay. The harness relay
// has no SMTP server so delivery fails and the message retries — the point is it
// gets PAST reconstruction rather than failing at it.
func TestWorkerReconstructsSplitMessage(t *testing.T) {
	worker, qs, blobs := newWorkerTestEnv(t, true)

	att := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5Cg", 60) // ~3 KB
	raw := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: split relay\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--B\r\nContent-Type: application/pdf; name=\"a.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"a.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + att + "\r\n--B--\r\n")

	split, err := store.StoreSplit(blobs, raw, 1024)
	if err != nil || split == nil {
		t.Fatalf("StoreSplit = (%v, %v), want a split", split, err)
	}

	if err := qs.Enqueue(&bongov1.OutboundMessage{
		Id:          "split-relay-1",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    split.SkeletonHash,
		Layout:      split.Layout,
		Attachments: split.Attachments,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	worker.poll(context.Background())

	retrieved, err := qs.Get("split-relay-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if strings.Contains(retrieved.StatusReason, "reconstructing") {
		t.Fatalf("message failed at reconstruction: %q", retrieved.StatusReason)
	}
	if retrieved.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 (reconstructed, relayed, network-failed)", retrieved.AttemptCount)
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
