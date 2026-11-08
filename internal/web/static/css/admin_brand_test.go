package css_test

import (
	"regexp"
	"strings"
	"testing"
)

type cssRule struct {
	sel   string
	decls map[string]string
}

// topLevelRules returns the unindented "selector { body }" rules of bongo.css
// whose selector matches want, in source order, one entry per comma-separated
// selector. Rules inside @media blocks are indented and so are skipped.
func topLevelRules(t *testing.T, want *regexp.Regexp) []cssRule {
	t.Helper()
	src := cssCommentRe.ReplaceAllString(mustApplicationCSS(t), "")
	re := regexp.MustCompile(`(?m)^([^\s@}/][^{}]*)\{([^{}]*)\}`)
	var out []cssRule
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		for _, sel := range strings.Split(m[1], ",") {
			sel = strings.Join(strings.Fields(sel), " ")
			if want.MatchString(sel) {
				out = append(out, cssRule{sel, cssDecls(m[2])})
			}
		}
	}
	return out
}

func normColour(c string) string { return strings.ToLower(strings.ReplaceAll(c, " ", "")) }

// The admin topbar is a fixed slate; the wordmark inside it must not resolve
// to the theme text colour, which is dark in the light theme.
func TestAdminWordmarkReadableOnAdminTopbar(t *testing.T) {
	bg := ""
	for _, r := range topLevelRules(t, regexp.MustCompile(`^\.admin-layout \.topbar$`)) {
		if v := r.decls["background"]; v != "" {
			bg = normColour(v)
		}
	}
	if bg == "" {
		t.Fatal("no .admin-layout .topbar background")
	}

	// Rules that can match a .wordmark under .admin-layout .brand; the most
	// specific wins and, on a tie, the later one.
	var win *cssRule
	var winSpec [2]int
	for _, r := range topLevelRules(t, regexp.MustCompile(`\.wordmark$`)) {
		if strings.Contains(r.sel, ".login-card") || strings.Contains(r.sel, ".brand-mark") {
			continue
		}
		if r.decls["color"] == "" {
			continue
		}
		if sp := specificity(r.sel); specAtLeast(sp, winSpec) {
			r := r
			win, winSpec = &r, sp
		}
	}
	if win == nil {
		t.Fatal("no wordmark colour rule")
	}
	if !strings.Contains(win.sel, ".admin-layout") {
		t.Fatalf("winning wordmark rule %q is not admin scoped", win.sel)
	}

	col := normColour(win.decls["color"])
	if col == "inherit" {
		for _, r := range topLevelRules(t, regexp.MustCompile(`^\.admin-layout \.brand$`)) {
			if v := r.decls["color"]; v != "" {
				col = normColour(v)
			}
		}
	}
	if m := regexp.MustCompile(`^var\((--[\w-]+)\)$`).FindStringSubmatch(col); m != nil {
		for _, r := range topLevelRules(t, regexp.MustCompile(`^\.admin-layout$`)) {
			if v := r.decls[m[1]]; v != "" {
				col = normColour(v)
			}
		}
	}
	if strings.HasPrefix(col, "var(") || col == bg || col == "" {
		t.Fatalf("admin wordmark colour %q is unresolved or equals topbar background %q", col, bg)
	}
}

func TestAdminChromeRulesExist(t *testing.T) {
	css := mustApplicationCSS(t)
	for _, sel := range []string{".admin-footer", ".admin-sidebar-footer", ".admin-back-link", ".admin-back-link:hover"} {
		if body := ruleBody(t, css, sel); strings.TrimSpace(body) == "" {
			t.Errorf("%s rule is empty", sel)
		}
	}
}
