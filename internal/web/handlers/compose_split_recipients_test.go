package handlers

import (
	"slices"
	"testing"
)

func TestSplitRecipients(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single bare", "a@x.example", []string{"a@x.example"}},
		{"bare list", "a@x.example, b@y.example", []string{"a@x.example", "b@y.example"}},
		{"bare list no spaces", "a@x.example,b@y.example", []string{"a@x.example", "b@y.example"}},
		{"named list is unchanged", "Jane Doe <j@x.example>, b@y.example", []string{"Jane Doe <j@x.example>", "b@y.example"}},
		{"quoted comma is one recipient", `"Doe, Jane" <j@x.example>, b@y.example`, []string{`"Doe, Jane" <j@x.example>`, "b@y.example"}},
		{"quoted comma last", `b@y.example, "Doe, Jane" <j@x.example>`, []string{"b@y.example", `"Doe, Jane" <j@x.example>`}},
		{"two quoted commas", `"A, B" <a@x.example>, "C, D" <c@x.example>`, []string{`"A, B" <a@x.example>`, `"C, D" <c@x.example>`}},
		{"trailing comma", "a@x.example,", []string{"a@x.example"}},
		{"separators only", " , ,", nil},
		{"invalid list falls back to comma split", "a@x.example, not an address", []string{"a@x.example", "not an address"}},
		{"unterminated quote falls back", `"Doe, Jane <j@x.example>, b@y.example`, []string{`"Doe`, `Jane <j@x.example>`, "b@y.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitRecipients(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("splitRecipients(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
