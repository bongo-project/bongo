package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cockroachdb/pebble"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// bayesTrainPrefix marks a synthetic Bayes training command, following the
// same convention as the queue and mailbox prefixes.
const bayesTrainPrefix = "/__bayes_train__/"

const (
	// MaxBayesTokens bounds the tokens one training command may carry, and so
	// the Pebble reads and writes one FSM apply performs. A 4KB body plus a
	// 1KB subject cannot hold many more than 1280 three-byte tokens, so the
	// cap only bites on hostile input. Do not raise it without measuring: the
	// apply runs on Raft's single goroutine.
	MaxBayesTokens = 1024

	// MaxBayesTokenLen bounds one token's byte length, and so the size of
	// the Pebble key it becomes. Real words are far shorter.
	MaxBayesTokenLen = 64
)

// Bayes training categories.
const (
	BayesSpam = "spam"
	BayesHam  = "ham"
)

// BayesTokenKey returns the Pebble key holding one token's count.
func BayesTokenKey(userID, category, token string) []byte {
	return []byte("/user/" + userID + "/bayes/" + category + "/" + token)
}

// BayesTrainedKey returns the key holding the user's trained message count.
func BayesTrainedKey(userID string) []byte {
	return []byte("/user/" + userID + "/bayes/trained")
}

// BayesMessageKey returns the key recording which category a message was
// last trained as.
func BayesMessageKey(userID, guid string) []byte {
	return []byte("/user/" + userID + "/bayes/msg/" + guid)
}

// bayesTrainPayload is one training request, applied inside the FSM.
type bayesTrainPayload struct {
	UserID   string   `json:"user_id"`
	GUID     string   `json:"guid"`
	Category string   `json:"category"`
	Tokens   []string `json:"tokens"`
}

// BuildBayesTrainCommand builds the synthetic command that trains a message's
// tokens as spam or ham.
//
// Training used to be one Raft write per token with a read-modify-write on the
// client, so a crafted message cost tens of thousands of log entries and two
// concurrent trainings lost updates. The increments now happen in the FSM, in
// one batch, in one log entry.
//
// guid is required. It makes training idempotent and reversible: the FSM
// records the category per message, so repeating a click or replaying the log
// changes nothing and moving a message from spam to ham removes its spam
// counts. An empty guid would increment without a marker and so double-count on
// replay; it is refused here and again in PlanBayesTrain.
func BuildBayesTrainCommand(userID, guid string, tokens []string, isSpam bool) (*bongov1.RaftCommand, error) {
	if userID == "" {
		return nil, fmt.Errorf("bayes training requires a user id")
	}
	if guid == "" {
		return nil, fmt.Errorf("bayes training requires a message guid")
	}
	if len(tokens) > MaxBayesTokens {
		return nil, fmt.Errorf("bayes training carries %d tokens, limit is %d", len(tokens), MaxBayesTokens)
	}
	category := BayesHam
	if isSpam {
		category = BayesSpam
	}
	payload, err := json.Marshal(bayesTrainPayload{UserID: userID, GUID: guid, Category: category, Tokens: tokens})
	if err != nil {
		return nil, fmt.Errorf("marshaling bayes training payload: %w", err)
	}
	return &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(bayesTrainPrefix + userID),
		Value: payload,
	}, nil
}

// IsBayesSyntheticCommand reports whether the key represents a synthetic
// Bayes training command handled directly by the FSM.
func IsBayesSyntheticCommand(key []byte) bool {
	return strings.HasPrefix(string(key), bayesTrainPrefix)
}

// BayesWrite is one key write produced by a training command.
type BayesWrite struct {
	Key   []byte
	Value []byte
}

