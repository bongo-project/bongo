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
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/antispam"
	bongomail "github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/srs"
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

// ForwardTargetChecker reports whether a user may forward mail to an external
// address (a verified or grandfathered target). An error must be treated as a
// denial.
type ForwardTargetChecker interface {
	Allowed(userID, target string) (bool, error)
}

// HostedDomainChecker reports whether this server hosts a mail domain. It
// decides which forward targets are exempt from verification, and must be a
// property of the domain: an external address can resolve to a local user
// through a verified send-as identity without being hosted here.
type HostedDomainChecker func(domain string) bool

// ForwardLimiter rate limits forwards per key (the owning user ID).
type ForwardLimiter interface {
	Allow(key string) bool
}

// SenderAuthorizer reports whether a user may send as an address. It is
// satisfied by store.SendAsPolicy.
type SenderAuthorizer interface {
	CanSendAs(userID, addr string) (bool, error)
}

// SRSCodec rewrites a forward's envelope sender and reverses a bounce's
// recipient. It is satisfied by *srs.Codec.
type SRSCodec interface {
	Forward(sender, fwdDomain string) (string, error)
	Reverse(rcpt string) (string, error)
}

// UserDomainLookup returns the domain of a user's primary address, the domain
// their forwards are sent from. An error or an empty domain means unknown.
type UserDomainLookup interface {
	UserDomain(userID string) (string, error)
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

	// fwdTargets and fwdLimiter gate every forward. With no checker set no
	// forward is sent (fail closed); a nil limiter applies no rate limit.
	fwdTargets ForwardTargetChecker
	fwdLimiter ForwardLimiter
	// hostedDomain exempts targets on locally hosted domains from
	// verification; nil means no target is exempt.
	hostedDomain HostedDomainChecker
	// authorizer decides whether a forward of locally authored mail may be
	// DKIM-signed. Nil means every forward stays unsigned (fail closed).
	authorizer SenderAuthorizer

	// srs and userDomains rewrite the envelope sender of forwards and reverse
	// bounces addressed to an SRS address. Nil srs leaves forwards untouched
	// (fail safe: the original behaviour) and drops mail to SRS addresses on
	// hosted domains rather than resolving them.
	srs         SRSCodec
	userDomains UserDomainLookup

	// srsAddrLimiter and srsGlobalLimiter cap reversed bounces per original
	// address and per node. Nil means unlimited.
	srsAddrLimiter   ForwardLimiter
	srsGlobalLimiter ForwardLimiter

	// authorRL is the author's shared outbound recipient budget (the one web
	// compose and SMTP submission draw on). A LOCAL forward, which leaves signed
	// as the author, charges it per external target. Nil means LOCAL forwards
	// are not charged.
	authorRL ForwardLimiter

	// blobs and dedupMinSize enable attachment deduplication: when set,
	// inbound messages with an attachment part whose encoded body is >=
	// dedupMinSize are stored as a skeleton + separate attachment blobs. nil blobs
	// or a non-positive threshold keeps whole-raw storage.
	blobs        *store.BlobStore
	dedupMinSize int

	// authservID and spamThresholds enable trace-header stamping: an
	// Authentication-Results header naming authservID plus X-Spam-Score,
	// X-Spam-Status and X-Spam-Reasons derived from the antispam StageResult.
	// authservID is the configured SMTP hostname; empty omits
	// Authentication-Results entirely (graceful degradation) rather than emit
	// a header with a blank authserv-id. X-Spam-* stamping is independent of
	// authservID: it is emitted whenever the envelope carries an antispam
	// StageResult. The tag threshold, read from spamThresholds, is the score
	// at/above which X-Spam-Status is "Yes"; it also drives Junk routing. Process
	// loads it once per message so the two never disagree.
	authservID     string
	spamThresholds ThresholdSource

	// vacation sends vacation replies; nil means none are sent.
	vacation *vacationState
}

// ThresholdSource supplies the shared tag/quarantine pair. Only Tag is used
// here.
type ThresholdSource interface {
	Thresholds() pipeline.SpamThresholds
}

// defaultSpamThreshold is used for both Junk routing and X-Spam-Status when
// no threshold source has been set, preserving prior behavior for default
// configuration.
const defaultSpamThreshold float32 = 0.5

// slowDeliverThreshold is the point at which the delivery stage logs a
// per-step breakdown of where its time went. Normal delivery is milliseconds.
const slowDeliverThreshold = 2 * time.Second

