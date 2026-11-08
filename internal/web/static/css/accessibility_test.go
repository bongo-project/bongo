package css_test

import (
	"strings"
	"testing"
)

func TestAccessibilityMediaQueriesRemainAvailable(t *testing.T) {
	css := mustApplicationCSS(t)
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
