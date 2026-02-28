package antispam

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/bongo-go/bongo-go/internal/antispam"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// mockProcessor is a test double for the processor interface.
type mockProcessor struct {
	name   string
	result *bongov1.StageResult
	err    error
}

func (m *mockProcessor) Name() string { return m.name }
func (m *mockProcessor) Process(_ context.Context, _ *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	return m.result, m.err
}

// unavailableResult builds a StageResult that signals an unavailable backend,
// matching the exact reason strings used by RspamdProcessor and
// SpamAssassinProcessor.
func unavailableResult(backend string) *bongov1.StageResult {
	return &bongov1.StageResult{
		Stage:   "antispam",
		Backend: backend,
		Action:  "pass",
		Score:   0,
		Reasons: []string{backend + " unavailable"},
	}
}

// activeResult builds a StageResult that represents an active (non-fallback) verdict.
func activeResult(backend, action string, score float32) *bongov1.StageResult {
	return &bongov1.StageResult{
		Stage:   "antispam",
		Backend: backend,
		Action:  action,
		Score:   score,
		Reasons: []string{"TEST_RULE"},
	}
}

// newTestBuiltin creates a BuiltinProcessor with very high thresholds so it
// always returns "pass" with a predictable low score unless explicitly
// configured otherwise.
func newTestBuiltin(threshold, highThreshold float32) *BuiltinProcessor {
	return NewBuiltinProcessor(
		antispam.NewScorer(),
		WithThreshold(threshold),
		WithHighThreshold(highThreshold),
		WithLogger(slog.Default()),
	)
}

// TestCompositeRspamdFirst verifies that when Rspamd is available its result
// is used and SpamAssassin is not consulted.
func TestCompositeRspamdFirst(t *testing.T) {
	rspamd := &mockProcessor{
		name:   "rspamd",
		result: activeResult("rspamd", "tag", 0.75),
	}
	sa := &mockProcessor{
		name:   "spamassassin",
		result: activeResult("spamassassin", "quarantine", 1.0),
	}
	builtin := newTestBuiltin(0.95, 0.99) // builtin will pass at low score

	c := NewCompositeProcessor(builtin, []processor{rspamd, sa}, slog.Default())
	env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
	msg := strings.NewReader(testMessage("user@example.com", "Hello", "Body", nil))

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Backend != "rspamd" {
		t.Errorf("backend = %q, want %q", result.Backend, "rspamd")
	}
	if result.Action != "tag" {
		t.Errorf("action = %q, want %q", result.Action, "tag")
	}
	if result.Score != 0.75 {
		t.Errorf("score = %f, want 0.75", result.Score)
	}
}

// TestCompositeFallbackToSA verifies that when Rspamd is unavailable the
// composite falls through to SpamAssassin.
func TestCompositeFallbackToSA(t *testing.T) {
	rspamd := &mockProcessor{
		name:   "rspamd",
		result: unavailableResult("rspamd"),
	}
	sa := &mockProcessor{
		name:   "spamassassin",
		result: activeResult("spamassassin", "quarantine", 1.0),
	}
	builtin := newTestBuiltin(0.95, 0.99)

	c := NewCompositeProcessor(builtin, []processor{rspamd, sa}, slog.Default())
	env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
	msg := strings.NewReader(testMessage("user@example.com", "Hello", "Body", nil))

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Backend != "spamassassin" {
		t.Errorf("backend = %q, want %q", result.Backend, "spamassassin")
	}
	if result.Action != "quarantine" {
		t.Errorf("action = %q, want %q", result.Action, "quarantine")
	}
}

// TestCompositeFallbackToBuiltin verifies that when all external processors
// are unavailable the builtin result is used.
func TestCompositeFallbackToBuiltin(t *testing.T) {
	rspamd := &mockProcessor{
		name:   "rspamd",
		result: unavailableResult("rspamd"),
	}
	sa := &mockProcessor{
		name:   "spamassassin",
		result: unavailableResult("spamassassin"),
	}
	// Use low thresholds so the builtin definitely produces a non-pass verdict.
	builtin := newTestBuiltin(0.05, 0.10)

	c := NewCompositeProcessor(builtin, []processor{rspamd, sa}, slog.Default())
	env := &bongov1.QueueEnvelope{
		Sender:    "spammer@evil.com",
		SpfResult: "fail",
	}
	// A message crafted to score above the very low builtin threshold.
	raw := "From: spammer@evil.com\r\nSubject: \r\nContent-Type: text/plain\r\n\r\nBuy cheap meds now!"
	msg := strings.NewReader(raw)

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Backend != "builtin" {
		t.Errorf("backend = %q, want %q", result.Backend, "builtin")
	}
	// With threshold=0.05/0.10 and a spammy message, action must be non-pass.
	if result.Action == "pass" {
		t.Errorf("expected non-pass action from builtin, got %q (score=%.3f)", result.Action, result.Score)
	}
}