// spamTag returns the current tag threshold, falling back to
// defaultSpamThreshold when no source is set. Process calls it once per
// message and uses the result for both the Junk-routing decision and the
// X-Spam-Status header, so a message is never routed to Junk while being
// stamped X-Spam-Status: No, or vice versa.
func (p *DeliverProcessor) spamTag() float32 {
	if p.spamThresholds != nil {
		return p.spamThresholds.Thresholds().Tag
	}
	return defaultSpamThreshold
}

// SetTraceHeaders enables stamping Authentication-Results and X-Spam-*
// headers onto delivered messages, following the SetAttachmentDedup
// setter-injection pattern. hostname is the authserv-id used in
// Authentication-Results (the configured SMTP.Hostname); an empty hostname
// omits that header but X-Spam-* stamping and inbound trace-header stripping
// still happen. The spam threshold comes from SetSpamThresholds.
func (p *DeliverProcessor) SetTraceHeaders(hostname string) {
	p.authservID = hostname
}

// SetSpamThresholds sets the source of the tag threshold used for Junk
// routing and X-Spam-Status. Nil keeps defaultSpamThreshold.
func (p *DeliverProcessor) SetSpamThresholds(src ThresholdSource) { p.spamThresholds = src }

// ThresholdSource returns the source set by SetSpamThresholds, or nil. Tests
// use it to prove the deliver and antispam stages share one holder.
func (p *DeliverProcessor) ThresholdSource() ThresholdSource { return p.spamThresholds }

// SetQueuer sets the outbound queue for forwarding messages.
func (p *DeliverProcessor) SetQueuer(q ForwardQueuer) { p.queuer = q }

// SetPreferences sets the preference provider for account-level forwarding.
func (p *DeliverProcessor) SetPreferences(pp PreferenceProvider) { p.prefs = pp }

// SetForwardTargets sets the verified-target check applied to every forward.
func (p *DeliverProcessor) SetForwardTargets(c ForwardTargetChecker) { p.fwdTargets = c }

// SetSenderAuthorizer sets the send-as check used to decide whether a forward
// of locally authored mail is signed. Nil keeps every forward unsigned.
func (p *DeliverProcessor) SetSenderAuthorizer(a SenderAuthorizer) { p.authorizer = a }

// SenderAuthorizer returns the authorizer set by SetSenderAuthorizer, or nil.
// Tests use it to prove agent wiring.
func (p *DeliverProcessor) SenderAuthorizer() SenderAuthorizer { return p.authorizer }

// SetHostedDomains sets the check for locally hosted domains; forward targets
// on them need no verification.
func (p *DeliverProcessor) SetHostedDomains(c HostedDomainChecker) { p.hostedDomain = c }

// SetSRS sets the codec used to rewrite forward senders and reverse bounces.
func (p *DeliverProcessor) SetSRS(c SRSCodec) { p.srs = c }

// SRSEnabled reports whether forward senders are rewritten and SRS bounces
// reversed.
func (p *DeliverProcessor) SRSEnabled() bool { return p.srs != nil && p.userDomains != nil }

// SetSRSBounceLimiters caps reversed SRS bounces: perAddress is keyed by the
// reversed (original) address, global covers the whole node. Either may be nil.
// A bounce over a limit is dropped, not queued for later.
func (p *DeliverProcessor) SetSRSBounceLimiters(perAddress, global ForwardLimiter) {
	p.srsAddrLimiter = perAddress
	p.srsGlobalLimiter = global
}

// SRSBounceLimiters returns the per-address and node limiters set by
// SetSRSBounceLimiters, for wiring tests.
func (p *DeliverProcessor) SRSBounceLimiters() (perAddress, global ForwardLimiter) {
	return p.srsAddrLimiter, p.srsGlobalLimiter
}

// SetAuthorRecipientLimiter sets the author's shared outbound recipient budget,
// keyed by user ID. LOCAL forwards charge it one slot per external target; a
// target over budget is sent as an ordinary unsigned FORWARD instead.
func (p *DeliverProcessor) SetAuthorRecipientLimiter(l ForwardLimiter) { p.authorRL = l }

// AuthorRecipientLimiter returns the limiter set by SetAuthorRecipientLimiter,
// for wiring tests.
func (p *DeliverProcessor) AuthorRecipientLimiter() ForwardLimiter { return p.authorRL }

