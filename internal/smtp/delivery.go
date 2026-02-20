package smtp

import (
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jhillyerd/enmime"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

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

// DeliveryPipeline processes inbound messages: parses, stores blobs,
// and applies mail metadata via Raft.
type DeliveryPipeline struct {
	applier  RaftApplier
	blobs    *store.BlobStore
	resolver RecipientResolver
	logger   *slog.Logger
}

// NewDeliveryPipeline creates a new DeliveryPipeline.
func NewDeliveryPipeline(applier RaftApplier, blobs *store.BlobStore, resolver RecipientResolver, logger *slog.Logger) *DeliveryPipeline {
	return &DeliveryPipeline{
		applier:  applier,
		blobs:    blobs,
		resolver: resolver,
		logger:   logger,
	}
}

// Deliver processes an inbound message for the given recipients.
func (d *DeliveryPipeline) Deliver(from string, recipients []string, r io.Reader) error {
	// Read the entire raw message.
	raw, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading message: %w", err)
	}

	// Store raw message as a blob.
	rawHash, err := d.blobs.Put(raw)
	if err != nil {
		return fmt.Errorf("storing raw message blob: %w", err)
	}

	// Parse the message envelope with enmime.
	envelope, err := enmime.ReadEnvelope(strings.NewReader(string(raw)))
	if err != nil {
		d.logger.Warn("smtp: failed to parse message envelope, delivering with minimal metadata", "error", err)
		// Still deliver with what we have.
		envelope = nil
	}

	subject := ""
	messageID := ""
	headerFrom := from
	var toAddrs []string
	if envelope != nil {
		subject = envelope.GetHeader("Subject")
		messageID = envelope.GetHeader("Message-Id")
		if fromHeader := envelope.GetHeader("From"); fromHeader != "" {
			headerFrom = fromHeader
		}
		if toHeader := envelope.GetHeader("To"); toHeader != "" {
			toAddrs = strings.Split(toHeader, ",")
			for i := range toAddrs {
				toAddrs[i] = strings.TrimSpace(toAddrs[i])
			}
		}
	}

	// Parse Date header, fall back to now.
	receivedAt := time.Now()
	if envelope != nil {
		if dateHeader := envelope.GetHeader("Date"); dateHeader != "" {
			if t, err := mail.ParseDate(dateHeader); err == nil {
				receivedAt = t
			}
		}
	}

	// Store attachment blobs.
	var attachmentRefs []*bongov1.BlobRef
	if envelope != nil {
		for _, att := range envelope.Attachments {
			attHash, err := d.blobs.Put(att.Content)
			if err != nil {
				d.logger.Warn("smtp: failed to store attachment blob", "error", err, "filename", att.FileName)
				continue
			}
			attachmentRefs = append(attachmentRefs, &bongov1.BlobRef{
				Hash:        attHash,
				Size:        int64(len(att.Content)),
				ContentType: att.ContentType,
			})
		}
	}

	// Deliver to each recipient.
	for _, rcpt := range recipients {
		userID, err := d.resolver.ResolveRecipient(rcpt)
		if err != nil {
			d.logger.Warn("smtp: unknown recipient, skipping", "recipient", rcpt, "error", err)
			continue
		}

		guid := uuid.New().String()

		meta := &bongov1.MailMeta{
			Guid:       guid,
			MessageId:  messageID,
			Subject:    subject,
			From:       headerFrom,
			To:         toAddrs,
			ReceivedAt: receivedAt.Unix(),
			RawMessage: &bongov1.BlobRef{
				Hash:        rawHash,
				Size:        int64(len(raw)),
				ContentType: "message/rfc822",
			},
			Attachments: attachmentRefs,
		}

		metaBytes, err := proto.Marshal(meta)
		if err != nil {
			return fmt.Errorf("marshaling mail meta: %w", err)
		}

		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
			Value: metaBytes,
		}

		if err := d.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			return fmt.Errorf("applying mail for %s: %w", rcpt, err)
		}

		d.logger.Info("smtp: mail stored", "user", userID, "guid", guid, "subject", subject)
	}

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
