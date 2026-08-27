// Package rules provides a pipeline Processor that evaluates per-user filter
// rules against inbound messages and updates the delivery envelope accordingly.
package rules

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/rules"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// RuleProvider is a minimal interface for fetching a user's filter rules.
// It is satisfied by *rules.RuleStore (internal/rules/store.go).
type RuleProvider interface {
	List(userID string) ([]*bongov1.FilterRule, error)
}

// RecipientResolver resolves an envelope recipient email address to a local
// user ID. Filter rules are keyed by user ID, but envelope recipients are email
// addresses, so they must be resolved before looking up rules.
type RecipientResolver interface {
	ResolveRecipient(email string) (string, error)
}

// RulesProcessor wraps the rules.Engine as a pipeline Processor. It evaluates
// each recipient's filter rules and updates the envelope with the outcome.
type RulesProcessor struct {
	ruleStore RuleProvider
	resolver  RecipientResolver
	engine    *rules.Engine
	logger    *slog.Logger
}

// SetResolver sets the recipient resolver used to map envelope recipient email
// addresses to user IDs. When unset, recipients are treated as user IDs (used
// by unit tests that pass IDs directly).
func (p *RulesProcessor) SetResolver(r RecipientResolver) {
	p.resolver = r
}

// NewRulesProcessor creates a RulesProcessor backed by the given RuleProvider.
func NewRulesProcessor(ruleStore RuleProvider, logger *slog.Logger) *RulesProcessor {
	if logger == nil {
		logger = slog.Default()
	}
	return &RulesProcessor{
		ruleStore: ruleStore,
		engine:    rules.NewEngine(),
		logger:    logger,
	}
}

// Name returns the stage identifier for this processor.
func (p *RulesProcessor) Name() string {
	return "rules"
}

// Process reads the message, evaluates each recipient's filter rules, and
// returns a StageResult. It mutates env.TargetMailbox when a move/copy/delete
// rule matches. Actions "flag" and "mark_read" are recorded in StageResult.Reasons.
// A "delete" rule results in StageResult.Action "reject". A "tag" rule appends
// tag names to env.PendingTags, which the deliver stage applies to MailMeta.
//
// When multiple recipients have conflicting rules, the most significant action
// wins: reject > pass (target mailbox is set from the first move/copy match).
func (p *RulesProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	start := time.Now()

	raw, err := io.ReadAll(msg)
	if err != nil {
		return nil, fmt.Errorf("rules: reading message: %w", err)
	}

	// Parse the message to extract fields used by rule conditions.
	parsed := parseMessage(raw)

	var (
		finalAction   = "pass"
		reasons       []string
		targetMailbox string
	)

	for _, recipient := range env.GetRecipients() {
		// Rules are keyed by user ID; envelope recipients are email addresses.
		userID := recipient
		if p.resolver != nil {
			resolved, err := p.resolver.ResolveRecipient(recipient)
			if err != nil || resolved == "" {
				// Not a local user (e.g. an external forward target) — no rules.
				continue
			}
			userID = resolved
		}

		userRules, err := p.ruleStore.List(userID)
		if err != nil {
			p.logger.Warn("rules: failed to load rules for recipient",
				"recipient", recipient,
				"error", err,
			)
			continue
		}
		if len(userRules) == 0 {
			continue
		}

		action := p.engine.Evaluate(userRules, parsed.meta, parsed.headers, parsed.bodyText)
		if action == nil {
			continue
		}

		p.logger.Info("rules: rule matched",
			"recipient", recipient,
			"action", action.Type,
			"target", action.Target,
		)

		switch strings.ToLower(action.Type) {
		case "move", "copy":
			if targetMailbox == "" {
				targetMailbox = action.Target
			}
			reasons = append(reasons, fmt.Sprintf("%s:%s", action.Type, action.Target))
		case "flag":
			reasons = append(reasons, "flag:"+action.Target)
		case "mark_read":
			reasons = append(reasons, "mark_read")
		case "delete":
			finalAction = "reject"
			reasons = append(reasons, "delete")
		case "forward":
			if action.Target != "" {
				env.ForwardAddresses = append(env.ForwardAddresses, action.Target)
				reasons = append(reasons, "forward:"+action.Target)
			}
		case "tag":
			env.PendingTags = appendUnique(env.PendingTags, action.Tags)
			for _, t := range action.Tags {
				reasons = append(reasons, "tag:"+t)
			}
		}
	}

	if targetMailbox != "" && finalAction != "reject" {
		env.TargetMailbox = targetMailbox
	}

	return &bongov1.StageResult{
		Stage:       "rules",
		Backend:     "builtin",
		Action:      finalAction,
		Reasons:     reasons,
		ProcessedAt: start.Unix(),
		DurationMs:  pipeline.ClampInt32(time.Since(start).Milliseconds()),
	}, nil
}

