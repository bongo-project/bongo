package tagname

import "testing"

func TestNormalise(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Work", "work"},
		{"My Projects", "my-projects"},
		{"hello_world", "hello-world"},
		{"  trim me  ", "trim-me"},
		{"multi---hyphens", "multi-hyphens"},
		{"label1", "label1"},
		{"Project Alpha", "project-alpha"},
		{"", ""},
		{"---", ""},
		{"café", "caf"},           // non-ASCII stripped
		{"work!", "work"},         // punctuation stripped
		{"a", "a"},                // single char
		{"a-b", "a-b"},            // valid hyphen in middle
		{"-leading", "leading"},   // leading hyphen trimmed
		{"trailing-", "trailing"}, // trailing hyphen trimmed
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Normalise(tt.input)
			if got != tt.want {
				t.Errorf("Normalise(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
