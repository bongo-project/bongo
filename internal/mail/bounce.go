package mail

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"mime/quotedprintable"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/net/publicsuffix"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Deliverer processes an inbound email message (matches smtp.Deliverer).
type Deliverer interface {
	Deliver(from string, recipients []string, r io.Reader) error
}

// BounceGenerator creates RFC 3464 DSN bounce messages and delivers them.
type BounceGenerator struct {
	deliverer Deliverer
	blobs     *store.BlobStore
	logger    *slog.Logger
	domain    string
	dkim      *DKIMRegistry

	// Postmaster copy (queue.bounce_copy_postmaster). Set once through
	// SetPostmasterCopy before the generator is shared; read-only afterwards.
	copyPostmaster  bool
	hostedDomain    func(domain string) bool
	postmasterAcct  string
	noTargetLogOnce sync.Once

	// External relay (SetExternalRelay): reports for a sender outside this
	// server go to the outbound queue instead of the local inbound pipeline.
	// Set once before the generator is shared; read-only afterwards.
	extQueue   ReportEnqueuer
	extIsLocal func(addr string) bool
	extPerAddr ReportLimiter
	extGlobal  ReportLimiter
}

// ReportEnqueuer is the slice of the outbound queue the generator needs to
// relay a report to an external sender.
type ReportEnqueuer interface {
	Enqueue(msg *bongov1.OutboundMessage) error
}

// ReportLimiter caps relayed reports. *RateLimiter satisfies it.
type ReportLimiter interface {
	Allow(key string) bool
}

// External report limiter keys. They are separate buckets from the SRS bounce
// limiters in the deliver stage, even where the limits are the same.
const (
	reportAddrKeyPrefix = "dsn:"
	reportGlobalKey     = "dsn:global"
)

// SetExternalRelay enables relaying of reports to senders outside the hosted
// domains. isLocal reports whether an address belongs to this server: a hosted
// domain, or an address that resolves to a local user (a send-as identity on
// an external domain is local, and its reports must reach the local inbox, not
// be mailed to that domain). Anything isLocal says is local keeps going
// through the local inbound pipeline, with its SRS reversal, no-relay and
// unknown-user handling unchanged.
//
// A report to an external sender is dropped, never queued for later, when
// either limiter refuses it: perAddr is keyed by the sender address and global
// is one bucket. Limiters are per process, so a cluster allows up to its node
// count times the global cap, as for SRS bounces.
//
// Without a queue, an isLocal func and both limiters nothing is enabled and a
// report to an external sender is dropped as before. Call before the
// generator is shared with the queue worker.
func (bg *BounceGenerator) SetExternalRelay(q ReportEnqueuer, isLocal func(addr string) bool, perAddr, global ReportLimiter) {
	if q == nil || isLocal == nil || perAddr == nil || global == nil {
		return
	}
	bg.extQueue, bg.extIsLocal, bg.extPerAddr, bg.extGlobal = q, isLocal, perAddr, global
}

// SetPostmasterCopy enables the best-effort postmaster copy of each bounce.
// hosted reports whether a domain is hosted here and able to receive mail
// (a nil func treats every domain as not hosted); fallback is the address
// used for senders on other domains (smtp.postmaster_account), "" for none.
// Call before the generator is shared with the queue worker.
func (bg *BounceGenerator) SetPostmasterCopy(enabled bool, hosted func(domain string) bool, fallback string) {
	bg.copyPostmaster = enabled
	bg.hostedDomain = hosted
	bg.postmasterAcct = strings.ToLower(strings.TrimSpace(fallback))
}

// NewBounceGenerator creates a new BounceGenerator. domain is the server's
// configured FQDN/primary domain (see ResolveServerDomain) used in bounce
// From/Message-ID/Reporting-MTA headers. registry selects the DKIM signer by
// the bounce's From domain (see DKIMRegistry.Sign); a nil registry is a
// valid passthrough and bounces are delivered unsigned.
func NewBounceGenerator(deliverer Deliverer, blobs *store.BlobStore, logger *slog.Logger, domain string, registry *DKIMRegistry) *BounceGenerator {
	return &BounceGenerator{
		deliverer: deliverer,
		blobs:     blobs,
		logger:    logger,
		domain:    domain,
		dkim:      registry,
	}
}

// ResolveServerDomain derives the server's primary mail domain from config values
// using the same fallback chain as agent.go ensureConfiguredDomain / autoconfig:
//
//  1. SMTP DKIM domain (if set)
//  2. EffectiveTLDPlusOne of SMTP hostname (if hostname is set and parseable)
//  3. SMTP hostname verbatim (if set)
//  4. os.Hostname()
//  5. "localhost" (last resort)
func ResolveServerDomain(dkimDomain, smtpHostname string) string {
	if d := strings.ToLower(strings.TrimSpace(dkimDomain)); d != "" {
		return d
	}
	if h := strings.ToLower(strings.TrimSpace(smtpHostname)); h != "" {
		if reg, err := publicsuffix.EffectiveTLDPlusOne(h); err == nil {
			return strings.ToLower(reg)
		}
		return h
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return strings.ToLower(h)
	}
	return "localhost"
}

