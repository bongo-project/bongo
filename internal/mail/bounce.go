package mail

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

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
	bounceMsg := buildBounce(original, failedRecipients, reason, bg.domain)

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
	// This looks up bg.domain, while Sign re-derives the domain from the
	// From: header (FromRawMessageDomain). They agree only because
	// buildBounce writes From: postmaster@<bg.domain>. Any change to that
	// From header must keep the two in step, or this warning silently stops
	// matching what actually gets signed.
	//
	// Signing is necessary but not sufficient for the bounce to reach the
	// inbox: the verifier resolves <selector>._domainkey.<domain> over live
	// DNS, so a deployment with a configured signer whose public key is not
	// published still fails DMARC and still files bounces to Junk.
	if bg.dkim == nil || bg.dkim.Get(bg.domain) == nil {
		bg.logger.Warn("bounce: no DKIM signer for domain, delivering unsigned", "domain", bg.domain)
	}

	// Sign the DSN so it passes our own DMARC check on delivery (mirrors
	// internal/queue/worker.go's outbound signing step). Never fails the
	// bounce: on a signing error, fall back to the unsigned message.
	signed, err := bg.dkim.Sign(bounceMsg, bg.logger)
	if err != nil {
		bg.logger.Error("bounce: DKIM signing failed, sending unsigned", "error", err)
		signed = bounceMsg
	}

	return bg.deliverer.Deliver(
		"MAILER-DAEMON@"+bg.domain,
		[]string{original.SenderEmail},
		bytes.NewReader(signed),
	)
}

func buildBounce(original *bongov1.OutboundMessage, failedRecipients []string, reason, domain string) []byte {
	boundary := fmt.Sprintf("bounce-%s", uuid.New().String()[:8])
	now := time.Now()

	// Sanitize all user-controlled values before writing them into headers.
	senderEmail := SanitizeHeaderValue(original.SenderEmail)
	safeReason := SanitizeHeaderValue(reason)
	safeRecipients := make([]string, len(failedRecipients))
	for i, r := range failedRecipients {
		safeRecipients[i] = SanitizeHeaderValue(r)
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: MAILER-DAEMON <postmaster@%s>\r\n", domain)
	fmt.Fprintf(&buf, "To: %s\r\n", senderEmail)
	buf.WriteString("Subject: Undelivered Mail Returned to Sender\r\n")
	fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <bounce-%s@%s>\r\n", uuid.New().String(), domain)
	if original.MessageGuid != "" {
		fmt.Fprintf(&buf, "In-Reply-To: %s\r\n", original.MessageGuid)
	}
	buf.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/report; report-type=delivery-status; boundary=\"%s\"\r\n", boundary)
	buf.WriteString("\r\n")

	// Part 1: human-readable.
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString("The following message could not be delivered to one or more recipients:\r\n\r\n")
	for _, rcpt := range safeRecipients {
		fmt.Fprintf(&buf, "  * %s\r\n", rcpt)
	}
	fmt.Fprintf(&buf, "\r\nReason: %s\r\n", safeReason)
	buf.WriteString("\r\n")

	// Part 2: machine-readable DSN.
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: message/delivery-status\r\n")
	buf.WriteString("\r\n")
	fmt.Fprintf(&buf, "Reporting-MTA: dns; %s\r\n", domain)
	buf.WriteString("\r\n")
	for _, rcpt := range safeRecipients {
		fmt.Fprintf(&buf, "Final-Recipient: rfc822; %s\r\n", rcpt)
		buf.WriteString("Action: failed\r\n")
		buf.WriteString("Status: 5.0.0\r\n")
		fmt.Fprintf(&buf, "Diagnostic-Code: smtp; %s\r\n", safeReason)
		buf.WriteString("\r\n")
	}

	// Close boundary.
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)

	return buf.Bytes()
}

// BuildBounceMessage builds a bounce message (exported for testing). domain is
// the server's configured FQDN/primary domain (see ResolveServerDomain).
func BuildBounceMessage(original *bongov1.OutboundMessage, failedRecipients []string, reason, domain string) []byte {
	return buildBounce(original, failedRecipients, reason, domain)
}

// FormatRecipientList formats failed recipients for display.
func FormatRecipientList(recipients []string) string {
	return strings.Join(recipients, ", ")
}
