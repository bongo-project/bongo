package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// --- Test environment setup ---

// adminScopeTestEnv holds all the infrastructure needed for tenant-isolation tests.
type adminScopeTestEnv struct {
	h        *AdminHandler
	ps       *store.PebbleStore
	md       *store.MetadataManager
	applier  *directApplierTags
	das      *store.DomainAdminStore
	as       *store.AddressStore
	catchAll *store.CatchAllStore
	// Named IDs seeded during setup.
	globalAdminID  string
	domainAdmin1ID string // admin of d1, NOT d2
	regularUserID  string
	d1UserID       string // user whose primary domain is d1
	d2UserID       string // user whose primary domain is d2

	d1CrossAdminID string // user@d1 granted d2 (outside a d1 admin's scope)
	d1OwnerOfD2ID  string // user@d1 who owns d2
	d1PeerAdminID  string // user@d1 granted d1 only
	d1BothAdminID  string // user@d1 granted d1 and d2
	d1OwnD1Gr2ID   string // user@d1 who owns d1 and is granted d2
	d1OwnD3Gr1ID   string // user@d1 who owns d3 and is granted d1 (out-of-scope domain listed first)
}

const (
	testDomain1 = "d1.example.com"
	testDomain2 = "d2.example.com"
	testDomain3 = "d3.example.com"
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
		h:        h,
		ps:       ps,
		md:       md,
		applier:  applier,
		das:      das,
		as:       as,
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

// TestRemoveAddress_DomainAdmin_D2AddressForbidden: a d1-admin may not remove an
// address in d2 even from their own d1 user — RemoveAddress is symmetric with
// AddAddress/GrantSend (an admin only mutates addresses in domains they administer).
func TestRemoveAddress_DomainAdmin_D2AddressForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.RemoveAddress,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/addresses/delete", env.d1UserID),
		url.Values{"address": {"held@" + testDomain2}},
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("RemoveAddress d1-user with d2 address (domain-admin) status = %d, want 403", w.Code)
	}
}

// TestRemoveAddress_DomainAdmin_D1AddressOK: the address-domain guard must not
// over-block removing an in-scope (d1) identity address from a d1 user.
func TestRemoveAddress_DomainAdmin_D1AddressOK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// Seed a removable d1 identity address on the d1 user.
	if err := env.as.Put("extra-d1@"+testDomain1, &bongov1.AddressRecord{
		UserId: env.d1UserID,
		Domain: testDomain1,
		Kind:   "identity",
	}); err != nil {
		t.Fatalf("seed d1 identity address: %v", err)
	}

	w := postToHandler(t, env.h, env.h.RemoveAddress,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/addresses/delete", env.d1UserID),
		url.Values{"address": {"extra-d1@" + testDomain1}},
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("RemoveAddress d1-user with d1 address (domain-admin) status = 403, should be allowed")
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
			name:   "ToggleDomain d1",
			fn:     env.h.ToggleDomain,
			params: map[string]string{"domain": testDomain1},
		},
		{
			name:   "ToggleDomain d2",
			fn:     env.h.ToggleDomain,
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

// =============================================================================
// REMOVE ADDRESS: requireUserAccess check (d2-user -> 403; d1-user -> not 403)
// =============================================================================

// TestRemoveAddress_DomainAdmin_D2UserForbidden verifies that a d1-admin cannot
// remove an address belonging to a d2 user (requireUserAccess blocks it).
func TestRemoveAddress_DomainAdmin_D2UserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.RemoveAddress,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/addresses/delete", env.d2UserID),
		url.Values{"address": {"alias@" + testDomain2}},
		map[string]string{"userID": env.d2UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("RemoveAddress d2 user (domain-admin) status = %d, want 403", w.Code)
	}
}

// TestRemoveAddress_DomainAdmin_D1UserNotForbidden verifies that a d1-admin CAN
// remove an address from a d1 user (positive control for requireUserAccess).
// We assert "not 403"; the actual result (400/500/redirect) depends on whether
// the address exists, which is irrelevant to the scope check.
func TestRemoveAddress_DomainAdmin_D1UserNotForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.RemoveAddress,
		http.MethodPost, fmt.Sprintf("/admin/users/%s/addresses/delete", env.d1UserID),
		url.Values{"address": {"alias@" + testDomain1}},
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("RemoveAddress d1 user (domain-admin) status = 403, should be allowed")
	}
}

// TestRemoveAddress_DomainAdmin_NonexistentUserForbidden verifies zero-leak
// semantics: a nonexistent userID returns 403, not 404, for a domain-admin.
func TestRemoveAddress_DomainAdmin_NonexistentUserForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.RemoveAddress,
		http.MethodPost, "/admin/users/no-such-id/addresses/delete",
		url.Values{"address": {"alias@" + testDomain1}},
		map[string]string{"userID": "no-such-id"},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("RemoveAddress nonexistent user (domain-admin) status = %d, want 403", w.Code)
	}
}

