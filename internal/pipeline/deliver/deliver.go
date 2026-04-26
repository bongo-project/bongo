// Package deliver implements the final pipeline stage that stores an inbound
// message into Pebble via Raft after all scoring and filtering stages are done.
package deliver

import (
	"context"
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

// ForwardQueuer enqueues an outbound message for relay delivery.
type ForwardQueuer interface {
	Enqueue(msg *bongov1.OutboundMessage) error
}

// PreferenceProvider retrieves user preferences by user ID.
type PreferenceProvider interface {
	Get(userID string) *bongov1.UserPreferences
}

// DeliverProcessor is the final pipeline stage. It receives a fully-scored
// QueueEnvelope (with blob already written to the blob store), parses the raw
// message from the io.Reader, constructs a MailMeta protobuf for each
// recipient, and applies it to Pebble via Raft.
type DeliverProcessor struct {
	applier  RaftApplier
	resolver RecipientResolver
	logger   *slog.Logger
	queuer   ForwardQueuer
	prefs    PreferenceProvider
}

// SetQueuer sets the outbound queue for forwarding messages.
func (p *DeliverProcessor) SetQueuer(q ForwardQueuer) { p.queuer = q }

// SetPreferences sets the preference provider for account-level forwarding.
func (p *DeliverProcessor) SetPreferences(pp PreferenceProvider) { p.prefs = pp }

// NewDeliverProcessor constructs a DeliverProcessor with the required dependencies.
func NewDeliverProcessor(applier RaftApplier, resolver RecipientResolver, logger *slog.Logger) *DeliverProcessor {
	return &DeliverProcessor{
		applier:  applier,
		resolver: resolver,
		logger:   logger,
	}
}

// Name returns the stage identifier used by the pipeline runner.
func (p *DeliverProcessor) Name() string {
	return "deliver"
}

// Process stores the message into Pebble for each envelope recipient.
// The blob is already written; this stage creates the MailMeta and applies it
// via Raft. It returns a StageResult with action "pass" on success.
func (p *DeliverProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	// Idempotency guard: if the envelope was already delivered (e.g. crash
	// recovery replaying a completed envelope), skip the Raft writes entirely.
	if pipeline.HasFlag(env.Flags, pipeline.MsgFlagDelivered) {
		p.logger.Info("deliver: skipping already-delivered envelope", "id", env.Id)
		return &bongov1.StageResult{
			Stage:       "deliver",
			Backend:     "builtin",
			Action:      "pass",
			ProcessedAt: time.Now().Unix(),
		}, nil
	}

	started := time.Now()

	raw, err := io.ReadAll(msg)
	if err != nil {
		return nil, fmt.Errorf("deliver: read message: %w", err)
	}

	// Parse the RFC 5322 message with enmime.
	parsed, err := enmime.ReadEnvelope(strings.NewReader(string(raw)))
	if err != nil {
		p.logger.Warn("deliver: failed to parse message envelope, using minimal metadata", "error", err, "id", env.Id)
		parsed = nil
	}

	// Extract headers.
	subject := ""
	messageID := ""
	headerFrom := env.Sender
	inReplyTo := ""
	references := ""
	var toAddrs []string
	var ccAddrs []string

	if parsed != nil {
		subject = parsed.GetHeader("Subject")
		messageID = parsed.GetHeader("Message-Id")
		inReplyTo = parsed.GetHeader("In-Reply-To")
		references = parsed.GetHeader("References")

		if fromHeader := parsed.GetHeader("From"); fromHeader != "" {
			headerFrom = fromHeader
		}
		if toHeader := parsed.GetHeader("To"); toHeader != "" {
			for _, addr := range strings.Split(toHeader, ",") {
				addr = strings.TrimSpace(addr)
				if addr != "" {
					toAddrs = append(toAddrs, addr)
				}
			}
		}
		if ccHeader := parsed.GetHeader("Cc"); ccHeader != "" {
			for _, addr := range strings.Split(ccHeader, ",") {
				addr = strings.TrimSpace(addr)
				if addr != "" {
					ccAddrs = append(ccAddrs, addr)
				}
			}
		}
	}

	// Parse Date header; fall back to envelope received_at, then now.
	receivedAt := time.Unix(env.ReceivedAt, 0)
	if env.ReceivedAt == 0 {
		receivedAt = time.Now()
	}
	if parsed != nil {
		if dateHeader := parsed.GetHeader("Date"); dateHeader != "" {
			if t, err2 := mail.ParseDate(dateHeader); err2 == nil {
				receivedAt = t
			}
		}
	}

	// Collect attachment BlobRefs from inline parts.
	var attachmentRefs []*bongov1.BlobRef
	if parsed != nil {
		for _, att := range parsed.Attachments {
			// We do not re-store blobs here: the SMTP handler already stored
			// the raw message. Attachment content is accessible via the raw
			// message blob. We record metadata only (no hash available without
			// re-storing). Future work can extract attachment hashes during
			// ingest and carry them in the envelope.
			attachmentRefs = append(attachmentRefs, &bongov1.BlobRef{
				Size:        int64(len(att.Content)),
				ContentType: att.ContentType,
				Filename:    att.FileName,
			})
		}
	}

	// Determine spam score from envelope stage results.
	spamScore := spamScoreFromStages(env.Stages)

	// Deliver to each recipient. Check account-level forwarding preferences.
	for _, rcpt := range env.Recipients {
		userID, err := p.resolver.ResolveRecipient(rcpt)
		if err != nil {
			p.logger.Warn("deliver: unknown recipient, skipping", "recipient", rcpt, "error", err, "id", env.Id)
			continue
		}

		// Check account-level forwarding preferences.
		skipLocal := false
		if p.prefs != nil {
			prefs := p.prefs.Get(userID)
			if prefs.ForwardAddress != "" {
				env.ForwardAddresses = append(env.ForwardAddresses, prefs.ForwardAddress)
				p.logger.Info("deliver: account-level forwarding enabled",
					"user", userID,
					"forward_to", prefs.ForwardAddress,
					"keep_copy", prefs.ForwardKeepCopy,
				)
				if !prefs.ForwardKeepCopy {
					skipLocal = true
				}
			}
		}

		if skipLocal {
			p.logger.Info("deliver: skipping local delivery (forward_keep_copy=false)",
				"user", userID, "id", env.Id)
			continue
		}

		// Determine target mailbox: envelope override takes precedence.
		targetMailbox := env.TargetMailbox

		guid := uuid.New().String()

		meta := &bongov1.MailMeta{
			Guid:      guid,
			MessageId: messageID,
			Subject:   subject,
			From:      headerFrom,
			To:        toAddrs,
			Cc:        ccAddrs,
			ReceivedAt: receivedAt.Unix(),
			RawMessage: &bongov1.BlobRef{
				Hash:        env.BlobHash,
				Size:        int64(len(raw)),
				ContentType: "message/rfc822",
			},
			Attachments:   attachmentRefs,
			InReplyTo:     inReplyTo,
			References:    references,
			SpamScore:     spamScore,
			SpfResult:     env.SpfResult,
			TargetMailbox: targetMailbox,
		}

		store.SanitizeMailMeta(meta)

		// Apply tags accumulated by the rules stage.
		if len(env.PendingTags) > 0 {
			if err := store.AddTags(meta, env.PendingTags); err != nil {
				p.logger.Warn("deliver: could not apply pending tags, skipping",
					"user", userID,
					"guid", guid,
					"error", err,
				)
			}
		}

		metaBytes, err := proto.Marshal(meta)
		if err != nil {
			return nil, fmt.Errorf("deliver: marshal mail meta for %s: %w", rcpt, err)
		}

		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
			Value: metaBytes,
		}

		if err := p.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			return nil, fmt.Errorf("deliver: apply raft command for %s: %w", rcpt, err)
		}

		p.logger.Info("deliver: mail stored",
			"user", userID,
			"guid", guid,
			"subject", subject,
			"id", env.Id,
			"target_mailbox", targetMailbox,
		)
	}

	// Enqueue forwarded copies to external addresses.
	if len(env.ForwardAddresses) > 0 && p.queuer != nil {
		fwdMsg := &bongov1.OutboundMessage{
			Id:          uuid.New().String(),
			SenderEmail: env.Sender,
			Recipients:  env.ForwardAddresses,
			BlobHash:    env.BlobHash,
		}
		if err := p.queuer.Enqueue(fwdMsg); err != nil {
			p.logger.Warn("deliver: failed to enqueue forwarded message",
				"id", env.Id,
				"forward_to", env.ForwardAddresses,
				"error", err,
			)
		} else {
			p.logger.Info("deliver: forwarded message enqueued",
				"id", env.Id,
				"forward_to", env.ForwardAddresses,
			)
		}
	}

	// Mark the envelope as delivered to prevent duplicate delivery on recovery.
	env.Flags = pipeline.SetFlag(env.Flags, pipeline.MsgFlagDelivered)

	durationMs := pipeline.ClampInt32(time.Since(started).Milliseconds())
	return &bongov1.StageResult{
		Stage:       "deliver",
		Backend:     "builtin",
		Action:      "pass",
		ProcessedAt: time.Now().Unix(),
		DurationMs:  durationMs,
	}, nil
}

// spamScoreFromStages extracts the spam score from the antispam stage result,
// returning 0.0 if no antispam stage was found.
func spamScoreFromStages(stages []*bongov1.StageResult) float32 {
	for _, s := range stages {
		if s.Stage == "antispam" {
			return s.Score
		}
	}
	return 0.0
}
