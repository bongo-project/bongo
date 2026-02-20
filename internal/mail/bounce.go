package mail

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

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
}

// NewBounceGenerator creates a new BounceGenerator.
func NewBounceGenerator(deliverer Deliverer, blobs *store.BlobStore, logger *slog.Logger) *BounceGenerator {
	return &BounceGenerator{
		deliverer: deliverer,
		blobs:     blobs,
		logger:    logger,
	}
}

// GenerateAndDeliver creates an RFC 3464 DSN bounce and delivers it to the sender's INBOX.
func (bg *BounceGenerator) GenerateAndDeliver(original *bongov1.OutboundMessage, failedRecipients []string, reason string) error {
	bounceMsg := buildBounce(original, failedRecipients, reason)

	bg.logger.Info("bounce: generating DSN",
		"sender", original.SenderEmail,
		"failed_recipients", failedRecipients,
		"reason", reason,
	)

	return bg.deliverer.Deliver(
		"MAILER-DAEMON@localhost",
		[]string{original.SenderEmail},
		bytes.NewReader(bounceMsg),
	)
}

func buildBounce(original *bongov1.OutboundMessage, failedRecipients []string, reason string) []byte {
	boundary := fmt.Sprintf("bounce-%s", uuid.New().String()[:8])
	now := time.Now()

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("From: MAILER-DAEMON <postmaster@localhost>\r\n"))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", original.SenderEmail))
	buf.WriteString("Subject: Undelivered Mail Returned to Sender\r\n")
	buf.WriteString(fmt.Sprintf("Date: %s\r\n", now.Format(time.RFC1123Z)))
	buf.WriteString(fmt.Sprintf("Message-ID: <bounce-%s@localhost>\r\n", uuid.New().String()))
	if original.MessageGuid != "" {
		buf.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", original.MessageGuid))
	}
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/report; report-type=delivery-status; boundary=\"%s\"\r\n", boundary))
	buf.WriteString("\r\n")

	// Part 1: human-readable.
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString("The following message could not be delivered to one or more recipients:\r\n\r\n")
	for _, rcpt := range failedRecipients {
		buf.WriteString(fmt.Sprintf("  * %s\r\n", rcpt))
	}
	buf.WriteString(fmt.Sprintf("\r\nReason: %s\r\n", reason))
	buf.WriteString("\r\n")

	// Part 2: machine-readable DSN.
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: message/delivery-status\r\n")
	buf.WriteString("\r\n")
	buf.WriteString("Reporting-MTA: dns; localhost\r\n")
	buf.WriteString("\r\n")
	for _, rcpt := range failedRecipients {
		buf.WriteString(fmt.Sprintf("Final-Recipient: rfc822; %s\r\n", rcpt))
		buf.WriteString("Action: failed\r\n")
		buf.WriteString("Status: 5.0.0\r\n")
		buf.WriteString(fmt.Sprintf("Diagnostic-Code: smtp; %s\r\n", reason))
		buf.WriteString("\r\n")
	}

	// Close boundary.
	buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	return buf.Bytes()
}

// BuildBounceMessage builds a bounce message (exported for testing).
func BuildBounceMessage(original *bongov1.OutboundMessage, failedRecipients []string, reason string) []byte {
	return buildBounce(original, failedRecipients, reason)
}

// FormatRecipientList formats failed recipients for display.
func FormatRecipientList(recipients []string) string {
	return strings.Join(recipients, ", ")
}
