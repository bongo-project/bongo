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
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/net/publicsuffix"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
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
	// FR-5: best-effort retrieval of the original message's header block for
	// the third (text/rfc822-headers) MIME part. Never fails the bounce: any
	// problem is logged inside originalHeaderBlock and it returns "", which
	// buildBounce treats as "omit part 3".
	originalHeaders := bg.originalHeaderBlock(original)

	bounceMsg := buildBounce(original, failedRecipients, reason, bg.domain, originalHeaders)

	bg.logger.Info("bounce: generating DSN",
		"sender", original.SenderEmail,
		"failed_recipients", failedRecipients,
		"reason", reason,
	)

	// FR-2: DKIMRegistry.Sign only logs at Info and returns the message
	// unsigned (nil error) on the no-key path, so this pre-check is what
	// surfaces a warning naming the domain — the caller cannot otherwise
	// distinguish signed from unsigned by inspecting Sign's return values.
	// bg.dkim == nil must short-circuit before Get, which is not nil-safe.
	//
	// warnDomain is derived from the already-built bounceMsg's From header
	// via FromRawMessageDomain, the same helper DKIMRegistry.Sign uses to
	// pick a signer — so the guard and the signer share one source of truth
	// and cannot silently diverge. Falls back to bg.domain if the From
	// header is missing or unparseable (should not happen for a
	// self-constructed bounce, but the warning must still fire rather than
	// be silently skipped).
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
	// internal/queue/worker.go's outbound signing step). Never fails the
	// bounce: on a signing error, fall back to the unsigned message.
	signed, err := bg.dkim.Sign(bounceMsg, bg.logger)
	if err != nil {
		bg.logger.Error("bounce: DKIM signing failed, sending unsigned", "error", err)
		signed = bounceMsg
	}

	// RFC 5321 s6.1: a DSN's envelope sender (MAIL FROM) must be null (<>) so
	// a failure to deliver this bounce itself cannot generate another bounce
	// (bounce-of-a-bounce loop). The From: header still identifies the
	// sender as `MAILER-DAEMON <postmaster@<domain>>` (set by buildBounce
	// above) — MAILER-DAEMON is the display name and postmaster@<domain> is
	// the addr-spec that DKIM/DMARC alignment is computed from. Only the
	// envelope sender is null.
	return bg.deliverer.Deliver(
		"",
		[]string{original.SenderEmail},
		bytes.NewReader(signed),
	)
}

// originalHeaderBlock returns the header block (normalized to CRLF, with a
// trailing CRLF) of the original outbound message's blob, for use as the DSN's
// FR-5 text/rfc822-headers part. It returns "" whenever the block cannot be
// safely obtained — a nil blob store, an empty blob hash, a read error, or no
// header/body boundary within the prefix cap — and never propagates an error:
// FR-5 is explicitly best-effort and must never fail bounce delivery.
//
// Only a bounded prefix of the blob is read. msg.BlobHash always addresses a
// skeleton blob (Phase 9 attachment dedup: proto/bongo/v1/queue.proto), whose
// first reconstruction piece is raw[0:firstAttachmentSpanStart] (see
// attsplit.Split, internal/attsplit/attsplit.go). Attachment spans are only
// ever located inside the top-level MIME body (attsplit.collect walks the
// body region returned by topHeaders), so that first literal piece always
// contains the complete top-level header block byte-for-byte — for both split
// records (msg.Layout non-empty, blob_hash is the skeleton) and legacy
// records (msg.Layout empty, blob_hash is the whole raw message). A bounded
// prefix read of blob_hash is therefore correct in both cases, and it avoids
// reconstructing/materializing a multi-MB message (via store.RawMessageBytes)
// just to read its headers on the bounce path.
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
// reason string. reason is not always a bare SMTP reply: on the max-retries
// path (internal/queue/worker.go) it is a wrapped Go error such as
// "all MX hosts failed for x.com: RCPT TO bob@x.com: 550 5.1.1 <bob@x.com>:
// User unknown", so the enhanced code is usually embedded mid-string rather
// than at the front. The match requires the enhanced code's class digit to
// equal the preceding basic reply code's first digit, which they always do in
// a well-formed SMTP reply and rejects mismatched or spurious matches. Falls
// back to "5.0.0" when no such anchored code is found.
// Every code this generator emits accompanies "Action: failed", and RFC 3464
// section 2.3.3 ties Status to Action: a permanent-failure report must carry a
// class-5 code. A reason string can legitimately contain a 2.x.x or 4.x.x code
// earlier in the text (a wrapped error may quote a successful RCPT before the
// failing one), so scanning takes the first class-5 match rather than the first
// match of any class — otherwise "250 2.0.0 accepted ... 550 5.1.1 user
// unknown" would report Status: 2.0.0 on a failure DSN.
func parseEnhancedStatus(reason string) string {
	for _, m := range enhancedStatusRe.FindAllStringSubmatch(reason, -1) {
		basicClass, enhancedClass := m[1], m[2]
		if basicClass != enhancedClass || enhancedClass != "5" {
			continue
		}
		return enhancedClass + "." + m[3] + "." + m[4]
	}
	return "5.0.0"
}

