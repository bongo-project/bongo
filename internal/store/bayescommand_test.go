package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func bayesTestStore(t *testing.T) *PebbleStore {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return ps
}

func bayesCount(t *testing.T, ps *PebbleStore, key []byte) uint64 {
	t.Helper()
	data, err := ps.Get(key)
	if errors.Is(err, ErrNotFound) {
		return 0
	}
	if err != nil {
		t.Fatalf("Get %s: %v", key, err)
	}
	n, _ := binary.Uvarint(data)
	return n
}

func trainBayes(t *testing.T, ps *PebbleStore, guid string, tokens []string, spam bool) {
	t.Helper()
	cmd, err := BuildBayesTrainCommand("u1", guid, tokens, spam)
	if err != nil {
		t.Fatalf("BuildBayesTrainCommand: %v", err)
	}
	if err := ApplyBayesTrainCommand(ps, cmd); err != nil {
		t.Fatalf("ApplyBayesTrainCommand: %v", err)
	}
}

func TestBuildBayesTrainCommand_Refusals(t *testing.T) {
	tooMany := make([]string, MaxBayesTokens+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("t%d", i)
	}
	tests := []struct {
		name   string
		user   string
		tokens []string
		ok     bool
	}{
		{"empty user", "", []string{"abc"}, false},
		{"one over the cap", "u1", tooMany, false},
		{"exactly the cap", "u1", tooMany[:MaxBayesTokens], true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildBayesTrainCommand(tt.user, "g", tt.tokens, true)
			if (err == nil) != tt.ok {
				t.Errorf("err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestApplyBayesTrain_CountsAndMarker(t *testing.T) {
	ps := bayesTestStore(t)
	tokens := []string{"alpha", "bravo"}

	trainBayes(t, ps, "g1", tokens, true)
	trainBayes(t, ps, "g1", tokens, true) // repeat: no-op
	if got := bayesCount(t, ps, BayesTokenKey("u1", "spam", "alpha")); got != 1 {
		t.Errorf("spam alpha = %d, want 1", got)
	}
	if got := bayesCount(t, ps, BayesTrainedKey("u1")); got != 1 {
		t.Errorf("trained = %d, want 1", got)
	}

	trainBayes(t, ps, "g2", tokens, true) // second message accumulates
	if got := bayesCount(t, ps, BayesTokenKey("u1", "spam", "alpha")); got != 2 {
		t.Errorf("spam alpha = %d, want 2", got)
	}

	trainBayes(t, ps, "g1", tokens, false) // g1 moves to ham
	if got := bayesCount(t, ps, BayesTokenKey("u1", "spam", "alpha")); got != 1 {
		t.Errorf("spam alpha after move = %d, want 1 (only g2)", got)
	}
	if got := bayesCount(t, ps, BayesTokenKey("u1", "ham", "alpha")); got != 1 {
		t.Errorf("ham alpha after move = %d, want 1", got)
	}
	if got := bayesCount(t, ps, BayesTrainedKey("u1")); got != 2 {
		t.Errorf("trained after move = %d, want 2", got)
	}
}

func TestApplyBayesTrain_DecrementStopsAtZero(t *testing.T) {
	ps := bayesTestStore(t)
	// Marker says spam but no spam counts exist (legacy data).
	if err := ps.Set(BayesMessageKey("u1", "g1"), []byte("spam")); err != nil {
		t.Fatal(err)
	}
	trainBayes(t, ps, "g1", []string{"alpha"}, false)
	if got := bayesCount(t, ps, BayesTokenKey("u1", "spam", "alpha")); got != 0 {
		t.Errorf("spam alpha = %d, want 0 (must not underflow)", got)
	}
	if got := bayesCount(t, ps, BayesTokenKey("u1", "ham", "alpha")); got != 1 {
		t.Errorf("ham alpha = %d, want 1", got)
	}
}

func TestBuildBayesTrainCommand_RejectsEmptyGUID(t *testing.T) {
	if _, err := BuildBayesTrainCommand("u1", "", []string{"alpha"}, true); err == nil {
		t.Error("empty guid accepted by the builder")
	}
	// A hand-built payload must be refused at the FSM boundary too.
	get := func([]byte) ([]byte, error) { return nil, ErrNotFound }
	cmd := &bongov1.RaftCommand{Key: []byte(bayesTrainPrefix + "u1"), Value: []byte(`{"user_id":"u1","category":"spam","tokens":["alpha"]}`)}
	if _, err := PlanBayesTrain(get, cmd); err == nil {
		t.Error("empty guid accepted by PlanBayesTrain")
	}
}

// Dedup is pinned by the number of planned writes: the final stored count is
// the same with or without it, because later writes overwrite earlier ones.
func TestPlanBayesTrain_DuplicateTokensPlannedOnce(t *testing.T) {
	cmd, err := BuildBayesTrainCommand("u1", "g1", []string{"alpha", "alpha", "alpha"}, true)
	if err != nil {
		t.Fatal(err)
	}
	writes, err := PlanBayesTrain(func([]byte) ([]byte, error) { return nil, ErrNotFound }, cmd)
	if err != nil {
		t.Fatal(err)
	}
	// One token write, the trained counter and the marker.
	if len(writes) != 3 {
		t.Errorf("planned %d writes, want 3: %q", len(writes), writes)
	}
}

// A failed commit must leave counters, trained total and marker untouched, so
// a retry is safe.
func TestApplyBayesTrain_AllOrNothing(t *testing.T) {
	ps := bayesTestStore(t)
	orig := commitSyncBatch
	t.Cleanup(func() { commitSyncBatch = orig })
	commitSyncBatch = func(ps *PebbleStore, fn func(batch *pebble.Batch) error) error {
		batch := ps.db.NewBatch()
		defer batch.Close()
		if err := fn(batch); err != nil {
			return err
		}
		return errors.New("simulated crash before commit")
	}

	cmd, _ := BuildBayesTrainCommand("u1", "g1", []string{"alpha", "bravo"}, true)
	if err := ApplyBayesTrainCommand(ps, cmd); err == nil {
		t.Fatal("apply reported success despite failed commit")
	}
	for _, k := range [][]byte{BayesTokenKey("u1", "spam", "alpha"), BayesTokenKey("u1", "spam", "bravo"), BayesTrainedKey("u1"), BayesMessageKey("u1", "g1")} {
		if _, err := ps.Get(k); !errors.Is(err, ErrNotFound) {
			t.Errorf("key %s survived an aborted commit: %v", k, err)
		}
	}

	commitSyncBatch = orig
	if err := ApplyBayesTrainCommand(ps, cmd); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := bayesCount(t, ps, BayesTrainedKey("u1")); got != 1 {
		t.Errorf("trained after retry = %d, want 1", got)
	}
}

func TestPlanBayesTrain_RejectsInvalid(t *testing.T) {
	get := func([]byte) ([]byte, error) { return nil, ErrNotFound }
	for name, cmd := range map[string]*bongov1.RaftCommand{
		"wrong prefix": {Key: []byte("/user/u1/x"), Value: []byte("{}")},
		"bad json":     {Key: []byte(bayesTrainPrefix + "u1"), Value: []byte("nope")},
		"bad category": {Key: []byte(bayesTrainPrefix + "u1"), Value: []byte(`{"user_id":"u1","category":"maybe"}`)},
		"no user":      {Key: []byte(bayesTrainPrefix + "u1"), Value: []byte(`{"category":"spam"}`)},
	} {
		if _, err := PlanBayesTrain(get, cmd); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A store read failure other than ErrNotFound must fail the plan: treating it
// as a zero count would overwrite a real counter with 1.
func TestPlanBayesTrain_ReadErrorFailsPlan(t *testing.T) {
	cmd, err := BuildBayesTrainCommand("u1", "g1", []string{"alpha"}, true)
	if err != nil {
		t.Fatal(err)
	}
	get := func([]byte) ([]byte, error) { return nil, errors.New("io") }
	if writes, err := PlanBayesTrain(get, cmd); err == nil {
		t.Errorf("plan succeeded despite a read error: %q", writes)
	}
}

// The FSM-side guards sit on the Raft trust boundary: a payload that bypassed
// BuildBayesTrainCommand must still be bounded.
func TestPlanBayesTrain_EnforcesBoundsOnHandBuiltPayload(t *testing.T) {
	get := func([]byte) ([]byte, error) { return nil, ErrNotFound }

	tokens := make([]string, 1025)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("tok%d", i)
	}
	payload, err := json.Marshal(bayesTrainPayload{UserID: "u1", GUID: "g1", Category: BayesSpam, Tokens: tokens})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanBayesTrain(get, &bongov1.RaftCommand{Key: []byte(bayesTrainPrefix + "u1"), Value: payload}); err == nil {
		t.Error("1025 tokens accepted")
	}

	long := strings.Repeat("a", 65)
	payload, err = json.Marshal(bayesTrainPayload{UserID: "u1", GUID: "g1", Category: BayesSpam, Tokens: []string{long, "okword"}})
	if err != nil {
		t.Fatal(err)
	}
	writes, err := PlanBayesTrain(get, &bongov1.RaftCommand{Key: []byte(bayesTrainPrefix + "u1"), Value: payload})
	if err != nil {
		t.Fatalf("PlanBayesTrain: %v", err)
	}
	var sawOK bool
	for _, w := range writes {
		if strings.Contains(string(w.Key), long) {
			t.Errorf("65-byte token written: %q", w.Key)
		}
		if string(w.Key) == string(BayesTokenKey("u1", BayesSpam, "okword")) {
			sawOK = true
		}
	}
	if !sawOK {
		t.Error("control token not written")
	}
}
