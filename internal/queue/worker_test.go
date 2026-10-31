package queue

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/observability"
	bongosmtp "github.com/bongo-project/bongo/internal/smtp"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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
	worker, qs, blobs, _ := newWorkerTestEnvWithDeliverer(t, isLeader)
	return worker, qs, blobs
}

// newWorkerTestEnvWithDeliverer is the same setup as newWorkerTestEnv but
// also returns the mockDeliverer underlying the worker's bouncer, so tests
// can assert on whether a bounce was actually delivered (GenerateAndDeliver
// unconditionally calls deliverer.Deliver — see bounce.go) rather than on
// log text alone.
func newWorkerTestEnvWithDeliverer(t *testing.T, isLeader bool) (*Worker, *QueueStore, *store.BlobStore, *mockDeliverer) {
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
	bouncer := mail.NewBounceGenerator(deliverer, blobs, slog.Default(), "test.local", nil)

	worker := NewWorker(qs, relay, nil, leader, bouncer, blobs, slog.Default())

	return worker, qs, blobs, deliverer
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

// logLineHasLevel reports whether any line in buf is a slog text-handler
// record at the given level containing msgSubstr in its msg field. Parsed
// per line (not a whole-buffer substring match) so a level string embedded
// in an unrelated field value cannot produce a false positive.
func logLineHasLevel(t *testing.T, buf *bytes.Buffer, level, msgSubstr string) bool {
	t.Helper()
	for _, line := range strings.Split(buf.String(), "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(line, "level="+level) {
			continue
		}
		// Match the msg= field specifically. A whole-line Contains would also
		// match the string appearing inside some other record's attribute
		// value, which is exactly the kind of assertion that passes for the
		// wrong reason.
		_, after, found := strings.Cut(line, " msg=")
		if !found {
			continue
		}
		if strings.Contains(after, msgSubstr) {
			return true
		}
	}
	return false
}

// TestWorkerMaxRetriesBounceSuppressesNullSender covers the worker.go:239
// max-retries/whole-message bounce path (RFC 3464 s6): a null (empty or
// whitespace-only) sender must never receive a generated DSN.
func TestWorkerMaxRetriesBounceSuppressesNullSender(t *testing.T) {
	tests := []struct {
		name          string
		sender        string
		wantSuppress  bool
		wantDelivered int
	}{
		{name: "empty sender", sender: "", wantSuppress: true, wantDelivered: 0},
		{name: "whitespace-only sender", sender: "   ", wantSuppress: true, wantDelivered: 0},
		{name: "populated sender (positive control)", sender: "alice@example.com", wantSuppress: false, wantDelivered: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, qs, blobs, deliverer := newWorkerTestEnvWithDeliverer(t, true)

			var logBuf bytes.Buffer
			worker.logger = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			rawMsg := []byte("From: sender@example.com\r\nTo: bob@external.com\r\nSubject: test\r\n\r\nHello")
			hash, _ := blobs.Put(rawMsg)

			msg := &bongov1.OutboundMessage{
				Id:           "msg-null-sender",
				SenderEmail:  tt.sender,
				Recipients:   []string{"bob@external.com"},
				BlobHash:     hash,
				AttemptCount: 9, // Next failure is attempt 10 (>= maxAttempts-1): max-retries bounce path.
			}
			if err := qs.Enqueue(msg); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}

			worker.poll(context.Background())

			// MarkBounced must still have happened regardless of DSN suppression.
			stats, err := qs.Stats()
			if err != nil {
				t.Fatalf("Stats: %v", err)
			}
			if stats.FailedCount != 1 {
				t.Errorf("bounced count = %d, want 1", stats.FailedCount)
			}

			close(deliverer.messages)
			var delivered []deliveredMsg
			for d := range deliverer.messages {
				delivered = append(delivered, d)
			}
			if len(delivered) != tt.wantDelivered {
				t.Errorf("delivered bounce messages = %d, want %d", len(delivered), tt.wantDelivered)
			}
			if tt.wantDelivered == 1 {
				got := delivered[0]
				if got.from != "" {
					t.Errorf("bounce envelope from = %q, want empty (RFC 5321 s6.1 null sender)", got.from)
				}
				if len(got.recipients) != 1 || got.recipients[0] != "alice@example.com" {
					t.Errorf("bounce recipients = %v, want [alice@example.com]", got.recipients)
				}
			}

			if tt.wantSuppress {
				if !logLineHasLevel(t, &logBuf, "INFO", "queue: DSN suppressed for null sender") {
					t.Errorf("expected INFO suppression log line, got log:\n%s", logBuf.String())
				}
			}
		})
	}
}