// SetUserDomains sets the lookup of the forwarding user's domain for SRS.
func (p *DeliverProcessor) SetUserDomains(l UserDomainLookup) { p.userDomains = l }

// UserDomainLookup returns the lookup set by SetUserDomains, for wiring tests.
func (p *DeliverProcessor) UserDomainLookup() UserDomainLookup { return p.userDomains }

// SetForwardLimiter sets the per-user forward rate limiter.
func (p *DeliverProcessor) SetForwardLimiter(l ForwardLimiter) { p.fwdLimiter = l }

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

	// One load per message: Junk routing and X-Spam-Status share it.
	spamTag := p.spamTag()

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
	parsed, _, err := mimesafe.ReadEnvelope(raw)
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
	traceHdrs = append(traceHdrs, buildSpamHeaders(spamStage, spamTag)...)
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

	// Honour the per-message time budget once, before the first Raft write, and
	// never inside the loop: stopping part-way through a multi-recipient delivery
	// would leave some recipients delivered, and the retry from error/ would then
	// deliver to them a second time.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("deliver: message %s abandoned before delivery: %w", env.Id, err)
	}

	// Decide every forward before the recipient loop needs the answer: a user who
	// forwards without keeping a copy must still get the mail locally whenever
	// the forward will not actually be sent.
	gate := p.newForwardGate(env, raw, spamScore, spamTag)
	gate.vetRuleRequests()

	// Deliver to each recipient. Check account-level forwarding preferences.
	stepStart = time.Now()
	reversed := make(map[string]struct{})
	var vacationBatch *vacationBatch
	var vacationReplies []*vacationCandidate
	for _, rcpt := range env.Recipients {
		// An SRS address on a hosted domain is never resolved: no user owns it,
		// and resolving would hand a bounce to a catch-all owner. It arrives
		// here without passing SMTP RCPT (our own DSNs, agent-role deposits).
		if p.isHostedSRS(rcpt) {
			p.relaySRSBounce(env, gate, rcpt, reversed)
			continue
		}
		// The reserved no-relay sender takes bounces for forwards of
		// unauthenticated mail and discards them. It is never resolved, so no
		// catch-all owner or user holding the local part receives them.
		if p.isHostedNoRelay(rcpt) {
			p.logger.Info("deliver: dropping mail to the no-relay sender address", "id", env.Id)
			continue
		}
		rcptStart := time.Now()
		userID, err := p.resolver.ResolveRecipient(rcpt)
		tResolve += mark(rcptStart)
		if err != nil {
			p.logger.Warn("deliver: unknown recipient, skipping", "recipient", rcpt, "error", err, "id", env.Id)
			continue
		}

		// Check account-level forwarding preferences.
		skipLocal := false
		var userPrefs *bongov1.UserPreferences
		if p.prefs != nil {
			prefsStart := time.Now()
			prefs := p.prefs.Get(userID)
			userPrefs = prefs
			tPrefs += mark(prefsStart)
			if prefs.ForwardAddress != "" {
				sent := gate.permit(userID, prefs.ForwardAddress)
				p.logger.Info("deliver: account-level forwarding",
					"recipient", rcpt,
					"user", userID,
					"forward_to", prefs.ForwardAddress,
					"keep_copy", prefs.ForwardKeepCopy,
					"forwarded", sent,
				)
				if sent && !prefs.ForwardKeepCopy {
					skipLocal = true
				}
			}
		}

		// Determine target mailbox: envelope override takes precedence.
		targetMailbox := env.TargetMailbox
		if targetMailbox == "" {
			targetMailbox = "INBOX"
			if spamScore >= spamTag {
				targetMailbox = "Junk"
			}
		}

		// Decide a vacation reply now that the final destination is known, and
		// before the skipLocal exit below: forwarding without a copy is not a
		// deletion, so such a user still replies. The reply is sent after the
		// loop and changes nothing about delivery.
		if p.vacation != nil && userPrefs != nil && store.VacationActive(userPrefs, p.vacation.now()) {
			if vacationBatch == nil {
				vacationBatch = newVacationBatch(raw, forwardedAsSpam(env, spamScore, spamTag))
			}
			if c := p.vacationCandidateFor(env, vacationBatch, userID, rcpt, userPrefs, targetMailbox); c != nil {
				vacationReplies = append(vacationReplies, c)
			}
		}

		if skipLocal {
			p.logger.Info("deliver: skipping local delivery (forward_keep_copy=false)",
				"recipient", rcpt, "user", userID, "id", env.Id)
			continue
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

		// Copies share the blob: each is a MailMeta of its own (new GUID, same
		// blob reference) in another mailbox, so no blob is written again and
		// the blob GC, which sees every MailMeta, keeps the content while any
		// copy remains. The clones are taken before DeliverMail, which
		// sanitises the meta it is given. The writes are separate Raft
		// commands; a crash between them repeats the earlier writes on retry,
		// the same at-least-once behaviour as the recipient loop.
		var copyMetas []*copyDelivery
		for _, mb := range env.CopyMailboxes {
			if mb == "" || (!env.DiscardNormal && strings.EqualFold(mb, targetMailbox)) {
				continue
			}
			if slices.ContainsFunc(copyMetas, func(c *copyDelivery) bool { return strings.EqualFold(c.mailbox, mb) }) {
				continue
			}
			cm := proto.Clone(meta).(*bongov1.MailMeta)
			cm.Guid = uuid.New().String()
			cm.TargetMailbox = mb
			copyMetas = append(copyMetas, &copyDelivery{mailbox: mb, meta: cm})
		}

		applyStart := time.Now()
		var applyErr error
		if env.DiscardNormal {
			// A delete rule won and the rules also asked for copies or
			// forwards: the normal destination is skipped, nothing else.
			p.logger.Info("deliver: normal destination skipped (delete rule)",
				"recipient", rcpt, "user", userID, "id", env.Id)
		} else {
			applyErr = store.DeliverMail(p.applier, userID, targetMailbox, meta, env.PendingFlags)
		}
		for _, c := range copyMetas {
			if applyErr != nil {
				break
			}
			applyErr = store.DeliverMail(p.applier, userID, c.mailbox, c.meta, env.PendingFlags)
			if applyErr == nil {
				p.logger.Info("deliver: copy stored",
					"recipient", rcpt, "user", userID, "guid", c.meta.Guid, "id", env.Id, "target_mailbox", c.mailbox)
			}
		}
		tApply += mark(applyStart)
		if applyErr != nil {
			return nil, fmt.Errorf("deliver: apply delivery command for %s: %w", rcpt, applyErr)
		}

		if env.DiscardNormal {
			continue
		}
		p.logger.Info("deliver: mail stored",
			// The envelope recipient, not just the resolved user ID: an alias and
			// its target share one UUID, so a UUID alone cannot say which address
			// the mail was actually addressed to.
			"recipient", rcpt,
			"user", userID,
			"guid", guid,
			"id", env.Id,
			"target_mailbox", targetMailbox,
		)
	}

	tRecipients = mark(stepStart)

	p.enqueueForwards(env, raw, gate.approved)
	p.sendVacationReplies(env, vacationReplies)

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

// copyDelivery is one extra mailbox a copy rule asked for and the MailMeta
// written there.
type copyDelivery struct {
	mailbox string
	meta    *bongov1.MailMeta
}

// forwardGate collects the forwards one message is allowed to send. Every
// forward, whether from an account preference or a rule, passes through permit,
// which is the single enforcement point: the owning user's verified targets,
// spam suppression, the hop limit and the per-user rate limit.
type forwardGate struct {
	p       *DeliverProcessor
	env     *bongov1.QueueEnvelope
	raw     []byte
	blocked string // message-level reason no forward may be sent; empty if none
	hops    *bool  // lazily computed: only messages that forward pay for the scan
	decided map[string]bool
	// approved lists the forwards to send, in decision order.
	approved []approvedForward
}

// approvedForward is a forward that passed the gate and the user whose rule or
// preference caused it, whose domain it is sent from.
type approvedForward struct {
	userID string
	target string
}

func (p *DeliverProcessor) newForwardGate(env *bongov1.QueueEnvelope, raw []byte, spamScore, spamTag float32) *forwardGate {
	g := &forwardGate{p: p, env: env, raw: raw, decided: make(map[string]bool)}
	if forwardedAsSpam(env, spamScore, spamTag) {
		g.blocked = "spam"
	}
	return g
}

// forwardedAsSpam reports whether the pipeline marked the message as spam,
// junk or quarantined. Such mail is never forwarded: it would relay spam to a
// third party under this server's name.
func forwardedAsSpam(env *bongov1.QueueEnvelope, spamScore, spamTag float32) bool {
	if spamScore >= spamTag {
		return true
	}
	if mb := strings.ToLower(env.GetTargetMailbox()); mb == "junk" || mb == "quarantine" {
		return true
	}
	for _, st := range env.GetStages() {
		// Antispam backends mark spam with action "tag" (rspamd add-header,
		// SpamAssassin) at scores that can sit below the builtin threshold, and
		// a later rule may overwrite TargetMailbox, so the verdict itself is
		// what counts. A message released from quarantine keeps its audit
		// "quarantine" result and is therefore never forwarded.
		if st.GetStage() == "antispam" && (st.GetAction() == "tag" || st.GetAction() == "quarantine") {
			return true
		}
		if st.GetAction() == "quarantine" {
			return true
		}
	}
	return false
}

// vetRuleRequests runs the forwards requested by rules through the gate.
// Entries without an owning user cannot be checked against anyone's verified
// targets, so they are dropped.
func (g *forwardGate) vetRuleRequests() {
	for _, req := range g.env.GetForwardRequests() {
		if req.GetUserId() == "" {
			g.p.logger.Warn("deliver: dropping unattributed forward request",
				"id", g.env.Id, "forward_to", req.GetTarget())
			continue
		}
		g.permit(req.GetUserId(), req.GetTarget())
	}
	if len(g.env.GetForwardAddresses()) > 0 {
		g.p.logger.Warn("deliver: dropping legacy unattributed forward addresses",
			"id", g.env.Id, "count", len(g.env.GetForwardAddresses()))
	}
}

// permit decides whether userID's forward to target is sent, records it when
// so, and reports the decision. The answer is cached per (user, target) so an
// alias and its account in one message use one rate-limit slot, not two.
func (g *forwardGate) permit(userID, target string) bool {
	target = strings.TrimSpace(target)
	key := userID + "|" + strings.ToLower(target)
	if d, ok := g.decided[key]; ok {
		return d
	}
	reason := g.deny(userID, target)
	g.decided[key] = reason == ""
	if reason != "" {
		g.p.logger.Warn("deliver: forward not sent",
			"id", g.env.Id, "user", userID, "forward_to", target, "reason", reason)
		return false
	}
	g.approved = append(g.approved, approvedForward{userID: userID, target: target})
	return true
}

// deny returns why the forward must not be sent, or "" when it may be. The
// rate-limit slot is taken last so a forward that is refused for another
// reason does not use up the user's budget.
func (g *forwardGate) deny(userID, target string) string {
	p := g.p
	if g.blocked != "" {
		return g.blocked
	}
	if target == "" {
		return "empty target"
	}
	if p.queuer == nil {
		return "no outbound queue"
	}
	// Each inbound pass through the MX adds a Received header, so a message
	// caught in a forward loop accumulates hops; past the ceiling forwarding
	// is suppressed to break the loop.
	if g.hopLimitExceeded() {
		return "hop limit exceeded"
	}
	if len(p.forwardTargets([]string{target}, g.env.Recipients)) == 0 {
		return "forwards to a recipient of this message"
	}
	if !p.isHostedAddress(target) {
		if p.fwdTargets == nil {
			return "target verification not configured"
		}
		ok, err := p.fwdTargets.Allowed(userID, target)
		if err != nil {
			p.logger.Warn("deliver: forward target lookup failed, denying",
				"id", g.env.Id, "user", userID, "error", err)
			return "target lookup failed"
		}
		if !ok {
			return "target not verified"
		}
	}
	if p.fwdLimiter != nil && !p.fwdLimiter.Allow(userID) {
		return "rate limit exceeded"
	}
	return ""
}

// hopLimitExceeded reports whether the message carries more Received headers
// than maxForwardHops, computed once.
func (g *forwardGate) hopLimitExceeded() bool {
	if g.hops == nil {
		over := countReceivedHeaders(g.raw) > maxForwardHops
		g.hops = &over
	}
	return *g.hops
}

// isHostedAddress reports whether address is delivered locally: its domain is
// hosted here and it resolves to a local user. Resolving alone is not enough,
// because a verified send-as identity on an external domain resolves to its
// owner. Hosted targets need no verification; every other target does.
func (p *DeliverProcessor) isHostedAddress(address string) bool {
	if p.resolver == nil || p.hostedDomain == nil {
		return false
	}
	at := strings.LastIndexByte(address, '@')
	if at < 0 || !p.hostedDomain(strings.ToLower(address[at+1:])) {
		return false
	}
	_, err := p.resolver.ResolveRecipient(address)
	return err == nil
}

// enqueueForwards relays one copy of the message to every approved target. It
// is the only place a forward is queued; callers must have passed each target
// through forwardGate.permit first.
//
// A LOCAL forward leaves signed as the author, so it is charged to the
// author's outbound recipient budget like a message the author sent: one slot
// per distinct external target, in decision order. A target over budget is not
// dropped but sent as an ordinary unsigned FORWARD (see enqueueGroups). Hosted
// targets cost nothing, matching compose, which counts external recipients.
// The budget is in memory and per node, and a slot is not refunded when the
// enqueue fails.
func (p *DeliverProcessor) enqueueForwards(env *bongov1.QueueEnvelope, raw []byte, approved []approvedForward) {
	if len(approved) == 0 || p.queuer == nil {
		return
	}
	// The original MAIL FROM is kept unless SRS applies. A FORWARD copy is
	// relayed byte for byte and never re-signed, so DMARC survives only through
	// the original author's DKIM-Signature. A LOCAL copy (see forwardOrigin) is
	// signed with the From domain's key; its MAIL FROM was already checked
	// against send-as at submission, and is on a hosted domain for hosted
	// authors.
	origin := p.forwardOrigin(env, raw)
	if origin == bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL && p.authorRL != nil {
		local, refused := p.chargeAuthor(env, approved)
		p.enqueueGroups(env, local, bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL)
		if len(refused) > 0 {
			p.logger.Warn("deliver: author recipient budget spent, forwarding unsigned",
				"id", env.Id, "user", env.AuthUserId, "targets", len(refused))
		}
		p.enqueueGroups(env, refused, bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD)
		return
	}
	p.enqueueGroups(env, approved, origin)
}

// chargeAuthor splits approved into the forwards that stay LOCAL and those the
// author's budget refused. Targets are deduplicated first, so one forwarded by
// two users is charged once; each distinct external target takes one slot
// (Allow, not AllowN) so a partly spent budget splits the targets instead of
// refusing all of them.
func (p *DeliverProcessor) chargeAuthor(env *bongov1.QueueEnvelope, approved []approvedForward) (local, refused []approvedForward) {
	all := make([]string, 0, len(approved))
	for _, a := range approved {
		all = append(all, a.target)
	}
	kept := make(map[string]struct{})
	for _, t := range p.forwardTargets(all, env.Recipients) {
		kept[t] = struct{}{}
	}
	stays := make(map[string]bool) // target -> stays LOCAL, once decided
	for _, a := range approved {
		if _, ok := kept[a.target]; !ok {
			continue
		}
		ok, decided := stays[a.target]
		if !decided {
			ok = p.isHostedAddress(a.target) || p.authorRL.Allow(env.AuthUserId)
			stays[a.target] = ok
		}
		if ok {
			local = append(local, a)
		} else {
			refused = append(refused, a)
		}
	}
	return local, refused
}

// forwardGroup is one queued forward: the targets that share an envelope sender.
type forwardGroup struct {
	sender   string
	senderID string
	targets  []string
}

// enqueueGroups queues approved with the given origin, one message per group.
//
// A FORWARD of mail with a sender leaves with a rewritten envelope sender on
// the forwarding user's domain, so the next hop's SPF check passes for that
// domain instead of failing for the original author's. The null sender (a
// bounce) and LOCAL forwards keep the original sender, as do authenticated
// forwards whose user's domain is unknown or not hosted here.
//
//   - Authenticated original (SPF pass or an authenticated submitter; see
//     senderAuthenticated): SRS, with targets
//     grouped by forwarding domain. Needs an SRS codec.
//   - Unauthenticated original: the sender is the reserved no-relay address on
//     the forwarding domain, which takes bounces and discards them, so a forged
//     sender never gets backscatter. Needs no SRS codec. Targets are grouped by
//     forwarding user so the queue worker's log can name that user. With no
//     forwarding domain the forward leaves with the null sender instead, so
//     nothing can bounce back to the forged one.
//
// A target forwarded by several users is queued once, by the first of them.
func (p *DeliverProcessor) enqueueGroups(env *bongov1.QueueEnvelope, approved []approvedForward, origin bongov1.OutboundOrigin) {
	if len(approved) == 0 {
		return
	}
	isForward := origin == bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD
	hasSender := strings.TrimSpace(env.Sender) != ""
	authenticated := senderAuthenticated(env)
	srsRewrite := p.srs != nil && isForward && hasSender && authenticated
	noRelay := isForward && hasSender && !authenticated

	var groups []*forwardGroup
	byKey := make(map[string]*forwardGroup)
	domains := make(map[string]string) // userID -> forwarding domain ("" = none)
	domainOf := func(userID string) string {
		d, seen := domains[userID]
		if !seen {
			d = p.forwardingDomain(env, userID)
			domains[userID] = d
		}
		return d
	}
	queued := make(map[string]struct{})
	for _, a := range approved {
		if _, dup := queued[a.target]; dup {
			continue
		}
		queued[a.target] = struct{}{}
		key, domain := "", ""
		switch {
		case srsRewrite:
			domain = domainOf(a.userID)
			key = domain
		case noRelay:
			domain = domainOf(a.userID)
			key = a.userID
		}
		g := byKey[key]
		if g == nil {
			g = &forwardGroup{sender: env.Sender}
			switch {
			case noRelay:
				g.senderID = a.userID
				g.sender = ""
				if domain != "" {
					g.sender = srs.NoRelayLocal + "@" + domain
				}
			case domain != "":
				sender, err := p.srs.Forward(env.Sender, domain)
				if err != nil {
					p.logger.Warn("deliver: cannot rewrite forward sender, keeping original",
						"id", env.Id, "domain", domain, "error", err)
				} else {
					g.sender = sender
				}
			}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.targets = append(g.targets, a.target)
	}

	for _, g := range groups {
		targets := p.forwardTargets(g.targets, env.Recipients)
		if len(targets) == 0 {
			continue
		}
		fwdMsg := &bongov1.OutboundMessage{
			Id:                  uuid.New().String(),
			SenderId:            g.senderID,
			SenderEmail:         g.sender,
			Recipients:          targets,
			BlobHash:            env.BlobHash,
			Origin:              origin,
			SenderAuthenticated: authenticated,
		}
		if err := p.queuer.Enqueue(fwdMsg); err != nil {
			p.logger.Warn("deliver: failed to enqueue forwarded message",
				"id", env.Id,
				"forward_to", targets,
				"error", err,
			)
			continue
		}
		p.logger.Info("deliver: forwarded message enqueued",
			"id", env.Id,
			"forward_to", targets,
			"origin", origin.String(),
			"srs", srsRewrite && g.sender != env.Sender,
			"no_relay_sender", noRelay && g.sender != env.Sender,
		)
	}
}

// forwardingDomain returns the hosted domain userID's forwards are sent from,
// or "" when it cannot be established. It never invents a domain: an unknown
// or unhosted one leaves the forward with its original sender (and failing SPF
// at the next hop), which is logged so an operator can see it.
func (p *DeliverProcessor) forwardingDomain(env *bongov1.QueueEnvelope, userID string) string {
	if p.userDomains == nil || p.hostedDomain == nil {
		return ""
	}
	domain, err := p.userDomains.UserDomain(userID)
	domain = strings.ToLower(strings.TrimSpace(domain))
	if err != nil || domain == "" || !p.hostedDomain(domain) {
		p.logger.Warn("deliver: forwarding domain unknown or not hosted, forwarding without SRS",
			"id", env.Id, "user", userID, "error", err)
		return ""
	}
	return domain
}

// isHostedSRS reports whether rcpt is an SRS0/SRS1 address on a hosted domain.
func (p *DeliverProcessor) isHostedSRS(rcpt string) bool {
	if p.hostedDomain == nil {
		return false
	}
	rcpt = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(rcpt), "<"), ">")
	at := strings.LastIndexByte(rcpt, '@')
	if at <= 0 || !srs.IsSRS(rcpt[:at]) {
		return false
	}
	return p.hostedDomain(strings.ToLower(rcpt[at+1:]))
}

