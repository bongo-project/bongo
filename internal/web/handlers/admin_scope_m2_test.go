package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func m2Run(env *adminScopeTestEnv, fn func(http.ResponseWriter, *http.Request), params map[string]string, body url.Values, global bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/admin/test", strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range params {
		r = withChiParam(r, k, v)
	}
	if global {
		r = withGlobalAdmin(r, env.globalAdminID)
	} else {
		r = withDomainAdmin1(r, env.domainAdmin1ID)
	}
	w := httptest.NewRecorder()
	fn(w, r)
	return w
}

// TestM2_SecondObjectScope checks that a d1 domain admin cannot aim d1
// resources at a d2 user, and that out-of-scope and nonexistent targets are
// indistinguishable. Each endpoint has a positive control (d1 admin, d1
// target) so a blanket deny cannot pass.
func TestM2_SecondObjectScope(t *testing.T) {
	t.Parallel()

	type endpoint struct {
		name string
		// call performs the request; target is a user ID (catch-all, grant) or
		// a username (alias).
		call               func(env *adminScopeTestEnv, target string, global bool) *httptest.ResponseRecorder
		d1Target, d2Target func(env *adminScopeTestEnv) string
		ghost              string
		// applied reports whether the write landed for the given target.
		applied func(env *adminScopeTestEnv, target string) bool
	}

	endpoints := []endpoint{
		{
			name: "CreateAlias",
			call: func(env *adminScopeTestEnv, target string, global bool) *httptest.ResponseRecorder {
				return m2Run(env, env.h.CreateAlias, nil, url.Values{
					"address": {"x@" + testDomain1}, "target_user": {target},
				}, global)
			},
			d1Target: func(*adminScopeTestEnv) string { return "user1@" + testDomain1 },
			d2Target: func(*adminScopeTestEnv) string { return "user2@" + testDomain2 },
			ghost:    "ghost@" + testDomain2,
			applied: func(env *adminScopeTestEnv, _ string) bool {
				v, err := env.ps.Get([]byte("/system/alias/x@" + testDomain1))
				return err == nil && len(v) > 0
			},
		},
		{
			name: "SetCatchAll",
			call: func(env *adminScopeTestEnv, target string, global bool) *httptest.ResponseRecorder {
				return m2Run(env, env.h.SetCatchAll, map[string]string{"domain": testDomain1},
					url.Values{"owner_user_id": {target}}, global)
			},
			d1Target: func(env *adminScopeTestEnv) string { return env.d1UserID },
			d2Target: func(env *adminScopeTestEnv) string { return env.d2UserID },
			ghost:    "no-such-user",
			applied: func(env *adminScopeTestEnv, _ string) bool {
				owner, ok, _ := env.catchAll.Get(testDomain1)
				return ok && owner != ""
			},
		},
		{
			name: "GrantDomainAdmin",
			call: func(env *adminScopeTestEnv, target string, global bool) *httptest.ResponseRecorder {
				return m2Run(env, env.h.GrantDomainAdmin, map[string]string{"domain": testDomain1},
					url.Values{"user_id": {target}}, global)
			},
			d1Target: func(env *adminScopeTestEnv) string { return env.d1UserID },
			d2Target: func(env *adminScopeTestEnv) string { return env.d2UserID },
			ghost:    "no-such-user",
			applied: func(env *adminScopeTestEnv, target string) bool {
				doms, _ := env.das.ListForUser(target)
				return len(doms) > 0
			},
		},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()

			t.Run("positive_control_d1_target", func(t *testing.T) {
				env := newAdminScopeTestEnv(t)
				w := ep.call(env, ep.d1Target(env), false)
				if w.Code != http.StatusSeeOther {
					t.Fatalf("status = %d, want 303; body %q", w.Code, w.Body.String())
				}
				if !ep.applied(env, ep.d1Target(env)) {
					t.Error("write did not land for in-scope target")
				}
			})

			t.Run("d2_target_denied_and_not_written", func(t *testing.T) {
				env := newAdminScopeTestEnv(t)
				w := ep.call(env, ep.d2Target(env), false)
				if w.Code != http.StatusForbidden {
					t.Errorf("status = %d, want 403; body %q", w.Code, w.Body.String())
				}
				if ep.applied(env, ep.d2Target(env)) {
					t.Error("write landed for out-of-scope target")
				}
			})

			t.Run("no_existence_oracle", func(t *testing.T) {
				env := newAdminScopeTestEnv(t)
				real := ep.call(env, ep.d2Target(env), false)
				ghost := ep.call(env, ep.ghost, false)
				if real.Code != ghost.Code || real.Body.String() != ghost.Body.String() {
					t.Errorf("existing d2 target gave %d %q, nonexistent gave %d %q",
						real.Code, real.Body.String(), ghost.Code, ghost.Body.String())
				}
			})

			t.Run("global_admin_unchanged", func(t *testing.T) {
				env := newAdminScopeTestEnv(t)
				w := ep.call(env, ep.d2Target(env), true)
				if w.Code != http.StatusSeeOther {
					t.Errorf("global admin, d2 target: status = %d, want 303; body %q", w.Code, w.Body.String())
				}
				g := ep.call(env, ep.ghost, true)
				if g.Code != http.StatusBadRequest {
					t.Errorf("global admin, nonexistent target: status = %d, want 400", g.Code)
				}
			})
		})
	}
}

// TestListAliases_DomainAdmin_NoOutOfScopeUsernames checks the alias page for a
// domain admin lists only in-scope usernames. The global-admin request on the
// same dataset proves the d2 username would otherwise be rendered.
func TestListAliases_DomainAdmin_NoOutOfScopeUsernames(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	d2User := "user2@" + testDomain2
	d1User := "user1@" + testDomain1

	get := func(global bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/admin/aliases", nil)
		if global {
			r = withGlobalAdmin(r, env.globalAdminID)
		} else {
			r = withDomainAdmin1(r, env.domainAdmin1ID)
		}
		w := httptest.NewRecorder()
		env.h.ListAliases(w, r)
		return w
	}

	g := get(true)
	if g.Code != http.StatusOK || !strings.Contains(g.Body.String(), d2User) {
		t.Fatalf("control: global admin page status %d, lists %q = %v", g.Code,
			d2User, strings.Contains(g.Body.String(), d2User))
	}

	w := get(false)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, d1User) {
		t.Errorf("body missing in-scope user %q", d1User)
	}
	for _, leak := range []string{d2User, testDomain2} {
		if strings.Contains(body, leak) {
			t.Errorf("body leaks out-of-scope %q", leak)
		}
	}
}
