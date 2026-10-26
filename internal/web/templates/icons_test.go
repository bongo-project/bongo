package templates

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func renderIcon(t *testing.T, name string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Icon(name, "extra").Render(context.Background(), &buf); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return buf.String()
}

var updateGolden = flag.Bool("update", false, "rewrite testdata/icons.golden")

var wantIconNames = []string{
	"alert", "archive", "arrow_right", "attach_sm", "bell", "calendar", "check", "chevron_left",
	"chevron_right", "draft", "folder", "forward", "inbox", "logo", "moon", "more", "paperclip",
	"plus", "plus_sm", "refresh", "reply", "reply_all", "search", "send", "settings", "shield",
	"sliders", "star", "star_fill", "tag", "trash", "users", "x",
}

func TestIconSetIsExactlyTheHandoffSet(t *testing.T) {
	var got []string
	for n := range iconSet {
		got = append(got, n)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantIconNames, ",") {
		t.Errorf("icon names = %v, want %v", got, wantIconNames)
	}
}

// The golden file was verified byte-for-byte against the handoff ICONS map and
// LOGO_GLYPH. It pins viewBox, attributes and inner markup of every icon.
// Regenerate with: go test ./internal/web/templates -run Golden -update
func TestIconMarkupGolden(t *testing.T) {
	var b strings.Builder
	for _, n := range wantIconNames {
		b.WriteString(n + "\t" + renderIcon(t, n) + "\n")
	}
	const path = "testdata/icons.golden"
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != b.String() {
		t.Errorf("icon markup differs from %s; rerun with -update only if the handoff changed", path)
	}
}

func TestIconBaseClass(t *testing.T) {
	cases := map[string]string{
		"inbox": `class="icon extra"`, "plus_sm": `class="icon-sm extra"`,
		"check": `class="extra"`, "logo": `class="extra"`,
	}
	for name, want := range cases {
		if out := renderIcon(t, name); !strings.Contains(out, want) {
			t.Errorf("%s: want %s in %s", name, want, out)
		}
	}
}

func TestIconSetRendersSafely(t *testing.T) {
	for name := range iconSet {
		out := renderIcon(t, name)
		if !strings.Contains(out, `aria-hidden="true"`) {
			t.Errorf("%s: missing aria-hidden: %s", name, out)
		}
		if strings.Contains(out, "style=") {
			t.Errorf("%s: inline style in output", name)
		}
		if strings.Contains(strings.ReplaceAll(out, `xmlns="http://www.w3.org/2000/svg"`, ""), "http") {
			t.Errorf("%s: unexpected external reference", name)
		}
		if !strings.Contains(out, "extra") || !strings.HasPrefix(out, "<svg") {
			t.Errorf("%s: unexpected output %s", name, out)
		}
	}
}

func TestIconKeepsSourceAttributes(t *testing.T) {
	out := renderIcon(t, "star_fill")
	if !strings.Contains(out, `stroke-width="1.5"`) || !strings.Contains(out, `fill="currentColor"`) {
		t.Errorf("star_fill lost its own attributes: %s", out)
	}
	if !strings.Contains(renderIcon(t, "plus_sm"), `class="icon-sm`) {
		t.Error("plus_sm should use icon-sm")
	}
}

func TestIconUnknownRendersNothing(t *testing.T) {
	for _, n := range []string{"nope", "", `"><script>`} {
		if out := renderIcon(t, n); out != "" {
			t.Errorf("Icon(%q) = %q, want empty", n, out)
		}
	}
}

func TestTemplatesOnlyUseKnownIcons(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`@Icon\("([^"]*)"`)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			if _, ok := iconSet[m[1]]; !ok {
				t.Errorf("%s uses unknown icon %q", f, m[1])
			}
		}
	}
}