// isHostedNoRelay reports whether rcpt is the reserved no-relay address on a
// hosted domain.
func (p *DeliverProcessor) isHostedNoRelay(rcpt string) bool {
	if p.hostedDomain == nil {
		return false
	}
	rcpt = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(rcpt), "<"), ">")
	at := strings.LastIndexByte(rcpt, '@')
	if at <= 0 || !srs.IsNoRelay(rcpt[:at]) {
		return false
	}
	return p.hostedDomain(strings.ToLower(rcpt[at+1:]))
}

// relaySRSBounce reverses a bounce addressed to an SRS address and relays it to
// the original sender. Only null-sender mail is accepted, so SRS addresses
// cannot be used to relay arbitrary mail; spam and looping mail is dropped. A
// refusal here is silent to the sender (the message was already accepted) and
// logged without the address.
func (p *DeliverProcessor) relaySRSBounce(env *bongov1.QueueEnvelope, gate *forwardGate, rcpt string, done map[string]struct{}) {
	drop := func(reason string) {
		p.logger.Warn("deliver: dropping mail to SRS address", "id", env.Id, "reason", reason)
	}
	switch {
	case p.srs == nil:
		drop("srs not configured")
		return
	case strings.TrimSpace(env.Sender) != "":
		drop("sender is not null")
		return
	case gate.blocked != "":
		drop(gate.blocked)
		return
	case p.queuer == nil:
		drop("no outbound queue")
		return
	case gate.hopLimitExceeded():
		drop("hop limit exceeded")
		return
	}
	orig, err := p.srs.Reverse(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(rcpt), "<"), ">"))
	if err != nil {
		drop("verification failed")
		return
	}
	if _, dup := done[orig]; dup {
		return
	}
	done[orig] = struct{}{}
	// Anyone holding a valid SRS address can send null-sender mail to it for the
	// whole validity window, and each such message is relayed from this host
	// unsigned. Bounces are best effort, so over the limit it is dropped. Only
	// the domain is logged, never the address or the SRS hash.
	if p.srsAddrLimiter != nil && !p.srsAddrLimiter.Allow("srs:"+strings.ToLower(orig)) {
		p.logger.Warn("deliver: dropping SRS bounce, per-address limit reached",
			"id", env.Id, "domain", addressDomain(orig))
		return
	}
	if p.srsGlobalLimiter != nil && !p.srsGlobalLimiter.Allow("srs:global") {
		p.logger.Warn("deliver: dropping SRS bounce, node limit reached",
			"id", env.Id, "domain", addressDomain(orig))
		return
	}
	err = p.queuer.Enqueue(&bongov1.OutboundMessage{
		Id:          uuid.New().String(),
		SenderEmail: "",
		Recipients:  []string{orig},
		BlobHash:    env.BlobHash,
		Origin:      bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD,
	})
	if err != nil {
		p.logger.Warn("deliver: failed to enqueue reversed bounce", "id", env.Id, "error", err)
		return
	}
	p.logger.Info("deliver: bounce reversed through SRS and enqueued", "id", env.Id)
}

