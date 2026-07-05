package queue

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/bongo-go/bongo-go/internal/mail"
	bongosmtp "github.com/bongo-go/bongo-go/internal/smtp"
	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	maxAttempts  = 10
	pollInterval = 5 * time.Second
	batchSize    = 100
	concurrency  = 5
)

// LeaderChecker returns whether this node is the Raft leader.
type LeaderChecker interface {
	IsLeader() bool
}

// Worker processes the outbound message queue.
type Worker struct {
	store   *QueueStore
	relay   *bongosmtp.Relay
	signer  *mail.DKIMSigner
	leader  LeaderChecker
	bouncer *mail.BounceGenerator
	blobs   *store.BlobStore
	logger  *slog.Logger
}

// NewWorker creates a new queue worker.
func NewWorker(
	queueStore *QueueStore,
	relay *bongosmtp.Relay,
	signer *mail.DKIMSigner,
	leader LeaderChecker,
	bouncer *mail.BounceGenerator,
	blobs *store.BlobStore,
	logger *slog.Logger,
) *Worker {
	return &Worker{
		store:   queueStore,
		relay:   relay,
		signer:  signer,
		leader:  leader,
		bouncer: bouncer,
		blobs:   blobs,
		logger:  logger,
	}
}

// Run starts the queue worker loop. Blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("queue worker started")
	defer w.logger.Info("queue worker stopped")

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *Worker) poll(ctx context.Context) {
	if !w.leader.IsLeader() {
		return
	}

	msgs, err := w.store.GetReady(time.Now(), batchSize)
	if err != nil {
		w.logger.Error("queue: failed to get ready messages", "error", err)
		return
	}

	if len(msgs) == 0 {
		return
	}

	w.logger.Info("queue: processing messages", "count", len(msgs))

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, msg := range msgs {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(m *bongov1.OutboundMessage) {
			defer wg.Done()
			defer func() { <-sem }()
			w.processMessage(ctx, m)
		}(msg)
	}

	wg.Wait()
}

func (w *Worker) processMessage(ctx context.Context, msg *bongov1.OutboundMessage) {
	// Read raw message from blob store.
	rc, err := w.blobs.Get(msg.BlobHash)
	if err != nil {
		w.logger.Error("queue: blob not found", "hash", msg.BlobHash, "error", err)
		w.handleFailure(msg, "blob not found: "+err.Error())
		return
	}
	rawMsg, err := io.ReadAll(rc)
	closeErr := rc.Close()
	if err != nil {
		w.logger.Error("queue: reading blob", "error", err)
		w.handleFailure(msg, "reading blob: "+err.Error())
		return
	}
	if closeErr != nil {
		w.logger.Error("queue: closing blob", "error", closeErr)
		w.handleFailure(msg, "closing blob: "+closeErr.Error())
		return
	}

	// DKIM sign.
	signed, err := w.signer.Sign(rawMsg)
	if err != nil {
		w.logger.Error("queue: DKIM signing failed", "error", err)
		signed = rawMsg // fall back to unsigned
	}

	// Deliver via relay.
	permanentFails, deliveryErr := w.relay.Deliver(ctx, msg.SenderEmail, msg.Recipients, signed)

	// Handle permanent failures.
	if len(permanentFails) > 0 && w.bouncer != nil {
		reason := "delivery permanently failed"
		if msg.StatusReason != "" {
			reason = msg.StatusReason
		}
		if err := w.bouncer.GenerateAndDeliver(msg, permanentFails, reason); err != nil {
			w.logger.Error("queue: bounce generation failed", "error", err)
		}
		msg.FailedRecipients = append(msg.FailedRecipients, permanentFails...)
	}

	if deliveryErr != nil {
		msg.StatusReason = deliveryErr.Error()
		w.handleFailure(msg, deliveryErr.Error())
		return
	}

	// Success — mark delivered.
	if err := w.store.MarkDelivered(msg.Id); err != nil {
		w.logger.Error("queue: marking delivered", "id", msg.Id, "error", err)
	} else {
		w.logger.Info("queue: message delivered", "id", msg.Id, "recipients", msg.Recipients)
	}
}

func (w *Worker) handleFailure(msg *bongov1.OutboundMessage, reason string) {
	if msg.AttemptCount >= maxAttempts-1 {
		// Max retries exhausted — bounce.
		w.logger.Warn("queue: max retries exhausted, bouncing", "id", msg.Id)
		if w.bouncer != nil {
			if err := w.bouncer.GenerateAndDeliver(msg, msg.Recipients, reason); err != nil {
				w.logger.Error("queue: bounce generation failed", "error", err)
			}
		}
		if err := w.store.MarkBounced(msg); err != nil {
			w.logger.Error("queue: marking bounced", "id", msg.Id, "error", err)
		}
		return
	}

	msg.StatusReason = reason
	if err := w.store.MarkFailed(msg); err != nil {
		w.logger.Error("queue: marking failed", "id", msg.Id, "error", err)
	} else {
		w.logger.Info("queue: message retry scheduled", "id", msg.Id, "attempt", msg.AttemptCount+1)
	}
}