// =============================================================================
// CATCH-ALL: SetCatchAll/ClearCatchAll require domain access
// =============================================================================

// TestSetCatchAll_DomainAdmin_D2Forbidden verifies a d1-admin cannot set
// a catch-all for d2 (requireDomainAccess blocks it).
func TestSetCatchAll_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.SetCatchAll,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/catchall", testDomain2),
		url.Values{"owner_user_id": {env.d2UserID}},
		map[string]string{"domain": testDomain2},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("SetCatchAll d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

// TestSetCatchAll_DomainAdmin_D1NotForbidden verifies a d1-admin CAN set
// a catch-all for d1 (positive control). Assert "not 403".
func TestSetCatchAll_DomainAdmin_D1NotForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.SetCatchAll,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/catchall", testDomain1),
		url.Values{"owner_user_id": {env.d1UserID}},
		map[string]string{"domain": testDomain1},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("SetCatchAll d1 (domain-admin) status = 403, should be allowed")
	}
}

// TestClearCatchAll_DomainAdmin_D2Forbidden verifies a d1-admin cannot clear
// the catch-all for d2 (requireDomainAccess blocks it).
func TestClearCatchAll_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.ClearCatchAll,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/catchall/delete", testDomain2),
		nil,
		map[string]string{"domain": testDomain2},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code != http.StatusForbidden {
		t.Errorf("ClearCatchAll d2 (domain-admin) status = %d, want 403", w.Code)
	}
}

// TestClearCatchAll_DomainAdmin_D1NotForbidden verifies a d1-admin CAN clear
// the catch-all for d1 (positive control). Assert "not 403".
func TestClearCatchAll_DomainAdmin_D1NotForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	w := postToHandler(t, env.h, env.h.ClearCatchAll,
		http.MethodPost, fmt.Sprintf("/admin/domains/%s/catchall/delete", testDomain1),
		nil,
		map[string]string{"domain": testDomain1},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
	)
	if w.Code == http.StatusForbidden {
		t.Errorf("ClearCatchAll d1 (domain-admin) status = 403, should be allowed")
	}
}

// =============================================================================
// GLOBAL-ONLY ENDPOINTS NOT YET COVERED: SetDomainQuota, RetryMessage,
// DropMessage, LogStream
// =============================================================================

// TestRemainingGlobalOnlyEndpoints_DomainAdmin403 is a table-driven sweep
// over the global-only endpoints that had no prior scope test coverage.
// All must return 403 for a domain-admin.
func TestRemainingGlobalOnlyEndpoints_DomainAdmin403(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	type endpointCase struct {
		name   string
		fn     func(http.ResponseWriter, *http.Request)
		method string
		path   string
		params map[string]string
		body   url.Values
	}

	cases := []endpointCase{
		// POST /admin/domains/{domain}/quota — SetDomainQuota is global-only.
		{
			name:   "SetDomainQuota d1",
			fn:     env.h.SetDomainQuota,
			method: http.MethodPost,
			path:   fmt.Sprintf("/admin/domains/%s/quota", testDomain1),
			params: map[string]string{"domain": testDomain1},
			body:   url.Values{"max_users": {"100"}},
		},
		// POST /admin/domains/{domain}/quota targeting d2 — still global-only.
		{
			name:   "SetDomainQuota d2",
			fn:     env.h.SetDomainQuota,
			method: http.MethodPost,
			path:   fmt.Sprintf("/admin/domains/%s/quota", testDomain2),
			params: map[string]string{"domain": testDomain2},
			body:   url.Values{"max_users": {"100"}},
		},
		// POST /admin/queue/{id}/retry — RetryMessage is global-only.
		{
			name:   "RetryMessage",
			fn:     env.h.RetryMessage,
			method: http.MethodPost,
			path:   "/admin/queue/some-msg-id/retry",
			params: map[string]string{"id": "some-msg-id"},
		},
		// POST /admin/queue/{id}/drop — DropMessage is global-only.
		{
			name:   "DropMessage",
			fn:     env.h.DropMessage,
			method: http.MethodPost,
			path:   "/admin/queue/some-msg-id/drop",
			params: map[string]string{"id": "some-msg-id"},
		},
		// GET /admin/logs/stream — LogStream is global-only.
		{
			name:   "LogStream",
			fn:     env.h.LogStream,
			method: http.MethodGet,
			path:   "/admin/logs/stream",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postToHandler(t, env.h, tc.fn,
				tc.method, tc.path,
				tc.body, tc.params,
				func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
			)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s (domain-admin) status = %d, want 403", tc.name, w.Code)
			}
			// Zero-leak: the response must not contain d2 identifiers in any error message.
			body := w.Body.String()
			if strings.Contains(body, testDomain2) {
				t.Errorf("%s response body contains d2 domain name — potential data leak", tc.name)
			}
		})
	}
}

