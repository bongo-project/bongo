package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/rules"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

func matchForm(match *string) url.Values {
	form := formFor(row{"flag", "INBOX", "", ""})
	form.Add("cond_field", "from")
	form.Add("cond_operator", "contains")
	form.Add("cond_value", "a")
	if match != nil {
		form.Set("match", *match)
	}
	return form
}

func strPtr(s string) *string { return &s }

func TestParseRuleFormMatch(t *testing.T) {
	tests := []struct {
		name    string
		match   *string
		want    string
		wantErr string
	}{
		{"absent", nil, "all", ""},
		{"empty", strPtr(""), "all", ""},
		{"all", strPtr("all"), "all", ""},
		{"any", strPtr("any"), "any", ""},
		{"upper case", strPtr("ANY"), "", "rules.errMatch"},
		{"or", strPtr("or"), "", "rules.errMatch"},
		{"padded", strPtr(" any"), "", "rules.errMatch"},
		{"x", strPtr("x"), "", "rules.errMatch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule, errKey := parseForm(t, matchForm(tc.match))
			if errKey != tc.wantErr {
				t.Fatalf("errKey = %q, want %q", errKey, tc.wantErr)
			}
			if tc.wantErr == "" && rule.Match != tc.want {
				t.Errorf("Match = %q, want %q", rule.Match, tc.want)
			}
		})
	}
}

func TestRuleToInfoMatch(t *testing.T) {
	if got := ruleToInfo(&bongov1.FilterRule{Match: "any"}).Match; got != "any" {
		t.Errorf("Match = %q, want any", got)
	}
}

// TestRuleMatchSaveAndEdit drives Create, Update and Edit: a bad value saves
// nothing and leaves a stored rule untouched; a good one round-trips and the
// edit form shows it selected.
func TestRuleMatchSaveAndEdit(t *testing.T) {
	env := newMailTagsTestEnv(t)
	store := rules.NewRuleStore(env.ps, env.applier)
	h := NewRulesHandler(store, env.mailboxes, env.ps, nil)
	const user = "u1"

	post := func(handler http.HandlerFunc, path string, form url.Values, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", id)
		ctx := middleware.WithUserContext(r.Context(), user, user+"@example.com")
		r = r.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rc))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}

	for _, bad := range []string{"ANY", "or", "x"} {
		w := post(h.CreateRule, "/settings/rules", matchForm(strPtr(bad)), "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("create %q: status %d, want 400", bad, w.Code)
		}
	}
	if list, _ := store.List(user); len(list) != 0 {
		t.Fatalf("bad create saved %d rules", len(list))
	}

	if w := post(h.CreateRule, "/settings/rules", matchForm(strPtr("any")), ""); w.Code != http.StatusSeeOther {
		t.Fatalf("create any: status %d", w.Code)
	}
	list, _ := store.List(user)
	if len(list) != 1 || list[0].Match != "any" {
		t.Fatalf("stored rules = %v", list)
	}
	id := list[0].Id

	if w := post(h.UpdateRule, "/settings/rules/"+id, matchForm(strPtr("ANY")), id); w.Code != http.StatusBadRequest {
		t.Errorf("update ANY: status %d, want 400", w.Code)
	}
	if got, _ := store.Get(user, id); got.Match != "any" {
		t.Errorf("refused update changed Match to %q", got.Match)
	}

	// Edit form shows the stored mode selected.
	r := httptest.NewRequest(http.MethodGet, "/settings/rules/"+id, nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", id)
	r = r.WithContext(context.WithValue(middleware.WithUserContext(r.Context(), user, user+"@example.com"), chi.RouteCtxKey, rc))
	w := httptest.NewRecorder()
	h.EditRule(w, r)
	out := w.Body.String()
	if !strings.Contains(out, `<option value="any" selected`) || strings.Contains(out, `<option value="all" selected`) {
		t.Errorf("edit form does not select any:\n%s", out)
	}

	if w := post(h.UpdateRule, "/settings/rules/"+id, matchForm(strPtr("all")), id); w.Code != http.StatusSeeOther {
		t.Fatalf("update all: status %d", w.Code)
	}
	if got, _ := store.Get(user, id); got.Match != "all" {
		t.Errorf("Match = %q, want all", got.Match)
	}
}
