package templates

import (
	"context"
	"strings"
	"testing"
)

func TestRulesListSeparatorFollowsMatch(t *testing.T) {
	conds := []ConditionInfo{{"from", "contains", "AAA1"}, {"subject", "contains", "BBB2"}}
	acts := []ActionInfo{{Type: "flag"}}
	tests := []struct {
		name  string
		match string
		want  string
		not   string
	}{
		{"any shows or", "any", "rules.or", "rules.and"},
		{"all shows and", "all", "rules.and", "rules.or"},
		{"empty shows and", "", "rules.and", "rules.or"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := renderRulesList(t, []RuleInfo{{ID: "r1", Name: "n", Enabled: true, Match: tc.match, Conditions: conds, Actions: acts}})
			ctx := context.Background()
			if !strings.Contains(out, `<span class="rule-and">`+ruleT(ctx, tc.want)+`</span>`) {
				t.Errorf("missing separator %s", tc.want)
			}
			if strings.Contains(out, `<span class="rule-and">`+ruleT(ctx, tc.not)+`</span>`) {
				t.Errorf("unexpected separator %s", tc.not)
			}
		})
	}
}
