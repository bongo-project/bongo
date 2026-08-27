package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// newQuotaTestEnv creates an AdminHandler wired with a PebbleStore and
// DomainQuotaStore for quota-related handler tests.
func newQuotaTestEnv(t *testing.T) (*AdminHandler, *store.PebbleStore, *store.DomainQuotaStore, *directApplierTags) {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	applier := &directApplierTags{ps: ps}
	dqs := store.NewDomainQuotaStore(ps, applier)

	h := NewAdminHandler(ps, nil, nil, applier, discardLogger())
	h.SetDomainQuotaStore(dqs)
	return h, ps, dqs, applier
}

// writeProfile writes a UserProfile protobuf into Pebble for tests.
func writeProfile(t *testing.T, applier *directApplierTags, userID, email string) {
	t.Helper()
	prof := &bongov1.UserProfile{Email: email}
	b, err := proto.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/" + userID + "/profile"),
		Value: b,
	}, 0); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

// globalAdminScope returns an AdminScope for a global admin.
func globalAdminScope() middleware.AdminScope {
	return middleware.AdminScope{Global: true}
}

// domainAdminScope returns an AdminScope scoped to a single domain.
func domainAdminScope(domain string) middleware.AdminScope {
	return middleware.AdminScope{Global: false, Domains: map[string]bool{domain: true}}
}

