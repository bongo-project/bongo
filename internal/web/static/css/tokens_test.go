package css_test

import (
	"os"
	"regexp"
	"slices"
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

// cssBlock returns the body of the first brace block opened at the end of loc
// (a regexp match ending in "{"), balancing nested braces.
func cssBlock(src string, loc []int) string {
	depth, start := 1, loc[1]
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start:i]
			}
		}
	}
	return ""
}

var cssCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)

func cssDecls(body string) map[string]string {
	out := map[string]string{}
	for _, d := range strings.Split(cssCommentRe.ReplaceAllString(body, ""), ";") {
		name, val, ok := strings.Cut(d, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(name)] = strings.Join(strings.Fields(val), " ")
	}
	return out
}

// cssRules parses flat "selector { body }" rules (no nesting).
func cssRules(body string) map[string]map[string]string {
	out := map[string]map[string]string{}
	body = cssCommentRe.ReplaceAllString(body, "")
	for {
		open := strings.Index(body, "{")
		if open < 0 {
			return out
		}
		end := strings.Index(body, "}")
		sel := strings.Join(strings.Fields(body[:open]), " ")
		if out[sel] == nil {
			out[sel] = map[string]string{}
		}
		for k, v := range cssDecls(body[open+1 : end]) {
			out[sel][k] = v
		}
		body = body[end+1:]
	}
}

var (
	specNotRe  = regexp.MustCompile(`:not\(([^)]*)\)`)
	specAttrRe = regexp.MustCompile(`\[[^\]]*\]`)
	specClsRe  = regexp.MustCompile(`\.[A-Za-z_-][\w-]*`)
	specPseuRe = regexp.MustCompile(`:[a-z-]+`)
	specElemRe = regexp.MustCompile(`(^|[\s>+~])[a-z][a-z0-9]*`)
)

// specificity returns (classes+attributes+pseudo-classes, elements) for a
// selector without ids; enough for the selectors in these files.
func specificity(sel string) [2]int {
	var s [2]int
	for {
		m := specNotRe.FindStringSubmatchIndex(sel)
		if m == nil {
			break
		}
		inner := specificity(sel[m[2]:m[3]])
		s[0] += inner[0]
		s[1] += inner[1]
		sel = sel[:m[0]] + sel[m[1]:]
	}
	s[0] += len(specAttrRe.FindAllString(sel, -1))
	sel = specAttrRe.ReplaceAllString(sel, "")
	s[0] += len(specClsRe.FindAllString(sel, -1))
	s[0] += len(specPseuRe.FindAllString(sel, -1))
	s[1] += len(specElemRe.FindAllString(sel, -1))
	return s
}

func specAtLeast(a, b [2]int) bool {
	return a[0] > b[0] || (a[0] == b[0] && a[1] >= b[1])
}