// GenerateAndDeliver creates an RFC 3464 DSN bounce and delivers it to the sender's INBOX.
func (bg *BounceGenerator) GenerateAndDeliver(original *bongov1.OutboundMessage, failedRecipients []string, reason string) error {
	return bg.generate(original, failedRecipients, reason, reportFailed, time.Time{})
}

// GenerateDelayReport delivers an "Action: delayed" report (RFC 3464) to the
// sender: the message is still queued and will be retried until retryUntil.
// The caller decides whether a delay report is wanted and allowed (NOTIFY=DELAY,
// not a null or unauthenticated-forward sender); like every report it goes
// through the local inbound pipeline, or to the outbound queue for a sender
// outside the hosted domains (see SetExternalRelay). It never produces a
// postmaster copy: that copy is for bounces only.
func (bg *BounceGenerator) GenerateDelayReport(original *bongov1.OutboundMessage, recipients []string, reason string, retryUntil time.Time) error {
	return bg.generate(original, recipients, reason, reportDelayed, retryUntil)
}

// GenerateDeliveredReport delivers an "Action: delivered" report to the sender
// for recipients the message reached. The caller has already applied the same
// backscatter gate as for a bounce. No postmaster copy.
func (bg *BounceGenerator) GenerateDeliveredReport(original *bongov1.OutboundMessage, recipients []string) error {
	return bg.generate(original, recipients, "", reportDelivered, time.Time{})
}

// GenerateRelayedReport sends an "Action: relayed" report (RFC 3464 section
// 2.3.3) for recipients the message was handed to, via a server that cannot
// report on delivery. The caller has already applied the backscatter gate. No
// postmaster copy.
func (bg *BounceGenerator) GenerateRelayedReport(original *bongov1.OutboundMessage, recipients []string) error {
	return bg.generate(original, recipients, "", reportRelayed, time.Time{})
}

func (bg *BounceGenerator) generate(original *bongov1.OutboundMessage, recipients []string, reason string, kind reportKind, retryUntil time.Time) error {
	// Best-effort retrieval of the original message's header block for the
	// third (text/rfc822-headers) MIME part. Never fails the bounce: any
	// problem is logged inside originalHeaderBlock and it returns "", which
	// buildBounce treats as "omit part 3".
	originalHeaders := bg.originalHeaderBlock(original)

	// A configured hostname may carry a trailing dot; an address with one is
	// not valid RFC 5322, and the strict DKIM signer-domain parse would refuse
	// to sign the DSN.
	domain := strings.TrimSuffix(bg.domain, ".")
	opts := bounceOptions{
		to:           original.SenderEmail,
		subject:      kind.subject(),
		kind:         kind,
		retryUntil:   retryUntil,
		originalFull: bg.originalFullMessage(original),
	}
	bounceMsg := buildBounce(original, recipients, reason, domain, originalHeaders, opts)

	// Recipient addresses are logged as before; the ENVID, ORCPT and report
	// body are never logged.
	bg.logger.Info("bounce: generating DSN",
		"kind", kind.action(),
		"sender", original.SenderEmail,
		"recipients", recipients,
		"reason", reason,
	)

	// A sender outside this server gets the report through the outbound queue.
	// The queue worker DKIM-signs it (server-generated mail has origin LOCAL),
	// so it is stored unsigned here or it would be signed twice.
	var senderErr error
	if bg.relaysExternally(original.SenderEmail) {
		senderErr = bg.relayExternal(original.SenderEmail, bounceMsg)
		bg.postmasterCopy(original, recipients, reason, kind, domain, originalHeaders)
		return senderErr
	}

	signed := bg.signBounce(bounceMsg)

	// RFC 5321 s6.1: a DSN's envelope sender (MAIL FROM) must be null (<>) so
	// a failure to deliver this bounce itself cannot generate another bounce
	// (bounce-of-a-bounce loop). The From: header still identifies the
	// sender as `MAILER-DAEMON <postmaster@<domain>>` (set by buildBounce
	// above) — MAILER-DAEMON is the display name and postmaster@<domain> is
	// the addr-spec that DKIM/DMARC alignment is computed from. Only the
	// envelope sender is null.
	//
	// The report goes through the local inbound pipeline, whose deliver stage
	// drops a recipient it cannot resolve. A sender outside the hosted domains
	// is handled above when SetExternalRelay is configured; without it such a
	// report is dropped there.
	senderErr = bg.deliverer.Deliver(
		"",
		[]string{original.SenderEmail},
		bytes.NewReader(signed),
	)

	// The postmaster copy is independent of the sender's bounce: it is
	// attempted even when that delivery failed, and never changes the result.
	// It sits after Worker.generateBounce's suppression (null sender,
	// unauthenticated forward), so a suppressed bounce is never copied. Only
	// failure reports are copied, and never with the original body (RET=FULL
	// applies to the sender's report only).
	bg.postmasterCopy(original, recipients, reason, kind, domain, originalHeaders)
	return senderErr
}

