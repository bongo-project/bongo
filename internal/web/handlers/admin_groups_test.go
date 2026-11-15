package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// failingGroupApplier refuses every command, as a lost leader would.
type failingGroupApplier struct{}

func (failingGroupApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error {
	return errors.New("not leader")
}

func adminGroupsReq(t *testing.T, method, target string, form url.Values, scope middleware.AdminScope, id string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rc := chi.NewRouteContext()
	if id != "" {
		rc.URLParams.Add("id", id)
	}
	ctx := middleware.WithUserContext(req.Context(), "admin", "admin@a.test")
	ctx = middleware.WithAdminScopeContext(ctx, scope)
	return req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rc))
}

// A write that fails must fail the request with a translated message and show
// the form again with what was typed, never a redirect that says it worked.
func TestAdminGroupsFailedWriteFailsTheRequest(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	if err := ps.Set([]byte("/system/domain/a.test"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	h := NewAdminGroupsHandler(store.NewGroupStore(ps, failingGroupApplier{}), ps, nil, discardLogger())

	rr := httptest.NewRecorder()
	h.Create(rr, adminGroupsReq(t, http.MethodPost, "/admin/groups",
		url.Values{"domain": {"a.test"}, "local": {"ops"}, "display_name": {"Ops Team"}},
		middleware.AdminScope{Global: true}, ""))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "The group could not be saved. Please try again.") {
		t.Errorf("no translated failure message:\n%s", body)
	}
	if !strings.Contains(body, "Ops Team") || !strings.Contains(body, `value="ops"`) {
		t.Error("the form did not keep the submitted values")
	}
	if loc := rr.Header().Get("Location"); loc != "" {
		t.Errorf("redirected to %q after a failed write", loc)
	}
}

func TestAdminGroupsCreateScopeAndInput(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	for _, d := range []string{"a.test", "b.test"} {
		if err := ps.Set([]byte("/system/domain/"+d), []byte("enabled")); err != nil {
			t.Fatal(err)
		}
	}
	profile := func(id, email, kind string) {
		data, _ := proto.Marshal(&bongov1.UserProfile{Email: email, Kind: kind})
		if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
			t.Fatal(err)
		}
	}
	profile("ann", "ann@a.test", "")
	profile("bob", "bob@b.test", "")
	profile("room", "room@a.test", "resource")
	gs := store.NewGroupStore(ps, &directApplierTags{ps: ps})
	h := NewAdminGroupsHandler(gs, ps, nil, discardLogger())
	domainAdmin := middleware.AdminScope{Domains: map[string]bool{"a.test": true}}

	create := func(scope middleware.AdminScope, form url.Values) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.Create(rr, adminGroupsReq(t, http.MethodPost, "/admin/groups", form, scope, ""))
		return rr
	}
	tests := []struct {
		name  string
		scope middleware.AdminScope
		form  url.Values
		want  int
		text  string
	}{
		{"domain admin, own domain", domainAdmin, url.Values{"domain": {"a.test"}, "local": {"ok"}, "member": {"ann"}}, http.StatusSeeOther, ""},
		{"domain admin, other domain", domainAdmin, url.Values{"domain": {"b.test"}, "local": {"x"}}, http.StatusForbidden, ""},
		{"another tenant's user as member", domainAdmin, url.Values{"domain": {"a.test"}, "local": {"pull"}, "member": {"bob"}}, http.StatusUnprocessableEntity, "existing users of the group"},
		{"another tenant's user as owner", domainAdmin, url.Values{"domain": {"a.test"}, "local": {"pull2"}, "owner": {"bob"}}, http.StatusUnprocessableEntity, "existing users of the group"},
		{"unknown user", domainAdmin, url.Values{"domain": {"a.test"}, "local": {"ghost"}, "member": {"nobody"}}, http.StatusUnprocessableEntity, "existing users of the group"},
		{"resource account", domainAdmin, url.Values{"domain": {"a.test"}, "local": {"res"}, "member": {"room"}}, http.StatusUnprocessableEntity, "existing users of the group"},
		{"bad policy", domainAdmin, url.Values{"domain": {"a.test"}, "local": {"pol"}, "posting_policy": {"everyone"}}, http.StatusUnprocessableEntity, "could not be saved"},
		{"no domain", domainAdmin, url.Values{"local": {"x"}}, http.StatusBadRequest, ""},
		{"address taken", domainAdmin, url.Values{"domain": {"a.test"}, "local": {"ok"}}, http.StatusConflict, "already in use"},
		{"taken ignoring case", domainAdmin, url.Values{"domain": {"A.test"}, "local": {"OK"}}, http.StatusConflict, "already in use"},
		{"malformed local part", domainAdmin, url.Values{"domain": {"a.test"}, "local": {"a b"}}, http.StatusUnprocessableEntity, "could not be saved"},
		{"unhosted domain", middleware.AdminScope{Global: true}, url.Values{"domain": {"c.test"}, "local": {"x"}}, http.StatusUnprocessableEntity, "could not be saved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := create(tt.scope, tt.form)
			if rr.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tt.want, rr.Body.String())
			}
			if tt.text != "" && !strings.Contains(rr.Body.String(), tt.text) {
				t.Errorf("body lacks %q:\n%s", tt.text, rr.Body.String())
			}
		})
	}
	groups, _ := gs.ListGroups("")
	if len(groups) != 1 || groups[0].Address != "ok@a.test" {
		t.Errorf("groups after the table = %v, want only ok@a.test", groups)
	}
}
