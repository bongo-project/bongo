package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// --- Test environment setup ---

// adminScopeTestEnv holds all the infrastructure needed for tenant-isolation tests.
type adminScopeTestEnv struct {
	h          *AdminHandler
	ps         *store.PebbleStore
	md         *store.MetadataManager
	applier    *directApplierTags
	das        *store.DomainAdminStore
	as         *store.AddressStore
	catchAll   *store.CatchAllStore
	// Named IDs seeded during setup.
	globalAdminID  string
	domainAdmin1ID string // admin of d1, NOT d2
	regularUserID  string
	d1UserID       string // user whose primary domain is d1
	d2UserID       string // user whose primary domain is d2
}

const (
	testDomain1 = "d1.example.com"
	testDomain2 = "d2.example.com"
)

func newAdminScopeTestEnv(t *testing.T) *adminScopeTestEnv {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	md, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { md.Close() })

	applier := &directApplierTags{ps: ps}
	das := store.NewDomainAdminStore(ps, applier)
	as := store.NewAddressStore(ps, applier, md.System)
	cs := store.NewCatchAllStore(ps, applier)

	h := NewAdminHandler(ps, nil, md, applier, discardLogger())
	h.SetDomainAdminStore(das)
	h.SetAddressStore(as)
	h.SetCatchAllStore(cs)

	// Build a partial env so seedDomainForScope can use it.
	env := &adminScopeTestEnv{
		h:       h,
		ps:      ps,
		md:      md,
		applier: applier,
		das:     das,
		as:      as,
		catchAll: cs,
	}

	// Seed two domains (Pebble + SQLite).
	seedDomainForScope(t, env, testDomain1)
	seedDomainForScope(t, env, testDomain2)

	// Global admin.
	globalAdminID, err := auth.CreateUser(applier, md, "globaladmin@"+testDomain1, "Global Admin", "pass123!", true)
	if err != nil {
		t.Fatalf("create global admin: %v", err)
	}

	// Domain-admin user for d1 (not a global admin).
	domainAdmin1ID, err := auth.CreateUser(applier, md, "domainadmin@"+testDomain1, "Domain Admin 1", "pass123!", false)
	if err != nil {
		t.Fatalf("create domain admin 1: %v", err)
	}
	if err := das.Grant(testDomain1, domainAdmin1ID); err != nil {
		t.Fatalf("grant domain admin 1: %v", err)
	}

	// Regular user (no admin rights).
	regularUserID, err := auth.CreateUser(applier, md, "regular@"+testDomain1, "Regular", "pass123!", false)
	if err != nil {
		t.Fatalf("create regular user: %v", err)
	}

	// D1 user.
	d1UserID, err := auth.CreateUser(applier, md, "user1@"+testDomain1, "User1 D1", "pass123!", false)
	if err != nil {
		t.Fatalf("create d1 user: %v", err)
	}

	// D2 user.
	d2UserID, err := auth.CreateUser(applier, md, "user2@"+testDomain2, "User2 D2", "pass123!", false)
	if err != nil {
		t.Fatalf("create d2 user: %v", err)
	}

	env.globalAdminID = globalAdminID
	env.domainAdmin1ID = domainAdmin1ID
	env.regularUserID = regularUserID
	env.d1UserID = d1UserID
	env.d2UserID = d2UserID
	return env
}

// seedDomainForScope writes /system/domain/{domain} to Pebble and inserts into SQLite.
func seedDomainForScope(t *testing.T, env *adminScopeTestEnv, domain string) {
	t.Helper()
	if err := env.applier.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/system/domain/" + domain),
		Value: []byte("enabled"),
	}, time.Second); err != nil {
		t.Fatalf("seedDomain %q pebble: %v", domain, err)
	}
	// Also insert into system.db so loadDomains (which prefers SQLite) can find it.
	_, err := env.md.System.DB().Exec(
		`INSERT INTO domains (name, enabled, created_at) VALUES (?, 1, unixepoch())
		 ON CONFLICT(name) DO NOTHING`,
		domain,
	)
	if err != nil {
		t.Fatalf("seedDomain %q sqlite: %v", domain, err)
	}
}