// postmasterCopy delivers the best-effort postmaster copy of a failure report.
func (bg *BounceGenerator) postmasterCopy(original *bongov1.OutboundMessage, recipients []string, reason string, kind reportKind, domain, originalHeaders string) {
	if bg.copyPostmaster && kind == reportFailed {
		bg.deliverPostmasterCopy(original, recipients, reason, domain, originalHeaders)
	}
}

// relaysExternally reports whether a report to sender goes to the outbound
// queue: relaying is configured, sender is a well-formed address, and it is not
// one this server owns. An address that cannot be judged local or external
// (isLocal says local on any doubt) never leaves the server.
func (bg *BounceGenerator) relaysExternally(sender string) bool {
	if bg.extQueue == nil || !externalReportAddress(sender) {
		return false
	}
	return !bg.extIsLocal(sender)
}

// externalReportAddress reports whether addr is a plain addr-spec safe to put
// in an SMTP RCPT command: a single '@' with a local part and a dotted domain,
// and no whitespace, control characters or address punctuation.
func externalReportAddress(addr string) bool {
	if addr == "" || len(addr) > 254 || addr != strings.TrimSpace(addr) {
		return false
	}
	at := strings.IndexByte(addr, '@')
	if at <= 0 || at != strings.LastIndexByte(addr, '@') || at == len(addr)-1 {
		return false
	}
	for _, r := range addr {
		if r <= ' ' || r == 0x7f || r == '<' || r == '>' || r == ',' || r == ';' || r == '"' || r == '\\' {
			return false
		}
	}
	return strings.Contains(addr[at+1:], ".")
}

// relayExternal queues an unsigned report for sender, which has already been
// judged external. The envelope sender is null (RFC 5321 s6.1) so a failure of
// this report cannot produce another, and origin LOCAL marks it server-
// generated. A report over either cap is dropped and logged, never queued for
// later. The report body is not logged.
func (bg *BounceGenerator) relayExternal(sender string, report []byte) error {
	// Global first, so a report the global cap refuses does not spend the
	// sender's per-address token. A dropped report does not suppress the
	// postmaster copy of a failure report, which is sent separately.
	if !bg.extGlobal.Allow(reportGlobalKey) || !bg.extPerAddr.Allow(reportAddrKeyPrefix+strings.ToLower(sender)) {
		bg.logger.Warn("bounce: report to external sender dropped, rate limit reached", "sender", sender)
		return nil
	}
	if bg.blobs == nil {
		return fmt.Errorf("relaying report to %s: no blob store", sender)
	}
	hash, err := bg.blobs.Put(report)
	if err != nil {
		return fmt.Errorf("storing report for %s: %w", sender, err)
	}
	msg := &bongov1.OutboundMessage{
		Id:         uuid.New().String(),
		Recipients: []string{sender},
		BlobHash:   hash,
		Origin:     bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL,
	}
	if err := bg.extQueue.Enqueue(msg); err != nil {
		return fmt.Errorf("queueing report for %s: %w", sender, err)
	}
	return nil
}

// signBounce DKIM-signs a built DSN. Never fails the bounce: on a signing
// error it returns the unsigned message.
func (bg *BounceGenerator) signBounce(bounceMsg []byte) []byte {
	// DKIMRegistry.Sign only logs at Info and returns the message unsigned
	// (nil error) on the no-key path, so this pre-check is the only thing that
	// surfaces a warning naming the domain — the caller cannot otherwise
	// distinguish signed from unsigned by inspecting Sign's return values.
	// bg.dkim == nil must short-circuit before Get, which is not nil-safe.
	//
	// warnDomain comes from the already-built bounceMsg's From header via
	// FromRawMessageDomain, the same helper Sign uses to pick a signer, so the
	// guard and the signer cannot silently diverge; it falls back to bg.domain
	// if that header is missing or unparseable (should not happen for a
	// self-constructed bounce, but the warning must still fire).
	//
	// Signing is necessary but not sufficient for the bounce to reach the
	// inbox: the verifier resolves <selector>._domainkey.<domain> over live
	// DNS, so a deployment with a configured signer whose public key is not
	// published still fails DMARC and still files bounces to Junk.
	warnDomain, fromErr := FromRawMessageDomain(bounceMsg)
	switch {
	case fromErr != nil:
		// Sign resolves the signer with this same helper, so it will fail
		// identically and the message is guaranteed to go out unsigned —
		// regardless of whether bg.domain happens to have a registered
		// signer. Warn unconditionally rather than consulting bg.domain,
		// which would suppress the warning on exactly the unsigned delivery
		// it exists to report.
		bg.logger.Warn("bounce: cannot determine From domain, delivering unsigned",
			"fallback_domain", bg.domain, "error", fromErr)
	case bg.dkim == nil || bg.dkim.Get(warnDomain) == nil:
		bg.logger.Warn("bounce: no DKIM signer for domain, delivering unsigned", "domain", warnDomain)
	}

	// Sign the DSN so it passes our own DMARC check on delivery (mirrors
	// internal/queue/worker.go's outbound signing step).
	signed, err := bg.dkim.Sign(bounceMsg, bg.logger)
	if err != nil {
		bg.logger.Error("bounce: DKIM signing failed, sending unsigned", "error", err)
		return bounceMsg
	}
	return signed
}

