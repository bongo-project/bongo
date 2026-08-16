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
// Fallback logic:
//  1. Try each external processor in order. If a processor reports that it is
//     unavailable (any reason contains "unavailable"), skip it and try the next.
//  2. Always run the builtin processor for a baseline score.
//  3. The final result is the external result (if one was active) when its
//     action is not "pass", or when its score exceeds the builtin score.
//     Otherwise the builtin result is used. External results always take
//     precedence over the builtin when they produce a non-pass verdict.
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

	// Try each external processor in order until one is available.
	var externalResult *bongov1.StageResult
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

	// Always run the builtin as a baseline.
	// Reset TargetMailbox before builtin so it does not inherit an externally
	// set value; we will restore the external value if it wins below.
	savedMailbox := env.TargetMailbox
	if externalResult != nil {
		// Clear so builtin starts from a clean state.
		env.TargetMailbox = ""
	}

	builtinResult, err := c.builtin.Process(ctx, env, bytes.NewReader(raw))
	if err != nil {
		// Builtin should never fail; propagate the error.
		return nil, err
	}

	// Determine winner.
	if externalResult == nil {
		// No reachable external processor; use builtin.
		c.logger.Debug("composite antispam: all externals unavailable, using builtin",
			"score", builtinResult.Score,
			"action", builtinResult.Action,
		)
		return builtinResult, nil
	}

	// External was active. Take the result with the stronger verdict.
	// Precedence: external wins if its action is not "pass", or if its score
	// is >= the builtin score. If builtin produced a stronger result than the
	// external, use builtin.
	if externalResult.Action != "pass" || externalResult.Score >= builtinResult.Score {
		// Restore the external TargetMailbox (builtin may have overwritten it).
		if externalResult.Action == "tag" {
			env.TargetMailbox = "Junk"
		} else {
			env.TargetMailbox = savedMailbox
		}
		// DMARC is evaluated only by the builtin processor, and the deliver
		// stage reads DmarcResult off whichever StageResult wins here. Carry
		// it across, or the dmarc= token silently vanishes from
		// Authentication-Results on every rspamd-enabled deployment.
		if externalResult.DmarcResult == "" {
			externalResult.DmarcResult = builtinResult.GetDmarcResult()
		}
		c.logger.Debug("composite antispam: external result wins",
			"backend", externalResult.Backend,
			"score", externalResult.Score,
			"action", externalResult.Action,
			"builtin_score", builtinResult.Score,
		)
		return externalResult, nil
	}

	// Builtin produced a stronger result.
	c.logger.Debug("composite antispam: builtin score exceeds external, using builtin",
		"external_backend", externalResult.Backend,
		"external_score", externalResult.Score,
		"builtin_score", builtinResult.Score,
		"action", builtinResult.Action,
	)
	return builtinResult, nil
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
