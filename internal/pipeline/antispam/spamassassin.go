package antispam

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	saDefaultHost      = "localhost"
	saDefaultPort      = 783
	saDefaultTimeout   = 30 * time.Second
	saDefaultThreshold = float32(5.0)
)

// SpamAssassinProcessor implements pipeline.Processor by connecting to a
// spamd daemon via the SPAMC/1.5 TCP protocol (SYMBOLS command). If spamd
// is unreachable the processor returns a "pass" verdict and logs a warning
// so that a missing SpamAssassin installation does not block mail delivery.
type SpamAssassinProcessor struct {
	host      string
	port      int
	timeout   time.Duration
	threshold float32 // SA raw score threshold; used for normalisation
	logger    *slog.Logger
}

// SAOption is a functional option for SpamAssassinProcessor.
type SAOption func(*SpamAssassinProcessor)

// WithSAHost sets the spamd hostname (default "localhost").
func WithSAHost(h string) SAOption {
	return func(p *SpamAssassinProcessor) {
		p.host = h
	}
}

// WithSAPort sets the spamd TCP port (default 783).
func WithSAPort(port int) SAOption {
	return func(p *SpamAssassinProcessor) {
		p.port = port
	}
}

// WithSATimeout sets the per-connection timeout (default 30s).
func WithSATimeout(d time.Duration) SAOption {
	return func(p *SpamAssassinProcessor) {
		p.timeout = d
	}
}

// WithSAThreshold sets the SA raw-score threshold used for normalisation
// (default 5.0). This should match the "required_score" in SpamAssassin's
// local.cf so the normalised 0-1 score is calibrated correctly.
func WithSAThreshold(t float32) SAOption {
	return func(p *SpamAssassinProcessor) {
		p.threshold = t
	}
}

// WithSALogger sets the structured logger (default slog.Default()).
func WithSALogger(l *slog.Logger) SAOption {
	return func(p *SpamAssassinProcessor) {
		p.logger = l
	}
}

// NewSpamAssassinProcessor creates a SpamAssassinProcessor with the given options.
func NewSpamAssassinProcessor(opts ...SAOption) *SpamAssassinProcessor {
	p := &SpamAssassinProcessor{
		host:      saDefaultHost,
		port:      saDefaultPort,
		timeout:   saDefaultTimeout,
		threshold: saDefaultThreshold,
		logger:    slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the pipeline stage identifier.
func (p *SpamAssassinProcessor) Name() string {
	return "antispam"
}

// Process sends the message to spamd using the SYMBOLS command, parses the
// response, normalises the score to [0, 1], and returns a StageResult with
// action "pass", "tag", or "quarantine".
//
// If spamd is unreachable a warning is logged and a "pass" result is returned
// so that delivery is not blocked by an unavailable external service.
func (p *SpamAssassinProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	start := time.Now()

	raw, err := io.ReadAll(msg)
	if err != nil {
		return nil, fmt.Errorf("spamassassin: reading message: %w", err)
	}

	resp, err := p.query(ctx, raw)
	if err != nil {
		// Graceful fallback: spamd is not required for mail delivery.
		p.logger.Warn("spamassassin: spamd unreachable, passing message",
			"error", err,
			"sender", env.GetSender(),
		)
		return &bongov1.StageResult{
			Stage:       "antispam",
			Backend:     "spamassassin",
			Action:      "pass",
			Score:       0,
			Reasons:     []string{"spamd_unavailable"},
			ProcessedAt: start.Unix(),
			DurationMs:  int32(time.Since(start).Milliseconds()), // #nosec G115 -- duration in ms never exceeds int32 range
		}, nil
	}

	normalised := normaliseSAScore(resp.score, resp.saThreshold)
	action := saAction(normalised)

	if action == "tag" && env != nil {
		env.TargetMailbox = "Junk"
	}

	p.logger.Info("spamassassin: scored",
		"sa_score", resp.score,
		"sa_threshold", resp.saThreshold,
		"normalised", normalised,
		"is_spam", resp.isSpam,
		"action", action,
		"rules", resp.rules,
		"sender", env.GetSender(),
	)

	return &bongov1.StageResult{
		Stage:       "antispam",
		Backend:     "spamassassin",
		Action:      action,
		Score:       normalised,
		Reasons:     resp.rules,
		ProcessedAt: start.Unix(),
		DurationMs:  int32(time.Since(start).Milliseconds()), // #nosec G115 -- duration in ms never exceeds int32 range
	}, nil
}

// saResponse holds the parsed fields from a SPAMD SYMBOLS response.
type saResponse struct {
	isSpam      bool
	score       float32
	saThreshold float32 // threshold reported by spamd itself
	rules       []string
}

// query opens a TCP connection to spamd, sends the SYMBOLS command, and
// returns the parsed response.
func (p *SpamAssassinProcessor) query(ctx context.Context, raw []byte) (*saResponse, error) {
	addr := net.JoinHostPort(p.host, strconv.Itoa(p.port))

	deadline := time.Now().Add(p.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	conn, err := net.DialTimeout("tcp", addr, time.Until(deadline))
	if err != nil {
		return nil, fmt.Errorf("spamassassin: dial %s: %w", addr, err)
	}
	defer conn.Close() //nolint:errcheck

	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("spamassassin: set deadline: %w", err)
	}

	// Build and send the SPAMC request.
	var req bytes.Buffer
	fmt.Fprintf(&req, "SYMBOLS SPAMC/1.5\r\nContent-length: %d\r\n\r\n", len(raw))
	req.Write(raw)

	if _, err := conn.Write(req.Bytes()); err != nil {
		return nil, fmt.Errorf("spamassassin: write request: %w", err)
	}

	return parseSAResponse(bufio.NewReader(conn))
}

// parseSAResponse reads and parses a SPAMD SYMBOLS response from r.
//
// Expected format:
//
//	SPAMD/1.5 0 EX_OK\r\n
//	Spam: True ; 15.3 / 5.0\r\n
//	\r\n
//	BAYES_99,HTML_MESSAGE,...
func parseSAResponse(r *bufio.Reader) (*saResponse, error) {
	// --- Status line ---
	statusLine, err := r.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("spamassassin: read status line: %w", err)
	}
	statusLine = strings.TrimRight(statusLine, "\r\n")

	parts := strings.Fields(statusLine)
	// Minimum: "SPAMD/1.5", "0", "EX_OK"
	if len(parts) < 2 {
		return nil, fmt.Errorf("spamassassin: unexpected status line: %q", statusLine)
	}
	statusCode, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("spamassassin: parse status code %q: %w", parts[1], err)
	}
	if statusCode != 0 {
		msg := ""
		if len(parts) > 2 {
			msg = strings.Join(parts[2:], " ")
		}
		return nil, fmt.Errorf("spamassassin: spamd error %d: %s", statusCode, msg)
	}

	// --- Header lines (scan until blank line) ---
	var resp saResponse
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("spamassassin: read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line separates headers from body
		}

		// Spam: True ; 15.3 / 5.0
		if strings.HasPrefix(line, "Spam:") {
			if parseErr := parseSpamHeader(line, &resp); parseErr != nil {
				return nil, parseErr
			}
		}
	}

	// --- Body: comma-separated rule names ---
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("spamassassin: read body: %w", err)
	}
	bodyStr := strings.TrimSpace(string(body))
	if bodyStr != "" {
		for _, rule := range strings.Split(bodyStr, ",") {
			rule = strings.TrimSpace(rule)
			if rule != "" {
				resp.rules = append(resp.rules, rule)
			}
		}
	}

	return &resp, nil
}

