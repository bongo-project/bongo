package antispam

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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

// dmarcEscalationFixture returns a BuiltinProcessor wired to a DMARC
// evaluator whose only record is p=<disposition> for example.com, and the
// message/envelope pair (crafted from TestBuiltinProcessorTrustedSkipsOriginAuth)
// that reliably makes the heuristic scorer alone produce "pass" so any
// escalation observed is attributable to DMARC, not the phrase scorer.
func dmarcEscalationFixture(t *testing.T, disposition string) (*BuiltinProcessor, *bongov1.QueueEnvelope, string) {
	t.Helper()
	eval := mail.NewDMARCEvaluatorWithResolver(&dmarcMockResolver{
		txt: map[string][]string{
			"_dmarc.example.com": {"v=DMARC1; p=" + disposition},
		},
	})
	proc := NewBuiltinProcessor(antispam.NewScorer(), WithDMARC(eval, true))
	env := &bongov1.QueueEnvelope{
		Sender:     "spammer@attacker.net",
		Recipients: []string{"victim@example.com"},
		SpfResult:  "softfail",
	}
	msg := testMessage("someone@example.com", "Hello", "Just a note.", nil)
	return proc, env, msg
}

// TestCompositeDMARCEscalationSurvivesExternalPass is the regression test for
// the severity-aware merge: a same-domain spoof that the builtin escalates to
// "reject" via DMARC must not be overturned by an external backend that saw
// nothing suspicious and returned "pass" at a HIGHER score than the builtin's
// own (the builtin's heuristic-only score is not near-zero here: SPF
// softfail/DKIM-missing/mismatch heuristics contribute their own baseline on
// top of the DMARC escalation). Before the severity-aware merge, composite.go's
// score-only comparison (external.Score >= builtin.Score) would have let this
// external "pass" win purely because its score was set higher, discarding the
// reject action and routing a spoofed message into the inbox instead of holding
// it out. The external's score is deliberately set above the builtin's actual
// score (computed from a direct call below) so this test would have failed
// under the old score-only rule and is not vacuous under the new one.
func TestCompositeDMARCEscalationSurvivesExternalPass(t *testing.T) {
	builtin, env, msg := dmarcEscalationFixture(t, "reject")

	// Prove the builtin alone actually escalates, so this test cannot pass
	// vacuously if applyDMARC regresses. Also gives us the builtin's real
	// score to set the external's score deliberately above.
	direct, err := builtin.Process(context.Background(), &bongov1.QueueEnvelope{
		Sender:     env.Sender,
		Recipients: env.Recipients,
		SpfResult:  env.SpfResult,
	}, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("builtin.Process: %v", err)
	}
	if direct.Action != "reject" {
		t.Fatalf("fixture invalid: builtin alone did not escalate to reject (got %q, score=%.3f) - test would be vacuous", direct.Action, direct.Score)
	}

	external := &mockProcessor{
		name:   "rspamd",
		result: activeResult("rspamd", "pass", direct.Score+0.2),
	}

	c := NewCompositeProcessor(builtin, []processor{external}, slog.Default())
	result, err := c.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "reject" {
		t.Errorf("action = %q, want %q (builtin's DMARC-escalated verdict must win over a lower-severity external pass)", result.Action, "reject")
	}
	if result.Backend != "builtin" {
		t.Errorf("backend = %q, want %q", result.Backend, "builtin")
	}
	// reject/quarantine hold the message out via runner.go without using
	// TargetMailbox; it must not have been left at "Junk" by the losing
	// external's own write or by the composite's mailbox bookkeeping.
	if env.TargetMailbox != "" {
		t.Errorf("TargetMailbox = %q, want empty (reject does not route via Junk)", env.TargetMailbox)
	}
}

