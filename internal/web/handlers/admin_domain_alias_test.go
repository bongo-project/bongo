package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
)

func aliasCreate(t *testing.T, env *adminScopeTestEnv, domain, aliasOf string, asGlobal bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"domain": {domain}}
	if aliasOf != "" {
		form.Set("alias_of", aliasOf)
	}
	return postToHandler(t, env.h, env.h.CreateDomain, http.MethodPost, "/admin/domains", form, nil,
		func(r *http.Request) *http.Request {
			if asGlobal {
				return withGlobalAdmin(r, env.globalAdminID)
			}
			return withDomainAdmin1(r, env.domainAdmin1ID)
		})
}

func aliasOfValue(env *adminScopeTestEnv, domain string) (string, bool) {
	b, err := env.ps.Get([]byte("/system/domain/" + domain + "/alias_of"))
	return string(b), err == nil
}

func TestCreateDomain_Alias(t *testing.T) {
	const target = "target.alias.test"
	tests := []struct {
		name       string
		setup      func(t *testing.T, env *adminScopeTestEnv)
		domain     string
		aliasOf    string
		global     bool
		wantStatus int
		wantStored string // alias_of value, "" = key must be absent
		wantHosted bool
	}{
		{name: "global admin creates alias", domain: "Alias.Test", aliasOf: "Target.Alias.Test", global: true,
			wantStatus: http.StatusSeeOther, wantStored: target, wantHosted: true},
		{name: "plain domain stores no alias_of", domain: "plain.test", global: true,
			wantStatus: http.StatusSeeOther, wantHosted: true},
		{name: "domain admin is refused", domain: "alias.test", aliasOf: target, global: false,
			wantStatus: http.StatusForbidden},
		{name: "self alias refused", domain: "selfy.test", aliasOf: "selfy.test", global: true,
			wantStatus: http.StatusBadRequest},
		{name: "unknown target refused", domain: "alias.test", aliasOf: "nowhere.test", global: true,
			wantStatus: http.StatusBadRequest},
		{name: "chain refused", domain: "second.test", aliasOf: "first.test", global: true,
			setup: func(t *testing.T, env *adminScopeTestEnv) {
				if w := aliasCreate(t, env, "first.test", target, true); w.Code != http.StatusSeeOther {
					t.Fatalf("setup alias: %d", w.Code)
				}
			},
			wantStatus: http.StatusBadRequest},
		{name: "slash in alias refused", domain: "x/owner", aliasOf: target, global: true,
			wantStatus: http.StatusBadRequest},
		{name: "slash in target refused", domain: "alias.test", aliasOf: target + "/owner", global: true,
			wantStatus: http.StatusBadRequest},
		{name: "whitespace in alias refused", domain: "al ias.test", aliasOf: target, global: true,
			wantStatus: http.StatusBadRequest},
		{name: "control character in target refused", domain: "alias.test", aliasOf: "t\x00.test", global: true,
			wantStatus: http.StatusBadRequest},
		{name: "existing domain refused", domain: "taken.test", aliasOf: target, global: true,
			setup: func(t *testing.T, env *adminScopeTestEnv) {
				cascadePut(t, env, "/system/domain/taken.test", []byte("enabled"))
			},
			wantStatus: http.StatusConflict, wantHosted: true},
		{name: "stale alias_of is purged by a plain create", domain: "stale.test", global: true,
			setup: func(t *testing.T, env *adminScopeTestEnv) {
				cascadePut(t, env, "/system/domain/stale.test/alias_of", []byte(target))
			},
			wantStatus: http.StatusSeeOther, wantHosted: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newAdminScopeTestEnv(t)
			cascadePut(t, env, "/system/domain/"+target, []byte("enabled"))
			if tc.setup != nil {
				tc.setup(t, env)
			}
			w := aliasCreate(t, env, tc.domain, tc.aliasOf, tc.global)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, tc.wantStatus, w.Body.String())
			}
			lower := strings.ToLower(tc.domain)
			// A refused request must leave nothing behind (an existing domain
			// keeps its key but gains no alias_of), and the pre-seeded stale
			// key of the purge case must be gone.
			if got, ok := aliasOfValue(env, lower); got != tc.wantStored || ok != (tc.wantStored != "") {
				t.Fatalf("alias_of = %q (present %v), want %q", got, ok, tc.wantStored)
			}
			if cascadeHas(env, "/system/domain/"+lower) != tc.wantHosted {
				t.Fatalf("hosted = %v, want %v", !tc.wantHosted, tc.wantHosted)
			}
		})
	}
}

