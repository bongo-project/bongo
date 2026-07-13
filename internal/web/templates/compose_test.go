package templates

import (
	"strings"
	"testing"
)

// TestComposeInitialBody verifies that composeInitialBody returns the body
// unconditionally so that new-message plain mode is prefilled with the plain
// signature supplied by NewMessage (FIX b).
func TestComposeInitialBody(t *testing.T) {
	tests := []struct {
		name string
		mode string
		body string
		want string
	}{
		{
			name: "new with plain signature contains sig text",
			mode: "new",
			body: "\n\n-- \nJane",
			want: "\n\n-- \nJane",
		},
		{
			name: "new with empty body returns empty",
			mode: "new",
			body: "",
			want: "",
		},
		{
			name: "reply passes body through unchanged",
			mode: "reply",
			body: "On Mon...\n> foo",
			want: "On Mon...\n> foo",
		},
		{
			name: "forward passes body through unchanged",
			mode: "forward",
			body: "> forwarded content",
			want: "> forwarded content",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := composeInitialBody(tc.mode, tc.body)
			if got != tc.want {
				t.Errorf("composeInitialBody(%q, %q) = %q, want %q", tc.mode, tc.body, got, tc.want)
			}
			// Extra assertion for the key new-message case: must contain "Jane".
			if tc.mode == "new" && strings.Contains(tc.body, "Jane") {
				if !strings.Contains(got, "Jane") {
					t.Errorf("composeInitialBody(%q, %q): expected result to contain 'Jane', got %q", tc.mode, tc.body, got)
				}
			}
		})
	}
}