// parseSpamHeader parses a line of the form:
//
//	Spam: True ; 15.3 / 5.0
func parseSpamHeader(line string, resp *saResponse) error {
	// Strip the "Spam:" prefix.
	value := strings.TrimSpace(strings.TrimPrefix(line, "Spam:"))

	// Split on " ; " to get verdict and score portion.
	semicolonIdx := strings.Index(value, ";")
	if semicolonIdx < 0 {
		return fmt.Errorf("spamassassin: malformed Spam header: %q", line)
	}

	verdict := strings.TrimSpace(value[:semicolonIdx])
	scorePart := strings.TrimSpace(value[semicolonIdx+1:])

	resp.isSpam = strings.EqualFold(verdict, "true")

	// Score part: "15.3 / 5.0"
	slashIdx := strings.Index(scorePart, "/")
	if slashIdx < 0 {
		return fmt.Errorf("spamassassin: malformed score in Spam header: %q", line)
	}

	scoreStr := strings.TrimSpace(scorePart[:slashIdx])
	threshStr := strings.TrimSpace(scorePart[slashIdx+1:])

	score64, err := strconv.ParseFloat(scoreStr, 32)
	if err != nil {
		return fmt.Errorf("spamassassin: parse score %q: %w", scoreStr, err)
	}
	thresh64, err := strconv.ParseFloat(threshStr, 32)
	if err != nil {
		return fmt.Errorf("spamassassin: parse threshold %q: %w", threshStr, err)
	}

	resp.score = float32(score64)
	resp.saThreshold = float32(thresh64)
	return nil
}

// normaliseSAScore maps a raw SA score to [0.0, 1.0] using the reported
// spamd threshold as the reference point. A score equal to threshold maps to
// 1.0; lower scores scale proportionally; higher scores are capped at 1.0.
//
// If saThreshold is zero or negative, the configured processor threshold is
// used to avoid a divide-by-zero.
func normaliseSAScore(score, saThreshold float32) float32 {
	ref := saThreshold
	if ref <= 0 {
		ref = saDefaultThreshold
	}
	normalised := score / ref
	if normalised > 1.0 {
		return 1.0
	}
	if normalised < 0 {
		return 0
	}
	return normalised
}

// saAction converts a normalised [0, 1] score into a pipeline action string.
// Thresholds mirror the builtin processor defaults so behaviour is consistent
// across backends.
func saAction(normalised float32) string {
	switch {
	case normalised >= defaultHighThreshold:
		return "quarantine"
	case normalised >= defaultThreshold:
		return "tag"
	default:
		return "pass"
	}
}
