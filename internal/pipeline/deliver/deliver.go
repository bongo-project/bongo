// Package deliver implements the final pipeline stage that stores an inbound
// message into Pebble via Raft after all scoring and filtering stages are done.
package deliver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jhillyerd/enmime"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/antispam"
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

	// blobs and dedupMinSize enable attachment deduplication (Phase 9): when set,
	// inbound messages with an attachment part whose encoded body is >=
	// dedupMinSize are stored as a skeleton + separate attachment blobs. nil blobs
	// or a non-positive threshold keeps whole-raw storage.
	blobs        *store.BlobStore
	dedupMinSize int
}

// SetQueuer sets the outbound queue for forwarding messages.
func (p *DeliverProcessor) SetQueuer(q ForwardQueuer) { p.queuer = q }

// SetPreferences sets the preference provider for account-level forwarding.
func (p *DeliverProcessor) SetPreferences(pp PreferenceProvider) { p.prefs = pp }

// SetAttachmentDedup enables attachment splitting with the given blob store and
// minimum encoded-attachment size in bytes. A non-positive size disables it.
func (p *DeliverProcessor) SetAttachmentDedup(blobs *store.BlobStore, minSize int) {
	p.blobs = blobs
	p.dedupMinSize = minSize
}

// splitResult carries the stored skeleton hash plus the attachment refs and
// layout for a de-MIME'd message.
type splitResult struct {
	skeletonHash string
	attachments  []*bongov1.BlobRef
	layout       []*bongov1.MessageSegment
}