// postmasterCopyTarget picks the recipient of the postmaster copy: postmaster@
// the sender's domain when that domain is hosted here, else the configured
// fallback account. ok is false when neither exists. The deliver stage then
// resolves postmaster@<domain> through the RecipientResolver postmaster tier.
func (bg *BounceGenerator) postmasterCopyTarget(senderEmail string) (target string, ok bool) {
	sender := strings.ToLower(strings.TrimSpace(senderEmail))
	if at := strings.LastIndexByte(sender, '@'); at >= 0 {
		d := strings.TrimSuffix(sender[at+1:], ".")
		// A domain that could not have come from a hosted-domain key (and that
		// could smuggle header or key syntax) is treated as not hosted.
		if validPostmasterDomain(d) && bg.hostedDomain != nil && bg.hostedDomain(d) {
			return "postmaster@" + d, true
		}
	}
	if bg.postmasterAcct != "" {
		return bg.postmasterAcct, true
	}
	return "", false
}

// validPostmasterDomain reports whether d is a plausible bare domain: non-empty
// and free of whitespace, control characters, '/', '@' and address punctuation.
func validPostmasterDomain(d string) bool {
	if d == "" {
		return false
	}
	for _, r := range d {
		if r <= ' ' || r == 0x7f || r == '/' || r == '@' || r == '<' || r == '>' || r == ',' || r == ';' {
			return false
		}
	}
	return true
}

// deliverPostmasterCopy delivers a copy of the DSN to the postmaster. It is
// best-effort: every failure is logged here and nothing is returned, so the
// bounce to the sender is never affected. It only covers failures to enqueue;
// if the target resolves to nobody the deliver stage drops it with its own
// warning.
//
// The copy is built from the header block alone (originalHeaders), never from
// the sender's built bytes, so a later RET=FULL on the sender's bounce cannot
// put the original body into a postmaster copy. "Delivery-status part and
// headers only" is read as: the DSN's own human-readable first part (required
// by RFC 6522, and not the original body), the message/delivery-status part
// and the text/rfc822-headers part.
func (bg *BounceGenerator) deliverPostmasterCopy(original *bongov1.OutboundMessage, failedRecipients []string, reason, domain, originalHeaders string) {
	target, ok := bg.postmasterCopyTarget(original.SenderEmail)
	if !ok {
		bg.noTargetLogOnce.Do(func() {
			bg.logger.Info("bounce: postmaster copy skipped, sender domain not hosted and smtp.postmaster_account unset")
		})
		return
	}
	// Compare without a trailing root dot: postmaster@d. is postmaster@d. The
	// domain owner can still get both the bounce and the copy when postmaster@
	// resolves to them; that is intended, they are two different messages.
	if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(original.SenderEmail), "."), target) {
		bg.logger.Debug("bounce: postmaster copy skipped, sender is the copy target", "target", target)
		return
	}
	msg := buildBounce(original, failedRecipients, reason, domain, originalHeaders, bounceOptions{
		to:      target,
		subject: "Postmaster Copy: Undelivered Mail",
	})
	signed := bg.signBounce(msg)
	if err := bg.deliverer.Deliver("", []string{target}, bytes.NewReader(signed)); err != nil {
		bg.logger.Warn("bounce: postmaster copy failed (best-effort, sender bounce unaffected)",
			"target", target, "error", err)
	}
}

// bounceOptions varies the parts of a DSN that differ between the sender's
// bounce and the postmaster copy. Neither ever carries the original body.
type bounceOptions struct {
	to      string
	subject string

	// kind selects failed (the zero value), delayed or delivered wording and
	// Action/Status. retryUntil is the Will-Retry-Until of a delayed report.
	kind       reportKind
	retryUntil time.Time

	// originalFull is the complete original message for a RET=FULL report, or
	// nil. Only the sender's report sets it; the postmaster copy never does.
	// buildBounce still falls back to the header block when the bytes cannot
	// safely sit in a message/rfc822 part.
	originalFull []byte
}

// reportKind is the kind of DSN: failure, delay or success.
type reportKind int

const (
	reportFailed reportKind = iota
	reportDelayed
	reportDelivered
	// reportRelayed is "Action: relayed": the message was handed to a server
	// that does not return delivery notifications.
	reportRelayed
)

// action is the RFC 3464 Action field value.
func (k reportKind) action() string {
	switch k {
	case reportDelayed:
		return "delayed"
	case reportDelivered:
		return "delivered"
	case reportRelayed:
		return "relayed"
	}
	return "failed"
}

// hasReason reports whether the report states a failure or delay reason.
func (k reportKind) hasReason() bool { return k != reportDelivered && k != reportRelayed }

