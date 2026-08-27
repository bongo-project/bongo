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

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
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

	// blobs and dedupMinSize enable attachment deduplication: when set,
	// inbound messages with an attachment part whose encoded body is >=
	// dedupMinSize are stored as a skeleton + separate attachment blobs. nil blobs
	// or a non-positive threshold keeps whole-raw storage.
	blobs        *store.BlobStore
	dedupMinSize int

	// authservID and spamThreshold enable trace-header stamping: an
	// Authentication-Results header naming authservID plus X-Spam-Score,
	// X-Spam-Status and X-Spam-Reasons derived from the antispam StageResult.
	// authservID is the configured SMTP hostname; empty omits
	// Authentication-Results entirely (graceful degradation) rather than emit
	// a header with a blank authserv-id. X-Spam-* stamping is independent of
	// authservID: it is emitted whenever the envelope carries an antispam
	// StageResult. spamThreshold is the score at/above which X-Spam-Status is
	// "Yes"; it also drives Junk routing (see effectiveSpamThreshold) so the
	// two never disagree.
	authservID    string
	spamThreshold float32
}

// defaultSpamThreshold is used for both Junk routing and X-Spam-Status when
// no threshold has been configured (spamThreshold <= 0), preserving prior
// behavior for default configuration.
const defaultSpamThreshold float32 = 0.5

// slowDeliverThreshold is the point at which the delivery stage logs a
// per-step breakdown of where its time went. Normal delivery is milliseconds.
const slowDeliverThreshold = 2 * time.Second

// effectiveSpamThreshold returns the configured spam threshold, falling back
// to defaultSpamThreshold when unset. Both the Junk-routing decision and the
// X-Spam-Status header use this so a message is never routed to Junk while
// being stamped X-Spam-Status: No, or vice versa.
func (p *DeliverProcessor) effectiveSpamThreshold() float32 {
	if p.spamThreshold > 0 {
		return p.spamThreshold
	}
	return defaultSpamThreshold
}

