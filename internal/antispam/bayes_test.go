package antispam

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestTokenize(t *testing.T) {
	tokens := Tokenize("Hello World", "This is a test message with some content")
	if len(tokens) == 0 {
		t.Fatal("expected tokens, got none")
	}

	// Should not contain stopwords or short tokens.
	for _, tok := range tokens {
		if len(tok) < 3 {
			t.Errorf("token too short: %q", tok)
		}
		if stopwords[tok] {
			t.Errorf("stopword not filtered: %q", tok)
		}
	}

	// Should contain meaningful words.
	sort.Strings(tokens)
	found := false
	for _, tok := range tokens {
		if tok == "hello" || tok == "world" || tok == "message" || tok == "content" || tok == "test" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected meaningful tokens, got: %v", tokens)
	}
}

func TestTokenize_Dedup(t *testing.T) {
	tokens := Tokenize("hello hello hello", "world world")
	counts := make(map[string]int)
	for _, tok := range tokens {
		counts[tok]++
	}
	for tok, count := range counts {
		if count > 1 {
			t.Errorf("duplicate token: %q appeared %d times", tok, count)
		}
	}
}

func TestTokenize_Empty(t *testing.T) {
	tokens := Tokenize("", "")
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens for empty input, got %d", len(tokens))
	}
}

// memBayes is a map-backed PebbleGetter and RaftApplier that interprets the
// synthetic training command exactly as the FSM does, and counts calls.
type memBayes struct {
	data  map[string][]byte
	calls int
	fail  error
}

func newMemBayes() *memBayes { return &memBayes{data: make(map[string][]byte)} }

func (m *memBayes) Get(key []byte) ([]byte, error) { return m.data[string(key)], nil }

func (m *memBayes) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	m.calls++
	if m.fail != nil {
		return m.fail
	}
	if store.IsBayesSyntheticCommand(cmd.Key) {
		writes, err := store.PlanBayesTrain(m.Get, cmd)
		if err != nil {
			return err
		}
		for _, w := range writes {
			m.data[string(w.Key)] = w.Value
		}
		return nil
	}
	m.data[string(cmd.Key)] = cmd.Value
	return nil
}

func (m *memBayes) tokenKeys() int {
	n := 0
	for k := range m.data {
		if strings.Contains(k, "/bayes/spam/") || strings.Contains(k, "/bayes/ham/") {
			n++
		}
	}
	return n
}

// distinctWords returns n distinct three-letter, non-stopword tokens.
func distinctWords(n int) []string {
	var out []string
	for a := 'a'; a <= 'z' && len(out) < n; a++ {
		for b := 'a'; b <= 'z' && len(out) < n; b++ {
			for c := 'a'; c <= 'z' && len(out) < n; c++ {
				w := string([]rune{a, b, c})
				if !stopwords[w] {
					out = append(out, w)
				}
			}
		}
	}
	return out
}

// distinctWords4 returns n distinct six-byte tokens.
func distinctWords4(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("w%05d", i)
	}
	return out
}

func TestTrain_HostileMessageIsBounded(t *testing.T) {
	// 50k distinct words in a 200KB-plus subject, plus a full body.
	subject := strings.Join(distinctWords4(50000), " ")
	body := strings.Repeat("lorem ipsum dolor ", 300)
	if len(subject) < 200000 {
		t.Fatalf("fixture subject is %d bytes, want >= 200000", len(subject))
	}

	m := newMemBayes()
	b := NewBayesFilter(m, m)
	tokens := Tokenize(subject, body)
	if err := b.Train("u1", "g1", tokens, true); err != nil {
		t.Fatalf("Train: %v", err)
	}
	if m.calls != 1 {
		t.Errorf("ApplyCommand calls = %d, want 1", m.calls)
	}
	if got := m.tokenKeys(); got > 1024 || got == 0 {
		t.Errorf("token keys written = %d, want 1..1024", got)
	}
}

func TestTokenize_GiantTokenSkipped(t *testing.T) {
	giant := strings.Repeat("x", 100000)
	tokens := Tokenize("realword "+giant, "")
	if len(tokens) != 1 || tokens[0] != "realword" {
		t.Errorf("tokens = %v, want [realword]", tokens)
	}
	// Exactly at the limit is kept; one over is dropped.
	if got := Tokenize(strings.Repeat("y", 64), ""); len(got) != 1 {
		t.Errorf("64-byte token dropped: %v", got)
	}
	if got := Tokenize(strings.Repeat("y", 65), ""); len(got) != 0 {
		t.Errorf("65-byte token kept: %d tokens", len(got))
	}
}

func TestTokenize_CapKeepsFirst1024(t *testing.T) {
	// 1025 distinct three-byte tokens: 256 fill the 1024-byte subject
	// exactly, 769 go in the body.
	words := distinctWords(1025)
	subject := strings.Join(words[:256], " ")
	body := strings.Join(words[256:], " ")
	got := Tokenize(subject, body)
	if len(got) != 1024 {
		t.Fatalf("len(tokens) = %d, want 1024", len(got))
	}
	for i, w := range got {
		if w != words[i] {
			t.Fatalf("token %d = %q, want %q", i, w, words[i])
		}
	}
}

