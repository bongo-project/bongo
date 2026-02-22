package antispam

import (
	"strings"
)

// DefaultSpamThreshold is the default score above which mail is routed to Junk.
const DefaultSpamThreshold = 0.5

// ScoringInput contains all signals available for spam scoring.
type ScoringInput struct {
	SPFResult         string
	DKIMResult        string
	Headers           map[string]string
	Subject           string
	BodySample        string // first 4KB of body
	EnvelopeSender    string
	EnvelopeRecipient string
}

// ScoringResult contains the spam score and reasons.
type ScoringResult struct {
	Score   float32
	Reasons []string
}

// DefaultSpamPhrases are common spam indicator phrases.
var DefaultSpamPhrases = []string{
	"click here",
	"act now",
	"limited time",
	"free money",
	"you have been selected",
	"congratulations",
	"winner",
	"urgent action",
	"verify your account",
	"unsubscribe",
}

// Scorer scores inbound messages based on multiple signals.
type Scorer struct {
	Threshold   float32
	SpamPhrases []string
}

// NewScorer creates a scorer with default settings.
func NewScorer() *Scorer {
	return &Scorer{
		Threshold:   DefaultSpamThreshold,
		SpamPhrases: DefaultSpamPhrases,
	}
}

// Score evaluates the input and returns a spam score between 0.0 and 1.0.
func (s *Scorer) Score(input *ScoringInput) ScoringResult {
	var score float32
	var reasons []string

	add := func(points float32, reason string) {
		score += points
		reasons = append(reasons, reason)
	}

	// SPF checks.
	switch strings.ToLower(input.SPFResult) {
	case "fail":
		add(0.20, "SPF fail")
	case "softfail":
		add(0.10, "SPF softfail")
	}

	// DKIM checks.
	switch strings.ToLower(input.DKIMResult) {
	case "fail":
		add(0.20, "DKIM fail")
	case "none":
		add(0.10, "DKIM missing")
	}

	// Missing Message-ID.
	if input.Headers != nil {
		if input.Headers["Message-Id"] == "" && input.Headers["Message-ID"] == "" {
			add(0.15, "missing Message-ID")
		}

		// Suspicious Date.
		// We check for the absence of a Date header as suspicious.
		if input.Headers["Date"] == "" {
			add(0.10, "missing Date header")
		}
	}

	// Empty subject.
	if strings.TrimSpace(input.Subject) == "" {
		add(0.05, "empty subject")
	}

	// Envelope sender domain vs From header domain mismatch.
	if input.EnvelopeSender != "" && input.Headers != nil {
		fromHeader := input.Headers["From"]
		if fromHeader != "" {
			envDomain := extractDomain(input.EnvelopeSender)
			fromDomain := extractDomain(fromHeader)
			if envDomain != "" && fromDomain != "" && !strings.EqualFold(envDomain, fromDomain) {
				add(0.15, "envelope sender domain mismatch")
			}
		}
	}

	// Spam phrases in subject.
	subjectLower := strings.ToLower(input.Subject)
	var phraseScore float32
	for _, phrase := range s.SpamPhrases {
		if strings.Contains(subjectLower, phrase) {
			phraseScore += 0.10
			if phraseScore >= 0.30 {
				phraseScore = 0.30
				break
			}
		}
	}
	if phraseScore > 0 {
		score += phraseScore
		reasons = append(reasons, "spam phrases in subject")
	}

	// Cap at 1.0.
	if score > 1.0 {
		score = 1.0
	}

	return ScoringResult{
		Score:   score,
		Reasons: reasons,
	}
}

// extractDomain extracts the domain from an email address or From header.
func extractDomain(addr string) string {
	// Handle "Name <email@domain>" format.
	if idx := strings.LastIndex(addr, "<"); idx >= 0 {
		addr = addr[idx+1:]
		addr = strings.TrimSuffix(addr, ">")
	}
	parts := strings.SplitN(addr, "@", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parts[1]))
}
