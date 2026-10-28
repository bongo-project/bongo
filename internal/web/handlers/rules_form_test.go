package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestParseRuleFormTargetOnlyForActionsThatUseOne: the editor posts the folder
// and forward fields whatever the action. A tag rule used to save the folder
// dropdown's value as its target, which the edit page then put into the hidden
// type=email forward field, and the browser refused to submit the form.
func TestParseRuleFormTargetOnlyForActionsThatUseOne(t *testing.T) {
	cases := []struct{ action, want string }{
		{"move", "Archive"},
		{"copy", "Archive"},
		{"forward", "fwd@example.com"},
		{"tag", ""},
		{"flag", ""},
		{"mark_read", ""},
		{"delete", ""},
	}
	for _, c := range cases {
		form := url.Values{
			"name": {"r"}, "action_type": {c.action},
			"action_target": {"Archive"}, "action_target_forward": {"fwd@example.com"},
			"action_tag": {"news"},
		}
		r := httptest.NewRequest(http.MethodPost, "/settings/rules", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		rule := (&RulesHandler{}).parseRuleForm(r)
		if got := rule.Action.Target; got != c.want {
			t.Errorf("%s: target = %q, want %q", c.action, got, c.want)
		}
	}
}