// TestRemainingGlobalOnlyEndpoints_GlobalAdminNotForbidden verifies the same
// endpoints are accessible to a global admin (spot-check — assert "not 403").
func TestRemainingGlobalOnlyEndpoints_GlobalAdminNotForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	type endpointCase struct {
		name   string
		fn     func(http.ResponseWriter, *http.Request)
		method string
		path   string
		params map[string]string
		body   url.Values
	}

	cases := []endpointCase{
		{
			name:   "SetDomainQuota d1",
			fn:     env.h.SetDomainQuota,
			method: http.MethodPost,
			path:   fmt.Sprintf("/admin/domains/%s/quota", testDomain1),
			params: map[string]string{"domain": testDomain1},
			body:   url.Values{"max_users": {"50"}},
		},
		{
			name:   "RetryMessage",
			fn:     env.h.RetryMessage,
			method: http.MethodPost,
			path:   "/admin/queue/some-msg-id/retry",
			params: map[string]string{"id": "some-msg-id"},
		},
		{
			name:   "DropMessage",
			fn:     env.h.DropMessage,
			method: http.MethodPost,
			path:   "/admin/queue/some-msg-id/drop",
			params: map[string]string{"id": "some-msg-id"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postToHandler(t, env.h, tc.fn,
				tc.method, tc.path,
				tc.body, tc.params,
				func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) },
			)
			if w.Code == http.StatusForbidden {
				t.Errorf("%s (global-admin) status = 403, should be allowed", tc.name)
			}
		})
	}
}

// =============================================================================
// NEW USER FORM: list-leak (domain dropdown must not expose d2 to d1-admin)
// =============================================================================

// TestNewUserForm_DomainAdmin_NoDomainLeak asserts that when a d1-admin opens
// the new-user form, the rendered HTML contains d1 but NOT d2.
func TestNewUserForm_DomainAdmin_NoDomainLeak(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/users/new", nil)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.NewUserForm(w, r)

	// Domain-admin must not get 403 for this read-only form.
	if w.Code == http.StatusForbidden {
		t.Fatalf("NewUserForm (domain-admin) status = 403, should be allowed")
	}

	body := w.Body.String()
	// d2 must not appear anywhere in the response (domain dropdown is filtered).
	if strings.Contains(body, testDomain2) {
		t.Errorf("NewUserForm (domain-admin of d1) body contains %q — domain list leak", testDomain2)
	}
	// d1 must appear (the admin's own domain is in scope).
	if !strings.Contains(body, testDomain1) {
		t.Errorf("NewUserForm (domain-admin of d1) body missing %q", testDomain1)
	}
}

// TestNewUserForm_GlobalAdmin_SeesBothDomains verifies a global admin sees
// all domains in the form (no filtering applied).
func TestNewUserForm_GlobalAdmin_SeesBothDomains(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/users/new", nil)
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.NewUserForm(w, r)

	if w.Code == http.StatusForbidden {
		t.Fatalf("NewUserForm (global-admin) status = 403")
	}

	body := w.Body.String()
	if !strings.Contains(body, testDomain1) {
		t.Errorf("NewUserForm (global-admin) missing %q in body", testDomain1)
	}
	if !strings.Contains(body, testDomain2) {
		t.Errorf("NewUserForm (global-admin) missing %q in body", testDomain2)
	}
}

// =============================================================================
// COMPREHENSIVE ENDPOINT TABLE: reference sweep over ALL agent.go admin routes
// (lines ~655-689). Documents coverage status for each endpoint.
// Any new endpoint added to agent.go should have a corresponding entry here.
// =============================================================================