func (k reportKind) subject() string {
	switch k {
	case reportDelayed:
		return "Delayed Mail (still being retried)"
	case reportDelivered:
		return "Successful Mail Delivery Report"
	case reportRelayed:
		return "Mail Relayed Report"
	}
	return "Undelivered Mail Returned to Sender"
}

// maxFullReturn is the largest original a RET=FULL report attaches whole;
// anything larger falls back to the header block.
const maxFullReturn = 1 << 20

// maxReportLine is the SMTP line limit (RFC 5321 section 4.5.3.1.6) without
// the CRLF.
const maxReportLine = 998

// originalFullMessage returns the original message for a RET=FULL report, or
// nil when RET is not FULL, the blob store is unavailable, the message is over
// maxFullReturn, or it cannot be read. The size is checked from blob sizes
// before anything is read, so a large message is never rebuilt in memory.
func (bg *BounceGenerator) originalFullMessage(msg *bongov1.OutboundMessage) []byte {
	if !strings.EqualFold(msg.GetDsnRet(), "FULL") || bg.blobs == nil || msg.GetBlobHash() == "" {
		return nil
	}
	total, err := bg.blobs.Size(msg.BlobHash)
	if err != nil {
		bg.logger.Warn("bounce: RET=FULL size unavailable, returning headers only", "hash", msg.BlobHash, "error", err)
		return nil
	}
	if len(msg.Layout) > 0 {
		// Split record: the skeleton holds the literal pieces; each attachment
		// segment adds that attachment's blob.
		total = 0
		for _, seg := range msg.Layout {
			switch seg.Kind {
			case bongov1.MessageSegment_LITERAL:
				total += seg.Length
			case bongov1.MessageSegment_ATTACHMENT:
				idx := int(seg.AttachmentIndex)
				if idx < 0 || idx >= len(msg.Attachments) {
					return nil
				}
				n, sizeErr := bg.blobs.Size(msg.Attachments[idx].Hash)
				if sizeErr != nil {
					bg.logger.Warn("bounce: RET=FULL attachment size unavailable, returning headers only", "error", sizeErr)
					return nil
				}
				total += n
			}
			if total > maxFullReturn {
				return nil
			}
		}
	}
	if total > maxFullReturn {
		return nil
	}
	raw, err := store.RawMessageBytes(bg.blobs, &bongov1.MailMeta{
		RawMessage:  &bongov1.BlobRef{Hash: msg.BlobHash},
		Attachments: msg.Attachments,
		Layout:      msg.Layout,
	})
	if err != nil {
		bg.logger.Warn("bounce: RET=FULL original unreadable, returning headers only", "hash", msg.BlobHash, "error", err)
		return nil
	}
	if len(raw) > maxFullReturn {
		return nil
	}
	return raw
}

// attachableOriginal normalises raw to CRLF and reports whether it can sit in
// a 7bit or 8bit message/rfc822 part: no NUL, no line over 998 octets and no
// line equal to the report's own boundary. The original is untrusted input.
func attachableOriginal(raw []byte, boundary string) (string, bool) {
	if len(raw) == 0 || bytes.IndexByte(raw, 0) >= 0 {
		return "", false
	}
	text := normalizeCRLF(raw)
	if !strings.HasSuffix(text, "\r\n") {
		text += "\r\n"
	}
	// CRLF conversion can grow a bare-LF original past the limit checked on
	// the raw size; check again on what would be attached.
	if len(text) > maxFullReturn {
		return "", false
	}
	for _, line := range strings.Split(text, "\r\n") {
		if len(line) > maxReportLine {
			return "", false
		}
	}
	if containsBoundaryLine(text, boundary) {
		return "", false
	}
	return text, true
}

