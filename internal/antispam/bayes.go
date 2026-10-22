package antispam

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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

const (
	// maxSubjectBytes and maxBodyBytes bound the text Tokenize reads, so one
	// message cannot make training or classification do unbounded work.
	maxSubjectBytes = 1024
	maxBodyBytes    = 4096
)

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Tokenize splits text into lowercase tokens, filtering out stopwords and short
// tokens. The result is bounded: the subject is read to 1KB and the body to
// 4KB, tokens over store.MaxBayesTokenLen bytes are skipped, and at most
// store.MaxBayesTokens distinct tokens are kept, first in subject-then-body
// order. First-N rather than most-significant is deliberate: it is
// deterministic and needs no reads, so re-tokenising the same message yields
// the same list. Training undo depends on that.
func Tokenize(subject, bodyText string) []string {
	text := strings.ToLower(truncateUTF8(subject, maxSubjectBytes) + " " + truncateUTF8(bodyText, maxBodyBytes))

	// Split on non-letter/digit characters.
	tokens := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	// Deduplicate and filter.
	seen := make(map[string]bool)
	var result []string
	for _, tok := range tokens {
		if len(tok) < 3 || len(tok) > store.MaxBayesTokenLen {
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
		if len(result) >= store.MaxBayesTokens {
			break
		}
	}
	return result
}

// Train records a message's tokens as spam or ham with a single Raft command.
// The FSM does the counting, so a repeat call for the same guid is a no-op and
// a call with the opposite category moves the message between classes. guid
// must not be empty.
func (b *BayesFilter) Train(userID, guid string, tokens []string, isSpam bool) error {
	cmd, err := store.BuildBayesTrainCommand(userID, guid, tokens, isSpam)
	if err != nil {
		return fmt.Errorf("building bayes training command: %w", err)
	}
	if err := b.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying bayes training: %w", err)
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
		spamKey := string(store.BayesTokenKey(userID, store.BayesSpam, token))
		hamKey := string(store.BayesTokenKey(userID, store.BayesHam, token))

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
	key := string(store.BayesTrainedKey(userID))
	count := b.getCount([]byte(key))
	if bits.UintSize == 32 && count > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(count) // #nosec G115 -- 64-bit platforms fit uint64 counts used here; 32-bit is clamped above
}

func (b *BayesFilter) getCount(key []byte) uint64 {
	data, err := b.store.Get(key)
	if err != nil || len(data) == 0 {
		return 0
	}
	val, _ := binary.Uvarint(data)
	return val
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