// TestCompositeDMARCResultSurvivesExternalWin verifies that when an external
// backend legitimately wins (same severity tier, higher score), the builtin's
// DmarcResult verdict is still carried onto the winning StageResult, so
// Authentication-Results reporting does not silently lose the dmarc= token.
func TestCompositeDMARCResultSurvivesExternalWin(t *testing.T) {
	builtin, env, msg := dmarcEscalationFixture(t, "quarantine")

	direct, err := builtin.Process(context.Background(), &bongov1.QueueEnvelope{
		Sender:     env.Sender,
		Recipients: env.Recipients,
		SpfResult:  env.SpfResult,
	}, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("builtin.Process: %v", err)
	}
	if direct.Action != "tag" || direct.DmarcResult != "fail" {
		t.Fatalf("fixture invalid: want builtin action=tag dmarcResult=fail, got action=%q dmarcResult=%q - test would be vacuous", direct.Action, direct.DmarcResult)
	}

	// Same severity tier ("tag") as the builtin, but a higher score, so the
	// tie-break rule picks the external result.
	external := &mockProcessor{
		name:   "rspamd",
		result: activeResult("rspamd", "tag", 0.9),
	}

	c := NewCompositeProcessor(builtin, []processor{external}, slog.Default())
	result, err := c.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Backend != "rspamd" {
		t.Fatalf("fixture invalid: expected external to win this tie-break, got backend %q", result.Backend)
	}
	if result.DmarcResult != "fail" {
		t.Errorf("DmarcResult = %q, want %q (must carry across from the builtin, which is the only processor that evaluates DMARC)", result.DmarcResult, "fail")
	}
	if env.TargetMailbox != "Junk" {
		t.Errorf("TargetMailbox = %q, want %q", env.TargetMailbox, "Junk")
	}
}

// spyProcessor records whether Process was invoked, used to prove (not just
// assert) that external backends are or are not consulted.
type spyProcessor struct {
	name   string
	result *bongov1.StageResult
	called atomic.Bool
}

func (s *spyProcessor) Name() string { return s.name }
func (s *spyProcessor) Process(_ context.Context, _ *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	s.called.Store(true)
	return s.result, nil
}

// TestCompositeConsultsExternalsRegardlessOfTrust is the scope-guard test for
// the composite, and the mirror of the one in builtin_test.go.
//
// Trust (authenticated submission or a trusted network) exempts a message from
// ORIGIN AUTHENTICATION interpretation (SPF, DKIM alignment, DMARC) and from
// nothing else. It must never become a content-filtering bypass: a "local
// origin" flag that skipped scoring was rejected for precisely this reason, and
// builtin.go's boundary comment says not to widen the skip beyond those three
// signals.
//
// An earlier revision of the composite DID skip externals for trusted mail,
// justified by the claim that external backends "run their own SPF/DKIM/DMARC
// checks against the connecting IP". That claim is false in this codebase:
// rspamd.go POSTs only the raw message to /checkv2 and spamassassin.go sends
// only the message over SPAMC — neither is given the connecting IP, HELO,
// envelope sender or recipients, so neither can evaluate origin at all. The
// skip therefore removed genuine content filtering and protected against
// nothing. This test exists so it cannot come back.
func TestCompositeConsultsExternalsRegardlessOfTrust(t *testing.T) {
	for _, trusted := range []bool{true, false} {
		t.Run(fmt.Sprintf("trusted=%v", trusted), func(t *testing.T) {
			external := &spyProcessor{name: "rspamd", result: activeResult("rspamd", "pass", 0.0)}
			builtin := newTestBuiltin(0.95, 0.99)

			c := NewCompositeProcessor(builtin, []processor{external}, slog.Default())
			env := &bongov1.QueueEnvelope{Sender: "user@example.com", Trusted: trusted}
			msg := strings.NewReader(testMessage("user@example.com", "Hello", "Body", nil))

			if _, err := c.Process(context.Background(), env, msg); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !external.called.Load() {
				t.Errorf("external processor was not consulted for Trusted=%v; trust must never skip content filtering", trusted)
			}
		})
	}
}

