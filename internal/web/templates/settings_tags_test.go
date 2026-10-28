package templates

import (
	"context"
	"html"
	"os"
	"regexp"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// translated returns the translation of key, the same way a template does.
func translated(key string) string { return t(context.Background(), key) }

func settingsPages(t *testing.T) map[string]string {
	t.Helper()
	prefs := &bongov1.UserPreferences{InfiniteScroll: true, ForwardKeepCopy: true}
	return map[string]string{
		"tags":     renderToString(t, TagSettingsPage(TagSettingsData{Email: "a@b.c"})),
		"settings": renderToString(t, SettingsPage(prefs, "a@b.c", "", ForwardView{})),
		"security": renderToString(t, SecuritySettings(false, "a@b.c", "")),
	}
}

func TestSettingsAlpineExpressionsAreCSPSafe(t *testing.T) {
	bad := []string{"`", "=>", "document.", "window.", "const ", "let ", "function"}
	for name, out := range settingsPages(t) {
		matches := alpineAttr.FindAllStringSubmatch(out, -1)
		if len(matches) == 0 {
			t.Fatalf("%s: no Alpine attributes found; the scan is not looking at anything", name)
		}
		for _, m := range matches {
			val := html.UnescapeString(m[2])
			if m[1] == "x-data" && strings.HasPrefix(val, "tagSettings(") {
				continue // JSON palette argument, not an expression the parser evaluates
			}
			for _, b := range bad {
				if strings.Contains(val, b) {
					t.Errorf("%s: %s=%q contains %q, which the Alpine CSP build cannot parse", name, m[1], val, b)
				}
			}
		}
	}
}

// srcAlpineAttr finds x-*, : and @ attributes in templ source, including ones
// whose value holds a backtick; Go expressions in braces are not matched.
var srcAlpineAttr = regexp.MustCompile("\\s((?:x-[\\w:.-]+)|(?:[:@][\\w:.-]+))=\"([^\"]*)\"")

func TestSettingsTagsTemplSourceHasNoTemplateLiterals(t *testing.T) {
	src, err := os.ReadFile("settings_tags.templ")
	if err != nil {
		t.Fatal(err)
	}
	matches := srcAlpineAttr.FindAllStringSubmatch(string(src), -1)
	if len(matches) < 10 {
		t.Fatalf("only %d Alpine attributes found in settings_tags.templ; the scan is broken", len(matches))
	}
	for _, m := range matches {
		if strings.Contains(m[2], "`") {
			t.Errorf("settings_tags.templ: %s=%q contains a backtick; the Alpine CSP build cannot parse template literals", m[1], m[2])
		}
	}
}

func TestTagSettingsUsesComponentMethodsAndTranslatedLabels(t *testing.T) {
	out := renderToString(t, TagSettingsPage(TagSettingsData{Email: "a@b.c"}))
	for _, want := range []string{
		`:title="countTitle(tag)"`,
		`:aria-label="colourLabel(idx)"`,
		`:aria-label="editLabel(tag)"`,
		`:aria-label="deleteLabel(tag)"`,
		`data-count-label="` + html.EscapeString(translated("tag.countTitle")) + `"`,
		`data-colour-label="` + html.EscapeString(translated("tag.colourLabel")) + `"`,
		`data-edit-tag-label="` + html.EscapeString(translated("tag.editLabel")) + `"`,
		`data-delete-tag-label="` + html.EscapeString(translated("tag.deleteLabel")) + `"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %s", want)
		}
	}
	if strings.Contains(out, "Edit tag ${") {
		t.Error("hardcoded English template literal still rendered")
	}
}

func TestTagLabelFormatsKeepPlaceholder(t *testing.T) {
	for _, k := range []string{"tag.countTitle", "tag.colourLabel", "tag.editLabel", "tag.deleteLabel"} {
		if v := translated(k); !strings.Contains(v, "%s") || v == k {
			t.Errorf("%s = %q; want a translation containing %%s", k, v)
		}
	}
}

func TestSettingsTogglesAreCheckboxesInLabels(t *testing.T) {
	out := settingsPages(t)["settings"]
	for _, name := range []string{"infinite_scroll", "forward_keep_copy"} {
		re := regexp.MustCompile(`(?s)<label class="toggle">\s*<input type="checkbox" name="` + name + `"[^>]*>`)
		if !re.MatchString(out) {
			t.Errorf("%s is not a checkbox input inside a label.toggle", name)
		}
	}
}