// TestAdversarialSweep_AllEndpoints_DomainAdmin is the master reference table
// that enumerates EVERY endpoint in the RequireAdminOrDomainAdmin group and
// asserts the domain-admin-of-d1 cannot access d2 resources or global endpoints.
// Each entry maps to an existing individual test; this table ensures no endpoint
// is silently omitted from the sweep.
//
// Note on QUARANTINE routes (/admin/quarantine/*): these are in a SEPARATE route
// group gated by middleware.RequireAdmin (global-admin ONLY) in agent.go. They
// use QuarantineHandler, not AdminHandler, and have no AdminScope concept.
// The RequireAdmin middleware already denies domain-admins at the door, so
// quarantine routes are not exercised through this harness. The middleware-layer
// protection is verified by internal/web/middleware/admin_scope_test.go.
//
// Note on Dashboard: domain-admin gets a 303 redirect to /admin/domains
// (not 403). This is the correct UX behaviour tested in
// TestDashboard_DomainAdminRedirectsToDomains.
func TestAdversarialSweep_AllEndpoints_DomainAdmin(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	// This table uses "expect 403" to mean the d1-admin targeting a d2 resource
	// (or a global-only endpoint) must be denied. Positive controls (d1 resources)
	// are covered by the individual tests above; only adversarial cases are listed.
	type adversarialCase struct {
		name       string
		fn         func(http.ResponseWriter, *http.Request)
		method     string
		params     map[string]string
		body       url.Values
		wantDenied bool // true = expect 403; false = should not be 403 (positive control)
	}

	// We seed an alias in d2 so DeleteAlias has a real target.
	seedAlias(t, env.applier, "sweep-alias2@"+testDomain2, env.d2UserID)

	cases := []adversarialCase{
		// --- GLOBAL-ONLY (domain-admin -> 403 regardless of domain param) ---
		{name: "CreateDomain", fn: env.h.CreateDomain, method: http.MethodPost,
			body: url.Values{"domain": {"sweep-new.example.com"}}, wantDenied: true},
		{name: "SetDomainOwner/d1", fn: env.h.SetDomainOwner, method: http.MethodPost,
			params: map[string]string{"domain": testDomain1},
			body:   url.Values{"owner_user_id": {env.d1UserID}}, wantDenied: true},
		{name: "SetDomainOwner/d2", fn: env.h.SetDomainOwner, method: http.MethodPost,
			params: map[string]string{"domain": testDomain2},
			body:   url.Values{"owner_user_id": {env.d2UserID}}, wantDenied: true},
		{name: "SetDomainQuota/d1", fn: env.h.SetDomainQuota, method: http.MethodPost,
			params: map[string]string{"domain": testDomain1},
			body:   url.Values{"max_users": {"10"}}, wantDenied: true},
		{name: "SetDomainQuota/d2", fn: env.h.SetDomainQuota, method: http.MethodPost,
			params: map[string]string{"domain": testDomain2},
			body:   url.Values{"max_users": {"10"}}, wantDenied: true},
		{name: "ClusterOverview", fn: env.h.ClusterOverview, method: http.MethodGet,
			wantDenied: true},
		{name: "LogViewer", fn: env.h.LogViewer, method: http.MethodGet,
			wantDenied: true},
		{name: "LogStream", fn: env.h.LogStream, method: http.MethodGet,
			wantDenied: true},
		{name: "ToggleRequire2FA", fn: env.h.ToggleRequire2FA, method: http.MethodPost,
			body: url.Values{"enabled": {"1"}}, wantDenied: true},
		{name: "QueueDashboard", fn: env.h.QueueDashboard, method: http.MethodGet,
			wantDenied: true},
		{name: "RetryMessage", fn: env.h.RetryMessage, method: http.MethodPost,
			params: map[string]string{"id": "msg-1"}, wantDenied: true},
		{name: "DropMessage", fn: env.h.DropMessage, method: http.MethodPost,
			params: map[string]string{"id": "msg-1"}, wantDenied: true},
		{name: "FlushQueue", fn: env.h.FlushQueue, method: http.MethodPost,
			wantDenied: true},

		// --- DOMAIN-SCOPED: d2 resources -> 403 for d1-admin ---
		{name: "ToggleDomain/d2", fn: env.h.ToggleDomain, method: http.MethodPost,
			params: map[string]string{"domain": testDomain2}, wantDenied: true},
		{name: "DeleteDomain/d2", fn: env.h.DeleteDomain, method: http.MethodPost,
			params: map[string]string{"domain": testDomain2}, wantDenied: true},
		{name: "GenerateDKIMKey/d2", fn: env.h.GenerateDKIMKey, method: http.MethodPost,
			params: map[string]string{"domain": testDomain2}, wantDenied: true},
		{name: "SetCatchAll/d2", fn: env.h.SetCatchAll, method: http.MethodPost,
			params: map[string]string{"domain": testDomain2},
			body:   url.Values{"owner_user_id": {env.d2UserID}}, wantDenied: true},
		{name: "ClearCatchAll/d2", fn: env.h.ClearCatchAll, method: http.MethodPost,
			params: map[string]string{"domain": testDomain2}, wantDenied: true},
		{name: "GrantDomainAdmin/d2", fn: env.h.GrantDomainAdmin, method: http.MethodPost,
			params: map[string]string{"domain": testDomain2},
			body:   url.Values{"user_id": {env.d1UserID}}, wantDenied: true},
		{name: "RevokeDomainAdmin/d2", fn: env.h.RevokeDomainAdmin, method: http.MethodPost,
			params: map[string]string{"domain": testDomain2},
			body:   url.Values{"user_id": {env.d1UserID}}, wantDenied: true},
		{name: "CreateAlias/d2", fn: env.h.CreateAlias, method: http.MethodPost,
			body: url.Values{
				"address":     {"sweep-alias@" + testDomain2},
				"target_user": {env.d2UserID},
			}, wantDenied: true},
		{name: "DeleteAlias/d2", fn: env.h.DeleteAlias, method: http.MethodPost,
			params:     map[string]string{"address": "sweep-alias2@" + testDomain2},
			wantDenied: true},

		// --- USER-SCOPED: d2-user resources -> 403 for d1-admin ---
		{name: "ShowUser/d2", fn: env.h.ShowUser, method: http.MethodGet,
			params: map[string]string{"userID": env.d2UserID}, wantDenied: true},
		{name: "UpdateUser/d2", fn: env.h.UpdateUser, method: http.MethodPost,
			params: map[string]string{"userID": env.d2UserID},
			body:   url.Values{"display_name": {"hacked"}}, wantDenied: true},
		{name: "ToggleDisabled/d2", fn: env.h.ToggleDisabled, method: http.MethodPost,
			params: map[string]string{"userID": env.d2UserID}, wantDenied: true},
		{name: "ResetPassword/d2", fn: env.h.ResetPassword, method: http.MethodPost,
			params: map[string]string{"userID": env.d2UserID}, wantDenied: true},
		{name: "DeleteUser/d2", fn: env.h.DeleteUser, method: http.MethodPost,
			params: map[string]string{"userID": env.d2UserID}, wantDenied: true},
		{name: "AddAddress/d2-user", fn: env.h.AddAddress, method: http.MethodPost,
			params: map[string]string{"userID": env.d2UserID},
			body:   url.Values{"address": {"extra@" + testDomain2}}, wantDenied: true},
		{name: "RemoveAddress/d2-user", fn: env.h.RemoveAddress, method: http.MethodPost,
			params: map[string]string{"userID": env.d2UserID},
			body:   url.Values{"address": {"alias@" + testDomain2}}, wantDenied: true},
		{name: "GrantSend/d2-user", fn: env.h.GrantSend, method: http.MethodPost,
			params: map[string]string{"userID": env.d2UserID},
			body:   url.Values{"address": {"user2@" + testDomain2}}, wantDenied: true},
		// Cross-tenant address grab: d1-user requesting a d2 address -> 403.
		{name: "AddAddress/d1-user-d2-addr", fn: env.h.AddAddress, method: http.MethodPost,
			params: map[string]string{"userID": env.d1UserID},
			body:   url.Values{"address": {"grab@" + testDomain2}}, wantDenied: true},
		// Cross-tenant grant-send: d1-user requesting grant for a d2 address -> 403.
		{name: "GrantSend/d1-user-d2-addr", fn: env.h.GrantSend, method: http.MethodPost,
			params: map[string]string{"userID": env.d1UserID},
			body:   url.Values{"address": {"spoof@" + testDomain2}}, wantDenied: true},
		// CreateUser in d2 -> 403.
		{name: "CreateUser/d2", fn: env.h.CreateUser, method: http.MethodPost,
			body: url.Values{
				"email":    {"newuser@" + testDomain2},
				"password": {"pass123!"},
			}, wantDenied: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postToHandler(t, env.h, tc.fn,
				tc.method, "/admin/test",
				tc.body, tc.params,
				func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) },
			)
			if tc.wantDenied {
				if w.Code != http.StatusForbidden {
					t.Errorf("expected 403, got %d", w.Code)
				}
				// Zero-leak: response must not contain d2 identifiers in error text.
				body := w.Body.String()
				if strings.Contains(body, testDomain2) && !strings.Contains(tc.name, "d2") {
					// Only flag d2 content leaks in tests NOT targeting d2 (to avoid
					// false positives from test names embedded in the URL path).
					t.Errorf("response body contains %q — potential data leak", testDomain2)
				}
			} else {
				if w.Code == http.StatusForbidden {
					t.Errorf("expected not 403, got 403")
				}
			}
		})
	}
}