// originalHeaderBlock returns the header block (normalized to CRLF, with a
// trailing CRLF) of the original outbound message's blob, for use as the DSN's
// text/rfc822-headers part. It returns "" whenever the block cannot be safely
// obtained — a nil blob store, an empty blob hash, a read error, or no
// header/body boundary within the prefix cap — and never propagates an error:
// this part is best-effort and must never fail bounce delivery.
//
// Only a bounded prefix of the blob is read. msg.BlobHash always addresses a
// skeleton blob (attachment dedup: proto/bongo/v1/queue.proto), whose first
// reconstruction piece is raw[0:firstAttachmentSpanStart] (see attsplit.Split,
// internal/attsplit/attsplit.go). Attachment spans are only ever located
// inside the top-level MIME body (attsplit.collect walks the body region
// returned by topHeaders), so that first literal piece always contains the
// complete top-level header block byte-for-byte — for both split records
// (msg.Layout non-empty, blob_hash is the skeleton) and legacy records
// (msg.Layout empty, blob_hash is the whole raw message). A bounded prefix
// read is therefore correct in both cases, and it avoids reconstructing a
// multi-MB message (via store.RawMessageBytes) just to read its headers.
func (bg *BounceGenerator) originalHeaderBlock(msg *bongov1.OutboundMessage) string {
	if bg.blobs == nil || msg.BlobHash == "" {
		return ""
	}
	rc, err := bg.blobs.Get(msg.BlobHash)
	if err != nil {
		bg.logger.Warn("bounce: original blob unavailable, omitting rfc822-headers part", "hash", msg.BlobHash, "error", err)
		return ""
	}
	defer rc.Close()

	const maxPrefix = 64 * 1024
	// Read one byte beyond the cap so we can tell "blob ended within the cap"
	// (a header-only message: no body at all) apart from "cap exhausted with
	// no blank line found" (truncated headers — unsafe to use as-is).
	prefix, err := io.ReadAll(io.LimitReader(rc, maxPrefix+1))
	if err != nil {
		bg.logger.Warn("bounce: reading original blob prefix, omitting rfc822-headers part", "hash", msg.BlobHash, "error", err)
		return ""
	}
	hitCap := len(prefix) > maxPrefix
	if hitCap {
		prefix = prefix[:maxPrefix]
	}

	idx := bytes.Index(prefix, []byte("\r\n\r\n"))
	sepLen := 4
	if idx < 0 {
		if i := bytes.Index(prefix, []byte("\n\n")); i >= 0 {
			idx, sepLen = i, 2
		}
	}
	switch {
	case idx >= 0:
		prefix = prefix[:idx+sepLen]
	case !hitCap:
		// EOF reached before any blank line: a header-only message (no
		// body). Use everything read as the header block.
	default:
		bg.logger.Warn("bounce: no header/body boundary within prefix cap, omitting rfc822-headers part", "hash", msg.BlobHash)
		return ""
	}

	headers := normalizeCRLF(prefix)
	if !strings.HasSuffix(headers, "\r\n") {
		headers += "\r\n"
	}
	return headers
}

// enhancedStatusRe matches an RFC 3463 enhanced status code (class.subject.detail)
// anchored to an immediately preceding 3-digit basic SMTP reply code, tolerating
// an intervening quote (real relay error text sometimes quotes the enhanced
// code, e.g. `504 "5.5.2 <localhost>: ...`). Anchoring to the basic code is what
// distinguishes a real status code from an incidental dotted number elsewhere in
// free text (e.g. "version 5.1.1 of the client").
var enhancedStatusRe = regexp.MustCompile(`(?:^|[^0-9A-Za-z.])([2-5])[0-9]{2}[ \t-]+"?([245])\.([0-9]{1,3})\.([0-9]{1,3})(?:[^0-9.]|$)`)

// parseEnhancedStatus extracts a well-formed RFC 3463 enhanced status code
// (class.subject.detail, class in {2,4,5}) from a free-form delivery failure
// reason string,
// falling back to "5.0.0" when none is found. reason is not always a bare SMTP
// reply: on the max-retries path (internal/queue/worker.go) it is a wrapped Go
// error such as "all MX hosts failed for x.com: RCPT TO bob@x.com: 550 5.1.1
// <bob@x.com>: User unknown", so the enhanced code is usually embedded
// mid-string rather than at the front. A match requires the enhanced code's
// class digit to equal the preceding basic reply code's first digit — always
// true in a well-formed SMTP reply, and it rejects spurious matches.
//
// Every code this generator emits accompanies "Action: failed", and RFC 3464
// section 2.3.3 ties Status to Action: a permanent-failure report must carry a
// class-5 code. A reason string can legitimately contain a 2.x.x or 4.x.x code
// earlier in the text (a wrapped error may quote a successful RCPT before the
// failing one), so scanning takes the first class-5 match rather than the first
// match of any class — otherwise "250 2.0.0 accepted ... 550 5.1.1 user
// unknown" would report Status: 2.0.0 on a failure DSN.
func parseEnhancedStatus(reason string) string {
	return parseEnhancedStatusClass(reason, "5")
}

// parseEnhancedStatusClass is parseEnhancedStatus for a given class digit
// ("5" for a failure, "4" for a delay), falling back to class.0.0.
func parseEnhancedStatusClass(reason, class string) string {
	for _, m := range enhancedStatusRe.FindAllStringSubmatch(reason, -1) {
		basicClass, enhancedClass := m[1], m[2]
		if basicClass != enhancedClass || enhancedClass != class {
			continue
		}
		return enhancedClass + "." + m[3] + "." + m[4]
	}
	return class + ".0.0"
}

// normalizeCRLF collapses any mix of bare LF and CRLF line endings in b to
// CRLF throughout.
func normalizeCRLF(b []byte) string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// asciiOnly replaces every byte/rune above the 7-bit ASCII range with '?'. Used
// for MIME parts that declare a 7bit Content-Transfer-Encoding, where emitting
// raw non-ASCII would be an interop bug.
func asciiOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r > unicode7BitMax {
			return '?'
		}
		return r
	}, s)
}

const unicode7BitMax = 127

// truncateRunes truncates s to at most maxRunes runes, appending "..." when
// truncated. Used to keep the Diagnostic-Code header value (a header-shaped
// field derived from an arbitrary error string) to a sane length.
func truncateRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes]) + "..."
}

