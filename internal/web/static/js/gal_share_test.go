package js_test

import (
	"strings"
	"testing"
)

// galAutocomplete serves the compose fields and, with data-gal-share, the
// share forms. Compose must keep its own query; the share mode adds share=1
// and the combobox helpers.
func TestGalAutocompleteShareMode(t *testing.T) {
	js := readJS(t, "bongo-components.js")
	start := strings.Index(js, "Alpine.data('galAutocomplete'")
	if start < 0 {
		t.Fatal("galAutocomplete is missing")
	}
	end := strings.Index(js[start:], "Alpine.data('recipientPills'")
	if end < 0 {
		t.Fatal("recipientPills marker is missing")
	}
	component := js[start : start+end]
	for _, want := range []string{
		"'&share=1'", "data-gal-share", "'&contact_groups=1'",
		"listboxId()", "optionId(i)", "activeId()", "isSelected(i)", "expanded()", "countText()",
		"data-gal-count-one", "data-gal-count-many", "++galSeq",
	} {
		if !strings.Contains(component, want) && !strings.Contains(js, want) {
			t.Errorf("galAutocomplete lacks %q", want)
		}
	}
}
