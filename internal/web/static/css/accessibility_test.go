package css_test

import (
	"os"
	"strings"
	"testing"
)

func TestAccessibilityMediaQueriesRemainAvailable(t *testing.T) {
	data, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatalf("read bongo.css: %v", err)
	}
	css := string(data)
	for _, want := range []string{
		"@media (forced-colors: active)",
		"outline-color: Highlight",
		"@media (prefers-reduced-motion: reduce)",
		"animation-duration: .01ms !important",
		"transition-duration: .01ms !important",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("accessibility media coverage missing %q", want)
		}
	}
}
