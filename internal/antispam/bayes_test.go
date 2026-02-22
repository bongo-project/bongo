package antispam

import (
	"sort"
	"testing"
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
