package templates

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func ancestorHasClass(n *html.Node, class string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && hasClass(p, class) {
			return true
		}
	}
	return false
}

// The sidebar import control follows the contacts pattern (a tool-btn label
// around a visually hidden file input), the settings link is a nav item, and
// no class from the removed .cal-list* rules is used.
func TestCalendarSidebarStyleHooks(t *testing.T) {
	cals, _, _ := calendarFilterFixture()
	out := renderToString(t, CalendarSidebar(cals))
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}

	var fileInputs, settingsLinks, dots int
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		a := attrMap(n)
		if n.Data == "input" && a["type"] == "file" && a["name"] == "ics" {
			fileInputs++
			if !hasClass(n, "sr-only") {
				t.Error("file input is not visually hidden with sr-only")
			}
			if !ancestorHasClass(n, "tool-btn") {
				t.Error("file input is not inside a tool-btn label")
			}
			if n.Parent == nil || n.Parent.Data != "label" {
				t.Error("file input is not a child of a label")
			}
			if a["aria-label"] == "" {
				t.Error("file input has no aria-label")
			}
		}
		if n.Type == html.ElementNode && n.Data == "a" && attrMap(n)["href"] == "/calendar/settings" {
			settingsLinks++
			if !ancestorHasClass(n, "nav-item") {
				t.Error("the settings link is not inside .nav-item")
			}
		}
		if hasClass(n, "cal-color-dot") {
			dots++
		}
		for _, c := range strings.Fields(a["class"]) {
			if strings.HasPrefix(c, "cal-list") || c == "cal-dot" {
				t.Errorf("dead class %q in use", c)
			}
		}
	})
	if fileInputs != 1 || settingsLinks != 1 {
		t.Errorf("file inputs=%d settings links=%d, want 1 each", fileInputs, settingsLinks)
	}
	if dots != len(cals) {
		t.Errorf("colour dots=%d, want %d", dots, len(cals))
	}
	if strings.Contains(out, "calendar-sidebar-file") || strings.Contains(out, `class="import-form`) {
		t.Error("old file class or the mobile-hidden import-form class is used")
	}
}

// Without JS the file input is the only visible control, so the form needs a
// submit a mouse can reach.
func TestCalendarImportSubmitsWithoutJS(t *testing.T) {
	out := renderToString(t, CalendarSidebar(nil))
	i := strings.Index(out, `action="/calendar/import"`)
	if i < 0 {
		t.Fatal("no import form")
	}
	form, _, _ := strings.Cut(out[i:], "</form>")
	if !strings.Contains(form, `<noscript><button type="submit" class="tool-btn calendar-sidebar-btn">`) {
		t.Errorf("no visible submit for no-JS users: %s", form)
	}
}