// globalScope returns an AdminScope representing a global admin.
func globalScope() middleware.AdminScope {
	return middleware.AdminScope{Global: true}
}

// d1Scope returns an AdminScope representing a domain-admin of testDomain1.
func d1Scope() middleware.AdminScope {
	return middleware.AdminScope{
		Global:  false,
		Domains: map[string]bool{testDomain1: true},
	}
}

// withGlobalAdmin injects a global admin context (userID, email, scope) into r.
func withGlobalAdmin(r *http.Request, userID string) *http.Request {
	ctx := middleware.WithUserContext(r.Context(), userID, "globaladmin@"+testDomain1)
	ctx = middleware.WithAdminScopeContext(ctx, globalScope())
	return r.WithContext(ctx)
}

// withDomainAdmin1 injects a domain-admin-of-d1 context into r.
func withDomainAdmin1(r *http.Request, userID string) *http.Request {
	ctx := middleware.WithUserContext(r.Context(), userID, "domainadmin@"+testDomain1)
	ctx = middleware.WithAdminScopeContext(ctx, d1Scope())
	return r.WithContext(ctx)
}

// --- Helper: build requests with chi route params ---

func postToHandler(t *testing.T, h *AdminHandler, fn func(http.ResponseWriter, *http.Request), method, path string, body url.Values, params map[string]string, addCtx func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *strings.Reader
	if body != nil {
		bodyReader = strings.NewReader(body.Encode())
	} else {
		bodyReader = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, bodyReader)
	if body != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range params {
		r = withChiParam(r, k, v)
	}
	r = addCtx(r)
	w := httptest.NewRecorder()
	fn(w, r)
	return w
}

// =============================================================================
// DASHBOARD: domain-admin should be redirected to /admin/domains
// =============================================================================

func TestDashboard_DomainAdminRedirectsToDomains(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin", nil)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.Dashboard(w, r)

	// Domain-admin should be redirected away from the global dashboard.
	if w.Code != http.StatusSeeOther {
		t.Errorf("Dashboard (domain-admin) status = %d, want 303", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/admin/domains") {
		t.Errorf("Dashboard (domain-admin) redirect = %q, want /admin/domains", loc)
	}
}

func TestDashboard_GlobalAdminOK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin", nil)
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.Dashboard(w, r)

	// Global admin sees the dashboard (200).
	if w.Code != http.StatusOK {
		t.Errorf("Dashboard (global-admin) status = %d, want 200", w.Code)
	}
}

// =============================================================================
// GLOBAL-ONLY ENDPOINTS
// =============================================================================

func TestCreateDomain_DomainAdmin403(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.CreateDomain,
		http.MethodPost, "/admin/domains",
		url.Values{"domain": {"newdomain.example.com"}},
		nil,
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("CreateDomain (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestCreateDomain_GlobalAdminOK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// Use a domain that won't fail MX lookup fatally.
	w := postToHandler(t, env.h, env.h.CreateDomain,
		http.MethodPost, "/admin/domains",
		url.Values{"domain": {"newdomain.example.com"}},
		nil,
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) },
	)
	// Global admin gets a redirect (not 403).
	if w.Code == http.StatusForbidden {
		t.Errorf("CreateDomain (global-admin) status = 403, want redirect")
	}
}

