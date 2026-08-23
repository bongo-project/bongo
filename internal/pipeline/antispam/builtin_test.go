package antispam

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/mail"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// testMessage builds a minimal RFC 5322 message for testing.
func testMessage(from, subject, body string, extraHeaders map[string]string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: recipient@example.com\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("Message-ID: <test@example.com>\r\n")
	sb.WriteString("Date: Thu, 01 Jan 2026 00:00:00 +0000\r\n")
	for k, v := range extraHeaders {
		sb.WriteString(k + ": " + v + "\r\n")
	}
	sb.WriteString("Content-Type: text/plain\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}

func TestBuiltinPass(t *testing.T) {
	scorer := antispam.NewScorer()
	proc := NewBuiltinProcessor(scorer)

	// A clean, well-formed message should score below the default threshold.
	msg := testMessage("sender@example.com", "Hello from a friend", "Just wanted to say hi.", nil)
	env := &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		SpfResult:  "pass",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action %q, got %q (score=%.3f, reasons=%v)", "pass", result.Action, result.Score, result.Reasons)
	}
	if result.Stage != "antispam" {
		t.Errorf("expected stage %q, got %q", "antispam", result.Stage)
	}
	if result.Backend != "builtin" {
		t.Errorf("expected backend %q, got %q", "builtin", result.Backend)
	}
	// TargetMailbox must not have been modified.
	if env.TargetMailbox != "" {
		t.Errorf("expected empty TargetMailbox, got %q", env.TargetMailbox)
	}
}

func TestBuiltinTag(t *testing.T) {
	scorer := antispam.NewScorer()
	// Use a low threshold so a slightly suspicious message is tagged.
	proc := NewBuiltinProcessor(scorer,
		WithThreshold(0.1),
		WithHighThreshold(0.9),
	)

	// Missing SPF + empty subject accumulates just enough score.
	msg := testMessage("sender@example.com", "", "Buy now!", nil)
	env := &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		SpfResult:  "fail",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	if result.Action != "tag" {
		t.Errorf("expected action %q, got %q (score=%.3f, reasons=%v)", "tag", result.Action, result.Score, result.Reasons)
	}
	if env.TargetMailbox != "Junk" {
		t.Errorf("expected TargetMailbox %q, got %q", "Junk", env.TargetMailbox)
	}
}

func TestBuiltinQuarantine(t *testing.T) {
	scorer := antispam.NewScorer()
	// Use very low thresholds so even a mildly suspicious message is quarantined.
	proc := NewBuiltinProcessor(scorer,
		WithThreshold(0.05),
		WithHighThreshold(0.1),
	)

	// SPF fail + missing Date/Message-ID + empty subject drives a high score.
	msg := "From: spammer@evil.com\r\nSubject: \r\nContent-Type: text/plain\r\n\r\nBuy cheap meds now!"
	env := &bongov1.QueueEnvelope{
		Sender:     "spammer@evil.com",
		Recipients: []string{"victim@example.com"},
		SpfResult:  "fail",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	if result.Action != "quarantine" {
		t.Errorf("expected action %q, got %q (score=%.3f, reasons=%v)", "quarantine", result.Action, result.Score, result.Reasons)
	}
}

func TestBuiltinNilBayes(t *testing.T) {
	scorer := antispam.NewScorer()
	// Explicitly pass no WithBayes option — bayes should remain nil.
	proc := NewBuiltinProcessor(scorer)

	if proc.bayes != nil {
		t.Fatal("expected bayes to be nil by default")
	}

	msg := testMessage("sender@example.com", "Regular newsletter", "Here is your update.", nil)
	env := &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		SpfResult:  "pass",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	// Should succeed without panicking and return a valid stage name.
	if result.Stage != "antispam" {
		t.Errorf("expected stage %q, got %q", "antispam", result.Stage)
	}
}

func TestBuiltinCustomThresholds(t *testing.T) {
	tests := []struct {
		name          string
		threshold     float32
		highThreshold float32
		spfResult     string
		subject       string
		wantAction    string
		wantJunk      bool
	}{
		{
			name:          "custom low threshold causes tag",
			threshold:     0.1,
			highThreshold: 0.8,
			spfResult:     "softfail",
			subject:       "Hello",
			wantAction:    "tag",
			wantJunk:      true,
		},
		{
			name:          "high thresholds cause pass",
			threshold:     0.95,
			highThreshold: 0.99,
			spfResult:     "fail",
			subject:       "Free money winner congratulations act now",
			wantAction:    "pass",
			wantJunk:      false,
		},
		{
			name:          "very low high threshold causes quarantine",
			threshold:     0.05,
			highThreshold: 0.06,
			spfResult:     "fail",
			subject:       "Hi",
			wantAction:    "quarantine",
			wantJunk:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scorer := antispam.NewScorer()
			proc := NewBuiltinProcessor(scorer,
				WithThreshold(tc.threshold),
				WithHighThreshold(tc.highThreshold),
			)

			msg := testMessage("sender@example.com", tc.subject, "Body text.", nil)
			env := &bongov1.QueueEnvelope{
				Sender:     "sender@example.com",
				Recipients: []string{"recipient@example.com"},
				SpfResult:  tc.spfResult,
			}

			result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
			if err != nil {
				t.Fatalf("Process returned unexpected error: %v", err)
			}
			if result.Action != tc.wantAction {
				t.Errorf("expected action %q, got %q (score=%.3f, reasons=%v)",
					tc.wantAction, result.Action, result.Score, result.Reasons)
			}
			if tc.wantJunk && env.TargetMailbox != "Junk" {
				t.Errorf("expected TargetMailbox %q, got %q", "Junk", env.TargetMailbox)
			}
			if !tc.wantJunk && result.Action != "tag" && env.TargetMailbox == "Junk" {
				t.Errorf("expected TargetMailbox to not be Junk")
			}
		})
	}
}

func TestBuiltinName(t *testing.T) {
	proc := NewBuiltinProcessor(antispam.NewScorer())
	if proc.Name() != "antispam" {
		t.Errorf("expected Name() %q, got %q", "antispam", proc.Name())
	}
}

func TestBuiltinExtractBasicHeaders(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantSubject   string
		wantHeaderKey string
		wantHeaderVal string
	}{
		{
			name:          "simple headers",
			raw:           "Subject: Test subject\r\nFrom: foo@bar.com\r\n\r\nBody",
			wantSubject:   "Test subject",
			wantHeaderKey: "From",
			wantHeaderVal: "foo@bar.com",
		},
		{
			name:          "folded header",
			raw:           "Subject: Long\r\n folded subject\r\n\r\nBody",
			wantSubject:   "Long folded subject",
			wantHeaderKey: "Subject",
			wantHeaderVal: "Long folded subject",
		},
		{
			name:          "empty input",
			raw:           "\r\n\r\nBody only",
			wantSubject:   "",
			wantHeaderKey: "",
			wantHeaderVal: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject, headers := extractBasicHeaders([]byte(tc.raw))
			if subject != tc.wantSubject {
				t.Errorf("subject: want %q, got %q", tc.wantSubject, subject)
			}
			if tc.wantHeaderKey != "" {
				if got := headers[tc.wantHeaderKey]; got != tc.wantHeaderVal {
					t.Errorf("header[%q]: want %q, got %q", tc.wantHeaderKey, tc.wantHeaderVal, got)
				}
			}
		})
	}
}