// TestGenerateBounceGuardsNullSender directly unit-tests the shared helper
// used by both worker.go:206 and worker.go:239, proving the RFC 3464 s6
// guard applies identically at both call sites (there is only one copy of
// the check for the compiler to type-check).
func TestGenerateBounceGuardsNullSender(t *testing.T) {
	tests := []struct {
		name         string
		sender       string
		wantDelivery bool
	}{
		{name: "empty sender is suppressed", sender: "", wantDelivery: false},
		{name: "whitespace-only sender is suppressed", sender: "  \t ", wantDelivery: false},
		{name: "populated sender delivers", sender: "alice@example.com", wantDelivery: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, _, _, deliverer := newWorkerTestEnvWithDeliverer(t, true)

			var logBuf bytes.Buffer
			worker.logger = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			msg := &bongov1.OutboundMessage{
				Id:          "msg-helper-direct",
				SenderEmail: tt.sender,
			}

			worker.generateBounce(msg, []string{"bob@external.com"}, "delivery permanently failed")

			close(deliverer.messages)
			var delivered []deliveredMsg
			for d := range deliverer.messages {
				delivered = append(delivered, d)
			}

			if tt.wantDelivery {
				if len(delivered) != 1 {
					t.Fatalf("delivered = %d, want 1", len(delivered))
				}
				if logLineHasLevel(t, &logBuf, "INFO", "queue: DSN suppressed for null sender") {
					t.Errorf("did not expect a suppression log for a populated sender, got:\n%s", logBuf.String())
				}
			} else {
				if len(delivered) != 0 {
					t.Fatalf("delivered = %d, want 0 (null sender must not receive a DSN)", len(delivered))
				}
				if !logLineHasLevel(t, &logBuf, "INFO", "queue: DSN suppressed for null sender") {
					t.Errorf("expected INFO suppression log line, got:\n%s", logBuf.String())
				}
			}
		})
	}
}

// A vacation reply is queued with a null sender, so a failed delivery of it
// must never produce a DSN, for the AUTOREPLY origin and for an origin value
// this binary does not know.
func TestGenerateBounceSuppressedForAutoReply(t *testing.T) {
	for _, origin := range []bongov1.OutboundOrigin{bongov1.OutboundOrigin_OUTBOUND_ORIGIN_AUTOREPLY, bongov1.OutboundOrigin(99)} {
		t.Run(origin.String(), func(t *testing.T) {
			worker, _, _, deliverer := newWorkerTestEnvWithDeliverer(t, true)
			msg := &bongov1.OutboundMessage{Id: "reply-1", SenderEmail: "", Origin: origin, SenderId: "u1"}
			worker.generateBounce(msg, []string{"bob@external.com"}, "delivery permanently failed")
			close(deliverer.messages)
			if n := len(deliverer.messages); n != 0 {
				t.Fatalf("delivered = %d, want 0", n)
			}
		})
	}
}

// fakeRelay lets a test drive the exact combination Deliver can return:
// permanent failures for some recipients together with a transient error for
// the rest.
type fakeRelay struct {
	calls      [][]string // recipients seen on each attempt
	permFails  []string
	deliverErr error
}

func (f *fakeRelay) Deliver(_ context.Context, _ string, recipients []string, _ []byte) ([]string, error) {
	f.calls = append(f.calls, append([]string(nil), recipients...))
	// Only report permanent failures for recipients actually presented.
	var fails []string
	for _, r := range recipients {
		for _, p := range f.permFails {
			if strings.EqualFold(r, p) {
				fails = append(fails, r)
			}
		}
	}
	return fails, f.deliverErr
}

