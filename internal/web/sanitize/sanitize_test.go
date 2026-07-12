package sanitize_test

import (
	"strings"
	"testing"

	"github.com/bongo-go/bongo-go/internal/web/sanitize"
)

func TestCompose(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		contains  string // expected substring in output (empty means no check)
		contains2 string // second expected substring (empty means no check)
		absent    string // substring that must NOT appear in output (empty means no check)
	}{
		{
			name:     "basic bold tag is kept",
			input:    "<b>hi</b>",
			contains: "<b>hi</b>",
		},
		{
			name:   "script tag is stripped",
			input:  "<script>alert(1)</script>",
			absent: "<script",
		},
		{
			name:     "data: img src is kept",
			input:    `<img src="data:image/png;base64,AAAA">`,
			contains: `<img`,
			// also assert the data URI value itself survived
			contains2: `data:image/png;base64,AAAA`,
		},
		{
			name:   "http remote img src is blocked",
			input:  `<img src="http://evil/x.png">`,
			absent: `http://evil/x.png`,
		},
		{
			name:     "cid: img src is kept",
			input:    `<img src="cid:abc123">`,
			contains: `<img`,
			contains2: `cid:abc123`,
		},
		{
			name:   "javascript: href is stripped",
			input:  `<a href="javascript:alert(1)">x</a>`,
			absent: "javascript:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitize.Compose(tc.input)
			if tc.contains != "" && !strings.Contains(got, tc.contains) {
				t.Errorf("Compose(%q) = %q; want output containing %q", tc.input, got, tc.contains)
			}
			if tc.contains2 != "" && !strings.Contains(got, tc.contains2) {
				t.Errorf("Compose(%q) = %q; want output containing %q", tc.input, got, tc.contains2)
			}
			if tc.absent != "" && strings.Contains(got, tc.absent) {
				t.Errorf("Compose(%q) = %q; want output NOT containing %q", tc.input, got, tc.absent)
			}
		})
	}
}
