package css_test

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestBongoCSSVarsResolve(t *testing.T) {
	defRe := regexp.MustCompile(`(--[A-Za-z0-9_-]+)\s*:`)
	useRe := regexp.MustCompile(`var\(\s*(--[A-Za-z0-9_-]+)`)
	defined := map[string]bool{}
	for _, f := range []string{"componentlib/colors_and_type.css", "bongo.css", "generated_geometry.css"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range defRe.FindAllStringSubmatch(string(data), -1) {
			defined[m[1]] = true
		}
	}
	data, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatal(err)
	}
	// Properties that bongo.css reads with a literal fallback and never
	// defines. Defining them would change today's appearance, so they stay
	// undefined until the screen story that restyles them (rules editor tag
	// pills, 27.x calendar today highlight, 27.x tag input and log colours)
	// replaces the rule. A stale entry (now defined) fails below.
	allowUndefined := map[string]bool{
		"--primary":                       true,
		"--muted-color":                   true,
		"--form-element-border-color":     true,
		"--form-element-background-color": true,
		"--border-light":                  true,
		"--status-red":                    true,
		"--accent-bg-subtle":              true,
	}
	for n := range allowUndefined {
		if defined[n] {
			t.Errorf("%s is allowlisted as undefined but is now defined; drop it from the allowlist", n)
		}
	}
	missing := map[string]bool{}
	for _, m := range useRe.FindAllStringSubmatch(string(data), -1) {
		if !defined[m[1]] && !allowUndefined[m[1]] {
			missing[m[1]] = true
		}
	}
	var names []string
	for n := range missing {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > 0 {
		t.Errorf("bongo.css uses undefined custom properties: %v", names)
	}
}

func TestFontFacePerWeight(t *testing.T) {
	data, err := os.ReadFile("componentlib/colors_and_type.css")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{"100": "Thin", "200": "ExtraLight", "300": "Light", "400": "Regular",
		"500": "Medium", "600": "SemiBold", "700": "Bold", "800": "ExtraBold", "900": "Black"}
	files := map[string]string{"Geist": "Geist", "Geist Mono": "GeistMono"}
	blockRe := regexp.MustCompile(`@font-face\s*\{[^}]*\}`)
	famRe := regexp.MustCompile(`font-family:\s*"([^"]+)"`)
	wRe := regexp.MustCompile(`font-weight:\s*(\d+)`)
	urlRe := regexp.MustCompile(`url\("([^"]+)"\)`)
	count := map[string]map[string]int{}
	for _, blk := range blockRe.FindAllString(string(data), -1) {
		fam := famRe.FindStringSubmatch(blk)
		w := wRe.FindStringSubmatch(blk)
		u := urlRe.FindStringSubmatch(blk)
		if fam == nil || w == nil || u == nil {
			t.Errorf("incomplete @font-face: %s", blk)
			continue
		}
		if count[fam[1]] == nil {
			count[fam[1]] = map[string]int{}
		}
		count[fam[1]][w[1]]++
		if want := "/static/fonts/" + files[fam[1]] + "-" + names[w[1]] + ".woff2"; u[1] != want {
			t.Errorf("%s weight %s uses %s, want %s", fam[1], w[1], u[1], want)
		}
		if !strings.Contains(blk, "font-display: swap") {
			t.Errorf("%s weight %s lacks font-display: swap", fam[1], w[1])
		}
	}
	for _, fam := range []string{"Geist", "Geist Mono"} {
		for w := 100; w <= 900; w += 100 {
			if n := count[fam][strconv.Itoa(w)]; n != 1 {
				t.Errorf("%s weight %d: %d @font-face rules, want 1", fam, w, n)
			}
		}
	}
}

func TestDensityAttributeIsApplied(t *testing.T) {
	data, err := os.ReadFile("../js/bongo.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "setAttribute('data-density', this.density)") {
		t.Error("bongo.js applyDensity must set data-density on <html>")
	}
}

// Aliases introduced for previously-undefined properties must point at a
// token that exists in both the light and the dark token blocks, otherwise
// the alias silently keeps the light value in dark mode.
func TestIntroducedAliasesDefinedInBothThemes(t *testing.T) {
	introduced := []string{"--bongo-amber"}
	lib, err := os.ReadFile("componentlib/colors_and_type.css")
	if err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatal(err)
	}
	light := regexp.MustCompile(`:root:not\(\[data-theme="dark"\]\)\s*\{([^}]*)\}`).FindStringSubmatch(string(lib))
	dark := regexp.MustCompile(`\[data-theme="dark"\]\s*\{([^}]*)\}`).FindStringSubmatch(string(lib))
	if light == nil || dark == nil {
		t.Fatal("could not locate light and dark token blocks")
	}
	for _, alias := range introduced {
		m := regexp.MustCompile(regexp.QuoteMeta(alias) + `:\s*var\(\s*(--[A-Za-z0-9_-]+)\s*\)`).FindStringSubmatch(string(app))
		if m == nil {
			t.Errorf("%s: alias not found in bongo.css", alias)
			continue
		}
		def := regexp.MustCompile(regexp.QuoteMeta(m[1]) + `\s*:`)
		if !def.MatchString(light[1]) {
			t.Errorf("%s -> %s is not defined in the light token block", alias, m[1])
		}
		if !def.MatchString(dark[1]) {
			t.Errorf("%s -> %s is not defined in the dark token block", alias, m[1])
		}
	}
}