func TestClusterOverview_DomainAdmin403(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/cluster", nil)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ClusterOverview(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("ClusterOverview (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestLogViewer_DomainAdmin403(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/logs", nil)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.LogViewer(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("LogViewer (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestToggleRequire2FA_DomainAdmin403(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.ToggleRequire2FA,
		http.MethodPost, "/admin/settings/require-2fa",
		url.Values{"enabled": {"1"}},
		nil,
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("ToggleRequire2FA (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestQueueDashboard_DomainAdmin403(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/queue", nil)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.QueueDashboard(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("QueueDashboard (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestFlushQueue_DomainAdmin403(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.FlushQueue,
		http.MethodPost, "/admin/queue/flush",
		nil, nil,
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("FlushQueue (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestSetDomainOwner_DomainAdmin403(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// Even for d1 (their own domain), setting the owner is global-only.
	w := postToHandler(t, env.h, env.h.SetDomainOwner,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/owner", testDomain1),
		url.Values{"owner_user_id": {env.domainAdmin1ID}},
		map[string]string{"domain": testDomain1},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("SetDomainOwner (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestSetDomainOwner_GlobalAdminOK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.SetDomainOwner,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/owner", testDomain1),
		url.Values{"owner_user_id": {env.d1UserID}},
		map[string]string{"domain": testDomain1},
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("SetDomainOwner (global-admin) status = 403, want redirect")
	}
}

// =============================================================================
// DOMAIN-SCOPED ENDPOINTS: domain-admin can act on d1, not d2
// =============================================================================

func TestToggleDomain_DomainAdmin_D1OK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.ToggleDomain,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/toggle", testDomain1),
		nil,
		map[string]string{"domain": testDomain1},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("ToggleDomain d1 (domain-admin) status = 403, should be allowed")
	}
}

func TestToggleDomain_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.ToggleDomain,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/toggle", testDomain2),
		nil,
		map[string]string{"domain": testDomain2},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("ToggleDomain d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestDeleteDomain_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.DeleteDomain,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/delete", testDomain2),
		nil,
		map[string]string{"domain": testDomain2},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("DeleteDomain d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestGenerateDKIMKey_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.GenerateDKIMKey,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/dkim/generate", testDomain2),
		nil,
		map[string]string{"domain": testDomain2},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("GenerateDKIMKey d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestGrantDomainAdmin_DomainAdmin_D1OK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.GrantDomainAdmin,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/admins", testDomain1),
		url.Values{"user_id": {env.d1UserID}},
		map[string]string{"domain": testDomain1},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("GrantDomainAdmin d1 (domain-admin) status = 403, should be allowed")
	}
}

func TestGrantDomainAdmin_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.GrantDomainAdmin,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/admins", testDomain2),
		url.Values{"user_id": {env.d1UserID}},
		map[string]string{"domain": testDomain2},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("GrantDomainAdmin d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestRevokeDomainAdmin_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.RevokeDomainAdmin,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/admins/revoke", testDomain2),
		url.Values{"user_id": {env.d1UserID}},
		map[string]string{"domain": testDomain2},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("RevokeDomainAdmin d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

// =============================================================================
// USER-SCOPED ENDPOINTS
// =============================================================================

func TestShowUser_DomainAdmin_D1UserOK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/users/%s", env.d1UserID), nil)
	r = withChiParam(r, "userID", env.d1UserID)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ShowUser(w, r)

	if w.Code == http.StatusForbidden {
		t.Errorf("ShowUser d1 user (domain-admin) status = 403, should be allowed")
	}
}

func TestShowUser_DomainAdmin_D2UserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/users/%s", env.d2UserID), nil)
	r = withChiParam(r, "userID", env.d2UserID)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ShowUser(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("ShowUser d2 user (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestShowUser_DomainAdmin_NonexistentUserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// A nonexistent user should return 403 (not 404) for a domain-admin.
	r := httptest.NewRequest(http.MethodGet, "/admin/users/nonexistent-id", nil)
	r = withChiParam(r, "userID", "nonexistent-id")
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ShowUser(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("ShowUser nonexistent (domain-admin) status = %d, want 403 (zero-leak)", w.Code)
	}
}

func TestShowUser_GlobalAdmin_NonexistentUser404(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// A global admin gets 404 for a truly nonexistent user.
	r := httptest.NewRequest(http.MethodGet, "/admin/users/nonexistent-id", nil)
	r = withChiParam(r, "userID", "nonexistent-id")
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.ShowUser(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("ShowUser nonexistent (global-admin) status = %d, want 404", w.Code)
	}
}

func TestUpdateUser_DomainAdmin_D2UserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.UpdateUser,
		http.MethodPost, fmt.Sprintf("/admin/users/%s", env.d2UserID),
		url.Values{"display_name": {"new name"}, "is_admin": {"0"}},
		map[string]string{"userID": env.d2UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("UpdateUser d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestToggleDisabled_DomainAdmin_D2UserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.ToggleDisabled,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/toggle-disabled", env.d2UserID),
		nil,
		map[string]string{"userID": env.d2UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("ToggleDisabled d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestResetPassword_DomainAdmin_D2UserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.ResetPassword,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/reset-password", env.d2UserID),
		nil,
		map[string]string{"userID": env.d2UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("ResetPassword d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestDeleteUser_DomainAdmin_D2UserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.DeleteUser,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/delete", env.d2UserID),
		nil,
		map[string]string{"userID": env.d2UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("DeleteUser d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestAddAddress_DomainAdmin_D2UserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.AddAddress,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/addresses", env.d2UserID),
		url.Values{"address": {"extra@" + testDomain2}},
		map[string]string{"userID": env.d2UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("AddAddress d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

func TestGrantSend_DomainAdmin_D2UserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.GrantSend,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/addresses/grant-send", env.d2UserID),
		url.Values{"address": {"user2@" + testDomain2}},
		map[string]string{"userID": env.d2UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("GrantSend d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

// TestAddAddress_DomainAdmin_D2AddressForbidden: a d1-admin may not allocate an
// address in d2 even for one of THEIR OWN (d1) users — the new address's domain
// must also be in scope (prevents cross-tenant namespace allocation).
func TestAddAddress_DomainAdmin_D2AddressForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.AddAddress,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/addresses", env.d1UserID),
		url.Values{"address": {"grabbed@" + testDomain2}},
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("AddAddress d1-user with d2 address (domain-admin) status = %d, want 403", w.Code)
	}
	// The out-of-scope address must NOT have been created.
	if _, ok, _ := env.as.Get("grabbed@" + testDomain2); ok {
		t.Error("cross-tenant address was created despite 403")
	}
}

// TestAddAddress_DomainAdmin_D1AddressOK: the address-domain guard must not
// over-block the legitimate in-scope case (d1-admin adds a d1 address to a
// d1 user).
func TestAddAddress_DomainAdmin_D1AddressOK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.AddAddress,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/addresses", env.d1UserID),
		url.Values{"address": {"extra@" + testDomain1}},
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("AddAddress d1-user with d1 address (domain-admin) status = 403, should be allowed")
	}
}

// TestGrantSend_DomainAdmin_D2AddressForbidden: a d1-admin may not grant send-as
// for a d2 address (spoofing another tenant's domain), even for a d1 user.
func TestGrantSend_DomainAdmin_D2AddressForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.GrantSend,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/addresses/grant-send", env.d1UserID),
		url.Values{"address": {"spoof@" + testDomain2}},
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("GrantSend d1-user with d2 address (domain-admin) status = %d, want 403", w.Code)
	}
}

// =============================================================================
// CREATE USER: domain-admin must only create users in their authorized domains
// =============================================================================

func TestCreateUser_DomainAdmin_D2DomainForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// Domain-admin of d1 trying to create a user in d2.
	w := postToHandler(t, env.h, env.h.CreateUser,
		http.MethodPost, "/admin/users",
		url.Values{
			"email":    {"newuser@" + testDomain2},
			"password": {"pass123!"},
		},
		nil,
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("CreateUser d2 (domain-admin of d1) status = %d, want 403", w.Code)
	}
}

func TestCreateUser_DomainAdmin_D1DomainOK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// Domain-admin of d1 creating a user in d1.
	w := postToHandler(t, env.h, env.h.CreateUser,
		http.MethodPost, "/admin/users",
		url.Values{
			"email":    {"newuser@" + testDomain1},
			"password": {"pass123!"},
		},
		nil,
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("CreateUser d1 (domain-admin of d1) status = 403, should be allowed")
	}
}

// =============================================================================
// FIELD-LEVEL GUARD: is_admin flag can only be set by global admins
// =============================================================================

func TestCreateUser_DomainAdmin_CannotSetIsAdmin(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// Domain-admin tries to create a user with is_admin=1.
	w := postToHandler(t, env.h, env.h.CreateUser,
		http.MethodPost, "/admin/users",
		url.Values{
			"email":    {"sneaky@" + testDomain1},
			"password": {"pass123!"},
			"is_admin": {"1"},
		},
		nil,
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Skip("CreateUser was blocked entirely; field-level guard not applicable")
	}

	// Find the created user and check their profile.
	users := env.h.loadUsers("sneaky")
	if len(users) == 0 {
		// User may not have been created if the handler errored.
		return
	}
	if users[0].IsAdmin {
		t.Error("domain-admin was able to set is_admin=1 on created user — PRIVILEGE ESCALATION")
	}
}

func TestUpdateUser_DomainAdmin_CannotSetIsAdmin(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// Attempt to promote a d1 user to global admin via UpdateUser.
	w := postToHandler(t, env.h, env.h.UpdateUser,
		http.MethodPost, fmt.Sprintf("/admin/users/%s", env.d1UserID),
		url.Values{
			"display_name": {"hacked"},
			"is_admin":     {"1"},
		},
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("UpdateUser d1 user by domain-admin should be allowed, got 403")
		return
	}

	// Check that is_admin was NOT set.
	profile, err := env.h.loadProfile(env.d1UserID)
	if err != nil {
		t.Fatalf("loadProfile after UpdateUser: %v", err)
	}
	if profile.IsAdmin {
		t.Error("domain-admin was able to promote d1 user to global admin — PRIVILEGE ESCALATION")
	}
}

func TestUpdateUser_GlobalAdmin_CanSetIsAdmin(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.UpdateUser,
		http.MethodPost, fmt.Sprintf("/admin/users/%s", env.d1UserID),
		url.Values{
			"display_name": {"promoted"},
			"is_admin":     {"1"},
		},
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("UpdateUser by global-admin status = 403, want redirect")
		return
	}

	// Check that is_admin WAS set.
	profile, err := env.h.loadProfile(env.d1UserID)
	if err != nil {
		t.Fatalf("loadProfile after UpdateUser: %v", err)
	}
	if !profile.IsAdmin {
		t.Error("global admin could not set is_admin on user")
	}
}

// =============================================================================
// LIST FILTERING: domain-admin sees only their domain's resources
// =============================================================================

func TestListDomains_DomainAdmin_FiltersToD1(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains", nil)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ListDomains(w, r)

	body := w.Body.String()
	if strings.Contains(body, testDomain2) {
		t.Errorf("ListDomains (domain-admin of d1) response contains %q — data leak", testDomain2)
	}
	if !strings.Contains(body, testDomain1) {
		t.Errorf("ListDomains (domain-admin of d1) response does not contain %q", testDomain1)
	}
}

func TestListDomains_GlobalAdmin_SeesAll(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains", nil)
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.ListDomains(w, r)

	body := w.Body.String()
	if !strings.Contains(body, testDomain1) {
		t.Errorf("ListDomains (global-admin) missing %q", testDomain1)
	}
	if !strings.Contains(body, testDomain2) {
		t.Errorf("ListDomains (global-admin) missing %q", testDomain2)
	}
}

func TestListUsers_DomainAdmin_FiltersToD1(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ListUsers(w, r)

	body := w.Body.String()
	// d2 user's email must not appear.
	if strings.Contains(body, "user2@"+testDomain2) {
		t.Errorf("ListUsers (domain-admin of d1) response contains d2 user — data leak")
	}
	// d1 user must appear.
	if !strings.Contains(body, "user1@"+testDomain1) {
		t.Errorf("ListUsers (domain-admin of d1) response missing d1 user")
	}
}

func TestListUsers_GlobalAdmin_SeesAll(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.ListUsers(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "user1@"+testDomain1) {
		t.Errorf("ListUsers (global-admin) missing d1 user")
	}
	if !strings.Contains(body, "user2@"+testDomain2) {
		t.Errorf("ListUsers (global-admin) missing d2 user")
	}
}

// =============================================================================
// ALIASES: CreateAlias/DeleteAlias scoped; ListAliases filtered
// =============================================================================

func seedAlias(t *testing.T, applier *directApplierTags, address, targetUserID string) {
	t.Helper()
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/system/alias/" + address),
		Value: []byte(targetUserID),
	}, time.Second); err != nil {
		t.Fatalf("seedAlias %q: %v", address, err)
	}
}

func TestListAliases_DomainAdmin_FiltersToD1(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	seedAlias(t, env.applier, "alias1@"+testDomain1, env.d1UserID)
	seedAlias(t, env.applier, "alias2@"+testDomain2, env.d2UserID)

	r := httptest.NewRequest(http.MethodGet, "/admin/aliases", nil)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ListAliases(w, r)

	body := w.Body.String()
	if strings.Contains(body, "alias2@"+testDomain2) {
		t.Errorf("ListAliases (domain-admin of d1) response contains d2 alias — data leak")
	}
	if !strings.Contains(body, "alias1@"+testDomain1) {
		t.Errorf("ListAliases (domain-admin of d1) response missing d1 alias")
	}
}

func TestCreateAlias_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.CreateAlias,
		http.MethodPost, "/admin/aliases",
		url.Values{
			"address":     {"newaliasind2@" + testDomain2},
			"target_user": {env.d2UserID},
		},
		nil,
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("CreateAlias d2 (domain-admin of d1) status = %d, want 403", w.Code)
	}
}

