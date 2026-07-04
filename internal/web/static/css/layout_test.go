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
