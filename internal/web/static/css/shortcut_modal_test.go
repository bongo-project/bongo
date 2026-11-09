package css_test

import (
	"strings"
	"testing"
)

func TestShortcutModalIsAnOnScreenDialog(t *testing.T) {
	css := mustReadCSSFile(t, "bongo-components.css")
	for _, want := range []string{
		".shortcut-modal-overlay {",
		"position: fixed",
		"inset: 0",
		"display: flex",
		"align-items: center",
		"justify-content: center",
		".shortcut-modal {",
		"max-height: calc(100vh - 32px)",
		"overflow-y: auto",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("shortcut modal CSS missing %q", want)
		}
	}
}