// Public pages ship data-theme="auto" and no script, so the only way they can
// render dark is CSS keyed on prefers-color-scheme. Each dark token block is
// repeated for the auto state; this guards against drift and against the auto
// selector losing the cascade to the light blocks (which also match "auto").
func TestAutoThemeFollowsDarkPreference(t *testing.T) {
	libB, err := os.ReadFile("componentlib/colors_and_type.css")
	if err != nil {
		t.Fatal(err)
	}
	appB, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatal(err)
	}
	lib, app := string(libB), string(appB)

	mediaRe := regexp.MustCompile(`@media \(prefers-color-scheme: dark\)\s*\{`)
	loc := mediaRe.FindStringIndex(app)
	if loc == nil {
		t.Fatal("bongo.css has no @media (prefers-color-scheme: dark) block")
	}
	auto := cssRules(cssBlock(app, loc))

	// Gather declarations per selector across every auto rule, last wins.
	declsFor := func(sel string) map[string]string {
		got := map[string]string{}
		for s, d := range auto {
			if slices.Contains(strings.Split(s, ", "), sel) {
				for k, v := range d {
					got[k] = v
				}
			}
		}
		return got
	}

	type source struct {
		name     string
		src      string
		re       *regexp.Regexp
		autoSel  string
		lightSel string
	}
	sources := []source{
		{"library dark tokens", lib, regexp.MustCompile(`(?m)^\[data-theme="dark"\]\s*\{`), `:root[data-theme="auto"]`, `:root:not([data-theme="dark"])`},
		{"app dark tokens", app, regexp.MustCompile(`(?m)^\[data-theme="dark"\]\s*\{`), `:root[data-theme="auto"]`, `:root:not([data-theme="dark"])`},
		{"bongo accent dark", app, regexp.MustCompile(`html\[data-theme="dark"\]\[data-accent="bongo"\]\s*\{`), `html[data-theme="auto"][data-accent="bongo"]`, `html:not([data-theme="dark"])[data-accent="bongo"]`},
	}
	for _, s := range sources {
		l := s.re.FindStringIndex(s.src)
		if l == nil {
			t.Fatalf("%s: block not found", s.name)
		}
		want := cssDecls(cssBlock(s.src, l))
		if len(want) == 0 {
			t.Fatalf("%s: parsed no declarations", s.name)
		}
		got := declsFor(s.autoSel)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: auto state has %s = %q, dark has %q", s.name, k, got[k], v)
			}
		}
		if !specAtLeast(specificity(s.autoSel), specificity(s.lightSel)) {
			t.Errorf("%s: %s (%v) loses to the light selector %s (%v)", s.name, s.autoSel, specificity(s.autoSel), s.lightSel, specificity(s.lightSel))
		}
		// Same file: equal specificity is decided by source order. The library
		// is linked before bongo.css, so only bongo.css selectors need the check.
		if s.src == app {
			if li := strings.Index(app, s.lightSel+" {"); li >= 0 && loc[0] < li {
				t.Errorf("%s: auto block precedes the light block, equal specificity would lose", s.name)
			}
		}
	}

	// Component overrides the public pages use under the dark theme.
	for _, sel := range []string{".flash-success", ".flash-warning"} {
		dl := regexp.MustCompile(`(?m)^\[data-theme="dark"\] ` + regexp.QuoteMeta(sel) + `\s*\{`).FindStringIndex(app)
		if dl == nil {
			t.Fatalf("dark rule for %s not found", sel)
		}
		want := cssDecls(cssBlock(app, dl))
		got := declsFor(`:root[data-theme="auto"] ` + sel)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: auto state has %s = %q, dark has %q", sel, k, got[k], v)
			}
		}
	}
}

// Every custom property the explicit dark Bongo-accent blocks set must also be
// set for the auto state under the dark media query, or the library's
// :root[data-theme="auto"] rule wins for the missing ones (for example
// --brand-text turning white on the amber accent).
func TestAutoDarkAccentCoversExplicitDark(t *testing.T) {
	appB, err := os.ReadFile("bongo.css")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appB)

	want := map[string]bool{}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`html\[data-theme="dark"\]\[data-accent="bongo"\],\s*html\[data-theme="dark"\]:not\(\[data-accent\]\)\s*\{`),
		regexp.MustCompile(`html\[data-theme="dark"\]\[data-accent="bongo"\]\s*\{`),
	} {
		l := re.FindStringIndex(app)
		if l == nil {
			t.Fatalf("explicit dark accent block %s not found", re)
		}
		for k := range cssDecls(cssBlock(app, l)) {
			want[k] = true
		}
	}
	if len(want) == 0 {
		t.Fatal("parsed no explicit dark accent properties")
	}

	loc := regexp.MustCompile(`@media \(prefers-color-scheme: dark\)\s*\{`).FindStringIndex(app)
	if loc == nil {
		t.Fatal("bongo.css has no @media (prefers-color-scheme: dark) block")
	}
	rules := cssRules(cssBlock(app, loc))
	const bongoSel = `html[data-theme="auto"][data-accent="bongo"], html[data-theme="auto"]:not([data-accent])`
	got, ok := rules[bongoSel]
	if !ok {
		t.Fatalf("auto-dark accent rule %q not found", bongoSel)
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("auto-dark accent rule is missing %s set by explicit dark", k)
		}
	}
	// Must beat the library's :root[data-theme="auto"] token rule.
	for _, sel := range strings.Split(bongoSel, ", ") {
		if !specAtLeast(specificity(sel), specificity(`:root[data-theme="auto"]`)) {
			t.Errorf("%s loses to :root[data-theme=\"auto\"]", sel)
		}
	}
}
