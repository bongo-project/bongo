package queue

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestWorkerForwardDSNSuppression pins when a permanently failed message
// produces a DSN. A FORWARD whose original sender was not authenticated never
// does (the sender may be forged, so the DSN would be backscatter); the field
// is absent on records queued before it existed, which therefore default to
// no DSN. LOCAL and UNSPECIFIED origins are unaffected.
func TestWorkerForwardDSNSuppression(t *testing.T) {
	const suppressed = "queue: DSN suppressed for unauthenticated forwarded sender"
	tests := []struct {
		name          string
		origin        bongov1.OutboundOrigin
		authenticated bool
		noSender      bool // record as queued before the field and SenderId existed
		wantDelivered int
	}{
		{"forward from before the upgrade (no SenderId, field absent)", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD, false, true, 0},
		{"forward unauthenticated", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD, false, false, 0},
		{"forward authenticated (control)", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD, true, false, 1},
		{"local, field unset (control)", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, false, false, 1},
		{"unspecified, field unset (control)", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_UNSPECIFIED, false, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, qs, blobs, deliverer := newWorkerTestEnvWithDeliverer(t, true)
			var logBuf bytes.Buffer
			worker.logger = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			hash, _ := blobs.Put([]byte("From: sender@example.com\r\nTo: bob@external.com\r\nSubject: test\r\n\r\nHello"))
			msg := &bongov1.OutboundMessage{
				Id:                  "fwd-1",
				SenderId:            "user-b",
				SenderEmail:         "SRS0=aaaaaaaa=bb=forged.example=mallory@hosted.example",
				Recipients:          []string{"bob@external.com"},
				BlobHash:            hash,
				AttemptCount:        3,
				CreatedAt:           time.Now().Add(-6 * 24 * time.Hour).Unix(), // past lifetime: whole-message bounce path
				Origin:              tt.origin,
				SenderAuthenticated: tt.authenticated,
			}
			if tt.noSender {
				msg.SenderId = ""
			}
			if err := qs.Enqueue(msg); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			worker.poll(context.Background())

			stats, err := qs.Stats()
			if err != nil {
				t.Fatalf("Stats: %v", err)
			}
			if stats.FailedCount != 1 {
				t.Fatalf("failed count = %d, want 1 (the permanent failure must still be recorded)", stats.FailedCount)
			}
			close(deliverer.messages)
			delivered := 0
			for range deliverer.messages {
				delivered++
			}
			if delivered != tt.wantDelivered {
				t.Errorf("DSNs delivered = %d, want %d", delivered, tt.wantDelivered)
			}
			if got := logLineHasLevel(t, &logBuf, "INFO", suppressed); got != (tt.wantDelivered == 0) {
				t.Errorf("suppression log present = %v, want %v\n%s", got, tt.wantDelivered == 0, logBuf.String())
			}
			if tt.wantDelivered == 0 && !tt.noSender {
				log := logBuf.String()
				if !strings.Contains(log, "id=fwd-1") || !strings.Contains(log, "user=user-b") {
					t.Errorf("suppression log must name the forward ID and the forwarding user:\n%s", log)
				}
			}
		})
	}
}
