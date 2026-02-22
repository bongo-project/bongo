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

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/rules"
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
	applier    RaftApplier
	blobs      *store.BlobStore
	resolver   RecipientResolver
	scorer     *antispam.Scorer
	bayes      *antispam.BayesFilter
	ruleStore  *rules.RuleStore
	ruleEngine *rules.Engine
	logger     *slog.Logger
}

// SetScorer sets the antispam scorer for inbound scoring.
func (d *DeliveryPipeline) SetScorer(s *antispam.Scorer) {
	d.scorer = s
}

// SetBayes sets the Bayesian filter for blended spam scoring.
func (d *DeliveryPipeline) SetBayes(b *antispam.BayesFilter) {
	d.bayes = b
}

// SetRuleStore sets the filter rules store and engine for rule-based routing.
func (d *DeliveryPipeline) SetRuleStore(rs *rules.RuleStore) {
	d.ruleStore = rs
	d.ruleEngine = rules.NewEngine()
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
	return d.DeliverWithSPF(from, recipients, r, "")
}

// DeliverWithSPF processes an inbound message with an SPF result from the SMTP session.
func (d *DeliveryPipeline) DeliverWithSPF(from string, recipients []string, r io.Reader, spfResult string) error {
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

	// Extract threading headers.
	var inReplyTo, references string
	var ccAddrs []string
	if envelope != nil {
		inReplyTo = envelope.GetHeader("In-Reply-To")
		references = envelope.GetHeader("References")
		if ccHeader := envelope.GetHeader("Cc"); ccHeader != "" {
			for _, addr := range strings.Split(ccHeader, ",") {
				addr = strings.TrimSpace(addr)
				if addr != "" {
					ccAddrs = append(ccAddrs, addr)
				}
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

	// Run antispam scoring.
	var spamScore float32
	var dkimResult, dkimDomain string

	dkimResult, dkimDomain, _ = antispam.VerifyDKIM(raw)

	if d.scorer != nil {
		headers := make(map[string]string)
		if envelope != nil {
			for _, key := range []string{"Message-Id", "Message-ID", "Date", "From"} {
				if v := envelope.GetHeader(key); v != "" {
					headers[key] = v
				}
			}
		}

		var bodySample string
		if envelope != nil && len(envelope.Text) > 0 {
			bodySample = envelope.Text
			if len(bodySample) > 4096 {
				bodySample = bodySample[:4096]
			}
		}

		input := &antispam.ScoringInput{
			SPFResult:      spfResult,
			DKIMResult:     dkimResult,
			Headers:        headers,
			Subject:        subject,
			BodySample:     bodySample,
			EnvelopeSender: from,
		}

		result := d.scorer.Score(input)
		spamScore = result.Score
		if len(result.Reasons) > 0 {
			d.logger.Info("antispam: scored", "score", spamScore, "reasons", result.Reasons, "from", from, "subject", subject)
		}
	}

	// Deliver to each recipient.
	for _, rcpt := range recipients {
		userID, err := d.resolver.ResolveRecipient(rcpt)
		if err != nil {
			d.logger.Warn("smtp: unknown recipient, skipping", "recipient", rcpt, "error", err)
			continue
		}

		// Blend Bayesian score with heuristic score (per-user training data).
		recipientSpamScore := spamScore
		if d.bayes != nil {
			trained := d.bayes.TrainedCount(userID)
			if trained >= 20 {
				var bodySample string
				if envelope != nil && len(envelope.Text) > 0 {
					bodySample = envelope.Text
					if len(bodySample) > 4096 {
						bodySample = bodySample[:4096]
					}
				}
				tokens := antispam.Tokenize(subject, bodySample)
				bayesScore := d.bayes.Classify(userID, tokens)
				recipientSpamScore = 0.6*spamScore + 0.4*float32(bayesScore)
				d.logger.Debug("antispam: blended score", "user", userID, "heuristic", spamScore, "bayes", bayesScore, "blended", recipientSpamScore)
			}
		}

		// Evaluate user-defined filter rules.
		var targetMailbox string
		var extraFlags int32
		if d.ruleStore != nil && d.ruleEngine != nil {
			userRules, err := d.ruleStore.List(userID)
			if err == nil && len(userRules) > 0 {
				// Collect all headers for rule matching.
				ruleHeaders := make(map[string]string)
				if envelope != nil {
					for _, key := range envelope.GetHeaderKeys() {
						if v := envelope.GetHeader(key); v != "" {
							ruleHeaders[key] = v
						}
					}
				}
				var bodyForRules string
				if envelope != nil {
					bodyForRules = envelope.Text
				}

				action := d.ruleEngine.Evaluate(userRules, &bongov1.MailMeta{
					From:        headerFrom,
					To:          toAddrs,
					Cc:          ccAddrs,
					Subject:     subject,
					Attachments: attachmentRefs,
					RawMessage:  &bongov1.BlobRef{Size: int64(len(raw))},
				}, ruleHeaders, bodyForRules)

				if action != nil {
					switch action.Type {
					case "move":
						targetMailbox = action.Target
					case "copy":
						targetMailbox = action.Target
					case "flag":
						// Set starred flag (bit 2).
						extraFlags |= 0x04
					case "mark_read":
						// Set seen flag (bit 0).
						extraFlags |= 0x01
					case "delete":
						targetMailbox = "Trash"
					}
					d.logger.Info("filter: rule matched", "user", userID, "action", action.Type, "target", action.Target)
				}
			}
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
			InReplyTo:   inReplyTo,
			References:  references,
			Cc:          ccAddrs,
			SpamScore:   recipientSpamScore,
			SpfResult:   spfResult,
			DkimResult:     dkimResult,
			DkimDomain:     dkimDomain,
			TargetMailbox:  targetMailbox,
			Flags:          extraFlags,
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
