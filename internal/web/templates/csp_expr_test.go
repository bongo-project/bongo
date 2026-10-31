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
		Actions: []ActionInfo{{Type: "tag", Tags: []string{"news", "o'brien"}}, {Type: "move", Target: "Archive"}},
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
	if got := ruleActionsJSON(nil); got != `[{"type":"move","target":"","forward":"","tags":[],"tagInput":""}]` {
		t.Errorf("ruleActionsJSON(nil) = %s, want one empty move row", got)
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
// the forward address of its row, where it would block submission.
func TestRuleEditorActionRowsCarryOnlyTheirOwnTarget(t *testing.T) {
	got := ruleActionsJSON([]ActionInfo{
		{Type: "tag", Target: "Archive", Tags: []string{"x"}},
		{Type: "move", Target: "Archive"},
		{Type: "forward", Target: "fwd@example.com"},
	})
	want := `[{"type":"tag","target":"","forward":"","tags":["x"],"tagInput":""},` +
		`{"type":"move","target":"Archive","forward":"","tags":[],"tagInput":""},` +
		`{"type":"forward","target":"","forward":"fwd@example.com","tags":[],"tagInput":""}]`
	if got != want {
		t.Errorf("ruleActionsJSON =\n%s\nwant\n%s", got, want)
	}
}

// The editor must post each of the four positional fields once per row, hidden
// or not, and offer a folder the rule names even if it was deleted since.
func TestRuleEditorRowFieldsAndStaleFolder(t *testing.T) {
	out := renderToString(t, RuleFormPage(RuleInfo{ID: "r1", Name: "n", Actions: []ActionInfo{{Type: "copy", Target: "Gone"}}}, []string{"INBOX", "Archive"}, false, "a@b.c", ""))
	for _, name := range []string{"action_type", "action_target", "action_target_forward", "action_tags"} {
		if n := strings.Count(out, `name="`+name+`"`); n != 1 {
			t.Errorf("%s appears %d times in the row template, want 1", name, n)
		}
	}
	if !strings.Contains(out, `<option value="Gone">Gone</option>`) {
		t.Error("a folder named by a saved action but missing from the mailbox list must still be an option")
	}
	if !strings.Contains(out, `data-default-folder="INBOX"`) {
		t.Error("missing default folder for new rows")
	}
}

func TestRuleFolderOptions(t *testing.T) {
	got := ruleFolderOptions([]string{"INBOX", "A"}, []ActionInfo{{Type: "move", Target: "A"}, {Type: "copy", Target: "Z"}, {Type: "forward", Target: "x@y.example"}, {Type: "tag", Target: "Q"}})
	if strings.Join(got, ",") != "INBOX,A,Z" {
		t.Errorf("options = %v, want INBOX,A,Z", got)
	}
	if ruleDefaultFolder([]string{"A", "B"}) != "A" || ruleDefaultFolder([]string{"A", "INBOX"}) != "INBOX" {
		t.Error("default folder must be INBOX when present, else the first folder")
	}
}