// TestCompositeTrustedSpamStillCaughtByExternal is the consequence of the
// above stated as behaviour rather than as a call-count: a trusted message
// that an external backend judges to be spam is still tagged. If the trusted
// skip ever returns, this fails with action "pass" — an authenticated account
// sending spam (a compromised account is the obvious case) would sail through.
func TestCompositeTrustedSpamStillCaughtByExternal(t *testing.T) {
	external := &spyProcessor{name: "rspamd", result: activeResult("rspamd", "tag", 0.9)}
	builtin := newTestBuiltin(0.95, 0.99)

	c := NewCompositeProcessor(builtin, []processor{external}, slog.Default())
	env := &bongov1.QueueEnvelope{Sender: "user@example.com", Trusted: true}
	msg := strings.NewReader(testMessage("user@example.com", "Hello", "Body", nil))

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "tag" {
		t.Errorf("Action = %q, want tag — an external spam verdict must still apply to trusted mail", result.Action)
	}
	if result.Backend != "rspamd" {
		t.Errorf("Backend = %q, want rspamd", result.Backend)
	}
}

// TestCompositeRspamdHTTPErrorFallsThroughToNextExternal is a regression test.
// A non-200 response from rspamd (e.g. 403 password-required, or a 5xx) must be
// treated as "unavailable" by isUnavailable() so the fallback chain tries the
// next external instead of stopping on rspamd's hardcoded "pass" result.
// Composed with the severity-aware merge, a stuck-on-rspamd "pass" would
// silently prevent SpamAssassin's real quarantine verdict from ever being
// considered, because the loop breaks as soon as it sees ANY result that
// isUnavailable does not recognise as unavailable — regardless of that result's
// own action. This test used the literal pre-fix reason string ("rspamd
// returned HTTP %d", no "unavailable") to confirm it fails without the fix,
// before restoring the real code.
func TestCompositeRspamdHTTPErrorFallsThroughToNextExternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	rspamdProc := NewRspamdProcessor(WithRspamdURL(srv.URL))
	sa := &mockProcessor{name: "spamassassin", result: activeResult("spamassassin", "quarantine", 1.0)}
	// High thresholds so the builtin alone would say "pass": any non-pass
	// result in the final verdict must have come from spamassassin.
	builtin := newTestBuiltin(0.95, 0.99)

	c := NewCompositeProcessor(builtin, []processor{rspamdProc, sa}, slog.Default())
	env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
	msg := strings.NewReader(testMessage("user@example.com", "Hello", "Body", nil))

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Backend != "spamassassin" {
		t.Errorf("backend = %q, want %q (rspamd's 403 must be skipped as unavailable so spamassassin is consulted)", result.Backend, "spamassassin")
	}
	if result.Action != "quarantine" {
		t.Errorf("action = %q, want %q", result.Action, "quarantine")
	}
}

// TestNewCompositeProcessorFor verifies the exported seam used by callers
// (internal/agent) that cannot name the unexported `processor` interface:
// it must behave identically to NewCompositeProcessor.
func TestNewCompositeProcessorFor(t *testing.T) {
	external := &mockProcessor{name: "rspamd", result: activeResult("rspamd", "tag", 0.75)}
	builtin := newTestBuiltin(0.95, 0.99)

	c := NewCompositeProcessorFor(builtin, slog.Default(), external)
	env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
	msg := strings.NewReader(testMessage("user@example.com", "Hello", "Body", nil))

	result, err := c.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Backend != "rspamd" || result.Action != "tag" {
		t.Errorf("result = %+v, want backend=rspamd action=tag", result)
	}
}

// hasReason reports whether reasons contains want exactly. Reason strings are
// merged verbatim by mergeReasons, so an exact match is the right assertion:
// a substring check would pass even if a future change started prefixing
// reasons with their backend, which is precisely what the DNSBL requirement
// ("the header names the zone that listed it") forbids.
func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