// writeQuotedPrintable writes s to w quoted-printable encoded, flushing any
// trailing soft-break state via Close.
func writeQuotedPrintable(w io.Writer, s string) error {
	qw := quotedprintable.NewWriter(w)
	if _, err := qw.Write([]byte(s)); err != nil {
		return err
	}
	return qw.Close()
}

func buildBounce(original *bongov1.OutboundMessage, failedRecipients []string, reason, domain, originalHeaders string, opts bounceOptions) []byte {
	boundary := fmt.Sprintf("bounce-%s", uuid.New().String()[:8])
	now := time.Now()

	// Sanitize all user-controlled values before writing them into headers.
	toAddr := SanitizeHeaderValue(opts.to)
	safeReason := SanitizeHeaderValue(reason)
	safeRecipients := make([]string, len(failedRecipients))
	for i, r := range failedRecipients {
		safeRecipients[i] = SanitizeHeaderValue(r)
	}
	var status string
	switch opts.kind {
	case reportDelayed:
		status = parseEnhancedStatusClass(reason, "4")
	case reportDelivered, reportRelayed:
		status = "2.0.0"
	default:
		status = parseEnhancedStatus(reason)
	}
	action := opts.kind.action()

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: MAILER-DAEMON <postmaster@%s>\r\n", domain)
	fmt.Fprintf(&buf, "To: %s\r\n", toAddr)
	fmt.Fprintf(&buf, "Subject: %s\r\n", SanitizeHeaderValue(opts.subject))
	fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <bounce-%s@%s>\r\n", uuid.New().String(), domain)
	// RFC 3834 section 5: mark this message as machine-generated so
	// compliant auto-responders / vacation-reply systems do not reply to it.
	buf.WriteString("Auto-Submitted: auto-replied\r\n")
	// In-Reply-To/References are emitted together, or neither — an
	// empty-value header must never be produced. MessageGuid is sanitized and
	// defensively bracketed even though the compose path (compose.go:392) and
	// address_verify.go already produce a bracketed msg-id; the bounce path
	// should not rely on that invariant holding upstream.
	if guid := SanitizeHeaderValue(strings.TrimSpace(original.MessageGuid)); guid != "" {
		if !strings.HasPrefix(guid, "<") || !strings.HasSuffix(guid, ">") {
			guid = "<" + guid + ">"
		}
		fmt.Fprintf(&buf, "In-Reply-To: %s\r\n", guid)
		fmt.Fprintf(&buf, "References: %s\r\n", guid)
	}
	buf.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/report; report-type=delivery-status; boundary=\"%s\"\r\n", boundary)
	buf.WriteString("\r\n")

	// Part 1: human-readable. Quoted-printable is the honest CTE for a
	// UTF-8 body that may contain a non-ASCII Diagnostic-Code/reason; it also
	// keeps line lengths within the SMTP 998-octet limit for a long relay
	// error string.
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	buf.WriteString("\r\n")
	var part1 strings.Builder
	switch opts.kind {
	case reportDelayed:
		part1.WriteString("Delivery of the following message to one or more recipients has been delayed. It is still queued and delivery will be retried:\r\n\r\n")
	case reportDelivered:
		part1.WriteString("The following message was delivered to one or more recipients:\r\n\r\n")
	case reportRelayed:
		part1.WriteString("The following message was relayed to a server that does not return delivery notifications. Delivery is not confirmed:\r\n\r\n")
	default:
		part1.WriteString("The following message could not be delivered to one or more recipients:\r\n\r\n")
	}
	for _, rcpt := range safeRecipients {
		fmt.Fprintf(&part1, "  * %s\r\n", rcpt)
	}
	if opts.kind == reportDelayed && !opts.retryUntil.IsZero() {
		fmt.Fprintf(&part1, "\r\nDelivery will be retried until %s.\r\n", opts.retryUntil.UTC().Format(time.RFC1123Z))
	}
	if opts.kind.hasReason() {
		fmt.Fprintf(&part1, "\r\nReason: %s\r\n", safeReason)
	}
	// Error ignored: the sink is a bytes.Buffer, whose Write never fails. The
	// helper still returns an error so a non-buffer sink would be handled.
	_ = writeQuotedPrintable(&buf, part1.String())
	buf.WriteString("\r\n")

	// Part 2: machine-readable DSN. RFC 2045 section 6.4 forbids
	// QP/base64 on a message/* part, so this must stay 7bit — which means the
	// Diagnostic-Code value must actually be ASCII, not merely declared so.
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: message/delivery-status\r\n")
	buf.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	buf.WriteString("\r\n")
	fmt.Fprintf(&buf, "Reporting-MTA: dns; %s\r\n", domain)
	// RFC 3464 section 2.2.1: the ENVID the client sent, decoded. go-smtp has
	// already checked it is printable ASCII; the check here keeps a record
	// from any other source out of the 7bit part.
	if envid := SanitizeHeaderValue(original.GetDsnEnvid()); envid != "" && isASCII(envid) {
		fmt.Fprintf(&buf, "Original-Envelope-Id: %s\r\n", envid)
	}
	buf.WriteString("\r\n")
	diagCode := asciiOnly(truncateRunes(safeReason, 500))
	for i, rcpt := range safeRecipients {
		if orcpt := orcptField(recipientDSN(original, failedRecipients[i])); orcpt != "" {
			fmt.Fprintf(&buf, "Original-Recipient: %s\r\n", orcpt)
		}
		fmt.Fprintf(&buf, "Final-Recipient: rfc822; %s\r\n", rcpt)
		fmt.Fprintf(&buf, "Action: %s\r\n", action)
		fmt.Fprintf(&buf, "Status: %s\r\n", status)
		if opts.kind.hasReason() {
			fmt.Fprintf(&buf, "Diagnostic-Code: smtp; %s\r\n", diagCode)
		}
		if opts.kind == reportDelayed && !opts.retryUntil.IsZero() {
			fmt.Fprintf(&buf, "Will-Retry-Until: %s\r\n", opts.retryUntil.UTC().Format(time.RFC1123Z))
		}
		buf.WriteString("\r\n")
	}

	// Part 3, RET=FULL: the whole original as message/rfc822 when it is safe
	// to embed; otherwise fall through to the header block below.
	attachedFull := false
	if len(opts.originalFull) > 0 {
		if full, ok := attachableOriginal(opts.originalFull, boundary); ok {
			fmt.Fprintf(&buf, "--%s\r\n", boundary)
			buf.WriteString("Content-Type: message/rfc822\r\n")
			// RFC 2045 section 6.4 forbids quoted-printable and base64 on a
			// message/* part, so the original goes in as it is, declared 7bit
			// or 8bit to match what it really contains.
			if isASCII(full) {
				buf.WriteString("Content-Transfer-Encoding: 7bit\r\n")
			} else {
				buf.WriteString("Content-Transfer-Encoding: 8bit\r\n")
			}
			buf.WriteString("\r\n")
			buf.WriteString(full)
			buf.WriteString("\r\n")
			attachedFull = true
		}
	}

	// Part 3: the original message's header block, when available.
	// Best-effort — omitted entirely when originalHeaders is "" (retrieval
	// failure or unavailable blob store, logged by the caller).
	if hdrs := originalHeaders; !attachedFull && hdrs != "" && !containsBoundaryLine(hdrs, boundary) {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		// charset is declared because a header block can carry raw non-ASCII
		// (an unencoded Subject from a non-conforming sender); without it a
		// consumer must guess, and us-ascii is the RFC 2045 default it would
		// wrongly assume.
		buf.WriteString("Content-Type: text/rfc822-headers; charset=utf-8\r\n")
		if isASCII(hdrs) {
			buf.WriteString("Content-Transfer-Encoding: 7bit\r\n")
		} else {
			// Legal for text/* (unlike message/*); this part is never relayed
			// over SMTP itself (it is delivered locally to the sender's
			// mailbox), so declaring 8bit here is truthful and readable.
			buf.WriteString("Content-Transfer-Encoding: 8bit\r\n")
		}
		buf.WriteString("\r\n")
		buf.WriteString(hdrs)
		// Explicit CRLF before the close delimiter, matching parts 1 and 2.
		// hdrs already ends with its own terminating CRLF, so the part ends
		// with a blank line; that is redundant but valid, and relying on
		// hdrs's final CRLF to serve as the delimiter's required leading CRLF
		// would make the framing depend on originalHeaderBlock's suffix.
		buf.WriteString("\r\n")
	}

	// Close boundary.
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)

	return buf.Bytes()
}

