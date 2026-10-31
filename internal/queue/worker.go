package queue

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/observability"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	defaultPollInterval = 5 * time.Second
	batchSize           = 100

	defaultMaxLifetime      = 5 * 24 * time.Hour
	defaultMaxRetryInterval = time.Hour
	defaultConcurrency      = 5
	maxConcurrency          = 64
)

// LeaderChecker returns whether this node is the Raft leader.
type LeaderChecker interface {
	IsLeader() bool
}

// Worker processes the outbound message queue.
type Worker struct {
	store    *QueueStore
	relay    messageRelay
	registry *mail.DKIMRegistry
	leader   LeaderChecker
	bouncer  *mail.BounceGenerator
	blobs    *store.BlobStore
	logger   *slog.Logger

	pollInterval     time.Duration
	maxLifetime      time.Duration
	maxRetryInterval time.Duration
	concurrency      int
	now              func() time.Time
}

// Option is a functional option for Worker.
type Option func(*Worker)

// WithPollInterval sets how often the worker polls the queue for ready
// messages. Non-positive values are ignored (the default is retained).
func WithPollInterval(d time.Duration) Option {
	return func(w *Worker) {
		if d > 0 {
			w.pollInterval = d
		}
	}
}

// WithMaxLifetime sets how long a message may stay queued, from created_at,
// before it is bounced. Non-positive values are ignored.
func WithMaxLifetime(d time.Duration) Option {
	return func(w *Worker) {
		if d > 0 {
			w.maxLifetime = d
		}
	}
}

// WithMaxRetryInterval caps the doubling retry delay. Non-positive values are
// ignored.
func WithMaxRetryInterval(d time.Duration) Option {
	return func(w *Worker) {
		if d > 0 {
			w.maxRetryInterval = d
		}
	}
}

// WithConcurrency sets the number of simultaneous deliveries. Values outside
// 1 to 64 are ignored.
func WithConcurrency(n int) Option {
	return func(w *Worker) {
		if n >= 1 && n <= maxConcurrency {
			w.concurrency = n
		}
	}
}

// NewWorker creates a new queue worker. registry may be nil, in which case
// all messages are sent unsigned.
// messageRelay is the delivery surface the worker needs, declared here at the
// consumer rather than in internal/smtp, per the repo's interface convention.
// *smtp.Relay satisfies it.
//
// It also makes the mixed-outcome path testable. Deliver can return permanent
// failures AND a non-nil error in the same call, and the handling of that
// combination is the difference between one bounce per dead address and one per
// delivery attempt.
type messageRelay interface {
	Deliver(ctx context.Context, from string, recipients []string, rawMsg []byte) (permanentFails []string, err error)
}

