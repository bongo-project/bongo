package css_test

import (
	"os"
	"strings"
	"testing"
)

func TestMailLayoutModesHaveCSSRules(t *testing.T) {
	data, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatalf("read bongo.css: %v", err)
	}
	css := string(data)

	for _, selector := range []string{
		".mail-layout",
		".message-list-pane",
		".reading-pane",
		".layout-two-pane",
		".layout-three-pane",
	} {
		if !strings.Contains(css, selector) {
			t.Fatalf("bongo.css missing selector %s; mailbox layout toggle needs real responsive pane rules", selector)
		}
	}
}

func TestMobileMailWorkflowHasDedicatedRules(t *testing.T) {
	data, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatalf("read bongo.css: %v", err)
	}
	css := string(data)

	for _, selector := range []string{
		"@media (max-width: 768px)",
		".mobile-mail-nav",
		".mobile-overflow-menu",
	} {
		if !strings.Contains(css, selector) {
			t.Fatalf("bongo.css missing %s; mobile mail needs dedicated navigation and overflow actions", selector)
		}
	}
}

func TestCompactDensityHidesMessagePreview(t *testing.T) {
	data, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatalf("read bongo.css: %v", err)
	}
	css := string(data)
	const sel = `[data-density="compact"] .msg-preview`
	i := strings.Index(css, sel)
	if i < 0 {
		t.Fatalf("bongo.css missing %s; compact density must hide the preview line", sel)
	}
	end := strings.Index(css[i:], "}")
	if end < 0 || !strings.Contains(css[i:i+end], "display: none") {
		t.Fatalf("%s must set display: none", sel)
	}
}

func TestMessageRowsHaveNoFixedHeight(t *testing.T) {
	data, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatalf("read bongo.css: %v", err)
	}
	for _, old := range []string{".density-compact .msg-row { height", ".density-comfortable .msg-row { height", ".density-spacious .msg-row { height"} {
		if strings.Contains(string(data), old) {
			t.Errorf("fixed row height %q clips the four-line message row", old)
		}
	}
}

// ruleBody returns the declarations of the first rule whose selector is
// exactly sel.
func ruleBody(t *testing.T, css, sel string) string {
	t.Helper()
	i := strings.Index(css, "\n"+sel+" {")
	if i < 0 {
		t.Fatalf("bongo.css has no rule for %s", sel)
	}
	body := css[i+len(sel)+3:]
	return body[:strings.Index(body, "}")]
}

// Two-pane and three-pane once shared one rule, so the two toggle buttons
// produced the same layout. Two-pane stacks the list above the reading pane;
// three-pane puts them side by side next to the sidebar.
func TestTwoAndThreePaneLayoutsDiffer(t *testing.T) {
	data, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatalf("read bongo.css: %v", err)
	}
	css := string(data)
	if two := ruleBody(t, css, ".mail-layout.layout-two-pane"); !strings.Contains(two, "flex-direction: column") {
		t.Errorf("two-pane must stack the list above the reading pane: %s", two)
	}
	if three := ruleBody(t, css, ".mail-layout.layout-three-pane"); !strings.Contains(three, "grid-template-columns") {
		t.Errorf("three-pane must place list and reading pane side by side: %s", three)
	}
	if !strings.Contains(css, ".mail-view.mail-view-panes {") || !strings.Contains(css, ".mail-view-panes .message-list-pane {") {
		t.Error("pane modes must bound the view height so the list and reading pane scroll separately")
	}
}
