package handlers

import "testing"

func TestNormalizeRecipient(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare address", "user@example.com", "user@example.com"},
		{"display name with angle brackets", "Lance <user@example.com>", "user@example.com"},
		{"angle brackets only", "<user@example.com>", "user@example.com"},
		{"surrounding whitespace", "  admin@example.com  ", "admin@example.com"},
		{"quoted display name", "\"Lance Haig\" <user@example.com>", "user@example.com"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"unparseable falls back to trimmed input", "not-an-address", "not-an-address"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRecipient(tt.in); got != tt.want {
				t.Errorf("normalizeRecipient(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