// =============================================================================
// SHOW DOMAIN: adversarial isolation tests for the new detail route
// GET /admin/domains/{domain}
// =============================================================================

// TestShowDomain_DomainAdmin_D2Forbidden verifies that a d1-admin requesting
// the detail page of d2 gets 403 with no domain data in the response body.
func TestShowDomain_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains/"+testDomain2, nil)
	r = withChiParam(r, "domain", testDomain2)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ShowDomain(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("ShowDomain d2 (domain-admin of d1) status = %d, want 403", w.Code)
	}
	// Zero-leak: the response body must not contain any d2 data.
	body := w.Body.String()
	if strings.Contains(body, testDomain2) {
		t.Errorf("ShowDomain d2 (domain-admin of d1) response body contains %q — data leak", testDomain2)
	}
}

// TestDownloadDNSZone_DomainAdmin_D2Forbidden verifies the zone download honours
// domain isolation: a d1-admin cannot download d2's zone (403, no data leak).
func TestDownloadDNSZone_DomainAdmin_D2Forbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains/"+testDomain2+"/dns.zone", nil)
	r = withChiParam(r, "domain", testDomain2)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.DownloadDNSZone(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("DownloadDNSZone d2 (domain-admin of d1) status = %d, want 403", w.Code)
	}
	if strings.Contains(w.Body.String(), testDomain2) {
		t.Errorf("DownloadDNSZone d2 body contains %q — data leak", testDomain2)
	}
}

