package css_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readStatic(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// The sidebar colour dot had no size and so was invisible; the checkbox rules
// targeted classes no template uses.
func TestCalendarSidebarRules(t *testing.T) {
	css := readStatic(t, "bongo.css")

	// The month title kept the sidebar's white on a card that is white in the
	// light theme.
	if !regexp.MustCompile(`\.mini-calendar \.mini-cal-title[^{]*\{[^}]*color:\s*var\(--text-primary\)`).MatchString(css) {
		t.Error(".mini-cal-title in the card does not use the page text colour")
	}

	dot := regexp.MustCompile(`(?s)\n\.cal-color-dot\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if dot == nil || !strings.Contains(dot[1], "width:") || !strings.Contains(dot[1], "height:") {
		t.Error(".cal-color-dot has no width and height rule")
	}
	if !regexp.MustCompile(`\.calendar-list-label input\[type="checkbox"\]\s*\{[^}]*accent-color`).MatchString(css) {
		t.Error("calendar checkboxes have no accent-color rule")
	}
	if regexp.MustCompile(`\.cal-list|\.cal-dot\b`).MatchString(css) {
		t.Error("dead .cal-list* / .cal-dot rules remain")
	}
	if strings.Contains(css, "calendar-sidebar-file") {
		t.Error("rules for the removed .calendar-sidebar-file remain")
	}
	// The panel sits inside the dark sidebar: no light box border.
	group := regexp.MustCompile(`(?s)Calendar box visibility.*?\{`).FindString(css)
	if strings.Contains(group, ".calendar-list-panel") {
		t.Error(".calendar-list-panel is still in the box-border group")
	}
	if !regexp.MustCompile(`\.calendar-sidebar-btn:focus-within`).MatchString(css) {
		t.Error("import label has no :focus-within outline")
	}
}

func TestCalendarImportAutoSubmitSelector(t *testing.T) {
	js := readStatic(t, "../js/bongo.js")
	if !strings.Contains(js, `input[name="ics"]`) {
		t.Error(`bongo.js does not auto-submit the calendar import (input[name="ics"])`)
	}
	if !strings.Contains(js, `input[name="vcf"]`) {
		t.Error("bongo.js lost the contacts vCard auto-submit")
	}
}

// The event form's controls must take the theme's colours, not the browser's.
func TestEventFormControlsUseThemeColours(t *testing.T) {
	css := readStatic(t, "bongo.css")
	if !regexp.MustCompile(`\.event-form select \{[^}]*background: var\(--surface-1\);[^}]*color: var\(--text-primary\)`).MatchString(css) {
		t.Error(".event-form controls do not set the theme background and text colour")
	}
}