// TestBuiltinProcessorTrustedSkipsOriginAuth is the paired test: the same
// message and envelope are run twice, once Trusted=false and once
// Trusted=true, through an identically configured processor (including a
// DMARC evaluator so the untrusted run has a real verdict to compare
// against). It asserts:
//   - untrusted: SPF/DKIM/DMARC are interpreted unchanged — "SPF softfail"
//     and "DKIM missing" are recorded, and DmarcResult is a real, non-empty
//     verdict. Any future change that breaks this half means the trusted skip
//     leaked into the untrusted path.
//   - trusted: none of "SPF fail", "SPF softfail", "DKIM fail",
//     "DKIM missing", or any "dmarc_"-prefixed reason is recorded, and
//     DmarcResult is "" (DMARC did not run, so it must not claim a verdict
//     it never evaluated).
//   - "envelope sender domain mismatch" (a header-consistency heuristic, not
//     an origin-authentication protocol) is recorded in BOTH runs: the trust
//     boundary names exactly which signals it skips, and this is not one of
//     them.
func TestBuiltinProcessorTrustedSkipsOriginAuth(t *testing.T) {
	eval := mail.NewDMARCEvaluatorWithResolver(&dmarcMockResolver{
		txt: map[string][]string{
			"_dmarc.example.com": {"v=DMARC1; p=reject"},
		},
	})

	// From a different domain than the envelope sender (attacker.net vs
	// example.com) so the envelope-sender-mismatch heuristic fires in both
	// runs, and unsigned so DKIM verification yields "none" -> "DKIM missing"
	// on the untrusted run.
	msg := testMessage("someone@example.com", "Hello", "Just a note.", nil)

	tests := []struct {
		name    string
		trusted bool
	}{
		{name: "untrusted: origin-auth checks apply unchanged", trusted: false},
		{name: "trusted: origin-auth checks are skipped", trusted: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proc := NewBuiltinProcessor(antispam.NewScorer(), WithDMARC(eval, true))
			env := &bongov1.QueueEnvelope{
				Sender:     "spammer@attacker.net",
				Recipients: []string{"victim@example.com"},
				SpfResult:  "softfail",
				Trusted:    tc.trusted,
			}

			result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}

			if tc.trusted {
				forbidden := map[string]bool{
					"SPF fail": true, "SPF softfail": true,
					"DKIM fail": true, "DKIM missing": true,
				}
				for _, r := range result.Reasons {
					if forbidden[r] || strings.HasPrefix(r, "dmarc_") {
						t.Errorf("trusted envelope: origin-auth reason %q present, want none (reasons=%v)", r, result.Reasons)
					}
				}
				if result.DmarcResult != "" {
					t.Errorf("trusted envelope: DmarcResult = %q, want \"\" (DMARC must not run)", result.DmarcResult)
				}
			} else {
				var sawSPF, sawDKIM bool
				for _, r := range result.Reasons {
					if r == "SPF softfail" {
						sawSPF = true
					}
					if r == "DKIM missing" {
						sawDKIM = true
					}
				}
				if !sawSPF {
					t.Errorf("untrusted envelope: reasons %v missing %q (AC4 regression: untrusted path must be byte-identical to pre-task behaviour)", result.Reasons, "SPF softfail")
				}
				if !sawDKIM {
					t.Errorf("untrusted envelope: reasons %v missing %q", result.Reasons, "DKIM missing")
				}
				if result.DmarcResult == "" {
					t.Errorf("untrusted envelope: DmarcResult is empty, want a real verdict")
				}
			}

			var sawMismatch bool
			for _, r := range result.Reasons {
				if r == "envelope sender domain mismatch" {
					sawMismatch = true
				}
			}
			if !sawMismatch {
				t.Errorf("reasons %v missing %q: this heuristic is not an origin-authentication signal and must fire regardless of Trusted", result.Reasons, "envelope sender domain mismatch")
			}
		})
	}
}