// forwardOrigin returns LOCAL (the queue worker DKIM-signs it) only for mail
// authored by an authenticated local user who may send as the single header
// From address. Anything else, including a missing authorizer, an ambiguous
// From, a lookup error or a refusal, is FORWARD and stays unsigned. The From
// is parsed with the signer's own strict rules, from the unstamped bytes the
// forward relays, so the check and the signing identity cannot disagree.
func (p *DeliverProcessor) forwardOrigin(env *bongov1.QueueEnvelope, raw []byte) bongov1.OutboundOrigin {
	const forward = bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD
	userID := env.GetAuthUserId()
	if userID == "" || p.authorizer == nil {
		return forward
	}
	addr, err := bongomail.StrictFromAddress(raw)
	if err != nil {
		return forward
	}
	ok, err := p.authorizer.CanSendAs(userID, strings.ToLower(addr))
	if err != nil {
		p.logger.Warn("deliver: send-as lookup failed, forwarding unsigned",
			"id", env.Id, "user", userID, "error", err)
		return forward
	}
	if !ok {
		return forward
	}
	return bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL
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

// addressDomain returns the lower-cased part of addr after the last "@", or ""
// when there is none. Used so logs identify a destination without its mailbox.
func addressDomain(addr string) string {
	at := strings.LastIndexByte(addr, '@')
	if at < 0 {
		return ""
	}
	return strings.ToLower(addr[at+1:])
}

// senderAuthenticated reports whether the original sender of env is
// authenticated: SPF passed for the envelope sender, or the message was
// submitted by an authenticated local user (whose SPF result describes the
// client's own IP, not the sender's domain, and whose trusted envelope skips
// DMARC). DMARC is deliberately not consulted: it aligns the header From, but
// bounces go to the envelope sender, so a DMARC pass with an SPF fail still
// leaves a forgeable MAIL FROM and would relay backscatter to it.
func senderAuthenticated(env *bongov1.QueueEnvelope) bool {
	return strings.EqualFold(env.GetSpfResult(), "pass") ||
		env.GetAuthUserId() != ""
}
