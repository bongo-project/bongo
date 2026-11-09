package js_test

import (
	"regexp"
	"strings"
	"testing"
)

// The shortcut help's close button is built in JavaScript, so no template test
// sees it. It must use the site's shared secondary button class: the bare
// "outline secondary" classes it used to carry are not styled by any
// stylesheet, and the browser drew a square default button.
func TestShortcutModalCloseUsesSharedButtonClass(t *testing.T) {
	src := readJS(t, "bongo-mail.js")
	m := regexp.MustCompile(`<button[^>]*id="shortcut-modal-close"[^>]*>`).FindString(src)
	if m == "" {
		t.Fatal("shortcut modal close button not found in bongo-mail.js")
	}
	if !strings.Contains(m, `btn-secondary`) {
		t.Errorf("close button %s does not use the btn-secondary class", m)
	}
	if regexp.MustCompile(`class="[^"]*\boutline\b`).MatchString(m) {
		t.Errorf("close button %s still carries the unstyled outline class", m)
	}
}
