package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

func ruleT(ctx context.Context, key string) string { return t(ctx, key) }

var ruleLocales = []string{"en", "de", "es", "fr"}

func TestRuleConditionText(t *testing.T) {
	tests := []struct {
		name  string
		cond  ConditionInfo
		label []string // message keys whose translation must appear
		value string
	}{
		{"from contains", ConditionInfo{"from", "contains", "boss@example.com"}, []string{"rules.fieldFrom", "rules.opContains"}, "boss@example.com"},
		{"to not_contains", ConditionInfo{"to", "not_contains", "me"}, []string{"rules.fieldTo", "rules.opNotContains"}, "me"},
		{"cc equals", ConditionInfo{"cc", "equals", "a@b.c"}, []string{"rules.fieldCc", "rules.opEquals"}, "a@b.c"},
		{"subject matches", ConditionInfo{"subject", "matches", "^\\[ci\\] <b>"}, []string{"rules.fieldSubject", "rules.opMatches"}, "^\\[ci\\] <b>"},
		{"body contains", ConditionInfo{"body", "contains", "invoice"}, []string{"rules.fieldBody", "rules.opContains"}, "invoice"},
		{"attachment true", ConditionInfo{"has_attachment", "equals", "true"}, []string{"rules.fieldHasAttachment", "rules.opEquals", "common.yes"}, ""},
		{"attachment false", ConditionInfo{"has_attachment", "equals", "false"}, []string{"rules.fieldHasAttachment", "rules.opEquals", "common.no"}, ""},
		{"size greater", ConditionInfo{"size", "greater_than", "1048576"}, []string{"rules.fieldSize", "rules.opGreaterThan"}, "1048576"},
		{"size less", ConditionInfo{"size", "less_than", "512"}, []string{"rules.fieldSize", "rules.opLessThan"}, "512"},
		{"header", ConditionInfo{"header:X-List-Id", "contains", "dev.example"}, []string{"rules.fieldHeader", "rules.opContains"}, "dev.example"},
		{"header name", ConditionInfo{"header:X-List-Id", "contains", "dev.example"}, []string{"rules.fieldHeader"}, "X-List-Id"},
		{"header uppercase prefix", ConditionInfo{"HEADER:X-Spam", "equals", "yes"}, []string{"rules.fieldHeader", "rules.opEquals"}, "yes"},
		{"header name upper", ConditionInfo{"HEADER:X-Spam", "equals", "yes"}, []string{"rules.fieldHeader"}, "X-Spam"},
		{"mixed case field", ConditionInfo{"From", "Contains", "bob"}, []string{"rules.fieldFrom", "rules.opContains"}, "bob"},
		{"mixed case attachment", ConditionInfo{"Has_Attachment", "equals", "TRUE"}, []string{"rules.fieldHasAttachment", "common.yes"}, ""},
		{"unknown field", ConditionInfo{"weird", "contains", "zzz"}, []string{"rules.opContains"}, "weird"},
		{"unknown operator", ConditionInfo{"from", "sounds_like", "bob"}, []string{"rules.fieldFrom"}, "sounds_like"},
	}
	for _, loc := range ruleLocales {
		ctx := middleware.WithLocale(context.Background(), loc)
		for _, tc := range tests {
			t.Run(loc+"/"+tc.name, func(t *testing.T) {
				got := ruleConditionText(ctx, tc.cond)
				if strings.TrimSpace(got) == "" {
					t.Fatal("empty summary")
				}
				if strings.Contains(got, "rules.") || strings.Contains(got, "common.") {
					t.Errorf("message id leaked: %q", got)
				}
				if tc.value != "" && !strings.Contains(got, tc.value) {
					t.Errorf("%q lacks value %q", got, tc.value)
				}
				for _, k := range tc.label {
					if want := ruleT(ctx, k); !strings.Contains(got, want) {
						t.Errorf("%q lacks %s=%q", got, k, want)
					}
				}
				if strings.HasSuffix(strings.TrimSpace(got), ":") {
					t.Errorf("dangling colon: %q", got)
				}
			})
		}
	}
}

func TestRuleActionText(t *testing.T) {
	tests := []struct {
		name   string
		action ActionInfo
		key    string
		needs  []string
		never  []string
	}{
		{"move", ActionInfo{Type: "move", Target: "Archive"}, "rules.actionMove", []string{"Archive"}, nil},
		{"copy", ActionInfo{Type: "copy", Target: "Backup"}, "rules.actionCopy", []string{"Backup"}, nil},
		{"forward", ActionInfo{Type: "forward", Target: "a@b.example"}, "rules.actionForward", []string{"a@b.example"}, nil},
		{"flag empty", ActionInfo{Type: "flag"}, "rules.actionFlag", nil, nil},
		{"flag target", ActionInfo{Type: "flag", Target: "TGT9"}, "rules.actionFlag", nil, []string{"TGT9"}},
		{"mark_read", ActionInfo{Type: "mark_read", Target: "TGT9"}, "rules.actionMarkRead", nil, []string{"TGT9"}},
		{"delete", ActionInfo{Type: "delete", Target: "TGT9"}, "rules.actionDelete", nil, []string{"TGT9"}},
		{"tag several", ActionInfo{Type: "tag", Tags: []string{"news", "later"}}, "rules.actionTag", []string{"news, later"}, nil},
		{"tag none", ActionInfo{Type: "tag"}, "rules.actionTag", nil, nil},
		{"empty type", ActionInfo{}, "rules.actionNone", nil, nil},
		{"unknown type", ActionInfo{Type: "explode", Target: "TGT7"}, "", []string{"explode", "TGT7"}, nil},
		{"mixed case move", ActionInfo{Type: "Move", Target: "Archive"}, "rules.actionMove", []string{"Archive"}, nil},
		{"mixed case tag", ActionInfo{Type: "TAG", Tags: []string{"news"}}, "rules.actionTag", []string{"news"}, []string{"TAG"}},
	}
	for _, loc := range ruleLocales {
		ctx := middleware.WithLocale(context.Background(), loc)
		for _, tc := range tests {
			t.Run(loc+"/"+tc.name, func(t *testing.T) {
				got := ruleActionText(ctx, tc.action)
				if strings.TrimSpace(got) == "" {
					t.Fatal("empty summary")
				}
				if strings.Contains(got, "rules.") {
					t.Errorf("message id leaked: %q", got)
				}
				if tc.key != "" && !strings.Contains(got, ruleT(ctx, tc.key)) {
					t.Errorf("%q lacks %s", got, tc.key)
				}
				for _, n := range tc.never {
					if strings.Contains(got, n) {
						t.Errorf("%q must not show %q", got, n)
					}
				}
				for _, n := range tc.needs {
					if !strings.Contains(got, n) {
						t.Errorf("%q lacks %q", got, n)
					}
				}
				if strings.HasSuffix(strings.TrimSpace(got), ":") {
					t.Errorf("dangling colon: %q", got)
				}
			})
		}
	}
}

