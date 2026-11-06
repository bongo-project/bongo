package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

type adminAppEnv struct {
	*adminScopeTestEnv
	apps  *auth.AppPasswords
	flaky *flakyRecords
	la    *auth.LocalAuth
	logs  *bytes.Buffer
}

func newAdminAppEnv(t *testing.T) *adminAppEnv {
	t.Helper()
	env := newAdminScopeTestEnv(t)
	logs := &bytes.Buffer{}
	flaky := &flakyRecords{AppPasswordRecords: store.NewAppPasswordStore(env.ps, env.applier)}
	apps := auth.NewAppPasswords(flaky, slog.New(slog.NewTextHandler(logs, nil)))
	env.h.SetAppPasswords(apps)
	env.h.logger = slog.New(slog.NewTextHandler(logs, nil))
	la := auth.NewLocalAuth(env.ps, env.md)
	la.SetAppPasswords(apps)
	return &adminAppEnv{adminScopeTestEnv: env, apps: apps, flaky: flaky, la: la, logs: logs}
}

func (e *adminAppEnv) revokeAll(t *testing.T, targetID string, asDomainAdmin bool) *httptest.ResponseRecorder {
	t.Helper()
	ctx := func(r *http.Request) *http.Request { return withGlobalAdmin(r, e.globalAdminID) }
	if asDomainAdmin {
		ctx = func(r *http.Request) *http.Request { return withDomainAdmin1(r, e.domainAdmin1ID) }
	}
	return postToHandler(t, e.h, e.h.RevokeAllAppPasswords, http.MethodPost,
		"/admin/users/"+targetID+"/app-passwords/revoke", nil, map[string]string{"userID": targetID}, ctx)
}

func (e *adminAppEnv) works(t *testing.T, email, pw string) bool {
	t.Helper()
	_, _, _, err := e.la.AuthenticateProtocol(email, pw)
	e.apps.Wait()
	return err == nil
}

func TestRevokeAllAppPasswords(t *testing.T) {
	e := newAdminAppEnv(t)
	const email = "user1@" + testDomain1
	pw1, _, err := e.apps.Create(e.d1UserID, "phone")
	if err != nil {
		t.Fatal(err)
	}
	pw2, _, _ := e.apps.Create(e.d1UserID, "laptop")
	other, _, _ := e.apps.Create(e.d2UserID, "other")
	profileKey := []byte("/user/" + e.d1UserID + "/profile")
	before, err := e.ps.Get(profileKey)
	if err != nil {
		t.Fatal(err)
	}
	// Control: the credentials work before the action.
	if !e.works(t, email, pw1) || !e.works(t, email, pw2) {
		t.Fatal("control: credentials refused before revoke-all")
	}

	w := e.revokeAll(t, e.d1UserID, false)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "flash=app-passwords-revoked-all") {
		t.Fatalf("status %d location %q", w.Code, w.Header().Get("Location"))
	}
	if e.works(t, email, pw1) || e.works(t, email, pw2) {
		t.Error("a revoked app password still authenticates")
	}
	if !e.works(t, email, "pass123!") {
		t.Error("account password stopped working")
	}
	if !e.works(t, "user2@"+testDomain2, other) {
		t.Error("another user's app password was revoked")
	}
	after, _ := e.ps.Get(profileKey)
	if !bytes.Equal(before, after) {
		t.Error("revoke-all changed the profile (sessions or password)")
	}

	logs := e.logs.String()
	if !strings.Contains(logs, "admin_id="+e.globalAdminID) || !strings.Contains(logs, "user_id="+e.d1UserID) || !strings.Contains(logs, "count=2") {
		t.Errorf("log lacks admin, user and count: %s", logs)
	}
	for _, secret := range []string{pw1, pw1[7:], pw2[7:], "$2a$"} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains credential material %q", secret)
		}
	}
}

func TestRevokeAllAppPasswordsScope(t *testing.T) {
	e := newAdminAppEnv(t)
	pwD1, _, _ := e.apps.Create(e.d1UserID, "in scope")
	pwD2, _, _ := e.apps.Create(e.d2UserID, "out of scope")
	pwG, _, _ := e.apps.Create(e.globalAdminID, "global admin")

	// Out of scope for the domain admin: refused, nothing revoked.
	for name, id := range map[string]string{"other domain": e.d2UserID, "global admin": e.globalAdminID} {
		if w := e.revokeAll(t, id, true); w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, w.Code)
		}
	}
	if !e.works(t, "user2@"+testDomain2, pwD2) || !e.works(t, "globaladmin@"+testDomain1, pwG) {
		t.Error("an out-of-scope action revoked credentials")
	}
	// Control: the same domain admin may act in scope.
	if w := e.revokeAll(t, e.d1UserID, true); w.Code != http.StatusSeeOther {
		t.Errorf("in scope: status %d", w.Code)
	}
	if e.works(t, "user1@"+testDomain1, pwD1) {
		t.Error("in-scope credential survived")
	}
	// A user who is not an admin never reaches the handler's data: unknown id.
	if w := e.revokeAll(t, "no-such-user", false); w.Code == http.StatusSeeOther {
		t.Errorf("unknown user reported success")
	}
}

func TestRevokeAllAppPasswordsFailureIsReported(t *testing.T) {
	e := newAdminAppEnv(t)
	pw, _, _ := e.apps.Create(e.d1UserID, "phone")
	e.flaky.failDelete = true
	w := e.revokeAll(t, e.d1UserID, false)
	if w.Code != http.StatusInternalServerError || w.Header().Get("Location") != "" || w.Header().Get("HX-Redirect") != "" {
		t.Errorf("status %d, headers %v", w.Code, w.Header())
	}
	if strings.Contains(w.Body.String(), "flash=") {
		t.Error("failure rendered as success")
	}
	if !strings.Contains(e.logs.String(), "revoke all app passwords failed") {
		t.Errorf("failure not logged: %s", e.logs.String())
	}
	e.flaky.failDelete = false
	if !e.works(t, "user1@"+testDomain1, pw) {
		t.Error("credential should still work after a failed revoke-all")
	}
}

func TestAdminUserPageOffersRevokeAll(t *testing.T) {
	e := newAdminAppEnv(t)
	show := func() string {
		w := postToHandler(t, e.h, e.h.ShowUser, http.MethodGet, "/admin/users/"+e.d1UserID, nil,
			map[string]string{"userID": e.d1UserID}, func(r *http.Request) *http.Request { return withGlobalAdmin(r, e.globalAdminID) })
		return w.Body.String()
	}
	if !strings.Contains(show(), "/admin/users/"+e.d1UserID+"/app-passwords/revoke") {
		t.Error("action missing from the admin user page")
	}
	e.h.SetAppPasswords(nil)
	if strings.Contains(show(), "app-passwords/revoke") {
		t.Error("action offered without a service")
	}
	if w := e.revokeAll(t, e.d1UserID, false); w.Code != http.StatusNotFound {
		t.Errorf("nil service: status %d", w.Code)
	}
}