// normalizeCRLF collapses any mix of bare LF and CRLF line endings in b to
// CRLF throughout.
func normalizeCRLF(b []byte) string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// asciiOnly replaces every byte/rune above the 7-bit ASCII range with '?'. Used
// for MIME parts that declare a 7bit Content-Transfer-Encoding, where emitting
// raw non-ASCII would be an interop bug (see FR-6).
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

func buildBounce(original *bongov1.OutboundMessage, failedRecipients []string, reason, domain, originalHeaders string) []byte {
	boundary := fmt.Sprintf("bounce-%s", uuid.New().String()[:8])
	now := time.Now()

	// Sanitize all user-controlled values before writing them into headers.
	senderEmail := SanitizeHeaderValue(original.SenderEmail)
	safeReason := SanitizeHeaderValue(reason)
	safeRecipients := make([]string, len(failedRecipients))
	for i, r := range failedRecipients {
		safeRecipients[i] = SanitizeHeaderValue(r)
	}
	status := parseEnhancedStatus(reason)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: MAILER-DAEMON <postmaster@%s>\r\n", domain)
	fmt.Fprintf(&buf, "To: %s\r\n", senderEmail)
	buf.WriteString("Subject: Undelivered Mail Returned to Sender\r\n")
	fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <bounce-%s@%s>\r\n", uuid.New().String(), domain)
	// FR-4 (RFC 3834 section 5): mark this message as machine-generated so
	// compliant auto-responders / vacation-reply systems do not reply to it.
	buf.WriteString("Auto-Submitted: auto-replied\r\n")
	// FR-7: In-Reply-To/References are emitted together, or neither — an
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

	// Part 1: human-readable. FR-6: quoted-printable is the honest CTE for a
	// UTF-8 body that may contain a non-ASCII Diagnostic-Code/reason; it also
	// keeps line lengths within the SMTP 998-octet limit for a long relay
	// error string.
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	buf.WriteString("\r\n")
	var part1 strings.Builder
	part1.WriteString("The following message could not be delivered to one or more recipients:\r\n\r\n")
	for _, rcpt := range safeRecipients {
		fmt.Fprintf(&part1, "  * %s\r\n", rcpt)
	}
	fmt.Fprintf(&part1, "\r\nReason: %s\r\n", safeReason)
	// Error ignored: the sink is a bytes.Buffer, whose Write never fails. The
	// helper still returns an error so a non-buffer sink would be handled.
	_ = writeQuotedPrintable(&buf, part1.String())
	buf.WriteString("\r\n")

	// Part 2: machine-readable DSN. FR-6/RFC 2045 section 6.4 forbids
	// QP/base64 on a message/* part, so this must stay 7bit — which means the
	// Diagnostic-Code value must actually be ASCII, not merely declared so.
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: message/delivery-status\r\n")
	buf.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	buf.WriteString("\r\n")
	fmt.Fprintf(&buf, "Reporting-MTA: dns; %s\r\n", domain)
	buf.WriteString("\r\n")
	diagCode := asciiOnly(truncateRunes(safeReason, 500))
	for _, rcpt := range safeRecipients {
		fmt.Fprintf(&buf, "Final-Recipient: rfc822; %s\r\n", rcpt)
		buf.WriteString("Action: failed\r\n")
		fmt.Fprintf(&buf, "Status: %s\r\n", status)
		fmt.Fprintf(&buf, "Diagnostic-Code: smtp; %s\r\n", diagCode)
		buf.WriteString("\r\n")
	}

	// Part 3 (FR-5): the original message's header block, when available.
	// Best-effort — omitted entirely when originalHeaders is "" (retrieval
	// failure or unavailable blob store, logged by the caller).
	if hdrs := originalHeaders; hdrs != "" && !containsBoundaryLine(hdrs, boundary) {
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
// originalHeaders is the original message's header block for the FR-5
// text/rfc822-headers MIME part; pass "" to omit that part.
func BuildBounceMessage(original *bongov1.OutboundMessage, failedRecipients []string, reason, domain, originalHeaders string) []byte {
	return buildBounce(original, failedRecipients, reason, domain, originalHeaders)
}

// FormatRecipientList formats failed recipients for display.
func FormatRecipientList(recipients []string) string {
	return strings.Join(recipients, ", ")
}
