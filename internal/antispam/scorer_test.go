package antispam

import (
	"testing"
)

func TestScorer_CleanMessage(t *testing.T) {
	s := NewScorer()
	result := s.Score(&ScoringInput{
		SPFResult:  "pass",
		DKIMResult: "pass",
		Headers: map[string]string{
			"Message-Id": "<test@example.com>",
			"Date":       "Mon, 01 Jan 2024 00:00:00 +0000",
			"From":       "user@example.com",
		},
		Subject:        "Hello World",
		EnvelopeSender: "user@example.com",
	})

	if result.Score >= 0.2 {
		t.Errorf("clean message score = %.2f (reasons: %v), want < 0.2", result.Score, result.Reasons)
	}
}

func TestScorer_SuspiciousMessage(t *testing.T) {
	s := NewScorer()
	result := s.Score(&ScoringInput{
		SPFResult:  "fail",
		DKIMResult: "none",
		Headers: map[string]string{
			"From": "spammer@evil.com",
		},
		Subject:        "",
		EnvelopeSender: "bounce@different.com",
	})

	if result.Score <= 0.5 {
		t.Errorf("suspicious message score = %.2f (reasons: %v), want > 0.5", result.Score, result.Reasons)
	}
}

func TestScorer_SpamPhrases(t *testing.T) {
	s := NewScorer()
	result := s.Score(&ScoringInput{
		SPFResult:  "pass",
		DKIMResult: "pass",
		Headers: map[string]string{
			"Message-Id": "<test@example.com>",
			"Date":       "Mon, 01 Jan 2024 00:00:00 +0000",
			"From":       "user@example.com",
		},
		Subject:        "Click here for free money and act now",
		EnvelopeSender: "user@example.com",
	})

	if result.Score < 0.2 {
		t.Errorf("spam-phrase message score = %.2f, want >= 0.2", result.Score)
	}
}

func TestScorer_BodyPhrases(t *testing.T) {
	s := NewScorer()
	result := s.Score(&ScoringInput{
		SPFResult:  "pass",
		DKIMResult: "pass",
		Headers: map[string]string{
			"Message-Id": "<test@example.com>",
			"Date":       "Mon, 01 Jan 2024 00:00:00 +0000",
			"From":       "user@example.com",
		},
		Subject:        "Hi there",
		BodySample:     "Act now! This limited time offer lets you make money fast from home.",
		EnvelopeSender: "user@example.com",
	})

	// Body phrases contribute, but capped below the Junk threshold on their own.
	if result.Score <= 0 {
		t.Errorf("body-phrase message score = %.2f (reasons: %v), want > 0", result.Score, result.Reasons)
	}
	if result.Score >= DefaultSpamThreshold {
		t.Errorf("body-phrase-only score = %.2f, want < %.2f (phrases alone must not junk mail)", result.Score, DefaultSpamThreshold)
	}
}

func TestScorer_LegitimateNewsletter(t *testing.T) {
	s := NewScorer()
	// A well-formed newsletter that authenticates and contains words previously
	// on the phrase list (unsubscribe, congratulations, winner) must not be
	// flagged as spam.
	result := s.Score(&ScoringInput{
		SPFResult:  "pass",
		DKIMResult: "pass",
		Headers: map[string]string{
			"Message-Id": "<news@list.example.com>",
			"Date":       "Mon, 01 Jan 2024 00:00:00 +0000",
			"From":       "news@list.example.com",
		},
		Subject:        "Congratulations to this month's community winner",
		BodySample:     "Thanks for reading. To unsubscribe, click the link at the bottom of this email.",
		EnvelopeSender: "news@list.example.com",
	})

	if result.Score >= DefaultSpamThreshold {
		t.Errorf("legitimate newsletter score = %.2f (reasons: %v), want < %.2f", result.Score, result.Reasons, DefaultSpamThreshold)
	}
}

func TestScorer_CappedAt1(t *testing.T) {
	s := NewScorer()
	// Trigger every check.
	result := s.Score(&ScoringInput{
		SPFResult:      "fail",
		DKIMResult:     "fail",
		Headers:        map[string]string{},
		Subject:        "Click here for free money act now limited time winner",
		EnvelopeSender: "bounce@evil.com",
	})

	if result.Score > 1.0 {
		t.Errorf("score = %.2f, want <= 1.0", result.Score)
	}
}

func TestScorer_EmptyInput(t *testing.T) {
	s := NewScorer()
	result := s.Score(&ScoringInput{})

	// Empty input should still produce a valid score.
	if result.Score < 0 || result.Score > 1.0 {
		t.Errorf("score = %.2f, want 0.0-1.0", result.Score)
	}
}

// TestScorer_EmptyEnvelopeSenderNoMismatchReason is a characterisation test
// for task 10.4 (FR-3): DSNs are now delivered with a null envelope sender
// (EnvelopeSender ""). The mismatch check is already guarded on
// EnvelopeSender != "", so an empty envelope sender must never add the
// "envelope sender domain mismatch" reason, even when the From header domain
// differs from (i.e. is unrelated to) any envelope domain.
func TestScorer_EmptyEnvelopeSenderNoMismatchReason(t *testing.T) {
	s := NewScorer()
	result := s.Score(&ScoringInput{
		SPFResult:  "pass",
		DKIMResult: "pass",
		Headers: map[string]string{
			"Message-Id": "<bounce@example.test>",
			"Date":       "Mon, 01 Jan 2024 00:00:00 +0000",
			"From":       "MAILER-DAEMON <postmaster@example.test>",
		},
		Subject:        "Undelivered Mail Returned to Sender",
		EnvelopeSender: "",
	})

	for _, reason := range result.Reasons {
		if reason == "envelope sender domain mismatch" {
			t.Errorf("got reason %q with an empty EnvelopeSender, want it never added; reasons: %v", reason, result.Reasons)
		}
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user@example.com", "example.com"},
		{"Test User <user@example.com>", "example.com"},
		{"<user@example.com>", "example.com"},
		{"nope", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := extractDomain(tt.input)
		if got != tt.want {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
