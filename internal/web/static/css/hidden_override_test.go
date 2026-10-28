package css_test

import (
	"os"
	"regexp"
	"testing"
)

// hiddenToggledPanels are shown and hidden only through the hidden attribute
// (x-bind:hidden), never x-show.
var hiddenToggledPanels = []string{".tag-dropdown", ".msg-more-menu", ".context-menu"}

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
	for _, class := range hiddenToggledPanels {
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

// TestToggledPanelsAreNotHiddenByBaseRule is the other direction: a base
// display:none was written for x-show, which sets an inline display. Under
// x-bind:hidden nothing overrides it, so clearing the attribute shows nothing.
// The right-click message menu was invisible this way.
func TestToggledPanelsAreNotHiddenByBaseRule(t *testing.T) {
	data, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	none := regexp.MustCompile(`(?:^|[;\s])display\s*:\s*none`)
	for _, class := range hiddenToggledPanels {
		rules := regexp.MustCompile(`(?s)(?:^|[^\w.\[-])`+regexp.QuoteMeta(class)+`\s*\{([^}]*)\}`).FindAllStringSubmatch(css, -1)
		for _, r := range rules {
			if none.MatchString(r[1]) {
				t.Errorf("%s has a base display:none; under x-bind:hidden it can never be shown", class)
			}
		}
	}
}
