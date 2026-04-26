// Package antispam provides a pipeline Processor wrapping the built-in
// heuristic scorer and optional Bayesian filter.
package antispam

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jhillyerd/enmime"

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/pipeline"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	defaultThreshold     float32 = 0.5
	defaultHighThreshold float32 = 0.9
)

// BuiltinProcessor wraps the antispam Scorer and optional BayesFilter as a
// pipeline Processor. It scores each message and returns a StageResult with
// an action of "pass", "tag", or "quarantine".
type BuiltinProcessor struct {
	scorer        *antispam.Scorer
	bayes         *antispam.BayesFilter // may be nil
	threshold     float32               // score >= this -> tag as spam (move to Junk)
	highThreshold float32               // score >= this -> quarantine
	logger        *slog.Logger
}

// Option is a functional option for BuiltinProcessor.
type Option func(*BuiltinProcessor)

// WithBayes configures an optional Bayesian filter to blend with heuristic scores.
func WithBayes(b *antispam.BayesFilter) Option {
	return func(p *BuiltinProcessor) {
		p.bayes = b
	}
}

// WithThreshold sets the spam tagging threshold (default 0.5).
// Messages scoring at or above this value are tagged as spam and routed to Junk.
func WithThreshold(t float32) Option {
	return func(p *BuiltinProcessor) {
		p.threshold = t
	}
}

// WithHighThreshold sets the quarantine threshold (default 0.9).
// Messages scoring at or above this value are quarantined.
func WithHighThreshold(t float32) Option {
	return func(p *BuiltinProcessor) {
		p.highThreshold = t
	}
}

// WithLogger sets the structured logger used for diagnostic output.
func WithLogger(l *slog.Logger) Option {
	return func(p *BuiltinProcessor) {
		p.logger = l
	}
}

// NewBuiltinProcessor creates a BuiltinProcessor with the given Scorer and options.
func NewBuiltinProcessor(scorer *antispam.Scorer, opts ...Option) *BuiltinProcessor {
	p := &BuiltinProcessor{
		scorer:        scorer,
		threshold:     defaultThreshold,
		highThreshold: defaultHighThreshold,
		logger:        slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the stage identifier for this processor.
func (p *BuiltinProcessor) Name() string {
	return "antispam"
}

// Process reads the message from msg, scores it, and returns a StageResult.
// The returned action is one of "pass", "tag", or "quarantine".
// When the action is "tag", env.TargetMailbox is set to "Junk".
// When the action is "quarantine", env.TargetMailbox is left unchanged.
func (p *BuiltinProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	start := time.Now()

	raw, err := io.ReadAll(msg)
	if err != nil {
		return nil, fmt.Errorf("antispam: reading message: %w", err)
	}

	// Parse with enmime; fall back gracefully on parse failure.
	var subject, bodySample string
	headers := make(map[string]string)

	envelope, parseErr := enmime.ReadEnvelope(bytes.NewReader(raw))
	if parseErr != nil {
		p.logger.Warn("antispam: enmime parse failed, using minimal metadata", "error", parseErr)
		// Try net/textproto for basic header extraction.
		subject, headers = extractBasicHeaders(raw)
	} else {
		subject = envelope.GetHeader("Subject")
		for _, key := range []string{"Message-Id", "Message-ID", "Date", "From"} {
			if v := envelope.GetHeader(key); v != "" {
				headers[key] = v
			}
		}
		if len(envelope.Text) > 0 {
			bodySample = envelope.Text
			if len(bodySample) > 4096 {
				bodySample = bodySample[:4096]
			}
		}
	}

	input := &antispam.ScoringInput{
		SPFResult:      env.GetSpfResult(),
		Headers:        headers,
		Subject:        subject,
		BodySample:     bodySample,
		EnvelopeSender: env.GetSender(),
	}

	result := p.scorer.Score(input)
	score := result.Score

	// Blend with Bayesian score if available and the envelope identifies a recipient.
	if p.bayes != nil {
		for _, rcpt := range env.GetRecipients() {
			trained := p.bayes.TrainedCount(rcpt)
			if trained >= 20 {
				tokens := antispam.Tokenize(subject, bodySample)
				bayesScore := p.bayes.Classify(rcpt, tokens)
				blended := 0.6*score + 0.4*float32(bayesScore)
				p.logger.Debug("antispam: blended score",
					"recipient", rcpt,
					"heuristic", score,
					"bayes", bayesScore,
					"blended", blended,
				)
				// Use the first recipient's blended score.
				score = blended
				break
			}
		}
	}

	// Determine action based on thresholds.
	action := "pass"
	switch {
	case score >= p.highThreshold:
		action = "quarantine"
	case score >= p.threshold:
		action = "tag"
		if env != nil {
			env.TargetMailbox = "Junk"
		}
	}

	reasons := result.Reasons

	p.logger.Info("antispam: scored",
		"score", score,
		"action", action,
		"reasons", reasons,
		"sender", env.GetSender(),
		"subject", subject,
	)

	durationMs := pipeline.ClampInt32(time.Since(start).Milliseconds())

	return &bongov1.StageResult{
		Stage:       "antispam",
		Backend:     "builtin",
		Action:      action,
		Score:       score,
		Reasons:     reasons,
		ProcessedAt: start.Unix(),
		DurationMs:  durationMs,
	}, nil
}

// extractBasicHeaders does a best-effort extraction of Subject and key headers
// from a raw RFC 5322 message without full MIME parsing. It is used as a
// fallback when enmime fails.
func extractBasicHeaders(raw []byte) (subject string, headers map[string]string) {
	headers = make(map[string]string)

	// Scan line by line until the blank line separating headers from body.
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
		// Blank line marks end of headers.
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