// TestCompositeMaxScore verifies that when an external processor returns a
// "pass" with a lower score than the builtin, the builtin result is used.
func TestCompositeMaxScore(t *testing.T) {
	// External returns a low-score pass verdict.
	rspamd := &mockProcessor{
		name:   "rspamd",
		result: activeResult("rspamd", "pass", 0.1),
	}
	// Use low thresholds so the builtin scores the message as spam.
	builtin := newTestBuiltin(0.05, 0.10)

	c := NewCompositeProcessor(builtin, []processor{rspamd}, slog.Default())
	env := &bongov1.QueueEnvelope{
		Sender:    "spammer@evil.com",
		SpfResult: "fail",
	}
	raw := "From: spammer@evil.com\r\nSubject: \r\nContent-Type: text/plain\r\n\r\nBuy cheap meds now!"
	msg := strings.NewReader(raw)

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Builtin must win because external said "pass" and builtin scored higher.
	if result.Backend != "builtin" {
		t.Errorf("backend = %q, want %q", result.Backend, "builtin")
	}
	if result.Action == "pass" {
		t.Errorf("expected non-pass action (builtin should dominate), got %q", result.Action)
	}
}

// TestCompositeExternalErrorFallthrough verifies that a hard error from an
// external processor causes it to be skipped rather than aborting the pipeline.
func TestCompositeExternalErrorFallthrough(t *testing.T) {
	rspamd := &mockProcessor{
		name: "rspamd",
		err:  errors.New("connection refused"),
	}
	sa := &mockProcessor{
		name:   "spamassassin",
		result: activeResult("spamassassin", "tag", 0.6),
	}
	builtin := newTestBuiltin(0.95, 0.99)

	c := NewCompositeProcessor(builtin, []processor{rspamd, sa}, slog.Default())
	env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
	msg := strings.NewReader(testMessage("user@example.com", "Hello", "Body", nil))

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Backend != "spamassassin" {
		t.Errorf("backend = %q, want %q", result.Backend, "spamassassin")
	}
}

// TestCompositeNoExternals verifies that a composite with no external
// processors always uses the builtin result.
func TestCompositeNoExternals(t *testing.T) {
	builtin := newTestBuiltin(0.95, 0.99)
	c := NewCompositeProcessor(builtin, nil, slog.Default())
	env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
	msg := strings.NewReader(testMessage("user@example.com", "Hello", "Clean message body.", nil))

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Backend != "builtin" {
		t.Errorf("backend = %q, want %q", result.Backend, "builtin")
	}
}

// TestCompositeName verifies that Name() always returns "antispam".
func TestCompositeName(t *testing.T) {
	c := NewCompositeProcessor(newTestBuiltin(0.5, 0.9), nil, nil)
	if c.Name() != "antispam" {
		t.Errorf("Name() = %q, want %q", c.Name(), "antispam")
	}
}

// TestCompositeTargetMailboxTag verifies that TargetMailbox is set to "Junk"
// when the external processor tags a message.
func TestCompositeTargetMailboxTag(t *testing.T) {
	rspamd := &mockProcessor{
		name:   "rspamd",
		result: activeResult("rspamd", "tag", 0.7),
	}
	builtin := newTestBuiltin(0.95, 0.99)

	c := NewCompositeProcessor(builtin, []processor{rspamd}, slog.Default())
	env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
	msg := strings.NewReader(testMessage("user@example.com", "Hello", "Body", nil))

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "tag" {
		t.Errorf("action = %q, want %q", result.Action, "tag")
	}
	if env.TargetMailbox != "Junk" {
		t.Errorf("TargetMailbox = %q, want %q", env.TargetMailbox, "Junk")
	}
}

// TestCompositeIsUnavailable covers the isUnavailable helper.
func TestCompositeIsUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		reasons []string
		want    bool
	}{
		{
			name:    "rspamd unavailable reason",
			reasons: []string{"rspamd unavailable"},
			want:    true,
		},
		{
			name:    "spamd unavailable reason",
			reasons: []string{"spamd_unavailable"},
			want:    true,
		},
		{
			name:    "active result no unavailable",
			reasons: []string{"BAYES_SPAM", "SPF_FAIL"},
			want:    false,
		},
		{
			name:    "empty reasons",
			reasons: nil,
			want:    false,
		},
		{
			name:    "mixed reasons with unavailable",
			reasons: []string{"some reason", "backend unavailable", "other"},
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &bongov1.StageResult{Reasons: tc.reasons}
			got := isUnavailable(r)
			if got != tc.want {
				t.Errorf("isUnavailable(%v) = %v, want %v", tc.reasons, got, tc.want)
			}
		})
	}
}