// parsedMessage holds the extracted fields from a raw RFC 5322 message.
type parsedMessage struct {
	meta     *bongov1.MailMeta
	headers  map[string]string
	bodyText string
}

// parseMessage extracts fields from raw message bytes needed for rule matching.
// It uses enmime for full MIME parsing with a plain-text fallback.
func parseMessage(raw []byte) *parsedMessage {
	meta := &bongov1.MailMeta{
		RawMessage: &bongov1.BlobRef{Size: int64(len(raw))},
	}
	headers := make(map[string]string)

	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		// Fallback: extract headers with a simple line scanner.
		meta.Subject, headers = extractBasicHeaders(raw)
		return &parsedMessage{meta: meta, headers: headers}
	}

	meta.From = env.GetHeader("From")
	meta.Subject = env.GetHeader("Subject")
	meta.To = splitAddresses(env.GetHeader("To"))
	meta.Cc = splitAddresses(env.GetHeader("Cc"))

	// Collect all headers for rule matching (e.g. header:X-List-Id).
	for _, key := range env.GetHeaderKeys() {
		if v := env.GetHeader(key); v != "" {
			headers[key] = v
		}
	}

	bodyText := env.Text
	if len(bodyText) > 8192 {
		bodyText = bodyText[:8192]
	}

	// Record attachment count so has_attachment rules work.
	for range env.Attachments {
		meta.Attachments = append(meta.Attachments, &bongov1.BlobRef{})
	}

	return &parsedMessage{
		meta:     meta,
		headers:  headers,
		bodyText: bodyText,
	}
}

// splitAddresses splits a comma-separated address list into individual entries.
func splitAddresses(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// extractBasicHeaders scans raw RFC 5322 bytes and returns the Subject and a
// map of all header names to their values. Used as a fallback when enmime fails.
func extractBasicHeaders(raw []byte) (subject string, headers map[string]string) {
	headers = make(map[string]string)
	lines := strings.Split(string(raw), "\n")
	var currentKey, currentVal string

	flush := func() {
		if currentKey == "" {
			return
		}
		v := strings.TrimSpace(currentVal)
		headers[currentKey] = v
		if strings.EqualFold(currentKey, "Subject") {
			subject = v
		}
	}

	for _, line := range lines {
		if strings.TrimRight(line, "\r") == "" {
			break
		}
		// Folded header continuation.
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			currentVal += " " + strings.TrimSpace(line)
			continue
		}
		flush()
		if idx := strings.IndexByte(line, ':'); idx > 0 {
			currentKey = strings.TrimSpace(line[:idx])
			currentVal = strings.TrimSpace(line[idx+1:])
		} else {
			currentKey = ""
			currentVal = ""
		}
	}
	flush()
	return subject, headers
}

// appendUnique appends items from src to dst, skipping duplicates already in dst.
func appendUnique(dst, src []string) []string {
	existing := make(map[string]bool, len(dst))
	for _, v := range dst {
		existing[v] = true
	}
	for _, v := range src {
		if !existing[v] {
			dst = append(dst, v)
			existing[v] = true
		}
	}
	return dst
}
