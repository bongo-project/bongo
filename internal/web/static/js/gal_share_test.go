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

// countText runs from the live region's x-text, where the CSP build binds $el
// to that child element; the wording must come from init, not from $el.
func TestGalAutocompleteCountTextAvoidsEl(t *testing.T) {
	js := readJS(t, "bongo-components.js")
	start := strings.Index(js, "    countText() {")
	if start < 0 {
		t.Fatal("countText is missing")
	}
	end := strings.Index(js[start:], "\n    },")
	if end < 0 {
		t.Fatal("countText end not found")
	}
	if body := js[start : start+end]; strings.Contains(body, "$el") {
		t.Errorf("countText reads $el, which is the live region in the CSP build:\n%s", body)
	}
	if !strings.Contains(js, "this.countOne = this.$el.getAttribute('data-gal-count-one')") {
		t.Error("galAutocomplete init does not read the count wording")
	}
}
