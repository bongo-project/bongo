// Package rules provides a pipeline Processor that evaluates per-user filter
// rules against inbound messages and updates the delivery envelope accordingly.
package rules

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/rules"
	"github.com/bongo-project/bongo/internal/store"
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
// returns a StageResult. Every enabled rule that matches runs, in priority
// order, up to and including the first match with stop_processing set; each
// rule holds one to ten actions.
//
// Actions combine as follows. The first terminal action reached (move or
// delete) wins and later terminal actions are ignored, whichever rule they
// came from. Non-terminal actions from every matched rule accumulate:
//
//   - copy adds a mailbox to env.CopyMailboxes (the deliver stage writes a
//     second MailMeta over the same blob);
//   - flag and mark_read set \Flagged and \Seen in env.PendingFlags;
//   - tag appends to env.PendingTags;
//   - forward appends a ForwardRequest the deliver stage vets.
//
// A winning move sets env.TargetMailbox. A winning delete returns Action
// "reject", which the runner turns into quarantine, but only when nothing
// else was asked for. If the rules also copy or forward, rejecting would drop
// those, so the stage passes and sets env.DiscardNormal: the deliver stage
// then skips the normal destination and still writes the copies and sends
// the forwards.
//
// Spam stays in Junk. The antispam stage runs first and files tagged mail by
// setting env.TargetMailbox to "Junk"; a rule move does not replace that
// unless it targets Junk or Trash, and a spam message is copied only to Junk
// or Trash (a copy elsewhere would be a second, unfiltered delivery of
// spam). A message released from quarantine arrives with INBOX, not Junk,
// so its rules apply as for any other mail.
//
// The stage decides per envelope, so producers write one envelope per local
// recipient (SMTP EnqueueLocal, agent DepositMessage, compose). The
// multi-recipient loop below exists only for legacy spool entries written
// before that change. Its merge semantics are known to be wrong for more than
// one recipient: any delete rejects the whole envelope and the first
// move target applies to every recipient. A warning is logged when an
// envelope carries more than one recipient.
func (p *RulesProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	start := time.Now()
	if n := len(env.GetRecipients()); n > 1 {
		p.logger.Warn("rules: envelope has multiple recipients; rules are applied envelope-wide", "id", env.GetId(), "recipients", n)
	}

	raw, err := io.ReadAll(msg)
	if err != nil {
		return nil, fmt.Errorf("rules: reading message: %w", err)
	}

	// Parse the message to extract fields used by rule conditions.
	parsed := rules.ParseMessage(raw)

	var (
		reasons       []string
		terminal      string // "", "move" or "delete": the first terminal action reached
		targetMailbox string
		pendingFlags  uint32
		copies        []string
		forwards      int
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

		for _, rule := range p.engine.MatchingRules(userRules, parsed) {
			actions := rules.Actions(rule)
			if len(actions) > store.MaxRuleActions {
				actions = actions[:store.MaxRuleActions]
			}
			for _, action := range actions {
				p.logger.Info("rules: rule matched",
					"recipient", recipient,
					"rule", rule.GetId(),
					"action", action.GetType(),
					"target", action.GetTarget(),
				)

				switch strings.ToLower(action.GetType()) {
				case "move":
					if terminal == "" {
						terminal = "move"
						targetMailbox = action.Target
					}
					reasons = append(reasons, "move:"+action.Target)
				case "copy":
					if action.Target != "" && !containsFold(copies, action.Target) {
						copies = append(copies, action.Target)
					}
					reasons = append(reasons, "copy:"+action.Target)
				case "flag":
					pendingFlags |= store.FlagFlagged
					reasons = append(reasons, "flag:"+action.Target)
				case "mark_read":
					pendingFlags |= store.FlagSeen
					reasons = append(reasons, "mark_read")
				case "delete":
					if terminal == "" {
						terminal = "delete"
					}
					reasons = append(reasons, "delete")
				case "forward":
					if action.Target != "" {
						// Attributed to the rule's owner: the deliver stage checks that
						// user's verified targets and rate limit.
						env.ForwardRequests = append(env.ForwardRequests, &bongov1.ForwardRequest{UserId: userID, Target: action.Target})
						forwards++
						reasons = append(reasons, "forward:"+action.Target)
					}
				case "tag":
					env.PendingTags = appendUnique(env.PendingTags, action.Tags)
					for _, t := range action.Tags {
						reasons = append(reasons, "tag:"+t)
					}
				}
			}
		}
	}

	// The antispam stage files spam in Junk; only a rule aimed at Junk or Trash
	// may change that.
	spam := strings.EqualFold(env.GetTargetMailbox(), "Junk")
	if spam {
		kept := copies[:0:0]
		for _, c := range copies {
			if isJunkOrTrash(c) {
				kept = append(kept, c)
			}
		}
		copies = kept
	}

	finalAction := "pass"
	switch terminal {
	case "move":
		if !spam || isJunkOrTrash(targetMailbox) {
			env.TargetMailbox = targetMailbox
		}
	case "delete":
		if len(copies) == 0 && forwards == 0 {
			finalAction = "reject"
		} else {
			env.DiscardNormal = true
		}
	}
	env.PendingFlags |= pendingFlags
	env.CopyMailboxes = appendUniqueFold(env.CopyMailboxes, copies)

	return &bongov1.StageResult{
		Stage:       "rules",
		Backend:     "builtin",
		Action:      finalAction,
		Reasons:     reasons,
		ProcessedAt: start.Unix(),
		DurationMs:  pipeline.ClampInt32(time.Since(start).Milliseconds()),
	}, nil
}

func isJunkOrTrash(mailbox string) bool {
	return strings.EqualFold(mailbox, "Junk") || strings.EqualFold(mailbox, "Trash")
}

func containsFold(list []string, v string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, v) })
}

// appendUniqueFold appends the items of src missing from dst, ignoring case.
func appendUniqueFold(dst, src []string) []string {
	for _, v := range src {
		if !containsFold(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
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