// PlanBayesTrain computes the writes a training command makes, reading current
// state through get. A missing key may be reported as ErrNotFound or as an
// empty value. It is separate from the batch commit so that test appliers
// without a real Pebble can interpret the command identically.
func PlanBayesTrain(get func(key []byte) ([]byte, error), cmd *bongov1.RaftCommand) ([]BayesWrite, error) {
	if !IsBayesSyntheticCommand(cmd.Key) {
		return nil, fmt.Errorf("not a synthetic bayes command")
	}
	var p bayesTrainPayload
	if err := json.Unmarshal(cmd.Value, &p); err != nil {
		return nil, fmt.Errorf("parsing bayes training payload: %w", err)
	}
	if p.UserID == "" {
		return nil, fmt.Errorf("bayes training payload missing user id")
	}
	if p.GUID == "" {
		return nil, fmt.Errorf("bayes training payload missing message guid")
	}
	if p.Category != BayesSpam && p.Category != BayesHam {
		return nil, fmt.Errorf("bayes training payload has invalid category %q", p.Category)
	}
	if len(p.Tokens) > MaxBayesTokens {
		return nil, fmt.Errorf("bayes training carries %d tokens, limit is %d", len(p.Tokens), MaxBayesTokens)
	}

	read := func(key []byte) (uint64, []byte, error) {
		data, err := get(key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return 0, nil, err
		}
		if len(data) == 0 {
			return 0, nil, nil
		}
		n, _ := binary.Uvarint(data)
		return n, data, nil
	}

	// Deduplicate so repeated tokens in one payload cannot read a stale count.
	seen := make(map[string]struct{}, len(p.Tokens))
	tokens := make([]string, 0, len(p.Tokens))
	for _, tok := range p.Tokens {
		if tok == "" || len(tok) > MaxBayesTokenLen {
			continue
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		tokens = append(tokens, tok)
	}

	var writes []BayesWrite
	adjust := func(category string, delta int) error {
		for _, tok := range tokens {
			key := BayesTokenKey(p.UserID, category, tok)
			n, _, err := read(key)
			if err != nil {
				return fmt.Errorf("reading bayes token %q: %w", tok, err)
			}
			switch {
			case delta > 0:
				n++
			case n > 0:
				// Stop at zero: legacy data or a changed tokeniser may not
				// match what was originally learned.
				n--
			}
			writes = append(writes, BayesWrite{Key: key, Value: encodeBayesCount(n)})
		}
		return nil
	}
	bumpTrained := func() error {
		key := BayesTrainedKey(p.UserID)
		n, _, err := read(key)
		if err != nil {
			return fmt.Errorf("reading bayes trained count: %w", err)
		}
		writes = append(writes, BayesWrite{Key: key, Value: encodeBayesCount(n + 1)})
		return nil
	}

	markerKey := BayesMessageKey(p.UserID, p.GUID)
	_, prev, err := read(markerKey)
	if err != nil {
		return nil, fmt.Errorf("reading bayes message marker: %w", err)
	}
	switch string(prev) {
	case p.Category:
		return nil, nil
	case "":
		if err := adjust(p.Category, 1); err != nil {
			return nil, err
		}
		if err := bumpTrained(); err != nil {
			return nil, err
		}
	default:
		// Reclassification: undo the old category, leave the trained count.
		if prev := string(prev); prev == BayesSpam || prev == BayesHam {
			if err := adjust(prev, -1); err != nil {
				return nil, err
			}
		}
		if err := adjust(p.Category, 1); err != nil {
			return nil, err
		}
	}
	writes = append(writes, BayesWrite{Key: markerKey, Value: []byte(p.Category)})
	return writes, nil
}

func encodeBayesCount(n uint64) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	return buf[:binary.PutUvarint(buf, n)]
}

// ApplyBayesTrainCommand applies a synthetic training command. Every counter
// and the message marker land in one synced batch, so a failure writes nothing
// and a retry is safe.
func ApplyBayesTrainCommand(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	writes, err := PlanBayesTrain(ps.Get, cmd)
	if err != nil {
		return err
	}
	if len(writes) == 0 {
		return nil
	}
	return commitSyncBatch(ps, func(batch *pebble.Batch) error {
		for _, w := range writes {
			if err := batch.Set(w.Key, w.Value, nil); err != nil {
				return fmt.Errorf("writing bayes key: %w", err)
			}
		}
		return nil
	})
}
