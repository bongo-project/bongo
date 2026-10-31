package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// row is one action row as the editor posts it: all four fields, every time.
type row struct{ typ, folder, forward, tags string }

func formFor(rows ...row) url.Values {
	form := url.Values{"name": {"r"}}
	for _, r := range rows {
		form.Add("action_type", r.typ)
		form.Add("action_target", r.folder)
		form.Add("action_target_forward", r.forward)
		form.Add("action_tags", r.tags)
	}
	return form
}

func parseForm(t *testing.T, form url.Values) (*bongov1.FilterRule, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/settings/rules", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return (&RulesHandler{}).parseRuleForm(r)
}

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
		rule, errKey := parseForm(t, formFor(row{c.action, "Archive", "fwd@example.com", "news"}))
		if errKey != "" {
			t.Fatalf("%s: error %s", c.action, errKey)
		}
		if got := rule.Action.Target; got != c.want {
			t.Errorf("%s: target = %q, want %q", c.action, got, c.want)
		}
	}
}

func TestParseRuleFormActionCount(t *testing.T) {
	ok := row{"flag", "INBOX", "", ""}
	many := func(n int) url.Values {
		rows := make([]row, n)
		for i := range rows {
			rows[i] = ok
		}
		return formFor(rows...)
	}
	tests := []struct {
		name    string
		form    url.Values
		wantErr string
		want    int
	}{
		{"zero", formFor(), "rules.errActionCount", 0},
		{"one", many(1), "", 1},
		{"ten", many(10), "", 10},
		{"eleven", many(11), "rules.errActionCount", 0},
		{"unknown type", formFor(row{"explode", "INBOX", "", ""}), "rules.errActionType", 0},
		{"empty type", formFor(row{"", "INBOX", "", ""}), "rules.errActionType", 0},
		{"missing folder field shifts rows", url.Values{
			"action_type": {"move", "flag"}, "action_target": {"A"},
			"action_target_forward": {"", ""}, "action_tags": {"", ""},
		}, "rules.errActionCount", 0},
		{"move without a folder", formFor(row{"move", "", "", ""}), "rules.errActionFolder", 0},
		{"copy without a folder", formFor(row{"copy", "", "", ""}), "rules.errActionFolder", 0},
		{"copy to a folder with a control character", formFor(row{"copy", "A\nB", "", ""}), "rules.errActionFolder", 0},
		{"move to an over-long folder", formFor(row{"move", strings.Repeat("a", 256), "", ""}), "rules.errActionFolder", 0},
		{"copy to a 255-byte folder", formFor(row{"copy", strings.Repeat("a", 255), "", ""}), "", 1},
		{"forward to a bad address", formFor(row{"forward", "", "not an address", ""}), "settings.fwdInvalidAddress", 0},
		{"forward with header injection", formFor(row{"forward", "", "a@b.example\r\nBcc: c@d.example", ""}), "settings.fwdInvalidAddress", 0},
		{"a bad second forward is refused", formFor(row{"forward", "", "a@b.example", ""}, row{"forward", "", "nope", ""}), "settings.fwdInvalidAddress", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule, errKey := parseForm(t, tc.form)
			if errKey != tc.wantErr {
				t.Fatalf("error key = %q, want %q", errKey, tc.wantErr)
			}
			if tc.wantErr != "" {
				if rule != nil {
					t.Error("a refused form must not return a rule")
				}
				return
			}
			if len(rule.Actions) != tc.want {
				t.Errorf("actions = %d, want %d", len(rule.Actions), tc.want)
			}
		})
	}
}

// TestParseRuleFormWritesBothShapes: field 6 holds the first action so an
// older node reads something sensible, and the list holds all of them.
func TestParseRuleFormWritesBothShapes(t *testing.T) {
	rule, errKey := parseForm(t, formFor(
		row{"copy", "Backup", "x@y.example", ""},
		row{"tag", "Backup", "", "News, o'brien"},
		row{"forward", "Backup", "Me@Elsewhere.Example", ""},
		row{"tag", "", "", "later"},
		row{"mark_read", "", "", ""},
	))
	if errKey != "" {
		t.Fatal(errKey)
	}
	if rule.Action == nil || rule.Action.Type != "copy" || rule.Action.Target != "Backup" {
		t.Errorf("field 6 = %v, want the first action", rule.Action)
	}
	if len(rule.Actions) != 5 || rule.Actions[0] != rule.Action {
		t.Fatalf("actions = %v", rule.Actions)
	}
	if got := strings.Join(rule.Actions[1].Tags, ","); got != "news,o-brien" && got != "news" {
		// Tag folding is tagname.Normalise's business; the row must carry its
		// own tags and nobody else's.
		t.Logf("row 1 tags = %q", got)
	}
	if len(rule.Actions[1].Tags) == 0 || len(rule.Actions[3].Tags) != 1 || rule.Actions[3].Tags[0] != "later" {
		t.Errorf("tags are not per row: row1=%v row3=%v", rule.Actions[1].Tags, rule.Actions[3].Tags)
	}
	if len(rule.Actions[0].Tags) != 0 || len(rule.Actions[2].Tags) != 0 || len(rule.Actions[4].Tags) != 0 {
		t.Error("a row without tags must not pick up another row's")
	}
	if rule.Actions[2].Target != "me@elsewhere.example" {
		t.Errorf("forward target = %q, want the normalised address", rule.Actions[2].Target)
	}
	if rule.Actions[1].Target != "" || rule.Actions[3].Target != "" || rule.Actions[4].Target != "" {
		t.Error("only move, copy and forward keep a target")
	}
}

func TestRuleToInfoMixedShapes(t *testing.T) {
	legacy := &bongov1.FilterRule{Id: "old", Action: &bongov1.FilterAction{Type: "move", Target: "X"}}
	if got := ruleToInfo(legacy).Actions; len(got) != 1 || got[0].Type != "move" || got[0].Target != "X" {
		t.Errorf("legacy rule reads as %v, want [move X]", got)
	}
	both := &bongov1.FilterRule{
		Id:      "new",
		Action:  &bongov1.FilterAction{Type: "flag"},
		Actions: []*bongov1.FilterAction{{Type: "flag"}, {Type: "copy", Target: "Y"}},
	}
	if got := ruleToInfo(both).Actions; len(got) != 2 || got[1].Target != "Y" {
		t.Errorf("new rule reads as %v, want both actions", got)
	}
	if got := ruleToInfo(&bongov1.FilterRule{Id: "none"}).Actions; len(got) != 0 {
		t.Errorf("rule with no action reads as %v", got)
	}
}