// trySplit stores the message as a skeleton + deduplicated attachment blobs and
// returns the resulting refs. It returns nil (so the caller keeps whole-raw
// storage) when dedup is disabled, the message has no qualifying attachment, a
// blob write fails, or — critically — the reconstruction self-check does not
// reproduce the original bytes. This makes splitting safe to run by default: a
// message that cannot be rebuilt byte-for-byte is never stored in the split form.
func (p *DeliverProcessor) trySplit(raw []byte, id string) *splitResult {
	if p.blobs == nil || p.dedupMinSize <= 0 {
		return nil
	}
	sm := store.PrepareSplit(raw, p.dedupMinSize)
	if sm == nil {
		return nil
	}

	skeletonHash, err := p.blobs.Put(sm.Skeleton)
	if err != nil {
		p.logger.Warn("deliver: storing skeleton failed, keeping whole raw", "id", id, "error", err)
		return nil
	}
	for _, enc := range sm.Encoded {
		if _, err := p.blobs.Put(enc); err != nil {
			p.logger.Warn("deliver: storing attachment blob failed, keeping whole raw", "id", id, "error", err)
			return nil
		}
	}

	// Self-check (FR-7): reconstruct from the stored blobs and require byte-exact
	// equality with the original before committing to the split form.
	check := &bongov1.MailMeta{
		RawMessage:  &bongov1.BlobRef{Hash: skeletonHash},
		Attachments: sm.Attachments,
		Layout:      sm.Layout,
	}
	got, err := store.RawMessageBytes(p.blobs, check)
	if err != nil || !bytes.Equal(got, raw) {
		p.logger.Warn("deliver: attachment split self-check failed, keeping whole raw", "id", id, "error", err)
		return nil
	}

	p.logger.Debug("deliver: stored message as skeleton + attachment blobs",
		"id", id, "attachments", len(sm.Attachments), "skeleton_bytes", len(sm.Skeleton), "raw_bytes", len(raw))
	return &splitResult{skeletonHash: skeletonHash, attachments: sm.Attachments, layout: sm.Layout}
}

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

	// Attachment handling. By default we keep the whole raw message
	// (env.BlobHash) and record attachment metadata only. When attachment dedup
	// is enabled and a message carries a large-enough attachment, we instead
	// store a skeleton + separate deduplicated attachment blobs and reconstruct
	// the original on read (Phase 9). rawHash is the blob the metadata points at.
	rawHash := env.BlobHash
	var attachmentRefs []*bongov1.BlobRef
	var layout []*bongov1.MessageSegment

	if split := p.trySplit(raw, env.Id); split != nil {
		rawHash = split.skeletonHash
		attachmentRefs = split.attachments
		layout = split.layout
	} else if parsed != nil {
		for _, att := range parsed.Attachments {
			// Metadata only (no per-attachment blob): the payload lives inside the
			// whole raw message blob.
			attachmentRefs = append(attachmentRefs, &bongov1.BlobRef{
				Size:        int64(len(att.Content)),
				ContentType: att.ContentType,
				Filename:    att.FileName,
			})
		}
	}

	// Determine spam score from envelope stage results.
	spamScore := spamScoreFromStages(env.Stages)

	// Verify the DKIM signature once (identical for every recipient) so the
	// result and signing domain are recorded on each delivered copy.
	dkimResult, dkimDomain, dkimErr := antispam.VerifyDKIM(raw)
	if dkimErr != nil {
		p.logger.Debug("deliver: DKIM verification error", "id", env.Id, "error", dkimErr)
	}

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
		if targetMailbox == "" {
			targetMailbox = "INBOX"
			if spamScore >= 0.5 {
				targetMailbox = "Junk"
			}
		}

		guid := uuid.New().String()

		meta := &bongov1.MailMeta{
			Guid:       guid,
			MessageId:  messageID,
			Subject:    subject,
			From:       headerFrom,
			To:         toAddrs,
			Cc:         ccAddrs,
			ReceivedAt: receivedAt.Unix(),
			RawMessage: &bongov1.BlobRef{
				Hash:        rawHash,
				Size:        int64(len(raw)),
				ContentType: "message/rfc822",
			},
			Attachments:   attachmentRefs,
			Layout:        layout,
			InReplyTo:     inReplyTo,
			References:    references,
			SpamScore:     spamScore,
			SpfResult:     env.SpfResult,
			DkimResult:    dkimResult,
			DkimDomain:    dkimDomain,
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

		if err := store.DeliverMail(p.applier, userID, targetMailbox, meta, 0); err != nil {
			return nil, fmt.Errorf("deliver: apply delivery command for %s: %w", rcpt, err)
		}

		p.logger.Info("deliver: mail stored",
			"user", userID,
			"guid", guid,
			"subject", subject,
			"id", env.Id,
			"target_mailbox", targetMailbox,
		)
	}

	// Enqueue forwarded copies to external addresses, with loop protection.
	if len(env.ForwardAddresses) > 0 && p.queuer != nil {
		// Hop-count guard: each inbound pass through the MX adds a Received
		// header, so a message that re-enters via a local forward accumulates
		// hops. Past the ceiling, suppress forwarding to break the loop.
		if hops := countReceivedHeaders(raw); hops > maxForwardHops {
			p.logger.Warn("deliver: forwarding suppressed, hop limit exceeded",
				"id", env.Id, "hops", hops, "forward_to", env.ForwardAddresses)
		} else if targets := p.forwardTargets(env.ForwardAddresses, env.Recipients); len(targets) > 0 {
			fwdMsg := &bongov1.OutboundMessage{
				Id:          uuid.New().String(),
				SenderEmail: env.Sender,
				Recipients:  targets,
				BlobHash:    env.BlobHash,
			}
			if err := p.queuer.Enqueue(fwdMsg); err != nil {
				p.logger.Warn("deliver: failed to enqueue forwarded message",
					"id", env.Id,
					"forward_to", targets,
					"error", err,
				)
			} else {
				p.logger.Info("deliver: forwarded message enqueued",
					"id", env.Id,
					"forward_to", targets,
				)
			}
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

// maxForwardHops caps how many Received headers a message may carry before the
// delivery stage stops forwarding it. Each inbound pass through the MX adds one
// Received header, so a message caught in a forward loop accumulates hops; past
// this ceiling forwarding is suppressed to break the loop.
const maxForwardHops = 25

// countReceivedHeaders counts the "Received:" headers in the message header
// block (up to the first blank line). "Received-SPF" and other Received-* fields
// are deliberately not counted.
func countReceivedHeaders(raw []byte) int {
	count := 0
	for _, line := range bytes.Split(raw, []byte("\n")) {
		trimmed := bytes.TrimRight(line, "\r")
		if len(trimmed) == 0 {
			break // end of header block
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue // folded continuation of the previous header
		}
		if idx := bytes.IndexByte(trimmed, ':'); idx > 0 {
			if strings.EqualFold(string(bytes.TrimSpace(trimmed[:idx])), "Received") {
				count++
			}
		}
	}
	return count
}

// forwardTargets deduplicates the forward addresses and drops any that resolve
// to one of the message's local recipients (a self-forward), which would loop
// the message straight back into the pipeline.
func (p *DeliverProcessor) forwardTargets(addresses, recipients []string) []string {
	recipientIDs := make(map[string]struct{})
	if p.resolver != nil {
		for _, r := range recipients {
			if id, err := p.resolver.ResolveRecipient(r); err == nil {
				recipientIDs[id] = struct{}{}
			}
		}
	}

	var out []string
	seen := make(map[string]struct{})
	for _, addr := range addresses {
		if addr == "" {
			continue
		}
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		if p.resolver != nil {
			if id, err := p.resolver.ResolveRecipient(addr); err == nil {
				if _, self := recipientIDs[id]; self {
					p.logger.Warn("deliver: dropping self-forward target", "target", addr)
					continue
				}
			}
		}
		out = append(out, addr)
	}
	return out
}
