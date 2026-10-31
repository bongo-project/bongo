package queue

import (
	"context"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestWorkerSuppressedBounceHasNoPostmasterCopy proves that a suppressed DSN
// (null sender, unauthenticated forward) produces no postmaster copy either,
// with queue.bounce_copy_postmaster on; the control case gets both.
func TestWorkerSuppressedBounceHasNoPostmasterCopy(t *testing.T) {
	tests := []struct {
		name          string
		sender        string
		origin        bongov1.OutboundOrigin
		wantDelivered int
	}{
		{"null sender", "", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, 0},
		{"unauthenticated forward", "alice@hosted.example", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD, 0},
		{"control: local sender gets bounce and copy", "alice@hosted.example", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, qs, blobs, deliverer := newWorkerTestEnvWithDeliverer(t, true)
			worker.bouncer.SetPostmasterCopy(true, func(string) bool { return true }, "admin@ops.example")

			hash, _ := blobs.Put([]byte("From: a@hosted.example\r\nTo: bob@external.com\r\nSubject: t\r\n\r\nHello"))
			msg := &bongov1.OutboundMessage{
				Id:           "copy-1",
				SenderId:     "user-a",
				SenderEmail:  tt.sender,
				Recipients:   []string{"bob@external.com"},
				BlobHash:     hash,
				AttemptCount: 9,
				Origin:       tt.origin,
			}
			if err := qs.Enqueue(msg); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			worker.poll(context.Background())

			close(deliverer.messages)
			delivered := 0
			for range deliverer.messages {
				delivered++
			}
			if delivered != tt.wantDelivered {
				t.Errorf("deliveries = %d, want %d", delivered, tt.wantDelivered)
			}
		})
	}
}
