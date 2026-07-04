package antispam

import (
	"fmt"
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

// DefaultSpamPhrases are common spam indicator phrases matched against the
// subject and body. Entries are deliberately specific: standalone words such
// as "winner", "congratulations" or "unsubscribe" appear routinely in
// legitimate newsletters and transactional mail and are excluded to avoid
// false positives.
var DefaultSpamPhrases = []string{
	"click here to claim",
	"act now",
	"limited time offer",
	"free money",
	"you have been selected",
	"urgent action required",
	"verify your account",
	"confirm your password",
	"account suspended",
	"wire transfer",
	"risk free",
	"100% free",
	"cash bonus",
	"claim your prize",
	"make money fast",
	"double your income",
	"work from home",
	"lowest price",
	"no credit check",
	"satisfaction guaranteed",
	"weight loss",
	"cheap meds",
	"viagra",
	"cialis",
	"nigerian prince",
	"bitcoin investment",
	"crypto giveaway",
	"meet singles",
	"this is not spam",
}

// Phrase-matching weights. Subject matches are a stronger signal than body
// matches. The combined phrase contribution is capped below the default Junk
// threshold so that phrase hits alone can never route mail to Junk without a
// corroborating signal (failing SPF/DKIM, missing headers, etc.).
const (
	subjectPhrasePoints float32 = 0.15
	bodyPhrasePoints    float32 = 0.08
	maxPhraseScore      float32 = 0.40
)

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

	// Spam phrases in the subject (strong signal) and body (weaker signal).
	// A phrase found in the subject scores higher than the same phrase in the
	// body; each phrase counts at most once, taking the subject weight when it
	// appears in both.
	subjectLower := strings.ToLower(input.Subject)
	bodyLower := strings.ToLower(input.BodySample)
	var phraseScore float32
	matched := 0
	for _, phrase := range s.SpamPhrases {
		switch {
		case strings.Contains(subjectLower, phrase):
			phraseScore += subjectPhrasePoints
			matched++
		case bodyLower != "" && strings.Contains(bodyLower, phrase):
			phraseScore += bodyPhrasePoints
			matched++
		default:
			continue
		}
		if phraseScore >= maxPhraseScore {
			phraseScore = maxPhraseScore
			break
		}
	}
	if phraseScore > 0 {
		score += phraseScore
		if matched == 1 {
			reasons = append(reasons, "spam phrase matched")
		} else {
			reasons = append(reasons, fmt.Sprintf("%d spam phrases matched", matched))
		}
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