// TestPermanentFailuresAreNotRetriedOrRebounced covers the mixed-outcome path:
// Deliver reports some recipients as permanently failed AND returns a transient
// error for the rest.
//
// Before this, the dead recipients stayed in msg.Recipients, so every retry
// relayed to them again and generated another DSN. The sender received up to
// maxAttempts copies of the same bounce for the same address.
// msg.FailedRecipients was appended to but never read by anything.
func TestPermanentFailuresAreNotRetriedOrRebounced(t *testing.T) {
	worker, qs, blobs, deliverer := newWorkerTestEnvWithDeliverer(t, true)

	relay := &fakeRelay{
		permFails:  []string{"dead@example.net"},
		deliverErr: errors.New("451 temporary failure for the rest"),
	}
	worker.relay = relay

	raw := []byte("From: alice@test.local\r\nSubject: hi\r\n\r\nbody")
	hash, err := blobs.Put(raw)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	msg := &bongov1.OutboundMessage{
		Id:          "msg-mixed",
		SenderEmail: "alice@test.local",
		Recipients:  []string{"dead@example.net", "live@example.net"},
		BlobHash:    hash,
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	worker.processMessage(context.Background(), msg)

	// The dead recipient must be gone from the retry set...
	if len(relay.calls) != 1 {
		t.Fatalf("relay called %d times, want 1", len(relay.calls))
	}
	for _, r := range msg.Recipients {
		if strings.EqualFold(r, "dead@example.net") {
			t.Error("a permanently-failed recipient is still in Recipients; it will be " +
				"relayed again on the next attempt and bounce again")
		}
	}
	if len(msg.Recipients) != 1 || !strings.EqualFold(msg.Recipients[0], "live@example.net") {
		t.Errorf("Recipients = %v, want only live@example.net", msg.Recipients)
	}

	// ...and recorded.
	if len(msg.FailedRecipients) != 1 {
		t.Errorf("FailedRecipients = %v, want one entry", msg.FailedRecipients)
	}

	// Exactly one bounce for the dead address.
	select {
	case <-deliverer.messages:
	case <-time.After(2 * time.Second):
		t.Fatal("no bounce was generated for the permanently-failed recipient")
	}
	select {
	case extra := <-deliverer.messages:
		t.Errorf("a second bounce was generated in one attempt: %v", extra.recipients)
	default:
	}
}

// TestAllRecipientsPermanentlyFailedStopsRetrying covers the degenerate case:
// every recipient permanently failed and Deliver still returned an error.
// Retrying would relay a message with an empty recipient list once per attempt
// until the retry budget ran out.
func TestAllRecipientsPermanentlyFailedStopsRetrying(t *testing.T) {
	worker, qs, blobs, _ := newWorkerTestEnvWithDeliverer(t, true)

	relay := &fakeRelay{
		permFails:  []string{"dead@example.net"},
		deliverErr: errors.New("451 nothing left"),
	}
	worker.relay = relay

	raw := []byte("From: alice@test.local\r\nSubject: hi\r\n\r\nbody")
	hash, err := blobs.Put(raw)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	msg := &bongov1.OutboundMessage{
		Id:          "msg-all-dead",
		SenderEmail: "alice@test.local",
		Recipients:  []string{"dead@example.net"},
		BlobHash:    hash,
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	worker.processMessage(context.Background(), msg)

	// The message must be off the pending queue, not rescheduled.
	ready, err := qs.GetReady(time.Now().Add(24*time.Hour), 10)
	if err != nil {
		t.Fatalf("GetReady: %v", err)
	}
	for _, m := range ready {
		if m.Id == "msg-all-dead" {
			t.Error("a message whose every recipient permanently failed was rescheduled; " +
				"each retry would relay to an empty recipient list")
		}
	}
}

func TestPruneRecipients(t *testing.T) {
	tests := []struct {
		name   string
		all    []string
		remove []string
		want   []string
	}{
		{"nothing to remove", []string{"a@x.test"}, nil, []string{"a@x.test"}},
		{"removes one", []string{"a@x.test", "b@x.test"}, []string{"a@x.test"}, []string{"b@x.test"}},
		{"case-insensitive", []string{"A@X.test"}, []string{"a@x.test"}, []string{}},
		{"whitespace tolerant", []string{" a@x.test "}, []string{"a@x.test"}, []string{}},
		{"unrelated removal", []string{"a@x.test"}, []string{"z@x.test"}, []string{"a@x.test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pruneRecipients(tt.all, tt.remove)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestWorkerBouncesImmediatelyWhenBodyBlobIsGone covers the fail-fast path.
//
// A message enqueued before the 2026-08-30 blob-GC incident spent seven days
// cycling through attempts that each failed identically, because handleFailure
// treats every error as worth retrying and only bounces once maxAttempts is
// spent. A missing body is not a transient condition: the bytes are gone, and
// the ninth attempt fails exactly as the first did.
//
// The control below matters as much as the assertion. Without it, an
// implementation that bounced on EVERY failure would pass this test while
// destroying the retry behaviour that transient failures depend on.
func TestWorkerBouncesImmediatelyWhenBodyBlobIsGone(t *testing.T) {
	worker, qs, _, deliverer := newWorkerTestEnvWithDeliverer(t, true)

	// A well-formed hash naming a blob that was never written.
	missing := strings.Repeat("ab", 32)
	if err := qs.Enqueue(&bongov1.OutboundMessage{
		Id:          "msg-missing-blob",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    missing,
		CreatedAt:   time.Now().Unix(),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	bouncedBefore := testutil.ToFloat64(observability.QueueMessagesTotal.WithLabelValues("bounced"))

	worker.poll(context.Background())

	bouncedAfter := testutil.ToFloat64(observability.QueueMessagesTotal.WithLabelValues("bounced"))
	if bouncedAfter-bouncedBefore != 1 {
		t.Errorf("bounced counter delta = %v, want 1 on the FIRST attempt; a missing body "+
			"cannot be fixed by retrying, so the message must not consume its retries",
			bouncedAfter-bouncedBefore)
	}

	// It must be gone from pending, not merely counted.
	pending, err := qs.ListPending(10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	for _, m := range pending {
		if m.Id == "msg-missing-blob" {
			t.Error("message with a missing body is still pending after a permanent failure")
		}
	}

	// The sender is told, and told the real reason.
	select {
	case d := <-deliverer.messages:
		if !strings.Contains(strings.ToLower(string(d.data)), "blob store") {
			t.Errorf("bounce does not say the body is gone, so the sender cannot tell "+
				"resending is the remedy; got: %s", truncateForLog(d.data))
		}
	default:
		t.Error("no bounce was delivered to the sender")
	}
}

// TestWorkerRetriesWhenRelayFailsRatherThanBouncing is the control for the test
// above: a transient failure must still consume a retry rather than bouncing on
// the first attempt. Without this, "bounce on every failure" passes the
// fail-fast test and silently removes the backoff that real delivery depends on.
func TestWorkerRetriesWhenRelayFailsRatherThanBouncing(t *testing.T) {
	worker, qs, blobs := newWorkerTestEnv(t, true)

	// A body that exists: the only failure here is the relay, which is transient.
	hash, err := blobs.Put([]byte("From: alice@example.com\r\nTo: bob@external.com\r\n\r\nHi"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := qs.Enqueue(&bongov1.OutboundMessage{
		Id:          "msg-transient",
		SenderEmail: "alice@example.com",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    hash,
		CreatedAt:   time.Now().Unix(),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	bouncedBefore := testutil.ToFloat64(observability.QueueMessagesTotal.WithLabelValues("bounced"))
	worker.poll(context.Background())
	bouncedAfter := testutil.ToFloat64(observability.QueueMessagesTotal.WithLabelValues("bounced"))

	if bouncedAfter != bouncedBefore {
		t.Errorf("a relay failure bounced on the first attempt (delta %v); transient failures "+
			"must keep their retries", bouncedAfter-bouncedBefore)
	}

	pending, err := qs.ListPending(10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	found := false
	for _, m := range pending {
		if m.Id == "msg-transient" {
			found = true
		}
	}
	if !found {
		t.Error("message disappeared from pending after a transient failure")
	}
}

func truncateForLog(b []byte) string {
	if len(b) > 200 {
		return string(b[:200])
	}
	return string(b)
}