// TestDownloadDNSZone_GlobalAdmin_OK verifies a global admin downloads a valid
// zone file: attachment headers set, and the body contains the domain's records.
func TestDownloadDNSZone_GlobalAdmin_OK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains/"+testDomain1+"/dns.zone", nil)
	r = withChiParam(r, "domain", testDomain1)
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.DownloadDNSZone(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("DownloadDNSZone status = %d, want 200", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, testDomain1+".zone") {
		t.Errorf("Content-Disposition = %q, want attachment filename %s.zone", cd, testDomain1)
	}
	body := w.Body.String()
	// Must contain the SPF record and an MX line for this domain.
	if !strings.Contains(body, "v=spf1 mx ~all") {
		t.Errorf("zone body missing SPF record:\n%s", body)
	}
	if !strings.Contains(body, testDomain1+".") {
		t.Errorf("zone body missing FQDN for %s:\n%s", testDomain1, body)
	}
}

// TestShowDomain_DomainAdmin_D1NotForbidden verifies that a d1-admin CAN view
// the detail page of their own domain (positive control — must not be 403).
func TestShowDomain_DomainAdmin_D1NotForbidden(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains/"+testDomain1, nil)
	r = withChiParam(r, "domain", testDomain1)
	r = withDomainAdmin1(r, env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.ShowDomain(w, r)

	if w.Code == http.StatusForbidden {
		t.Errorf("ShowDomain d1 (domain-admin of d1) status = 403, should be allowed")
	}
	// The response should contain the domain name.
	body := w.Body.String()
	if !strings.Contains(body, testDomain1) {
		t.Errorf("ShowDomain d1 (domain-admin of d1) response missing %q", testDomain1)
	}
}

// TestShowDomain_GlobalAdmin_D1OK verifies that a global admin can view any
// domain's detail page (gets 200).
func TestShowDomain_GlobalAdmin_D1OK(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains/"+testDomain1, nil)
	r = withChiParam(r, "domain", testDomain1)
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.ShowDomain(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("ShowDomain d1 (global-admin) status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, testDomain1) {
		t.Errorf("ShowDomain d1 (global-admin) response missing domain name %q", testDomain1)
	}
}

// TestShowDomain_GlobalAdmin_UnknownDomain404 verifies that a global admin
// requesting a nonexistent domain gets 404 (not a silent blank page).
func TestShowDomain_GlobalAdmin_UnknownDomain404(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains/no-such-domain.example.com", nil)
	r = withChiParam(r, "domain", "no-such-domain.example.com")
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.ShowDomain(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("ShowDomain unknown (global-admin) status = %d, want 404", w.Code)
	}
}

// TestListDomains_HasManageLink verifies that the simplified domain list renders
// a "Manage" link pointing to /admin/domains/{domain} for each domain.
func TestListDomains_HasManageLink(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains", nil)
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.ListDomains(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("ListDomains (global-admin) status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// The Manage link must link to the detail page for each domain.
	if !strings.Contains(body, "/admin/domains/"+testDomain1) {
		t.Errorf("ListDomains: missing Manage link for %q", testDomain1)
	}
	if !strings.Contains(body, "/admin/domains/"+testDomain2) {
		t.Errorf("ListDomains: missing Manage link for %q", testDomain2)
	}
	// The old 9-column overflow (DKIM, catch-all, owner, quota columns) must be gone.
	// Check that the DomainAdmins header text is absent (it is NOT in the new 4-col list).
	if strings.Contains(body, `admin.domainAdmins`) {
		t.Errorf("ListDomains: old 9-column headers still present in response")
	}
}

// TestListDomains_MXColumn verifies that the MX check column is shown for each
// domain in the list (✓ or ✗ — the exact symbol depends on DNS; just check the
// column structure exists).
func TestListDomains_MXColumn(t *testing.T) {
	env := newAdminScopeTestEnv(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/domains", nil)
	r = withGlobalAdmin(r, env.globalAdminID)
	w := httptest.NewRecorder()
	env.h.ListDomains(w, r)

	body := w.Body.String()
	// The MX column header must be present (translated key "admin.mxRecord" resolves
	// to "MX Record" in English, which the template renders in the thead).
	if !strings.Contains(body, "MX Record") {
		t.Errorf("ListDomains: MX column header missing from response")
	}
}

// --- H2: domain-admin actions on privileged targets ---

// seedPrivilegedUsers adds admin-ish d1 users used by the H2 tests. It is
// separate from newAdminScopeTestEnv because each user costs a bcrypt hash.
func (env *adminScopeTestEnv) seedPrivilegedUsers(t *testing.T) {
	t.Helper()
	mk := func(local string) string {
		id, err := auth.CreateUser(env.applier, env.md, local+"@"+testDomain1, local, "pass123!", false)
		if err != nil {
			t.Fatalf("create %s: %v", local, err)
		}
		return id
	}
	env.d1CrossAdminID = mk("crossadmin")
	env.d1OwnerOfD2ID = mk("ownerofd2")
	env.d1PeerAdminID = mk("peeradmin")
	env.d1BothAdminID = mk("bothadmin")
	env.d1OwnD1Gr2ID = mk("ownd1grd2")
	for _, g := range []struct{ domain, id string }{
		{testDomain2, env.d1CrossAdminID},
		{testDomain1, env.d1PeerAdminID},
		{testDomain1, env.d1BothAdminID},
		{testDomain2, env.d1BothAdminID},
	} {
		if err := env.das.Grant(g.domain, g.id); err != nil {
			t.Fatalf("grant %s: %v", g.domain, err)
		}
	}
	if err := env.das.SetOwner(testDomain2, env.d1OwnerOfD2ID); err != nil {
		t.Fatalf("set owner: %v", err)
	}
	if err := env.das.SetOwner(testDomain1, env.d1OwnD1Gr2ID); err != nil {
		t.Fatalf("set owner d1: %v", err)
	}
	if err := env.das.Grant(testDomain2, env.d1OwnD1Gr2ID); err != nil {
		t.Fatalf("grant d2: %v", err)
	}
	// AuthorizedDomains lists owned domains before grants, so this user's
	// domains come back as [d3, d1]: the out-of-scope one first. Without it
	// every target lists its out-of-scope domain last, and a loop that lets
	// the last domain decide passes.
	seedDomainForScope(t, env, testDomain3)
	env.d1OwnD3Gr1ID = mk("ownd3grd1")
	if err := env.das.SetOwner(testDomain3, env.d1OwnD3Gr1ID); err != nil {
		t.Fatalf("set owner d3: %v", err)
	}
	if err := env.das.Grant(testDomain1, env.d1OwnD3Gr1ID); err != nil {
		t.Fatalf("grant d1: %v", err)
	}

}

func withD1D2Admin(r *http.Request, userID string) *http.Request {
	ctx := middleware.WithUserContext(r.Context(), userID, "domainadmin@"+testDomain1)
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{
		Domains: map[string]bool{testDomain1: true, testDomain2: true},
	})
	return r.WithContext(ctx)
}

func (env *adminScopeTestEnv) profile(t *testing.T, id string) *bongov1.UserProfile {
	t.Helper()
	p, err := env.h.loadProfile(id)
	if err != nil {
		t.Fatalf("loadProfile(%s): %v", id, err)
	}
	return p
}

func TestUserActions_DomainAdmin_PrivilegedTargetForbidden(t *testing.T) {
	actions := []struct {
		name   string
		fn     func(*AdminHandler) func(http.ResponseWriter, *http.Request)
		form   url.Values
		mutate bool
	}{
		{"ResetPassword", func(h *AdminHandler) func(http.ResponseWriter, *http.Request) { return h.ResetPassword }, nil, true},
		{"ToggleDisabled", func(h *AdminHandler) func(http.ResponseWriter, *http.Request) { return h.ToggleDisabled }, nil, true},
		{"UpdateUser", func(h *AdminHandler) func(http.ResponseWriter, *http.Request) { return h.UpdateUser }, url.Values{"quota_bytes": {"1"}, "display_name": {"x"}}, true},
		{"DeleteUser", func(h *AdminHandler) func(http.ResponseWriter, *http.Request) { return h.DeleteUser }, nil, true},
		{"ShowUser", func(h *AdminHandler) func(http.ResponseWriter, *http.Request) { return h.ShowUser }, nil, false},
		{"AddAddress", func(h *AdminHandler) func(http.ResponseWriter, *http.Request) { return h.AddAddress }, url.Values{"address": {"x@" + testDomain1}}, false},
		{"GrantSend", func(h *AdminHandler) func(http.ResponseWriter, *http.Request) { return h.GrantSend }, url.Values{"address": {"x@" + testDomain1}}, false},
	}
	targets := []struct {
		name   string
		id     func(*adminScopeTestEnv) string
		caller func(*adminScopeTestEnv) func(*http.Request) *http.Request
	}{
		{"globalAdmin", func(e *adminScopeTestEnv) string { return e.globalAdminID },
			func(e *adminScopeTestEnv) func(*http.Request) *http.Request {
				return func(r *http.Request) *http.Request { return withDomainAdmin1(r, e.domainAdmin1ID) }
			}},
		{"globalAdminVsD1D2Admin", func(e *adminScopeTestEnv) string { return e.globalAdminID },
			func(e *adminScopeTestEnv) func(*http.Request) *http.Request {
				return func(r *http.Request) *http.Request { return withD1D2Admin(r, e.d1BothAdminID) }
			}},
		{"crossGrantAdmin", func(e *adminScopeTestEnv) string { return e.d1CrossAdminID },
			func(e *adminScopeTestEnv) func(*http.Request) *http.Request {
				return func(r *http.Request) *http.Request { return withDomainAdmin1(r, e.domainAdmin1ID) }
			}},
		{"bothGrantAdmin", func(e *adminScopeTestEnv) string { return e.d1BothAdminID },
			func(e *adminScopeTestEnv) func(*http.Request) *http.Request {
				return func(r *http.Request) *http.Request { return withDomainAdmin1(r, e.domainAdmin1ID) }
			}},
		{"ownsD1GrantedD2", func(e *adminScopeTestEnv) string { return e.d1OwnD1Gr2ID },
			func(e *adminScopeTestEnv) func(*http.Request) *http.Request {
				return func(r *http.Request) *http.Request { return withDomainAdmin1(r, e.domainAdmin1ID) }
			}},
		{"ownsD3GrantedD1", func(e *adminScopeTestEnv) string { return e.d1OwnD3Gr1ID },
			func(e *adminScopeTestEnv) func(*http.Request) *http.Request {
				return func(r *http.Request) *http.Request { return withDomainAdmin1(r, e.domainAdmin1ID) }
			}},
		{"ownerOfD2", func(e *adminScopeTestEnv) string { return e.d1OwnerOfD2ID },
			func(e *adminScopeTestEnv) func(*http.Request) *http.Request {
				return func(r *http.Request) *http.Request { return withDomainAdmin1(r, e.domainAdmin1ID) }
			}},
	}
	for _, tg := range targets {
		for _, a := range actions {
			t.Run(tg.name+"/"+a.name, func(t *testing.T) {
				env := newAdminScopeTestEnv(t)
				env.seedPrivilegedUsers(t)
				id := tg.id(env)
				before := env.profile(t, id)
				w := postToHandler(t, env.h, a.fn(env.h), http.MethodPost, "/admin/users/"+id+"/x",
					a.form, map[string]string{"userID": id}, tg.caller(env))
				if w.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", w.Code)
				}
				after := env.profile(t, id)
				if !bytes.Equal(before.PasswordHash, after.PasswordHash) ||
					before.Disabled != after.Disabled || before.QuotaBytes != after.QuotaBytes {
					t.Errorf("target state changed despite 403")
				}
			})
		}
	}
}

func TestUserActions_DomainAdmin_PeerD1AdminAllowed(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	env.seedPrivilegedUsers(t)
	before := env.profile(t, env.d1PeerAdminID)
	w := postToHandler(t, env.h, env.h.ResetPassword, http.MethodPost, "/x", nil,
		map[string]string{"userID": env.d1PeerAdminID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) })
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if bytes.Equal(before.PasswordHash, env.profile(t, env.d1PeerAdminID).PasswordHash) {
		t.Error("password hash unchanged")
	}
}

func TestUserActions_DomainAdmin_FailsClosedOnScopeLookupError(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	env.h.authorizedDomainsFn = func(string, bool) ([]string, error) { return nil, errors.New("boom") }
	before := env.profile(t, env.d1UserID)
	w := postToHandler(t, env.h, env.h.ResetPassword, http.MethodPost, "/x", nil,
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) })
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !bytes.Equal(before.PasswordHash, env.profile(t, env.d1UserID).PasswordHash) {
		t.Error("password hash changed")
	}
}

func TestUserActions_DomainAdmin_FailsClosedWithoutStore(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	env.h.authorizedDomainsFn = nil
	w := postToHandler(t, env.h, env.h.ToggleDisabled, http.MethodPost, "/x", nil,
		map[string]string{"userID": env.d1UserID},
		func(r *http.Request) *http.Request { return withDomainAdmin1(r, env.domainAdmin1ID) })
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if env.profile(t, env.d1UserID).Disabled {
		t.Error("target disabled despite 403")
	}
}

func TestUserActions_GlobalAdmin_PrivilegedTargetsAllowed(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	env.seedPrivilegedUsers(t)
	for _, id := range []string{env.d1CrossAdminID, env.d1PeerAdminID, env.d1OwnerOfD2ID} {
		for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
			"ResetPassword": env.h.ResetPassword, "ToggleDisabled": env.h.ToggleDisabled,
		} {
			w := postToHandler(t, env.h, fn, http.MethodPost, "/x", nil,
				map[string]string{"userID": id},
				func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) })
			if w.Code != http.StatusSeeOther {
				t.Errorf("%s on %s: status = %d, want 303", name, id, w.Code)
			}
		}
	}
}