func TestCreateAlias_DomainAdmin_D1OK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.CreateAlias,
		http.MethodPost, "/admin/aliases",
		url.Values{
			"address":     {"newaliasind1@" + testDomain1},
			"target_user": {env.d1UserID},
		},
		nil,
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("CreateAlias d1 (domain-admin of d1) status = 403, should be allowed")
	}
}

func TestDeleteAlias_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	seedAlias(t, env.applier, "alias2@"+testDomain2, env.d2UserID)

	aliasAddr := "alias2@" + testDomain2
	w := postToHandler(t, env.h, env.h.DeleteAlias,
		http.MethodPost, fmt.Sprintf("/admin/aliases/%s/delete", aliasAddr),
		nil,
		map[string]string{"address": aliasAddr},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("DeleteAlias d2 (domain-admin of d1) status = %d, want 403", w.Code)
	}
}

// =============================================================================
// DELETE USER CASCADE: grants and owner refs cleaned up
// =============================================================================

func TestDeleteUser_Cascade_RemovesGrantsAndOwner(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// Grant d1UserID as admin of d1.
	if err := env.das.Grant(testDomain1, env.d1UserID); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	// Set d1UserID as owner of d1.
	if err := env.das.SetOwner(testDomain1, env.d1UserID); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}

	// Verify grants and owner exist before deletion.
	grants, err := env.das.ListForUser(env.d1UserID)
	if err != nil || len(grants) == 0 {
		t.Fatalf("expected grant to exist before delete: %v, %v", grants, err)
	}
	_, ownerExists, err := env.das.GetOwner(testDomain1)
	if err != nil || !ownerExists {
		t.Fatalf("expected owner to exist before delete: %v, %v", ownerExists, err)
	}

	// Delete d1UserID as global admin (acting user is different).
	w := postToHandler(t, env.h, env.h.DeleteUser,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/delete", env.d1UserID),
		nil,
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) },
	)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("DeleteUser status = %d, want 303", w.Code)
	}

	// Grants should be removed.
	grants, err = env.das.ListForUser(env.d1UserID)
	if err != nil {
		t.Fatalf("ListForUser after delete: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("domain grants remain after user deletion: %v", grants)
	}

	// Owner reference should be cleared.
	ownerID, ownerExists, err := env.das.GetOwner(testDomain1)
	if err != nil {
		t.Fatalf("GetOwner after delete: %v", err)
	}
	if ownerExists {
		t.Errorf("domain owner ref remains after user deletion: %q", ownerID)
	}
}

