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

func TestScorer_CappedAt1(t *testing.T) {
	s := NewScorer()
	// Trigger every check.
	result := s.Score(&ScoringInput{
		SPFResult:  "fail",
		DKIMResult: "fail",
		Headers:    map[string]string{},
		Subject:    "Click here for free money act now limited time winner",
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
