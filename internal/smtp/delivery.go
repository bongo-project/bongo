package smtp

import (
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/pipeline"
	"github.com/bongo-go/bongo-go/internal/store"
)

// InboundMeta carries metadata about an inbound message including SPF results,
// source IP, and trust status. Introduced as an options struct to avoid
// adding more positional parameters to delivery methods.
type InboundMeta struct {
	SPFResult string
	SourceIP  string
	Trusted   bool
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
// RecipientOverQuota loads the owning user's profile to derive the primary
// domain, compares the domain's last-recomputed usage against its
// max_storage_bytes limit, and returns:
//   - (false, nil) when the domain has no quota configured (0 = unlimited) or
//     usage has not yet been recomputed (first job run pending);
//   - (true, nil) when usage >= max_storage_bytes (over or at limit); the
//     inbound message MUST be rejected at RCPT with a permanent 5xx error;
//   - (false, err) on an unexpected store error; callers should accept the
//     message (fail open) so transient errors do not block legitimate mail.
type QuotaChecker interface {
	RecipientOverQuota(userID string) (bool, error)
}

// DeliveryPipeline accepts inbound messages from the SMTP backend, stores the
// raw message blob, and enqueues a QueueEnvelope into the spool for
// asynchronous processing by the pipeline Runner.
type DeliveryPipeline struct {
	blobs  *store.BlobStore
	spool  *pipeline.SpoolStore
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

// EnqueueLocal enqueues a QueueEnvelope referencing an already-stored blob into
// the spool for asynchronous local pipeline delivery.
func (d *DeliveryPipeline) EnqueueLocal(from string, recipients []string, blobHash string, meta *InboundMeta) error {
	if meta == nil {
		meta = &InboundMeta{}
	}
	env := &bongov1.QueueEnvelope{
		Id:         uuid.New().String(),
		Sender:     from,
		Recipients: recipients,
		ReceivedAt: time.Now().Unix(),
		BlobHash:   blobHash,
		SpfResult:  meta.SPFResult,
		SourceIp:   meta.SourceIP,
		Trusted:    meta.Trusted,
	}

	if err := d.spool.Enqueue(env); err != nil {
		return fmt.Errorf("enqueuing message to spool: %w", err)
	}

	d.logger.Info("smtp: message enqueued",
		"id", env.Id,
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