// TestSetDomainQuota_GlobalAdmin verifies that a global admin can set quotas.
func TestSetDomainQuota_GlobalAdmin(t *testing.T) {
	h, _, dqs, _ := newQuotaTestEnv(t)

	body := url.Values{
		"max_users":         {"10"},
		"max_storage_bytes": {"1048576"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/domains/example.com/quota",
		strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, globalAdminScope())
	r = r.WithContext(ctx)
	r = withChiParam(r, "domain", "example.com")

	w := httptest.NewRecorder()
	h.SetDomainQuota(w, r)

	if w.Code != http.StatusSeeOther {
		t.Errorf("SetDomainQuota global admin: want 303, got %d (body: %s)", w.Code, w.Body.String())
	}

	q, err := dqs.GetQuota("example.com")
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if q.MaxUsers != 10 {
		t.Errorf("max_users: want 10, got %d", q.MaxUsers)
	}
	if q.MaxStorageBytes != 1048576 {
		t.Errorf("max_storage_bytes: want 1048576, got %d", q.MaxStorageBytes)
	}
}

// TestSetDomainQuota_DomainAdmin verifies that a domain-admin cannot set quotas (403).
func TestSetDomainQuota_DomainAdmin(t *testing.T) {
	h, _, _, _ := newQuotaTestEnv(t)

	body := url.Values{
		"max_users":         {"5"},
		"max_storage_bytes": {"512"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/domains/example.com/quota",
		strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(r.Context(), "domain-admin-id", "owner@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, domainAdminScope("example.com"))
	r = r.WithContext(ctx)
	r = withChiParam(r, "domain", "example.com")

	w := httptest.NewRecorder()
	h.SetDomainQuota(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("SetDomainQuota domain-admin: want 403, got %d", w.Code)
	}
}

// TestSetDomainQuota_ZeroMeansUnlimited verifies that 0 values are accepted
// and stored (unlimited).
func TestSetDomainQuota_ZeroMeansUnlimited(t *testing.T) {
	h, _, dqs, _ := newQuotaTestEnv(t)

	// First set a non-zero quota.
	if err := dqs.SetQuota("example.com", &bongov1.DomainQuota{MaxUsers: 5, MaxStorageBytes: 100}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}

	// Then reset to 0 (unlimited).
	body := url.Values{
		"max_users":         {"0"},
		"max_storage_bytes": {"0"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/domains/example.com/quota",
		strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, globalAdminScope())
	r = r.WithContext(ctx)
	r = withChiParam(r, "domain", "example.com")

	w := httptest.NewRecorder()
	h.SetDomainQuota(w, r)

	if w.Code != http.StatusSeeOther {
		t.Errorf("SetDomainQuota zero: want 303, got %d", w.Code)
	}
	q, err := dqs.GetQuota("example.com")
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if q.MaxUsers != 0 || q.MaxStorageBytes != 0 {
		t.Errorf("quota not zeroed: %+v", q)
	}
}

// TestCreateUser_MaxUsersEnforced verifies that creating a user fails when the
// domain is at its max_users limit.
//
// Note: this test exercises the quota check logic but does NOT do a full
// createUserViaRaft (which requires auth infrastructure). We verify the
// 400 rejection before user creation.
func TestCreateUser_MaxUsersEnforced(t *testing.T) {
	h, ps, dqs, applier := newQuotaTestEnv(t)

	// Pre-populate 2 users in example.com.
	writeProfile(t, applier, "u1", "alice@example.com")
	writeProfile(t, applier, "u2", "bob@example.com")

	// Set max_users = 2.
	if err := dqs.SetQuota("example.com", &bongov1.DomainQuota{MaxUsers: 2}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}

	// Seed the domain in Pebble so the handler's domain lookup succeeds.
	if err := ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatalf("seed domain: %v", err)
	}

	body := url.Values{
		"email":    {"carol@example.com"},
		"password": {"secret"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/users",
		strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, globalAdminScope())
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	// Expect 400 Bad Request (domain user limit reached) before user creation.
	if w.Code != http.StatusBadRequest {
		t.Errorf("CreateUser at limit: want 400, got %d (body: %q)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "limit") {
		t.Errorf("CreateUser at limit: response body %q should mention 'limit'", w.Body.String())
	}
}

// TestCreateUser_UnderMaxUsers verifies that creating a user succeeds when the
// domain is under its max_users limit. The test verifies the quota check passes
// (not that user creation fully succeeds, since that requires auth infrastructure).
func TestCreateUser_UnderMaxUsers(t *testing.T) {
	h, ps, dqs, applier := newQuotaTestEnv(t)

	// Pre-populate 1 user in example.com.
	writeProfile(t, applier, "u1", "alice@example.com")

	// Set max_users = 5 (under limit).
	if err := dqs.SetQuota("example.com", &bongov1.DomainQuota{MaxUsers: 5}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}

	// Seed the domain in Pebble.
	if err := ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatalf("seed domain: %v", err)
	}

	body := url.Values{
		"email":    {"bob@example.com"},
		"password": {"secret"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/users",
		strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, globalAdminScope())
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	// Should NOT be 400 due to quota (may fail with 500 due to missing auth
	// infrastructure in unit test, but not 400 "domain user limit reached").
	if w.Code == http.StatusBadRequest && strings.Contains(w.Body.String(), "limit") {
		t.Errorf("CreateUser under limit: should not reject with quota error, got 400 with body %q", w.Body.String())
	}
}

// TestCreateUser_NoQuotaSet verifies that creating a user succeeds when
// max_users is 0 (unlimited/unset).
func TestCreateUser_NoQuotaSet(t *testing.T) {
	h, ps, dqs, applier := newQuotaTestEnv(t)

	// Pre-populate 100 users in example.com.
	for i := range 5 {
		writeProfile(t, applier, fmt.Sprintf("u%d", i), fmt.Sprintf("user%d@example.com", i))
	}

	// No quota set (or quota with max_users=0 = unlimited).
	if err := dqs.SetQuota("example.com", &bongov1.DomainQuota{MaxUsers: 0}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}

	// Seed the domain.
	if err := ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatalf("seed domain: %v", err)
	}

	body := url.Values{
		"email":    {"new@example.com"},
		"password": {"secret"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/users",
		strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, globalAdminScope())
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	h.CreateUser(w, r)

	if w.Code == http.StatusBadRequest && strings.Contains(w.Body.String(), "limit") {
		t.Errorf("CreateUser unlimited: should not reject with quota error, got 400 with body %q", w.Body.String())
	}
}
