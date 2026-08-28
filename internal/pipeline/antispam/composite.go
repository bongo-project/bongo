package antispam

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// CompositeProcessor implements pipeline.Processor by running a fallback chain
// of external antispam backends (e.g. Rspamd, SpamAssassin) followed by the
// built-in heuristic scorer, which always runs as a baseline.
//
// External backends run for EVERY message, including Trusted ones. Trust
// exempts a message from origin-authentication interpretation — SPF, DKIM
// alignment, DMARC — and from nothing else; it is not a content-filtering
// bypass, and a "local origin" flag that skipped scoring outright was rejected
// for exactly this reason (see builtin.go's boundary comment). Skipping
// externals for Trusted mail would also rest on a false premise: neither
// rspamd.go nor spamassassin.go sends the connecting IP, HELO, envelope sender
// or recipients to the backend — both transmit only the raw message — so an
// external backend here cannot evaluate origin at all. There is nothing
// origin-related to protect Trusted mail from, and skipping them would remove
// genuine content filtering.
//
// Fallback logic:
//  1. Try each external processor in order, skipping any that reports itself
//     unavailable (any reason contains "unavailable").
//  2. Always run the builtin processor for a baseline score.
//  3. The winner is chosen by severity, not raw score: actionSeverity ranks
//     pass < tag < reject/quarantine. The external result wins only if its
//     action is strictly more severe than the builtin's; if they are equally
//     severe, the higher score wins (preserving prior tie-break behaviour).
//     A builtin verdict is never weakened — this matters because DMARC
//     enforcement (applyDMARC) can escalate the builtin to "reject" at a low
//     score, which a naive score-only comparison would let a "no action"
//     external result overturn.
//  4. The loser's evidence — Reasons, and the builtin-only DmarcResult — is
//     merged onto the winner, so a verdict from one backend never discards
//     what the other found. Score and Action remain the winner's alone.
type CompositeProcessor struct {
	externals []processor // ordered: rspamd first, then spamassassin
	builtin   *BuiltinProcessor
	logger    *slog.Logger
}

// processor is a local alias for the pipeline.Processor interface so that
// this package does not import its own parent package (cycle avoidance).
// The interface is identical to pipeline.Processor.
type processor interface {
	Name() string
	Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error)
}

// NewCompositeProcessor returns a CompositeProcessor that tries each processor
// in externals in order before always running builtin as a baseline.
// externals is typically [rspamdProcessor, spamAssassinProcessor] but may be
// empty, in which case the builtin result is always used.
func NewCompositeProcessor(builtin *BuiltinProcessor, externals []processor, logger *slog.Logger) *CompositeProcessor {
	if logger == nil {
		logger = slog.Default()
	}
	return &CompositeProcessor{
		externals: externals,
		builtin:   builtin,
		logger:    logger,
	}
}

// NewCompositeProcessorFor adapts NewCompositeProcessor for callers outside
// this package (e.g. internal/agent) that hold concrete or pipeline.Processor
// values and cannot name the unexported `processor` interface used above —
// composite.go deliberately does not import internal/pipeline itself, because
// internal/pipeline (rescan.go) already imports this package, and importing
// it back would create a cycle. Each variadic argument is converted to
// `processor` individually via ordinary interface satisfaction, which does
// not require the caller to spell the type.
func NewCompositeProcessorFor(builtin *BuiltinProcessor, logger *slog.Logger, externals ...processor) *CompositeProcessor {
	return NewCompositeProcessor(builtin, externals, logger)
}

// Name returns the pipeline stage identifier.
func (c *CompositeProcessor) Name() string {
	return "antispam"
}