// containsBoundaryLine reports whether text contains a standalone line equal
// to the MIME boundary delimiter, which would corrupt the multipart
// structure. The boundary is a fresh UUID-derived string per bounce, so this
// is not a practical risk for the current caller (embedding a sender's own
// outbound message headers), but the original blob is nonetheless untrusted
// input by the time it reaches here and this check is cheap insurance.
func containsBoundaryLine(text, boundary string) bool {
	delim := "--" + boundary
	for _, line := range strings.Split(text, "\r\n") {
		if line == delim || line == delim+"--" {
			return true
		}
	}
	return false
}

// isASCII reports whether s contains only 7-bit ASCII bytes.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > unicode7BitMax {
			return false
		}
	}
	return true
}

// BuildBounceMessage builds a bounce message (exported for testing). domain is
// the server's configured FQDN/primary domain (see ResolveServerDomain).
// originalHeaders is the original message's header block for the
// text/rfc822-headers MIME part; pass "" to omit that part.
func BuildBounceMessage(original *bongov1.OutboundMessage, failedRecipients []string, reason, domain, originalHeaders string) []byte {
	return buildBounce(original, failedRecipients, reason, domain, originalHeaders, bounceOptions{
		to:      original.SenderEmail,
		subject: "Undelivered Mail Returned to Sender",
	})
}

// FormatRecipientList formats failed recipients for display.
func FormatRecipientList(recipients []string) string {
	return strings.Join(recipients, ", ")
}