// fakeBayesStore is a map-backed fake satisfying both antispam.PebbleGetter
// and antispam.RaftApplier, letting tests drive antispam.BayesFilter without
// a real Pebble store or Raft cluster.
type fakeBayesStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeBayesStore() *fakeBayesStore {
	return &fakeBayesStore{data: make(map[string][]byte)}
}

func (f *fakeBayesStore) Get(key []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data[string(key)], nil
}

func (f *fakeBayesStore) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		f.data[string(cmd.Key)] = cmd.Value
	case bongov1.Op_OP_DELETE:
		delete(f.data, string(cmd.Key))
	}
	return nil
}

// identityResolver resolves a recipient email to itself, used so
// BayesFilter's per-user keying can operate on the email address directly in
// tests without a real recipient-resolution store.
type identityResolver struct{}

func (identityResolver) ResolveRecipient(email string) (string, error) {
	return email, nil
}

// TestBuiltinProcessorTrustedStillScoresContent is the scope-guard test: this
// is the load-bearing distinction the whole trust boundary rests on. A general
// "local origin" flag that skipped scoring outright was explicitly REJECTED,
// precisely because inbound internet mail enters through the same
// EnqueueLocal path and such a flag would sit one careless caller away from a
// full antispam bypass. Trusted must skip ORIGIN AUTHENTICATION (SPF/DKIM/
// DMARC) only — never the phrase scorer, never the Bayesian blend, never the
// threshold routing. If a future change makes this test start failing because
// scoring stopped running for trusted mail, that is the regression this test
// exists to catch: a trusted envelope whose CONTENT is spammy must still be
// tagged and routed to Junk.
func TestBuiltinProcessorTrustedStillScoresContent(t *testing.T) {
	// Spam-phrase-laden subject/body plus missing Message-ID/Date headers
	// drive the heuristic score above threshold on content alone: the
	// scorer's own phrase-matching is deliberately capped below the default
	// 0.5 Junk threshold (maxPhraseScore=0.40 in scorer.go) so that phrase
	// hits never route mail to Junk unassisted — a corroborating
	// header-hygiene signal is required, exactly as for any other message.
	// This is not an origin-authentication signal, so it is not skipped for
	// a trusted envelope. No testMessage() helper here: it stamps
	// Message-ID/Date, which would remove that corroborating signal.
	msg := "From: registrations@example.test\r\n" +
		"To: lance@example.test\r\n" +
		"Subject: FREE MONEY WINNER ACT NOW\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Buy cheap meds now, guaranteed! Click here to claim your prize!"

	t.Run("heuristic scorer alone", func(t *testing.T) {
		proc := NewBuiltinProcessor(antispam.NewScorer(), WithThreshold(0.5), WithHighThreshold(0.9))
		env := &bongov1.QueueEnvelope{
			Sender:     "registrations@example.test",
			Recipients: []string{"lance@example.test"},
			SpfResult:  "pass",
			Trusted:    true,
		}

		result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if result.Action != "tag" && result.Action != "quarantine" {
			t.Errorf("trusted+spammy content: Action = %q, want tag or quarantine (content scoring must still run; score=%.3f, reasons=%v)",
				result.Action, result.Score, result.Reasons)
		}
		if env.TargetMailbox != "Junk" {
			t.Errorf("trusted+spammy content: TargetMailbox = %q, want Junk", env.TargetMailbox)
		}
	})

	// Bayesian-blend sub-check. This deliberately does NOT reuse the spammy
	// message above: that message already crosses the threshold on heuristics
	// alone, so asserting "trusted+bayes still tags it" would pass even if the
	// Bayes blend were skipped entirely for trusted mail — vacuous as a guard.
	//
	// Instead it uses a message the heuristic scorer rates BELOW threshold
	// (0.250 — header hygiene only, action "pass"), and trains Bayes on that
	// message's own tokens as spam. The blend at builtin.go is
	// 0.6*heuristic + 0.4*bayes, so a strong Bayes verdict is what carries it
	// over 0.5. The paired negative control in the first subtest below proves
	// the heuristic half really is under threshold, so if a future change
	// stopped running the Bayes blend for trusted envelopes, the second
	// subtest sees "pass" and fails. That is the regression this guards.
	t.Run("bayes blend still applies", func(t *testing.T) {
		const userID = "user@example.test"
		const subject = "Quarterly figures"
		const body = "Please review the attached quarterly figures before Friday."
		bayesMsg := "From: reports@example.test\r\n" +
			"To: " + userID + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"Content-Type: text/plain\r\n" +
			"\r\n" + body

		newEnv := func() *bongov1.QueueEnvelope {
			return &bongov1.QueueEnvelope{
				Sender:     "reports@example.test",
				Recipients: []string{userID},
				SpfResult:  "pass",
				Trusted:    true,
			}
		}

		// Negative control: without Bayes this content is under threshold.
		// If this ever starts returning tag/quarantine the positive case
		// below has stopped proving anything, and must be re-tuned.
		plainProc := NewBuiltinProcessor(antispam.NewScorer(), WithThreshold(0.5), WithHighThreshold(0.9))
		controlEnv := newEnv()
		controlResult, err := plainProc.Process(context.Background(), controlEnv, strings.NewReader(bayesMsg))
		if err != nil {
			t.Fatalf("Process (control): %v", err)
		}
		if controlResult.Action != "pass" {
			t.Fatalf("negative control: Action = %q, want pass — this content must be BELOW threshold on heuristics alone, otherwise the bayes assertion below is vacuous (score=%.3f, reasons=%v)",
				controlResult.Action, controlResult.Score, controlResult.Reasons)
		}
		if controlEnv.TargetMailbox != "" {
			t.Fatalf("negative control: TargetMailbox = %q, want empty", controlEnv.TargetMailbox)
		}

		// Now train Bayes on this exact content as spam and re-run: only the
		// blend can push it over the threshold.
		store := newFakeBayesStore()
		bayes := antispam.NewBayesFilter(store, store)
		spamTokens := antispam.Tokenize(subject, body)
		for range 20 {
			if err := bayes.Train(userID, spamTokens, true); err != nil {
				t.Fatalf("Train: %v", err)
			}
			if err := bayes.IncrementTrained(userID); err != nil {
				t.Fatalf("IncrementTrained: %v", err)
			}
		}
		if got := bayes.TrainedCount(userID); got < 20 {
			t.Fatalf("TrainedCount = %d, want >= 20 (the blend gate must be crossed)", got)
		}

		bayesProc := NewBuiltinProcessor(antispam.NewScorer(),
			WithBayes(bayes),
			WithThreshold(0.5),
			WithHighThreshold(0.9),
		)
		bayesProc.SetResolver(identityResolver{})

		env := newEnv()
		result, err := bayesProc.Process(context.Background(), env, strings.NewReader(bayesMsg))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if result.Action != "tag" && result.Action != "quarantine" {
			t.Errorf("trusted envelope: Action = %q, want tag or quarantine — the Bayes blend must still run for trusted mail (score=%.3f, control was %.3f, reasons=%v)",
				result.Action, result.Score, controlResult.Score, result.Reasons)
		}
		if env.TargetMailbox != "Junk" {
			t.Errorf("trusted envelope: TargetMailbox = %q, want Junk", env.TargetMailbox)
		}
		if result.Score <= controlResult.Score {
			t.Errorf("blended score %.3f did not exceed heuristic-only score %.3f — the blend did not contribute", result.Score, controlResult.Score)
		}
	})
}