func renderRulesList(t *testing.T, rules []RuleInfo) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RulesListPage(rules, "a@example.com", "").Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestRulesListPageStructure(t *testing.T) {
	rules := []RuleInfo{
		{ID: "r1", Name: "Multi", Priority: 1, Enabled: true,
			Conditions: []ConditionInfo{{"from", "contains", "AAA1"}, {"subject", "contains", "BBB2"}, {"size", "greater_than", "CCC3"}},
			Action:     ActionInfo{Type: "move", Target: "Archive"}},
		{ID: "r2", Name: "<script>x</script>", Priority: 2, Enabled: false,
			Conditions: []ConditionInfo{{"body", "matches", "<img src=x>"}},
			Action:     ActionInfo{Type: "tag", Tags: []string{"a<b"}}},
	}
	out := renderRulesList(t, rules)
	and := ruleT(context.Background(), "rules.and")
	if n := strings.Count(out, `<span class="rule-and">`+and+`</span>`); n != 2 {
		t.Errorf("want 2 localised and separators, got %d", n)
	}
	dis, en := ruleT(context.Background(), "rules.stateDisabled"), ruleT(context.Background(), "common.enabled")
	if !strings.Contains(out, `<span class="sr-only">`+en+`</span>`) || !strings.Contains(out, `<span class="sr-only">`+dis+`</span>`) {
		t.Error("state labels wrong")
	}
	r1 := out[strings.Index(out, `data-id="r1"`):strings.Index(out, `data-id="r2"`)]
	if !strings.Contains(r1, `<span class="sr-only">`+en+`</span>`) || strings.Contains(r1, `<span class="sr-only">`+dis+`</span>`) {
		t.Error("r1 must read Enabled")
	}
	if !strings.Contains(out[strings.Index(out, `data-id="r2"`):], `<span class="sr-only">`+dis+`</span>`) {
		t.Error("r2 must read Disabled")
	}
	if n := strings.Count(out, `class="condition"`); n != 4 {
		t.Errorf("want 4 condition chips, got %d", n)
	}
	for _, v := range []string{"AAA1", "BBB2", "CCC3"} {
		if !strings.Contains(out, v) {
			t.Errorf("missing condition value %s", v)
		}
	}
	for _, want := range []string{
		`id="rules-list"`, `id="sortable-rules"`, `data-id="r1"`, `data-id="r2"`,
		`data-move-dir="down"`, `data-move-id="r1"`, `data-move-dir="up"`,
		`id="test-results-r1"`, `hx-post="/settings/rules/r1/test"`,
		`hx-target="#test-results-r1"`, `hx-delete="/settings/rules/r1"`,
		`hx-confirm=`, `hx-target="closest .rule-item"`, `href="/settings/rules/new"`,
		`class="page-head"`, `x-data="messageMore"`, `aria-controls="rule-menu-r1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	r1menu := out[strings.Index(out, `hx-post="/settings/rules/r1/test"`):]
	r1menu = r1menu[:strings.Index(r1menu, "</button>")]
	if !strings.Contains(r1menu, `x-on:click="dismiss()"`) {
		t.Error("Test item must close the menu")
	}
	if strings.Contains(out, "<script>x") || strings.Contains(out, "<img src=x>") || strings.Contains(out, "a<b") {
		t.Error("user input not escaped")
	}
	if strings.Contains(out, "rules.") {
		t.Error("message id leaked")
	}
	if strings.Count(out, `<input`) > 0 && strings.Contains(out, `type="checkbox"`) {
		t.Error("row must not render a toggle input (no backend)")
	}
}

func TestRulesListPageEmpty(t *testing.T) {
	out := renderRulesList(t, nil)
	if !strings.Contains(out, ruleT(context.Background(), "rules.noRulesYet")) || !strings.Contains(out, `href="/settings/rules/new"`) {
		t.Error("empty state lost")
	}
}

func TestRuleTestResultsIsDiv(t *testing.T) {
	var buf bytes.Buffer
	if err := RuleTestResults("r1", 1, 5, []MailSample{{Subject: "S", From: "F", Date: "D"}}).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `<div id="test-results-r1"`) || strings.Contains(out, "<tr") {
		t.Errorf("unexpected markup: %s", out)
	}
}
