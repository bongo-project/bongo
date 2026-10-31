package smtp

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
)

// InboundMeta carries metadata about an inbound message including SPF results,
// source IP, and trust status. Introduced as an options struct to avoid
// adding more positional parameters to delivery methods.
type InboundMeta struct {
	SPFResult string
	SourceIP  string
	Trusted   bool
	// AuthUserID is the SMTP AUTH user, set only for authenticated sessions.
	// Empty for trusted-network and unauthenticated sources.
	AuthUserID string

	// DSN parameters (RFC 3461) of the transaction. DSNRcpts lines up with the
	// recipients handed to EnqueueLocal; it may be nil or shorter, in which
	// case the missing recipients carry no NOTIFY or ORCPT.
	DSNRet   string
	DSNEnvID string
	DSNRcpts []RcptDSN
}

// RaftApplier applies commands through Raft consensus.
type RaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// RecipientResolver resolves an email address to a local user ID.
type RecipientResolver interface {
	ResolveRecipient(email string) (string, error)
}

// QuotaChecker checks whether a resolved local recipient's domain is over its
// storage quota. It is called after a successful ResolveRecipient so that the
// userID is already known (no duplicate resolution).
//
// RecipientWouldExceedQuota loads the owning user's profile to derive the
// primary domain, compares the domain's last-recomputed usage plus addBytes
// against its max_storage_bytes limit, and returns:
//   - (false, nil) when the domain has no quota configured (0 = unlimited) or
//     usage has not yet been recomputed (first job run pending);
//   - (true, nil) when usage >= max_storage_bytes (over or at limit) or usage
//     plus addBytes would exceed it (addBytes == 0 is the plain at-or-over test); the
//     inbound message MUST be rejected at RCPT with a permanent 5xx error;
//   - (false, err) on an unexpected store error; callers should accept the
//     message (fail open) so transient errors do not block legitimate mail.
type QuotaChecker interface {
	RecipientWouldExceedQuota(userID string, addBytes int64) (bool, error)
}

// DeliveryPipeline accepts inbound messages from the SMTP backend, stores the
// raw message blob, and enqueues a QueueEnvelope into the spool for
// asynchronous processing by the pipeline Runner.
type DeliveryPipeline struct {
	blobs  *store.BlobStore
	spool  pipeline.EnvelopeSpool
	logger *slog.Logger
}

// NewDeliveryPipeline creates a new DeliveryPipeline that stores blobs and
// enqueues messages to the spool for asynchronous pipeline processing.
func NewDeliveryPipeline(blobs *store.BlobStore, spool *pipeline.SpoolStore, logger *slog.Logger) *DeliveryPipeline {
	return &DeliveryPipeline{
		blobs:  blobs,
		spool:  spool,
		logger: logger,
	}
}

// Deliver processes an inbound message for the given recipients by storing the
// raw message blob and enqueuing a QueueEnvelope into the spool.
func (d *DeliveryPipeline) Deliver(from string, recipients []string, r io.Reader) error {
	return d.DeliverWithSPF(from, recipients, r, &InboundMeta{})
}

// DeliverWithSPF stores the raw message as a blob and enqueues a QueueEnvelope
// with SPF result and source IP metadata into the spool for asynchronous
// pipeline processing.
func (d *DeliveryPipeline) DeliverWithSPF(from string, recipients []string, r io.Reader, meta *InboundMeta) error {
	if meta == nil {
		meta = &InboundMeta{}
	}
	blobHash, err := d.StoreBlob(r)
	if err != nil {
		return err
	}
	return d.EnqueueLocal(from, recipients, blobHash, meta)
}

// StoreBlob streams the raw message to the content-addressable blob store and
// returns its hash. Streaming avoids buffering the whole message in memory.
func (d *DeliveryPipeline) StoreBlob(r io.Reader) (string, error) {
	blobHash, _, err := d.blobs.PutReader(r)
	if err != nil {
		return "", fmt.Errorf("storing raw message blob: %w", err)
	}
	return blobHash, nil
}

// EnqueueLocal enqueues one QueueEnvelope per local recipient, all referencing
// the same already-stored blob, into the spool for asynchronous local pipeline
// delivery.
//
// The rules stage, quarantine and the deliver target mailbox decide per
// envelope, so one envelope per recipient stops one user's rule from filing or
// dropping another's copy. The blob is content-addressed and stored once. The
// Received and Received-SPF headers are part of that blob, and SPF result,
// source IP and trust are copied unchanged, so every copy carries identical
// authentication input.
//
// Partial failure: the envelopes cannot be committed atomically. If any write
// fails, those already written are removed best-effort and an error is
// returned, so the SMTP reply is 451 for the whole transaction and the reply
// never claims success for an envelope that was not written. If the runner has
// already picked up a written envelope it cannot be rolled back, and the
// client's retry delivers a duplicate to that recipient. That is deliberate.
func (d *DeliveryPipeline) EnqueueLocal(from string, recipients []string, blobHash string, meta *InboundMeta) error {
	if len(recipients) == 0 {
		return errors.New("enqueuing message to spool: no recipients")
	}
	if meta == nil {
		meta = &InboundMeta{}
	}
	receivedAt := time.Now().Unix()
	envs := make([]*bongov1.QueueEnvelope, 0, len(recipients))
	ids := make([]string, 0, len(recipients))
	for i, rcpt := range recipients {
		env := &bongov1.QueueEnvelope{
			Id:         uuid.New().String(),
			Sender:     from,
			Recipients: []string{rcpt},
			ReceivedAt: receivedAt,
			BlobHash:   blobHash,
			SpfResult:  meta.SPFResult,
			SourceIp:   meta.SourceIP,
			Trusted:    meta.Trusted,
			AuthUserId: meta.AuthUserID,
			DsnRet:     meta.DSNRet,
			DsnEnvid:   meta.DSNEnvID,
		}
		if i < len(meta.DSNRcpts) {
			d := meta.DSNRcpts[i]
			env.DsnNotify = d.Notify
			env.DsnOrcptType = d.ORCPTType
			env.DsnOrcpt = d.ORCPT
		}
		envs = append(envs, env)
		ids = append(ids, env.Id)
	}

	if err := pipeline.EnqueueAll(d.spool, envs, d.logger); err != nil {
		return fmt.Errorf("enqueuing message to spool: %w", err)
	}

	d.logger.Info("smtp: message enqueued",
		"ids", ids,
		"from", from,
		"recipients", recipients,
		"blob_hash", blobHash,
		"source_ip", meta.SourceIP,
		"trusted", meta.Trusted,
	)

	return nil
}

// BlobStoreAdapter wraps a store.BlobStore to satisfy the pipeline.BlobReader
// interface needed by the pipeline Runner.
type BlobStoreAdapter struct {
	Blobs *store.BlobStore
}

// ReadBlob opens a blob by hash and returns an io.ReadCloser.
func (a *BlobStoreAdapter) ReadBlob(hash string) (io.ReadCloser, error) {
	return a.Blobs.Get(hash)
}