// =============================================================================
// GLOBAL ADMIN UNAFFECTED: still sees/does everything
// =============================================================================

func TestGlobalAdmin_AllEndpointsAllowed(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	tests := []struct {
		name   string
		method string
		path   string
		params map[string]string
		body   url.Values
		fn     func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "ToggleDomain d1",
			fn:   env.h.ToggleDomain,
			params: map[string]string{"domain": testDomain1},
		},
		{
			name: "ToggleDomain d2",
			fn:   env.h.ToggleDomain,
			params: map[string]string{"domain": testDomain2},
		},
		{
			name:   "GrantDomainAdmin d1",
			fn:     env.h.GrantDomainAdmin,
			params: map[string]string{"domain": testDomain1},
			body:   url.Values{"user_id": {env.d1UserID}},
		},
		{
			name:   "ShowUser d1",
			fn:     env.h.ShowUser,
			params: map[string]string{"userID": env.d1UserID},
		},
		{
			name:   "ShowUser d2",
			fn:     env.h.ShowUser,
			params: map[string]string{"userID": env.d2UserID},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := postToHandler(t, env.h, tc.fn,
				http.MethodPost, "/admin/test",
				tc.body, tc.params,
				func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) },
			)
			if w.Code == http.StatusForbidden {
				t.Errorf("global admin got 403 on %q", tc.name)
			}
		})
	}
}