func NewWorker(
	queueStore *QueueStore,
	relay messageRelay,
	registry *mail.DKIMRegistry,
	leader LeaderChecker,
	bouncer *mail.BounceGenerator,
	blobs *store.BlobStore,
	logger *slog.Logger,
	opts ...Option,
) *Worker {
	w := &Worker{
		store:        queueStore,
		relay:        relay,
		registry:     registry,
		leader:       leader,
		bouncer:      bouncer,
		blobs:        blobs,
		logger:       logger,
		pollInterval: defaultPollInterval,

		maxLifetime:      defaultMaxLifetime,
		maxRetryInterval: defaultMaxRetryInterval,
		concurrency:      defaultConcurrency,
		now:              time.Now,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Run starts the queue worker loop. Blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("queue worker started")
	defer w.logger.Info("queue worker stopped")

	ticker := time.NewTicker(w.pollInterval)
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

	// Publish queue-depth gauges (leader-only; the leader owns delivery).
	if stats, err := w.store.Stats(); err == nil {
		observability.QueueDepth.Set(float64(stats.PendingCount))
		observability.QueueOldestPendingSeconds.Set(float64(stats.OldestPendingSeconds))
	}

	msgs, err := w.store.GetReady(w.now(), batchSize)
	if err != nil {
		w.logger.Error("queue: failed to get ready messages", "error", err)
		return
	}

	if len(msgs) == 0 {
		return
	}

	w.logger.Info("queue: processing messages", "count", len(msgs))

	sem := make(chan struct{}, w.concurrency)
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
	// Read the full raw message. A split record stores a skeleton +
	// attachment blobs; the queue reconstructs the exact original bytes here
	// before signing/relaying, so DKIM and the wire message are unchanged.
	var rawMsg []byte
	if len(msg.Layout) > 0 {
		meta := &bongov1.MailMeta{
			RawMessage:  &bongov1.BlobRef{Hash: msg.BlobHash},
			Attachments: msg.Attachments,
			Layout:      msg.Layout,
		}
		reconstructed, err := store.RawMessageBytes(w.blobs, meta)
		if err != nil {
			w.logger.Error("queue: reconstructing split message", "hash", msg.BlobHash, "error", err)
			// A missing skeleton or attachment blob is permanent: the bytes are
			// gone and no later attempt can reassemble them.
			if errors.Is(err, store.ErrNotFound) {
				w.handlePermanentFailure(msg, "message body no longer in the blob store: "+err.Error())
				return
			}
			w.handleFailure(msg, "reconstructing message: "+err.Error())
			return
		}
		rawMsg = reconstructed
	} else {
		rc, err := w.blobs.Get(msg.BlobHash)
		if err != nil {
			w.logger.Error("queue: blob not found", "hash", msg.BlobHash, "error", err)
			// Distinguish "the body is gone" from "the blob store is unwell".
			// Only the first is permanent. An I/O error, a permissions problem
			// or a store that has not finished starting are all transient and
			// must keep their retries.
			if errors.Is(err, store.ErrNotFound) {
				w.handlePermanentFailure(msg, "message body no longer in the blob store: "+err.Error())
				return
			}
			w.handleFailure(msg, "blob not found: "+err.Error())
			return
		}
		raw, err := io.ReadAll(rc)
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
		rawMsg = raw
	}

	// DKIM sign — registry selects signer by From domain; unkeyed domains are
	// sent unsigned (skip is logged inside Sign). A nil registry also passes
	// through unsigned. Forwarded inbound mail is never signed (see shouldSign):
	// it keeps its original bytes and any DKIM-Signature from the author.
	signed := rawMsg
	if shouldSign(msg) {
		var err error
		signed, err = w.registry.Sign(rawMsg, w.logger)
		if err != nil {
			// Sign only returns a non-nil error on unexpected internal failures;
			// "no key for domain" is a silent skip handled inside Sign.
			w.logger.Error("queue: DKIM signing failed, sending unsigned", "error", err)
			signed = rawMsg
		}
	} else {
		w.logger.Info("dkim: not signing forwarded message", "id", msg.Id)
	}

	// Deliver via relay.
	permanentFails, deliveryErr := w.relay.Deliver(ctx, msg.SenderEmail, msg.Recipients, signed)

	// Handle permanent failures.
	//
	// Deliver can return permanent failures AND a non-nil error together: some
	// recipients 5xx at RCPT, then the transaction fails for the rest. Pruning
	// the dead recipients here is what stops the retry below re-sending to
	// them. Without it they were relayed again on every attempt, each one
	// producing another DSN, so the sender received one copy per attempt of
	// the same bounce for the same address.
	//
	// Pruning is not conditional on having a bouncer: retrying an address that
	// permanently failed is wrong whether or not we can tell anyone about it.
	if len(permanentFails) > 0 {
		if w.bouncer != nil {
			reason := "delivery permanently failed"
			if msg.StatusReason != "" {
				reason = msg.StatusReason
			}
			w.generateBounce(msg, permanentFails, reason)
		}
		msg.FailedRecipients = append(msg.FailedRecipients, permanentFails...)
		msg.Recipients = pruneRecipients(msg.Recipients, permanentFails)
	}

	if deliveryErr != nil {
		// Every remaining recipient permanently failed, so there is nothing
		// left to retry. Rescheduling here would relay a message with an empty
		// recipient list once per attempt until the budget ran out.
		if len(msg.Recipients) == 0 {
			w.logger.Info("queue: all recipients permanently failed, not retrying",
				"id", msg.Id, "failed", msg.FailedRecipients)
			if err := w.store.MarkDelivered(msg.Id); err != nil {
				w.logger.Error("queue: dequeuing fully-failed message", "id", msg.Id, "error", err)
			}
			return
		}
		msg.StatusReason = deliveryErr.Error()
		w.handleFailure(msg, deliveryErr.Error())
		return
	}

	// Success — mark delivered.
	if err := w.store.MarkDelivered(msg.Id); err != nil {
		w.logger.Error("queue: marking delivered", "id", msg.Id, "error", err)
	} else {
		observability.QueueMessagesTotal.WithLabelValues("delivered").Inc()
		latency := -1.0
		if msg.CreatedAt > 0 {
			latency = time.Since(time.Unix(msg.CreatedAt, 0)).Seconds()
			observability.QueueDeliverySeconds.Observe(latency)
		}
		// latency_seconds is emitted for the Loki dashboard (unwrap); -1 means the
		// message predates enqueue-time tracking.
		w.logger.Info("queue: message delivered", "id", msg.Id, "recipients", msg.Recipients, "latency_seconds", latency)
	}
}

// handlePermanentFailure bounces a message immediately, without waiting out
// queue.max_lifetime, for a condition no later attempt can change.
//
// The case this exists for is a body blob that no longer exists. A message
// enqueued before the 2026-08-30 blob-GC incident kept cycling through
// attempts that each failed identically, because handleFailure treats every
// error as worth retrying and only bounces once the next attempt would fall
// past created_at + max_lifetime. The wasted deliveries are the smaller cost;
// the larger one is that the DSN the sender eventually receives blames
// delivery, when the truth is that the body is gone and resending is the only
// remedy.
//
// Reserve this for genuinely unrecoverable conditions. Anything that might
// succeed later -- a peer that is down, a blob store mid-startup, a transient
// I/O error -- belongs in handleFailure, where the backoff can do its job.
func (w *Worker) handlePermanentFailure(msg *bongov1.OutboundMessage, reason string) {
	w.logger.Warn("queue: permanent failure, bouncing without further retries",
		"id", msg.Id, "reason", reason, "attempts_used", msg.AttemptCount)

	if w.bouncer != nil {
		w.generateBounce(msg, msg.Recipients, reason)
	}
	if err := w.store.MarkBounced(msg); err != nil {
		w.logger.Error("queue: marking bounced", "id", msg.Id, "error", err)
		return
	}
	observability.QueueMessagesTotal.WithLabelValues("bounced").Inc()
}

// handleFailure reschedules a failed message with a doubling delay capped at
// max_retry_interval, or bounces it when the next attempt would fall after
// created_at + max_lifetime. The attempt count is still incremented and shown
// in /admin/queue but is no longer the bounce criterion, so messages queued
// before the upgrade are judged by their created_at.
func (w *Worker) handleFailure(msg *bongov1.OutboundMessage, reason string) {
	now := w.now()
	if msg.CreatedAt == 0 {
		// Predates enqueue-time tracking. Judged against epoch 0 it would bounce
		// at once; stamp it so it gets a full lifetime (MarkFailed persists it).
		msg.CreatedAt = now.Unix()
	}
	next := now.Add(retryDelay(msg.AttemptCount+1, w.maxRetryInterval))
	deadline := time.Unix(msg.CreatedAt, 0).Add(w.maxLifetime)
	if next.After(deadline) {
		w.logger.Warn("queue: queue lifetime exceeded, bouncing", "id", msg.Id,
			"attempts", msg.AttemptCount, "age", now.Sub(time.Unix(msg.CreatedAt, 0)).Round(time.Second).String())
		if w.bouncer != nil {
			w.generateBounce(msg, msg.Recipients, reason)
		}
		if err := w.store.MarkBounced(msg); err != nil {
			w.logger.Error("queue: marking bounced", "id", msg.Id, "error", err)
		} else {
			observability.QueueMessagesTotal.WithLabelValues("bounced").Inc()
		}
		return
	}

	msg.StatusReason = reason
	if err := w.store.MarkFailed(msg, next.Unix()); err != nil {
		w.logger.Error("queue: marking failed", "id", msg.Id, "error", err)
	} else {
		observability.QueueMessagesTotal.WithLabelValues("failed").Inc()
		w.logger.Info("queue: message retry scheduled", "id", msg.Id, "attempt", msg.AttemptCount)
	}
}

// generateBounce invokes the bounce generator, unless the failing message has
// no sender to bounce to. RFC 3464 section 6 prohibits generating a DSN in
// response to a null-sender message — that is what stops bounce loops between
// two servers.
//
// Both current call sites already check w.bouncer != nil, but this checks too:
// the whole point of routing both sites through one helper is that a future
// third caller cannot get it half-right, and a caller that forgets the nil
// check would panic on an edge node where no bounce generator is configured.
func (w *Worker) generateBounce(msg *bongov1.OutboundMessage, recipients []string, reason string) {
	if w.bouncer == nil {
		return
	}
	if strings.TrimSpace(msg.SenderEmail) == "" {
		w.logger.Info("queue: DSN suppressed for null sender (RFC 3464 s6)", "id", msg.Id, "failed_recipients", recipients)
		return
	}
	// A forward of mail whose sender was not authenticated may carry a forged
	// sender; a DSN to it would be backscatter. Records queued before the field
	// existed read as unauthenticated, so they default to no DSN. Only FORWARD
	// is affected: every other origin never sets the field.
	if msg.GetOrigin() == bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD && !msg.GetSenderAuthenticated() {
		w.logger.Info("queue: DSN suppressed for unauthenticated forwarded sender", "id", msg.Id, "user", msg.SenderId)
		return
	}
	if err := w.bouncer.GenerateAndDeliver(msg, recipients, reason); err != nil {
		w.logger.Error("queue: bounce generation failed", "error", err)
	}
}

// pruneRecipients returns all minus remove, comparing addresses
// case-insensitively.
//
// The local part of an address is technically case-sensitive per RFC 5321, but
// no real mail system treats it that way, and the alternative failure here is
// to keep retrying an address the remote server has already refused outright.
func pruneRecipients(all, remove []string) []string {
	if len(remove) == 0 {
		return all
	}
	drop := make(map[string]bool, len(remove))
	for _, r := range remove {
		drop[strings.ToLower(strings.TrimSpace(r))] = true
	}
	out := make([]string, 0, len(all))
	for _, r := range all {
		if drop[strings.ToLower(strings.TrimSpace(r))] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// shouldSign reports whether the worker may DKIM-sign msg with a hosted-domain
// key. Forwarded inbound mail is relayed byte-for-byte: signing it by header
// From domain would let an outsider who spoofs a hosted domain obtain a valid
// signature for it. UNSPECIFIED (records queued before Origin existed) keeps
// the previous behaviour and is signed.
func shouldSign(msg *bongov1.OutboundMessage) bool {
	return msg.GetOrigin() != bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD
}