func TestDeleteDomain_Alias(t *testing.T) {
	const target = "target.alias.test"
	setup := func(t *testing.T) *adminScopeTestEnv {
		env := newAdminScopeTestEnv(t)
		cascadePut(t, env, "/system/domain/"+target, []byte("enabled"))
		for _, a := range []string{"b.alias.test", "a.alias.test"} {
			if w := aliasCreate(t, env, a, target, true); w.Code != http.StatusSeeOther {
				t.Fatalf("create %s: %d", a, w.Code)
			}
		}
		return env
	}

	t.Run("target with aliases is refused and listed", func(t *testing.T) {
		env := setup(t)
		w := cascadeDeleteDomain(t, env, target)
		loc, _ := url.QueryUnescape(w.Header().Get("Location"))
		want := fmt.Sprintf(i18n.T("en", "admin.domainHasAliases"), target, "a.alias.test, b.alias.test")
		if w.Code != http.StatusSeeOther || !strings.Contains(loc, want) {
			t.Fatalf("status %d location %q, want %q", w.Code, loc, want)
		}
		if !cascadeHas(env, "/system/domain/"+target) {
			t.Fatal("target was deleted")
		}
	})

	t.Run("deleting an alias removes alias_of and then the target can go", func(t *testing.T) {
		env := setup(t)
		if w := cascadeDeleteDomain(t, env, "a.alias.test"); w.Code != http.StatusSeeOther {
			t.Fatalf("status %d", w.Code)
		}
		if _, ok := aliasOfValue(env, "a.alias.test"); ok || cascadeHas(env, "/system/domain/a.alias.test") {
			t.Fatal("alias keys survived delete")
		}
		if _, ok := aliasOfValue(env, "b.alias.test"); !ok {
			t.Fatal("sibling alias lost its alias_of")
		}
		cascadeDeleteDomain(t, env, "b.alias.test")
		if w := cascadeDeleteDomain(t, env, target); !strings.Contains(w.Header().Get("Location"), "Domain+deleted") {
			t.Fatalf("target not deleted: %q", w.Header().Get("Location"))
		}
	})
}

func TestSetCatchAll_AliasDomainRefused(t *testing.T) {
	const target = "target.alias.test"
	env := newAdminScopeTestEnv(t)
	cascadePut(t, env, "/system/domain/"+target, []byte("enabled"))
	if w := aliasCreate(t, env, "alias.test", target, true); w.Code != http.StatusSeeOther {
		t.Fatal(w.Code)
	}
	w := postToHandler(t, env.h, env.h.SetCatchAll, http.MethodPost, "/admin/domains/alias.test/catchall",
		url.Values{"owner_user_id": {env.d1UserID}}, map[string]string{"domain": "alias.test"},
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) })
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if cascadeHas(env, "/system/catchall/alias.test") {
		t.Fatal("catch-all was written")
	}
}

func TestListDomains_AliasVisibleReadOnlyToTargetAdmin(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	if w := aliasCreate(t, env, "viewable.alias.test", testDomain1, true); w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := aliasCreate(t, env, "hidden.alias.test", testDomain2, true); w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", w.Code)
	}
	r := withDomainAdmin1(httptest.NewRequest(http.MethodGet, "/admin/domains", nil), env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ListDomains(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "viewable.alias.test") {
		t.Fatal("alias of an administered domain is not listed")
	}
	if strings.Contains(body, "hidden.alias.test") {
		t.Fatal("alias of a foreign domain leaked")
	}
	if strings.Contains(body, "/admin/domains/viewable.alias.test") {
		t.Fatal("alias has a manage link for a domain admin")
	}
	if !strings.Contains(body, "/admin/domains/"+testDomain1) {
		t.Fatal("own domain lost its manage link")
	}
}