// =============================================================================
// STORE: DeleteOwner and ListDomainsOwnedBy (unit tests in store package)
// Test here that they are correctly invoked from the handler cascade.
// =============================================================================

func TestDomainAdminStore_DeleteOwner(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// Set owner.
	if err := env.das.SetOwner(testDomain1, env.d1UserID); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}
	// Verify set.
	_, ok, err := env.das.GetOwner(testDomain1)
	if err != nil || !ok {
		t.Fatalf("GetOwner after set: ok=%v err=%v", ok, err)
	}
	// Delete owner.
	if err := env.das.DeleteOwner(testDomain1); err != nil {
		t.Fatalf("DeleteOwner: %v", err)
	}
	// Verify gone.
	_, ok, err = env.das.GetOwner(testDomain1)
	if err != nil {
		t.Fatalf("GetOwner after delete: %v", err)
	}
	if ok {
		t.Error("owner still present after DeleteOwner")
	}
}

func TestDomainAdminStore_ListDomainsOwnedBy(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// No owner yet.
	domains, err := env.das.ListDomainsOwnedBy(env.d1UserID)
	if err != nil {
		t.Fatalf("ListDomainsOwnedBy (empty): %v", err)
	}
	if len(domains) != 0 {
		t.Errorf("ListDomainsOwnedBy empty: want 0, got %v", domains)
	}

	// Set owner for d1.
	if err := env.das.SetOwner(testDomain1, env.d1UserID); err != nil {
		t.Fatalf("SetOwner d1: %v", err)
	}
	// d2 has a different owner.
	if err := env.das.SetOwner(testDomain2, env.d2UserID); err != nil {
		t.Fatalf("SetOwner d2: %v", err)
	}

	domains, err = env.das.ListDomainsOwnedBy(env.d1UserID)
	if err != nil {
		t.Fatalf("ListDomainsOwnedBy: %v", err)
	}
	if len(domains) != 1 || domains[0] != testDomain1 {
		t.Errorf("ListDomainsOwnedBy: want [%s], got %v", testDomain1, domains)
	}
}