// SetTraceHeaders enables stamping Authentication-Results and X-Spam-*
// headers onto delivered messages, following the SetAttachmentDedup
// setter-injection pattern. hostname is the authserv-id used in
// Authentication-Results (the configured SMTP.Hostname); an empty hostname
// omits that header but X-Spam-* stamping and inbound trace-header stripping
// still happen. threshold should match the antispam stage's tag threshold; a
// non-positive value falls back to defaultSpamThreshold.
func (p *DeliverProcessor) SetTraceHeaders(hostname string, threshold float32) {
	p.authservID = hostname
	p.spamThreshold = threshold
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

// trySplit stores the message as a skeleton + deduplicated attachment blobs via
// store.StoreSplit and returns the resulting refs. It returns nil (so the caller
// keeps whole-raw storage) when dedup is disabled, the message has no qualifying
// attachment, a blob write fails, or the reconstruction self-check does not
// reproduce the original bytes — making splitting safe to run by default.
func (p *DeliverProcessor) trySplit(raw []byte, id string) *store.SplitStored {
	if p.blobs == nil || p.dedupMinSize <= 0 {
		return nil
	}
	split, err := store.StoreSplit(p.blobs, raw, p.dedupMinSize)
	if err != nil {
		p.logger.Warn("deliver: attachment split failed, keeping whole raw", "id", id, "error", err)
		return nil
	}
	if split != nil {
		p.logger.Info("deliver: attachment dedup",
			"id", id,
			"attachments", len(split.Attachments),
			"blobs_written", split.BlobsWritten,
			"blobs_deduped", split.BlobsDeduped,
			"bytes_saved", split.BytesSaved)
	}
	return split
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

	// Per-step timings. The stage records only a total in StageResult, which
	// was enough to prove delivery was the slow stage but not which call
	// inside it — diagnosing an 85s production stall meant decoding a spool
	// envelope and then guessing. The breakdown below is logged only when the
	// stage is slow, so it costs a few time.Since calls in the normal case.
	var tRead, tParse, tDKIM, tStamp, tBlob, tRecipients time.Duration
	// Within the recipient loop, split further: resolving the recipient,
	// reading preferences, and the Raft apply are three very different
	// dependencies (SQLite, Pebble, consensus) and only one of them is slow.
	var tResolve, tPrefs, tApply time.Duration
	mark := func(since time.Time) time.Duration { return time.Since(since) }
	defer func() {
		if total := time.Since(started); total >= slowDeliverThreshold {
			p.logger.Warn("deliver: stage was slow",
				"id", env.Id,
				"total_ms", total.Milliseconds(),
				"read_ms", tRead.Milliseconds(),
				"parse_ms", tParse.Milliseconds(),
				"dkim_verify_ms", tDKIM.Milliseconds(),
				"stamp_ms", tStamp.Milliseconds(),
				"blob_write_ms", tBlob.Milliseconds(),
				"recipients_ms", tRecipients.Milliseconds(),
				"resolve_ms", tResolve.Milliseconds(),
				"prefs_ms", tPrefs.Milliseconds(),
				"raft_apply_ms", tApply.Milliseconds(),
			)
		}
	}()

	stepStart := time.Now()
	raw, err := io.ReadAll(msg)
	if err != nil {
		return nil, fmt.Errorf("deliver: read message: %w", err)
	}
	tRead = mark(stepStart)

	// Parse the RFC 5322 message with enmime.
	stepStart = time.Now()
	parsed, err := enmime.ReadEnvelope(strings.NewReader(string(raw)))
	if err != nil {
		p.logger.Warn("deliver: failed to parse message envelope, using minimal metadata", "error", err, "id", env.Id)
		parsed = nil
	}
	tParse = mark(stepStart)

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

	// Determine spam score and antispam stage result from envelope stage results.
	spamStage := antispamStage(env.Stages)
	spamScore := spamScoreFromStages(env.Stages)

	// Verify the DKIM signature once (identical for every recipient), against
	// the ORIGINAL inbound bytes before any header mutation below, so the
	// result reflects what the sender actually signed and is recorded on each
	// delivered copy.
	stepStart = time.Now()
	dkimResult, dkimDomain, dkimErr := antispam.VerifyDKIM(raw)
	tDKIM = mark(stepStart)
	if dkimErr != nil {
		p.logger.Debug("deliver: DKIM verification error", "id", env.Id, "error", dkimErr)
	}
	dmarcResult := ""
	if spamStage != nil {
		dmarcResult = spamStage.DmarcResult
	}

	// Stamp trace headers: strip any inbound Authentication-Results / X-Spam-*
	// headers so a remote sender cannot forge a passing verdict, then prepend
	// our own Authentication-Results and X-Spam-* headers above the original
	// header set (so a DKIM signature over the original headers stays valid).
	// This must run before trySplit so the stored skeleton contains the trace
	// headers. The generated content has no timestamp, so a retried delivery
	// of the same message re-derives the identical stamped bytes and blob hash.
	stepStart = time.Now()
	stamped := stripTraceHeaders(raw)
	var traceHdrs []string
	if authHdr := buildAuthenticationResults(p.authservID, env.GetSpfResult(), dkimResult, dkimDomain, dmarcResult); authHdr != "" {
		traceHdrs = append(traceHdrs, authHdr)
	}
	traceHdrs = append(traceHdrs, buildSpamHeaders(spamStage, p.effectiveSpamThreshold())...)
	stamped = prependHeaders(stamped, traceHdrs)
	tStamp = mark(stepStart)

	// Persist the stamped bytes so what is actually stored (not just the
	// in-memory raw) carries the trace headers. Content addressing makes this
	// idempotent: a retried delivery re-derives the identical hash rather than
	// orphaning a divergent blob. The original env.BlobHash blob is left
	// untouched and reclaimed by the normal blob GC; the forwarding path below
	// deliberately keeps using env.BlobHash so relayed copies retain the
	// sender's original (and its original DKIM signature) rather than our
	// locally-stamped copy.
	//
	// trySplit is attempted first, on the STAMPED bytes: when attachment dedup
	// is enabled and the message carries a large-enough attachment it is stored
	// as a skeleton + separate deduplicated attachment blobs, reconstructed on
	// read, which therefore carry the trace headers too — and a message that
	// does split is never also written a second time as a redundant whole-raw
	// blob. Otherwise the stamped message is written as a single whole-raw blob.
	// When there is no blob store, or the write fails, fall back to the
	// original raw bytes, hash, and (further below) size together so stored
	// metadata never disagrees with stored bytes.
	rawForStorage := raw
	rawHash := env.BlobHash
	var attachmentRefs []*bongov1.BlobRef
	var layout []*bongov1.MessageSegment

	stepStart = time.Now()
	if split := p.trySplit(stamped, env.Id); split != nil {
		rawForStorage = stamped
		rawHash = split.SkeletonHash
		attachmentRefs = split.Attachments
		layout = split.Layout
	} else if p.blobs != nil {
		if h, err := p.blobs.Put(stamped); err != nil {
			p.logger.Warn("deliver: failed to store stamped message, keeping original raw",
				"id", env.Id, "error", err)
		} else {
			rawForStorage = stamped
			rawHash = h
		}
	}
	tBlob = mark(stepStart)

	if len(layout) == 0 && parsed != nil {
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

	// Deliver to each recipient. Check account-level forwarding preferences.
	stepStart = time.Now()
	for _, rcpt := range env.Recipients {
		rcptStart := time.Now()
		userID, err := p.resolver.ResolveRecipient(rcpt)
		tResolve += mark(rcptStart)
		if err != nil {
			p.logger.Warn("deliver: unknown recipient, skipping", "recipient", rcpt, "error", err, "id", env.Id)
			continue
		}

		// Check account-level forwarding preferences.
		skipLocal := false
		if p.prefs != nil {
			prefsStart := time.Now()
			prefs := p.prefs.Get(userID)
			tPrefs += mark(prefsStart)
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
			if spamScore >= p.effectiveSpamThreshold() {
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
				Size:        int64(len(rawForStorage)),
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

		applyStart := time.Now()
		applyErr := store.DeliverMail(p.applier, userID, targetMailbox, meta, 0)
		tApply += mark(applyStart)
		if applyErr != nil {
			return nil, fmt.Errorf("deliver: apply delivery command for %s: %w", rcpt, applyErr)
		}

		p.logger.Info("deliver: mail stored",
			"user", userID,
			"guid", guid,
			"subject", subject,
			"id", env.Id,
			"target_mailbox", targetMailbox,
		)
	}

	tRecipients = mark(stepStart)

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
