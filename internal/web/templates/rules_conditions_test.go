package templates

import "testing"

func TestRuleConditionsJSONSplitsHeader(t *testing.T) {
	got := ruleConditionsJSON([]ConditionInfo{
		{Field: "header:List-Id", Operator: "contains", Value: "dev"},
		{Field: "mime_type", Operator: "equals", Value: "application/pdf"},
	})
	want := `[{"field":"header","header":"List-Id","operator":"contains","value":"dev"},` +
		`{"field":"mime_type","operator":"equals","value":"application/pdf"}]`
	if got != want {
		t.Errorf("ruleConditionsJSON = %s, want %s", got, want)
	}
}
