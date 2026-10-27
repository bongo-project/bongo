package css_test

import (
	"os"
	"regexp"
	"testing"
)

// TestDisplayRulesOnToggledPanelsKeepHiddenWorking: a class rule that sets
// display beats the user-agent [hidden]{display:none}. The tag popover sat open
// over the sender line because .tag-dropdown set display:flex and nothing put
// display:none back. Every panel toggled with the hidden attribute needs an
// explicit [hidden] rule.
func TestDisplayRulesOnToggledPanelsKeepHiddenWorking(t *testing.T) {
	data, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	for _, class := range []string{".tag-dropdown", ".msg-more-menu"} {
		rules := regexp.MustCompile(`(?s)(?:^|[^\w.\[-])`+regexp.QuoteMeta(class)+`\s*\{([^}]*)\}`).FindAllStringSubmatch(css, -1)
		if len(rules) == 0 {
			t.Fatalf("no rule for %s", class)
		}
		sets := false
		for _, r := range rules {
			if regexp.MustCompile(`(?:^|[;\s])display\s*:\s*(?:flex|grid|block|inline)`).MatchString(r[1]) {
				sets = true
			}
		}
		if !sets {
			continue // nothing overrides [hidden]
		}
		hidden := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(class) + `\[hidden\]\s*\{[^}]*display:\s*none`)
		if !hidden.MatchString(css) {
			t.Errorf("%s sets display but has no %s[hidden]{display:none} rule", class, class)
		}
	}
}
