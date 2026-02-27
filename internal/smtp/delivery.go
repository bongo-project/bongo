package smtp

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/pipeline"
	"github.com/bongo-go/bongo-go/internal/store"
)

// RaftApplier applies commands through Raft consensus.
type RaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// RecipientResolver resolves an email address to a local user ID.
type RecipientResolver interface {
	ResolveRecipient(email string) (string, error)
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
	return d.DeliverWithSPF(from, recipients, r, "")
}

// DeliverWithSPF stores the raw message as a blob and enqueues a QueueEnvelope
// with SPF result metadata into the spool for asynchronous pipeline processing.
func (d *DeliveryPipeline) DeliverWithSPF(from string, recipients []string, r io.Reader, spfResult string) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading message: %w", err)
	}

	blobHash, err := d.blobs.Put(raw)
	if err != nil {
		return fmt.Errorf("storing raw message blob: %w", err)
	}

	env := &bongov1.QueueEnvelope{
		Id:         uuid.New().String(),
		Sender:     from,
		Recipients: recipients,
		ReceivedAt: time.Now().Unix(),
		BlobHash:   blobHash,
		SpfResult:  spfResult,
	}

	if err := d.spool.Enqueue(env); err != nil {
		return fmt.Errorf("enqueuing message to spool: %w", err)
	}

	d.logger.Info("smtp: message enqueued",
		"id", env.Id,
		"from", from,
		"recipients", recipients,
		"blob_hash", blobHash,
	)

	return nil
}

// SystemDBResolver resolves email addresses using the system.db users table.
type SystemDBResolver struct {
	metadata *store.MetadataManager
}

// NewSystemDBResolver creates a resolver backed by system.db.
func NewSystemDBResolver(metadata *store.MetadataManager) *SystemDBResolver {
	return &SystemDBResolver{metadata: metadata}
}

// ResolveRecipient looks up a user ID by email address.
func (r *SystemDBResolver) ResolveRecipient(email string) (string, error) {
	// Normalize: strip angle brackets if present.
	email = strings.TrimPrefix(email, "<")
	email = strings.TrimSuffix(email, ">")
	email = strings.TrimSpace(email)

	if r.metadata == nil || r.metadata.System == nil {
		return "", fmt.Errorf("metadata not available")
	}

	var userID string
	err := r.metadata.System.DB().QueryRow(
		"SELECT id FROM users WHERE username = ? AND enabled = 1",
		email,
	).Scan(&userID)
	if err != nil {
		return "", fmt.Errorf("recipient not found: %s", email)
	}
	return userID, nil
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
