package templates

import (
	"html"
	"regexp"
	"strings"
	"testing"
)

// cspUnsafeTokens are constructs the Alpine CSP build's expression parser
// rejects, throwing "CSP Parser Error" when the expression first runs.
var cspUnsafeTokens = []string{"`", "=>", "document.", "window.", "const ", "let ", "function"}

// propertyAssignment matches "a.b = c" (not ==, === or !=).
var propertyAssignment = regexp.MustCompile(`[\w\]]\.\w+\s*=[^=]`)

// assertCSPSafe fails for any Alpine attribute in out that the CSP build
// cannot parse: an unsafe token, a statement list, a property write, or an
// inline x-data object that defines methods or holds a regex (move those into
// Alpine.data).
func assertCSPSafe(t *testing.T, name, out string) {
	t.Helper()
	matches := alpineAttr.FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		t.Fatalf("%s: no Alpine attributes found; the scan is not looking at anything", name)
	}
	for _, m := range matches {
		val := html.UnescapeString(m[2])
		if m[1] == "x-data" {
			if strings.HasPrefix(strings.TrimSpace(val), "{") && strings.ContainsAny(val, "(/") {
				t.Errorf("%s: x-data=%q is an inline object with methods or a regex; register it with Alpine.data", name, val)
			}
			continue
		}
		if strings.HasPrefix(m[1], "x-model") && strings.Contains(val, ".") {
			t.Errorf("%s: %s=%q writes a property, which the CSP build forbids; bind the value and call a setter", name, m[1], val)
		}
		if propertyAssignment.MatchString(val) {
			t.Errorf("%s: %s=%q assigns to a property, which the CSP build forbids", name, m[1], val)
		}
		for _, b := range cspUnsafeTokens {
			if strings.Contains(val, b) {
				t.Errorf("%s: %s=%q contains %q, which the Alpine CSP build cannot parse", name, m[1], val, b)
			}
		}
		if hasUnquotedSemicolon(val) {
			t.Errorf("%s: %s=%q is a statement list, which the Alpine CSP build cannot parse", name, m[1], val)
		}
	}
}

func TestRuleEditorAlpineExpressionsAreCSPSafe(t *testing.T) {
	edit := RuleInfo{
		ID:   "r1",
		Name: "Newsletters",
		Conditions: []ConditionInfo{
			{Field: "from", Operator: "contains", Value: `it's "quoted"; \ </script>`},
			{Field: "has_attachment", Operator: "equals", Value: "true"},
			{Field: "size", Operator: "greater_than", Value: "1000"},
		},
		Action: ActionInfo{Type: "tag", Tags: []string{"news", "o'brien"}},
	}
	for name, c := range map[string]RuleInfo{"new": {}, "edit": edit} {
		out := renderToString(t, RuleFormPage(c, []string{"INBOX", "Archive"}, name == "new", "a@b.c", ""))
		assertCSPSafe(t, "RuleFormPage("+name+")", out)
	}
}

// The editor's initial state travels as JSON in data attributes, so values
// with quotes, backslashes or markup arrive intact rather than being spliced
// into a JavaScript expression.
func TestRuleEditorStateIsJSON(t *testing.T) {
	got := ruleConditionsJSON([]ConditionInfo{{Field: "from", Operator: "contains", Value: `a'b"c\d`}})
	want := `[{"field":"from","operator":"contains","value":"a'b\"c\\d"}]`
	if got != want {
		t.Errorf("ruleConditionsJSON = %s, want %s", got, want)
	}
	if got := ruleConditionsJSON(nil); got != `[{"field":"from","operator":"contains","value":""}]` {
		t.Errorf("empty rule gets %s, want one default condition", got)
	}
	if got := ruleTagsJSON(nil); got != `[]` {
		t.Errorf("ruleTagsJSON(nil) = %s, want []", got)
	}
	if got := ruleActionType(""); got != "move" {
		t.Errorf("ruleActionType(\"\") = %q, want move", got)
	}
	initI18nForTest()
	labels := ruleOperatorLabelsJSON(t.Context())
	for _, op := range []string{"contains", "not_contains", "equals", "matches", "greater_than", "less_than"} {
		if !strings.Contains(labels, `"`+op+`":`) {
			t.Errorf("operator labels miss %s: %s", op, labels)
		}
	}
}

// A rule of another type can carry a stale folder target; it must not reach
// the hidden type=email forward field, where it would block submission.
func TestRuleEditorForwardFieldOnlyForForwardRules(t *testing.T) {
	for _, c := range []struct {
		action ActionInfo
		want   string
	}{
		{ActionInfo{Type: "tag", Target: "Archive", Tags: []string{"x"}}, ""},
		{ActionInfo{Type: "move", Target: "Archive"}, ""},
		{ActionInfo{Type: "forward", Target: "fwd@example.com"}, "fwd@example.com"},
	} {
		out := renderToString(t, RuleFormPage(RuleInfo{ID: "r1", Name: "n", Action: c.action}, []string{"INBOX", "Archive"}, false, "a@b.c", ""))
		m := regexp.MustCompile(`id="action_target_forward"[^>]*value="([^"]*)"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("%s: no forward field value in output", c.action.Type)
		}
		if m[1] != c.want {
			t.Errorf("%s: forward field value = %q, want %q", c.action.Type, m[1], c.want)
		}
	}
}