// mergedReasonsFixture builds a builtin wired to a fake DNSBL that lists zone,
// giving it exactly one predictable reason ("dnsbl_"+zone) to contribute to a
// merge. weight controls whether the resulting score clears the 0.5 threshold,
// which is how each caller chooses who wins the composite comparison.
//
// It returns the builtin, an envelope and the message, plus the builtin's own
// direct verdict so callers can assert the fixture is not vacuous before
// relying on it.
func mergedReasonsFixture(t *testing.T, zone string, weight float64) (*BuiltinProcessor, *bongov1.QueueEnvelope, string, *bongov1.StageResult) {
	t.Helper()
	proc := newDNSBLProcessor(t, newFakeDNSBLResolver(zone), []string{zone}, map[string]float64{zone: weight})

	direct, err := proc.Process(context.Background(), cleanEnv("198.51.100.7", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("builtin.Process: %v", err)
	}
	if !hasReason(direct.GetReasons(), "dnsbl_"+zone) {
		t.Fatalf("fixture invalid: builtin produced reasons %v, want one naming %q - a merge test needs the builtin to actually contribute a reason", direct.GetReasons(), "dnsbl_"+zone)
	}
	return proc, cleanEnv("198.51.100.7", false), cleanMessage, direct
}

// TestCompositeMergesBuiltinReasonsWhenExternalWins is the primary regression
// test: on a deployment where rspamd outranks the builtin, the builtin's DNSBL
// evidence must still reach X-Spam-Reasons. Before the evidence merge the
// winning external StageResult replaced the builtin's wholesale, so the zone
// that listed the sender was silently dropped and "why was this junked" had no
// answer on exactly the two-scanner deployments that gather the most evidence.
func TestCompositeMergesBuiltinReasonsWhenExternalWins(t *testing.T) {
	const zone = "zen.example-bl.test"
	builtin, env, msg, direct := mergedReasonsFixture(t, zone, 0.3)

	if actionSeverity(direct.Action) >= actionSeverity("tag") {
		t.Fatalf("fixture invalid: builtin action = %q, want a lower severity than the external's \"tag\" so the external wins", direct.Action)
	}

	// The external wins on severity alone while scoring BELOW the builtin.
	// That ordering is load-bearing: it is what makes the Score assertion
	// below able to observe a merge that combined scores instead of keeping
	// the winner's. With the external scoring higher (the intuitive fixture)
	// a max-of-both merge is indistinguishable from no merge at all.
	const externalScore = 0.25
	if direct.Score <= externalScore {
		t.Fatalf("fixture invalid: builtin score %v must exceed the external's %v, or the score assertion cannot fail", direct.Score, externalScore)
	}
	external := &mockProcessor{name: "rspamd", result: activeResult("rspamd", "tag", externalScore)}
	c := NewCompositeProcessor(builtin, []processor{external}, slog.Default())

	result, err := c.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Backend != "rspamd" {
		t.Fatalf("fixture invalid: expected the external to win, got backend %q", result.Backend)
	}
	if !hasReason(result.GetReasons(), "dnsbl_"+zone) {
		t.Errorf("reasons = %v, want one naming the listing zone %q (the builtin's evidence must survive an external win)", result.GetReasons(), "dnsbl_"+zone)
	}
	if !hasReason(result.GetReasons(), "TEST_RULE") {
		t.Errorf("reasons = %v, want the winning external's own reason %q to be retained", result.GetReasons(), "TEST_RULE")
	}
	// The merge moves evidence, not judgements.
	if result.Score != externalScore {
		t.Errorf("Score = %v, want the winner's %v unchanged - merging must not combine scores, and the losing builtin's higher %v must not leak in", result.Score, externalScore, direct.Score)
	}
	if result.Action != "tag" {
		t.Errorf("Action = %q, want the winner's %q unchanged", result.Action, "tag")
	}
}

// TestCompositeMergesExternalReasonsWhenBuiltinWins is the mirror direction:
// when the builtin outranks the external, the external's symbols must not be
// discarded either. The gap was symmetric even though only one half of it was
// reported.
func TestCompositeMergesExternalReasonsWhenBuiltinWins(t *testing.T) {
	const zone = "sbl.example-bl.test"
	// Weight 0.5 puts the builtin on "tag" at a score an external can still
	// exceed. A weight that clamps the builtin to 1.0 would make the Score
	// assertion below unfalsifiable, since no external score could ever be
	// higher for a merge to wrongly adopt.
	builtin, env, msg, direct := mergedReasonsFixture(t, zone, 0.5)

	if actionSeverity(direct.Action) <= actionSeverity("pass") {
		t.Fatalf("fixture invalid: builtin action = %q, want a higher severity than the external's \"pass\" so the builtin wins", direct.Action)
	}

	// Higher score than the builtin's, to prove the builtin wins on severity
	// rather than on score, and that the merge still runs in that direction.
	const externalScore = 0.99
	if direct.Score >= externalScore {
		t.Fatalf("fixture invalid: builtin score %v must be below the external's %v, or the score assertion cannot fail", direct.Score, externalScore)
	}
	external := &mockProcessor{name: "rspamd", result: activeResult("rspamd", "pass", externalScore)}
	c := NewCompositeProcessor(builtin, []processor{external}, slog.Default())

	result, err := c.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Backend != "builtin" {
		t.Fatalf("fixture invalid: expected the builtin to win, got backend %q", result.Backend)
	}
	if !hasReason(result.GetReasons(), "TEST_RULE") {
		t.Errorf("reasons = %v, want the losing external's reason %q to be carried across", result.GetReasons(), "TEST_RULE")
	}
	if !hasReason(result.GetReasons(), "dnsbl_"+zone) {
		t.Errorf("reasons = %v, want the winner's own reason naming zone %q to be retained", result.GetReasons(), "dnsbl_"+zone)
	}
	if result.Score != direct.Score {
		t.Errorf("Score = %v, want the winning builtin's %v unchanged - the external's higher %v must not leak in", result.Score, direct.Score, externalScore)
	}
}

// TestCompositeMergesReasonsWhenSpamAssassinWins covers the second backend in
// the fallback chain: reason merging must not be specific to rspamd, which is
// the only external most tests exercise. Rspamd reports itself unavailable so
// SpamAssassin is the external that reaches the comparison.
func TestCompositeMergesReasonsWhenSpamAssassinWins(t *testing.T) {
	const zone = "dbl.example-bl.test"
	builtin, env, msg, direct := mergedReasonsFixture(t, zone, 0.3)

	if actionSeverity(direct.Action) >= actionSeverity("tag") {
		t.Fatalf("fixture invalid: builtin action = %q, want a lower severity than SpamAssassin's \"tag\"", direct.Action)
	}

	rspamd := &mockProcessor{name: "rspamd", result: unavailableResult("rspamd")}
	sa := &mockProcessor{name: "spamassassin", result: activeResult("spamassassin", "tag", 0.7)}
	c := NewCompositeProcessor(builtin, []processor{rspamd, sa}, slog.Default())

	result, err := c.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Backend != "spamassassin" {
		t.Fatalf("fixture invalid: expected SpamAssassin to win, got backend %q", result.Backend)
	}
	if !hasReason(result.GetReasons(), "dnsbl_"+zone) {
		t.Errorf("reasons = %v, want one naming the listing zone %q", result.GetReasons(), "dnsbl_"+zone)
	}
	// The skipped backend's "unavailable" marker must never be merged in: it
	// would be read back as a real finding and, worse, would make a later
	// isUnavailable check on this result true.
	if hasReason(result.GetReasons(), "rspamd unavailable") {
		t.Errorf("reasons = %v, must not include the skipped backend's unavailable marker", result.GetReasons())
	}
}

// TestCompositeMergedReasonsDeduplicated proves the merge does not double up
// findings both backends independently reported. Duplicates would reach the
// recipient as a repeated X-Spam-Reasons entry.
func TestCompositeMergedReasonsDeduplicated(t *testing.T) {
	const zone = "dedup.example-bl.test"
	builtin, env, msg, _ := mergedReasonsFixture(t, zone, 0.3)

	// The external reports the SAME finding the builtin does.
	shared := "dnsbl_" + zone
	external := &mockProcessor{name: "rspamd", result: &bongov1.StageResult{
		Stage:   "antispam",
		Backend: "rspamd",
		Action:  "tag",
		Score:   0.6,
		Reasons: []string{shared, "TEST_RULE"},
	}}
	c := NewCompositeProcessor(builtin, []processor{external}, slog.Default())

	result, err := c.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	count := 0
	for _, r := range result.GetReasons() {
		if r == shared {
			count++
		}
	}
	if count != 1 {
		t.Errorf("reason %q appears %d times in %v, want exactly 1", shared, count, result.GetReasons())
	}
}

// TestCompositeDMARCReasonSurvivesExternalWin complements
// TestCompositeDMARCResultSurvivesExternalWin, which only ever asserted the
// DmarcResult field. The human-readable "dmarc_<disposition>" reason lives in
// Reasons and was dropped by the same replacement, so an operator reading
// headers could see dmarc=fail in Authentication-Results with nothing in
// X-Spam-Reasons explaining that DMARC is what escalated the message.
func TestCompositeDMARCReasonSurvivesExternalWin(t *testing.T) {
	builtin, env, msg := dmarcEscalationFixture(t, "quarantine")

	direct, err := builtin.Process(context.Background(), &bongov1.QueueEnvelope{
		Sender:     env.Sender,
		Recipients: env.Recipients,
		SpfResult:  env.SpfResult,
	}, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("builtin.Process: %v", err)
	}
	if !hasReason(direct.GetReasons(), "dmarc_quarantine") {
		t.Fatalf("fixture invalid: builtin reasons %v, want %q - test would be vacuous", direct.GetReasons(), "dmarc_quarantine")
	}

	external := &mockProcessor{name: "rspamd", result: activeResult("rspamd", "tag", 0.9)}
	c := NewCompositeProcessor(builtin, []processor{external}, slog.Default())

	result, err := c.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Backend != "rspamd" {
		t.Fatalf("fixture invalid: expected the external to win this tie-break, got backend %q", result.Backend)
	}
	if !hasReason(result.GetReasons(), "dmarc_quarantine") {
		t.Errorf("reasons = %v, want %q carried across from the builtin", result.GetReasons(), "dmarc_quarantine")
	}
}

// TestMergeReasons unit-tests the helper directly, covering the cases the
// Process-level tests cannot easily stage: empty inputs, and the guarantee
// that neither input slice is mutated.
func TestMergeReasons(t *testing.T) {
	tests := []struct {
		name      string
		primary   []string
		secondary []string
		want      []string
	}{
		{"both empty", nil, nil, nil},
		{"secondary empty returns primary", []string{"a"}, nil, []string{"a"}},
		{"primary empty takes secondary", nil, []string{"b"}, []string{"b"}},
		{"disjoint, primary first", []string{"a"}, []string{"b"}, []string{"a", "b"}},
		{"overlap deduped", []string{"a", "b"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{"duplicates within primary deduped", []string{"a", "a"}, []string{"b"}, []string{"a", "b"}},
		{"identical inputs collapse", []string{"a", "b"}, []string{"a", "b"}, []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primaryCopy := append([]string(nil), tt.primary...)
			secondaryCopy := append([]string(nil), tt.secondary...)

			got := mergeReasons(tt.primary, tt.secondary)
			if len(got) != len(tt.want) {
				t.Fatalf("mergeReasons(%v, %v) = %v, want %v", tt.primary, tt.secondary, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("mergeReasons(%v, %v) = %v, want %v", tt.primary, tt.secondary, got, tt.want)
				}
			}

			// Merging must not disturb either input: composite.go assigns the
			// result onto the winner while the loser is still live.
			for i := range primaryCopy {
				if tt.primary[i] != primaryCopy[i] {
					t.Errorf("primary was mutated: %v, want %v", tt.primary, primaryCopy)
					break
				}
			}
			for i := range secondaryCopy {
				if tt.secondary[i] != secondaryCopy[i] {
					t.Errorf("secondary was mutated: %v, want %v", tt.secondary, secondaryCopy)
					break
				}
			}
		})
	}
}

// TestMergeReasonsIdempotent proves re-merging an already-merged result is a
// no-op. CompositeProcessor assigns the merged slice back onto the winning
// StageResult, so a processor that returns the same result value on repeated
// calls (or any future re-entry into the merge) must not accumulate.
func TestMergeReasonsIdempotent(t *testing.T) {
	once := mergeReasons([]string{"a", "b"}, []string{"c"})
	twice := mergeReasons(once, []string{"c"})
	if len(twice) != len(once) {
		t.Fatalf("re-merge changed the result: %v then %v", once, twice)
	}
	for i := range once {
		if once[i] != twice[i] {
			t.Fatalf("re-merge changed the result: %v then %v", once, twice)
		}
	}
}