func TestTokenize_TruncatesOnRuneBoundary(t *testing.T) {
	// 1023 ASCII bytes then a two-byte rune straddling the 1024 limit.
	subject := strings.Repeat("a", 1023) + "é"
	got := Tokenize(subject, "")
	for _, tok := range got {
		if !utf8.ValidString(tok) {
			t.Errorf("token %q is not valid UTF-8", tok)
		}
	}
	if s := truncateUTF8("ééé", 3); s != "é" {
		t.Errorf("truncateUTF8 = %q, want %q", s, "é")
	}
}

func TestTokenize_NormalMessageUnchanged(t *testing.T) {
	got := Tokenize("Meeting Notes: Q3 Budget review", "Please review the attached budget numbers before the meeting on Friday.")
	want := []string{"meeting", "notes", "budget", "review", "please", "attached", "numbers", "before", "friday"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}

	m := newMemBayes()
	b := NewBayesFilter(m, m)
	if err := b.Train("u1", "g1", got, false); err != nil {
		t.Fatalf("Train: %v", err)
	}
	for _, tok := range want {
		if c := b.getCount([]byte("/user/u1/bayes/ham/" + tok)); c != 1 {
			t.Errorf("ham count for %q = %d, want 1", tok, c)
		}
		if c := b.getCount([]byte("/user/u1/bayes/spam/" + tok)); c != 0 {
			t.Errorf("spam count for %q = %d, want 0", tok, c)
		}
	}
	if n := b.TrainedCount("u1"); n != 1 {
		t.Errorf("TrainedCount = %d, want 1", n)
	}
}

func TestTrain_Retrain(t *testing.T) {
	tokens := []string{"alpha", "bravo", "charlie"}
	tests := []struct {
		name              string
		guid              string
		steps             []bool
		wantSpam, wantHam uint64
		wantTrained       int
	}{
		{"repeat spam is a no-op", "g", []bool{true, true, true}, 1, 0, 1},
		{"spam then ham moves counts", "g", []bool{true, false}, 0, 1, 1},
		{"ham spam ham", "g", []bool{false, true, false}, 0, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMemBayes()
			b := NewBayesFilter(m, m)
			for _, spam := range tt.steps {
				if err := b.Train("u1", tt.guid, tokens, spam); err != nil {
					t.Fatalf("Train: %v", err)
				}
			}
			for _, tok := range tokens {
				if c := b.getCount([]byte("/user/u1/bayes/spam/" + tok)); c != tt.wantSpam {
					t.Errorf("spam[%s] = %d, want %d", tok, c, tt.wantSpam)
				}
				if c := b.getCount([]byte("/user/u1/bayes/ham/" + tok)); c != tt.wantHam {
					t.Errorf("ham[%s] = %d, want %d", tok, c, tt.wantHam)
				}
			}
			if n := b.TrainedCount("u1"); n != tt.wantTrained {
				t.Errorf("TrainedCount = %d, want %d", n, tt.wantTrained)
			}
		})
	}
}

func TestTrain_ErrorPropagates(t *testing.T) {
	m := newMemBayes()
	m.fail = errors.New("raft down")
	b := NewBayesFilter(m, m)
	if err := b.Train("u1", "g", []string{"alpha"}, true); !errors.Is(err, m.fail) {
		t.Errorf("Train error = %v, want wrapping %v", err, m.fail)
	}
}

func TestTrain_RefusesOversizedAndAnonymous(t *testing.T) {
	m := newMemBayes()
	b := NewBayesFilter(m, m)
	if err := b.Train("u1", "g", distinctWords(1025), true); err == nil {
		t.Error("1025 tokens accepted, want error")
	}
	if err := b.Train("", "g", []string{"alpha"}, true); err == nil {
		t.Error("empty user accepted, want error")
	}
	if err := b.Train("u1", "", []string{"alpha"}, true); err == nil {
		t.Error("empty guid accepted, want error")
	}
	if m.calls != 0 {
		t.Errorf("ApplyCommand called %d times for refused commands", m.calls)
	}
}

// Words past the byte caps must be dropped even when the distinct-token cap is
// nowhere near reached, so these tests isolate the byte caps from the token cap.
func TestTokenize_ByteCapsDropLateWords(t *testing.T) {
	// Filler of repeated "pad" (a single distinct token) puts the marker
	// words precisely beyond the cap.
	subject := strings.Repeat("pad ", 260) + "latesubjectword" // marker starts at byte 1040
	body := strings.Repeat("pad ", 1030) + "latebodyword"      // marker starts at byte 4120
	early := "earlysubject " + subject
	got := Tokenize(early, body)

	has := func(w string) bool {
		for _, tok := range got {
			if tok == w {
				return true
			}
		}
		return false
	}
	if !has("earlysubject") || !has("pad") {
		t.Fatalf("control tokens missing: %v", got)
	}
	if has("latesubjectword") {
		t.Error("word past byte 1024 of the subject was tokenised")
	}
	if has("latebodyword") {
		t.Error("word past byte 4096 of the body was tokenised")
	}

	// Positive control: the same words inside the caps are kept.
	got = Tokenize("insidesubject", "insidebody")
	if len(got) != 2 {
		t.Errorf("in-cap words = %v, want 2 tokens", got)
	}
}
