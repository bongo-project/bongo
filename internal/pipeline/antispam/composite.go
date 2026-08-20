package antispam

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// CompositeProcessor implements pipeline.Processor by running a fallback chain
// of external antispam backends (e.g. Rspamd, SpamAssassin) followed by the
// built-in heuristic scorer, which always runs as a baseline.
//
// External backends run for EVERY message, including Trusted ones. The
// Trusted flag (task 12.3) exempts a message from origin-authentication
// interpretation — SPF, DKIM alignment, DMARC — and from nothing else. It is
// not a content-filtering bypass; builtin.go's boundary comment states that
// explicitly, and Phase 10 rejected a "local origin" flag that skipped
// scoring for exactly this reason.
//
// Skipping externals for Trusted mail would also rest on a false premise:
// neither rspamd.go nor spamassassin.go sends the connecting IP, HELO,
// envelope sender or recipients to the backend — both transmit only the raw
// message — so an external backend here cannot evaluate origin at all. There
// is nothing origin-related to protect Trusted mail from, and skipping them
// would remove genuine content filtering.
//
// Fallback logic:
//  1. Try each external processor in order. If a processor reports that it is
//     unavailable (any reason contains "unavailable"), skip it and try the
//     next.
//  2. Always run the builtin processor for a baseline score.
//  3. The winner is chosen by severity, not raw score: actionSeverity ranks
//     pass < tag < reject/quarantine. The external result wins only if its
//     action is strictly more severe than the builtin's; if they are equally
//     severe, the higher score wins (preserving prior tie-break behaviour).
//     A builtin verdict is never weakened — this matters because DMARC
//     enforcement (applyDMARC) can escalate the builtin to "reject" at a low
//     score, which a naive score-only comparison would let a "no action"
//     external result overturn.
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
	// Buffer the message so we can pass it to multiple processors.
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
			// This external processor produced an active result.
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
			winner = externalResult
			// DMARC is evaluated only by the builtin processor, and the
			// deliver stage reads DmarcResult off whichever StageResult wins
			// here. Carry it across, or the dmarc= token silently vanishes
			// from Authentication-Results on every rspamd-enabled deployment.
			if winner.DmarcResult == "" {
				winner.DmarcResult = builtinResult.GetDmarcResult()
			}
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
