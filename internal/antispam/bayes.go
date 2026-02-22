package antispam

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// Common English stopwords to exclude from tokenization.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "had": true,
	"her": true, "was": true, "one": true, "our": true, "out": true,
	"has": true, "have": true, "been": true, "from": true, "this": true,
	"that": true, "with": true, "they": true, "will": true, "each": true,
	"make": true, "like": true, "then": true, "them": true, "than": true,
	"its": true, "also": true, "into": true, "just": true, "what": true,
	"which": true, "when": true, "who": true, "how": true, "where": true,
}

// RaftApplier applies commands through Raft consensus.
type RaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// PebbleGetter reads keys from Pebble.
type PebbleGetter interface {
	Get(key []byte) ([]byte, error)
}

// BayesFilter implements a per-user naive Bayes spam classifier.
type BayesFilter struct {
	store   PebbleGetter
	applier RaftApplier
}

// NewBayesFilter creates a new Bayesian spam filter.
func NewBayesFilter(store PebbleGetter, applier RaftApplier) *BayesFilter {
	return &BayesFilter{
		store:   store,
		applier: applier,
	}
}

// Tokenize splits text into lowercase tokens, filtering out stopwords and short tokens.
func Tokenize(subject, bodyText string) []string {
	text := strings.ToLower(subject + " " + bodyText)

	// Split on non-letter/digit characters.
	tokens := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	// Deduplicate and filter.
	seen := make(map[string]bool)
	var result []string
	for _, tok := range tokens {
		if len(tok) < 3 {
			continue
		}
		if stopwords[tok] {
			continue
		}
		if seen[tok] {
			continue
		}
		seen[tok] = true
		result = append(result, tok)
	}
	return result
}

// Train updates token counts for a user. isSpam=true trains as spam, false as ham.
func (b *BayesFilter) Train(userID string, tokens []string, isSpam bool) error {
	category := "ham"
	if isSpam {
		category = "spam"
	}

	for _, token := range tokens {
		key := fmt.Sprintf("/user/%s/bayes/%s/%s", userID, category, token)
		count := b.getCount([]byte(key))
		count++
		if err := b.setCount([]byte(key), count); err != nil {
			return fmt.Errorf("training token %q: %w", token, err)
		}
	}
	return nil
}

// Classify returns a spam probability (0.0 = ham, 1.0 = spam) for the given tokens.
func (b *BayesFilter) Classify(userID string, tokens []string) float64 {
	if len(tokens) == 0 {
		return 0.5
	}

	var logSpam, logHam float64
	counted := 0

	for _, token := range tokens {
		spamKey := fmt.Sprintf("/user/%s/bayes/spam/%s", userID, token)
		hamKey := fmt.Sprintf("/user/%s/bayes/ham/%s", userID, token)

		spamCount := b.getCount([]byte(spamKey))
		hamCount := b.getCount([]byte(hamKey))

		// Skip tokens we've never seen.
		if spamCount == 0 && hamCount == 0 {
			continue
		}

		// Laplace smoothing.
		pTokenSpam := float64(spamCount+1) / float64(spamCount+hamCount+2)
		pTokenHam := 1.0 - pTokenSpam

		logSpam += math.Log(pTokenSpam)
		logHam += math.Log(pTokenHam)
		counted++
	}

	if counted == 0 {
		return 0.5
	}

	// Convert log odds to probability.
	// P(spam|tokens) = 1 / (1 + exp(logHam - logSpam))
	diff := logHam - logSpam
	if diff > 500 {
		return 0.0
	}
	if diff < -500 {
		return 1.0
	}
	return 1.0 / (1.0 + math.Exp(diff))
}

// TrainedCount returns the total number of trained tokens for a user.
func (b *BayesFilter) TrainedCount(userID string) int {
	// We count by checking a summary key.
	key := fmt.Sprintf("/user/%s/bayes/trained", userID)
	return int(b.getCount([]byte(key)))
}

// IncrementTrained increments the trained message count.
func (b *BayesFilter) IncrementTrained(userID string) error {
	key := fmt.Sprintf("/user/%s/bayes/trained", userID)
	count := b.getCount([]byte(key))
	return b.setCount([]byte(key), count+1)
}

func (b *BayesFilter) getCount(key []byte) uint64 {
	data, err := b.store.Get(key)
	if err != nil || len(data) == 0 {
		return 0
	}
	val, _ := binary.Uvarint(data)
	return val
}

func (b *BayesFilter) setCount(key []byte, count uint64) error {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(buf, count)

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   key,
		Value: buf[:n],
	}
	return b.applier.ApplyCommand(cmd, 5*time.Second)
}

// UpdateSpamScore updates a mail message's spam score via Raft.
func UpdateSpamScore(pebble PebbleGetter, applier RaftApplier, userID, guid string, newScore float32) error {
	key := []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid))
	data, err := pebble.Get(key)
	if err != nil {
		return fmt.Errorf("loading mail meta: %w", err)
	}

	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("parsing mail meta: %w", err)
	}

	meta.SpamScore = newScore
	metaBytes, err := proto.Marshal(&meta)
	if err != nil {
		return fmt.Errorf("marshaling mail meta: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   key,
		Value: metaBytes,
	}
	return applier.ApplyCommand(cmd, 5*time.Second)
}