// Process runs the fallback chain and returns a StageResult. The message body
// is buffered once so that it can be replayed for each processor.
func (c *CompositeProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	raw, err := io.ReadAll(msg)
	if err != nil {
		return nil, err
	}

	// Externals run for trusted mail too. Trust exempts a message from
	// origin authentication, never from content filtering — see the doc
	// comment on CompositeProcessor.
	//
	// Try each external processor in order until one is available.
	var externalResult *bongov1.StageResult
	{
		for _, ext := range c.externals {
			result, err := ext.Process(ctx, env, bytes.NewReader(raw))
			if err != nil {
				c.logger.Warn("composite antispam: external processor error, trying next",
					"backend", ext.Name(),
					"error", err,
				)
				continue
			}
			if isUnavailable(result) {
				c.logger.Debug("composite antispam: external processor unavailable, trying next",
					"backend", result.Backend,
					"reasons", result.Reasons,
				)
				continue
			}
			externalResult = result
			break
		}
	}

	// Always run the builtin as a baseline.
	// Reset TargetMailbox before builtin so it does not inherit an externally
	// set value; the final winner (below) sets it from scratch either way.
	savedMailbox := env.TargetMailbox
	if externalResult != nil {
		env.TargetMailbox = ""
	}

	builtinResult, err := c.builtin.Process(ctx, env, bytes.NewReader(raw))
	if err != nil {
		// Builtin should never fail; propagate the error.
		return nil, err
	}

	// Determine the winner by severity, not raw score, so an external "no
	// action"/"pass" result can never overturn a builtin verdict that was
	// escalated by DMARC enforcement (builtin.go applyDMARC) at a low score.
	// actionSeverity ranks pass(0) < tag(1) < reject/quarantine(3).
	winner := builtinResult
	var loser *bongov1.StageResult
	if externalResult != nil {
		extSeverity := actionSeverity(externalResult.Action)
		builtinSeverity := actionSeverity(builtinResult.Action)
		externalWins := false
		switch {
		case extSeverity > builtinSeverity:
			externalWins = true
		case extSeverity < builtinSeverity:
			externalWins = false
		default:
			// Equally severe: break the tie by score (prior behaviour).
			externalWins = externalResult.Score >= builtinResult.Score
		}
		if externalWins {
			winner, loser = externalResult, builtinResult
		} else {
			loser = externalResult
		}
	}

	// Both processors ran, so both gathered evidence; only one of them gets
	// to decide the action. Carry the loser's findings onto the winner so
	// they still reach the recipient, in either direction:
	//
	//   - Reasons become X-Spam-Reasons (deliver/headers.go) and the
	//     quarantine entry's Reason. Replacing rather than merging meant that
	//     on an rspamd/spamassassin deployment where the external verdict
	//     outranked the builtin, none of the builtin's evidence survived --
	//     no dnsbl_<zone>, no heuristic reason, no dmarc_<disposition> --
	//     leaving "why was this junked" unanswerable for exactly the
	//     deployments running two scanners.
	//   - DmarcResult is evaluated ONLY by the builtin, and the deliver stage
	//     reads it off whichever StageResult wins here, so without the carry
	//     the dmarc= token vanishes from Authentication-Results.
	//
	// Score and Action are deliberately NOT merged: they stay the winner's
	// alone. Combining them would change verdicts, and would break the
	// "an external result can strengthen but never weaken the builtin"
	// invariant that the severity comparison above exists to enforce. This
	// merge moves evidence, not judgements.
	if loser != nil {
		winner.Reasons = mergeReasons(winner.GetReasons(), loser.GetReasons())
		if winner.DmarcResult == "" {
			winner.DmarcResult = loser.GetDmarcResult()
		}
	}

	// TargetMailbox reflects the winning action alone, not whichever
	// processor happened to touch it last: "tag" routes to Junk;
	// "reject"/"quarantine" are held out of the inbox by runner.go without
	// using TargetMailbox; "pass" must restore whatever an earlier stage
	// (e.g. rules) had already set, not the antispam stage's own scratch value.
	if winner.Action == "tag" {
		env.TargetMailbox = "Junk"
	} else {
		env.TargetMailbox = savedMailbox
	}

	if winner == externalResult {
		c.logger.Debug("composite antispam: external result wins",
			"backend", externalResult.Backend,
			"score", externalResult.Score,
			"action", externalResult.Action,
			"builtin_score", builtinResult.Score,
			"builtin_action", builtinResult.Action,
		)
	} else {
		c.logger.Debug("composite antispam: builtin result wins",
			"score", builtinResult.Score,
			"action", builtinResult.Action,
		)
	}

	return winner, nil
}

// mergeReasons returns primary's reasons followed by any of secondary's that
// are not already present, preserving order of first occurrence. Neither input
// slice is modified: the result is always a fresh allocation, so merging onto a
// winner cannot mutate the loser's backing array.
//
// Reason strings are merged verbatim rather than being prefixed with their
// backend, because the namespaces are already distinct and self-describing:
// the builtin emits "dnsbl_<zone>" and "dmarc_<disposition>", rspamd emits its
// symbol names, SpamAssassin its rule names. Prefixing would also break the
// DNSBL requirement that the header name the zone that listed the sender.
//
// Deduplication makes the merge idempotent, so re-merging an already-merged
// result is harmless.
func mergeReasons(primary, secondary []string) []string {
	if len(secondary) == 0 {
		return primary
	}
	seen := make(map[string]bool, len(primary)+len(secondary))
	out := make([]string, 0, len(primary)+len(secondary))
	for _, r := range primary {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, r := range secondary {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// isUnavailable reports whether a StageResult indicates that the backend was
// unreachable. Both Rspamd and SpamAssassin embed "unavailable" in one of the
// reason strings when they cannot connect to their external daemon.
func isUnavailable(r *bongov1.StageResult) bool {
	for _, reason := range r.GetReasons() {
		if strings.Contains(reason, "unavailable") {
			return true
		}
	}
	return false
}
