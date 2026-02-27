package antispam

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	defaultRspamdURL     = "http://localhost:11333"
	defaultRspamdTimeout = 30 * time.Second
)

// rspamdResponse represents the JSON response from Rspamd's /checkv2 endpoint.
type rspamdResponse struct {
	Score         float64                   `json:"score"`
	RequiredScore float64                   `json:"required_score"`
	Action        string                    `json:"action"`
	Symbols       map[string]rspamdSymbol   `json:"symbols"`
}

// rspamdSymbol represents a single symbol entry in the Rspamd response.
type rspamdSymbol struct {
	Score       float64 `json:"score"`
	Description string  `json:"description"`
}

// RspamdProcessor implements the pipeline.Processor interface by delegating
// spam evaluation to a Rspamd instance via its HTTP /checkv2 API. On connection
// failure the processor logs a warning and returns a "pass" verdict so that
// Rspamd being unavailable never blocks mail delivery.
type RspamdProcessor struct {
	url     string
	timeout time.Duration
	headers map[string]string
	client  *http.Client
	logger  *slog.Logger
}

// RspamdOption is a functional option for RspamdProcessor.
type RspamdOption func(*RspamdProcessor)

// WithRspamdURL sets the base URL for the Rspamd instance (default: http://localhost:11333).
func WithRspamdURL(url string) RspamdOption {
	return func(p *RspamdProcessor) {
		p.url = url
	}
}

// WithRspamdTimeout sets the HTTP client timeout (default: 30s).
func WithRspamdTimeout(d time.Duration) RspamdOption {
	return func(p *RspamdProcessor) {
		p.timeout = d
		p.client.Timeout = d
	}
}

// WithRspamdHeaders sets custom HTTP headers sent with every request (e.g. Password).
func WithRspamdHeaders(h map[string]string) RspamdOption {
	return func(p *RspamdProcessor) {
		p.headers = h
	}
}

// WithRspamdLogger sets the structured logger used for diagnostic output.
func WithRspamdLogger(l *slog.Logger) RspamdOption {
	return func(p *RspamdProcessor) {
		p.logger = l
	}
}

// NewRspamdProcessor creates a RspamdProcessor with sensible defaults and applies
// any provided options.
func NewRspamdProcessor(opts ...RspamdOption) *RspamdProcessor {
	p := &RspamdProcessor{
		url:     defaultRspamdURL,
		timeout: defaultRspamdTimeout,
		headers: make(map[string]string),
		client:  &http.Client{Timeout: defaultRspamdTimeout},
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the stage identifier for this processor.
func (p *RspamdProcessor) Name() string {
	return "antispam"
}

// Process POSTs the raw message to Rspamd's /checkv2 endpoint, parses the
// response, and maps the Rspamd action to a pipeline StageResult. If Rspamd is
// unreachable the method logs a warning and returns a "pass" result so mail
// delivery is never blocked by an unavailable scanner.
func (p *RspamdProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	start := time.Now()

	endpoint := p.url + "/checkv2"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, msg)
	if err != nil {
		return nil, fmt.Errorf("antispam/rspamd: building request: %w", err)
	}
	req.Header.Set("Content-Type", "message/rfc822")
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		p.logger.Warn("antispam/rspamd: unreachable, passing message",
			"url", endpoint,
			"sender", env.GetSender(),
			"error", err,
		)
		return &bongov1.StageResult{
			Stage:       "antispam",
			Backend:     "rspamd",
			Action:      "pass",
			Score:       0,
			Reasons:     []string{"rspamd unavailable"},
			ProcessedAt: start.Unix(),
			DurationMs:  int32(time.Since(start).Milliseconds()),
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		p.logger.Warn("antispam/rspamd: unexpected status, passing message",
			"status", resp.StatusCode,
			"sender", env.GetSender(),
		)
		return &bongov1.StageResult{
			Stage:       "antispam",
			Backend:     "rspamd",
			Action:      "pass",
			Score:       0,
			Reasons:     []string{fmt.Sprintf("rspamd returned HTTP %d", resp.StatusCode)},
			ProcessedAt: start.Unix(),
			DurationMs:  int32(time.Since(start).Milliseconds()),
		}, nil
	}

	var rspamdResp rspamdResponse
	if err := json.NewDecoder(resp.Body).Decode(&rspamdResp); err != nil {
		return nil, fmt.Errorf("antispam/rspamd: decoding response: %w", err)
	}

	action := mapRspamdAction(rspamdResp.Action)
	score := normaliseScore(rspamdResp.Score, rspamdResp.RequiredScore)
	reasons := extractReasons(rspamdResp.Symbols)

	if action == "tag" && env != nil {
		env.TargetMailbox = "Junk"
	}

	p.logger.Info("antispam/rspamd: scored",
		"rspamd_action", rspamdResp.Action,
		"action", action,
		"score", score,
		"raw_score", rspamdResp.Score,
		"required_score", rspamdResp.RequiredScore,
		"sender", env.GetSender(),
	)

	return &bongov1.StageResult{
		Stage:       "antispam",
		Backend:     "rspamd",
		Action:      action,
		Score:       float32(score),
		Reasons:     reasons,
		ProcessedAt: start.Unix(),
		DurationMs:  int32(time.Since(start).Milliseconds()),
	}, nil
}

// mapRspamdAction converts a Rspamd action string to a pipeline action.
//
// Mapping:
//   - "no action"       -> "pass"
//   - "greylist"        -> "pass"  (greylisting handled at SMTP level)
//   - "add header"      -> "tag"   (route to Junk)
//   - "rewrite subject" -> "tag"
//   - "soft reject"     -> "tag"
//   - "reject"          -> "quarantine"
//
// Any unknown action defaults to "pass" to avoid unintended blocking.
func mapRspamdAction(rspamdAction string) string {
	switch rspamdAction {
	case "no action", "greylist":
		return "pass"
	case "add header", "rewrite subject", "soft reject":
		return "tag"
	case "reject":
		return "quarantine"
	default:
		return "pass"
	}
}

// normaliseScore maps a raw Rspamd score to the [0.0, 1.0] range used by the
// pipeline. The formula is score / required_score, capped at 1.0. If
// required_score is zero or negative the raw score is returned capped at 1.0.
func normaliseScore(score, requiredScore float64) float64 {
	if requiredScore <= 0 {
		if score > 1.0 {
			return 1.0
		}
		return score
	}
	normalised := score / requiredScore
	if normalised > 1.0 {
		return 1.0
	}
	if normalised < 0 {
		return 0
	}
	return normalised
}

// extractReasons builds a sorted slice of human-readable reasons from the
// Rspamd symbols map. Sorting ensures deterministic output for tests.
func extractReasons(symbols map[string]rspamdSymbol) []string {
	if len(symbols) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(symbols))
	for name, sym := range symbols {
		if sym.Description != "" {
			reasons = append(reasons, fmt.Sprintf("%s: %s (%.2f)", name, sym.Description, sym.Score))
		} else {
			reasons = append(reasons, fmt.Sprintf("%s (%.2f)", name, sym.Score))
		}
	}
	sort.Strings(reasons)
	return reasons
}
