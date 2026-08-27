package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// newCatchAllTestHandler builds an AdminHandler wired with real Pebble and a
// MetadataManager so owner-validation queries can execute.
func newCatchAllTestHandler(t *testing.T) (*AdminHandler, *store.CatchAllStore, *store.MetadataManager, *store.PebbleStore) {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	mgr, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })

	applier := &directApplierTags{ps: ps}
	cs := store.NewCatchAllStore(ps, applier)

	h := NewAdminHandler(ps, nil, mgr, applier, discardLogger())
	h.SetCatchAllStore(cs)
	return h, cs, mgr, ps
}

// insertTestUser puts a minimal user row into the system SQLite so owner-validation passes.
func insertTestUser(t *testing.T, mgr *store.MetadataManager, id, email string) {
	t.Helper()
	_, err := mgr.System.DB().Exec(
		`INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
		 VALUES (?, ?, ?, 1, unixepoch(), unixepoch())
		 ON CONFLICT(id) DO NOTHING`,
		id, email, "Test User",
	)
	if err != nil {
		t.Fatalf("insertTestUser %q: %v", id, err)
	}
}

// postCatchAll posts to /admin/domains/{domain}/catchall with form data.
// Uses a global-admin scope so the test is not affected by tenant isolation checks.
func postCatchAll(t *testing.T, h *AdminHandler, domain, ownerID string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"owner_user_id": {ownerID}}
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/admin/domains/%s/catchall", domain),
		strings.NewReader(form.Encode()),
	)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	r = r.WithContext(ctx)
	r = withChiParam(r, "domain", domain)
	w := httptest.NewRecorder()
	h.SetCatchAll(w, r)
	return w
}

// deleteCatchAll posts to /admin/domains/{domain}/catchall/delete.
// Uses a global-admin scope so the test is not affected by tenant isolation checks.
func deleteCatchAll(t *testing.T, h *AdminHandler, domain string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/admin/domains/%s/catchall/delete", domain),
		nil,
	)
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	r = r.WithContext(ctx)
	r = withChiParam(r, "domain", domain)
	w := httptest.NewRecorder()
	h.ClearCatchAll(w, r)
	return w
}

// TestAdminSetCatchAll_OK verifies a successful set redirects and persists via Raft.
func TestAdminSetCatchAll_OK(t *testing.T) {
	h, cs, mgr, _ := newCatchAllTestHandler(t)

	insertTestUser(t, mgr, "user-abc", "alice@example.com")

	w := postCatchAll(t, h, "example.com", "user-abc")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("SetCatchAll status = %d, want %d; body: %s", w.Code, http.StatusSeeOther, w.Body.String())
	}

	// Value must be persisted in Pebble via catch-all store.
	ownerID, ok, err := cs.Get("example.com")
	if err != nil {
		t.Fatalf("CatchAllStore.Get: %v", err)
	}
	if !ok || ownerID != "user-abc" {
		t.Errorf("catch-all not persisted: got (%q, %v), want (user-abc, true)", ownerID, ok)
	}
}

// TestAdminSetCatchAll_UnknownOwner verifies that setting a catch-all with a
// non-existent owner returns 400 Bad Request (owner validation).
func TestAdminSetCatchAll_UnknownOwner(t *testing.T) {
	h, cs, _, _ := newCatchAllTestHandler(t)

	// Do NOT insert any user — owner does not exist.
	w := postCatchAll(t, h, "example.com", "ghost-user")
	if w.Code == http.StatusSeeOther {
		t.Fatal("SetCatchAll should reject unknown owner, but redirected (accepted)")
	}
	if w.Code < 400 {
		t.Fatalf("SetCatchAll status = %d for unknown owner, want 4xx", w.Code)
	}

	// Nothing should be stored.
	_, ok, err := cs.Get("example.com")
	if err != nil {
		t.Fatalf("CatchAllStore.Get: %v", err)
	}
	if ok {
		t.Error("catch-all was stored despite unknown owner")
	}
}

// TestAdminSetCatchAll_EmptyOwner verifies that an empty owner_user_id returns 400.
func TestAdminSetCatchAll_EmptyOwner(t *testing.T) {
	h, _, _, _ := newCatchAllTestHandler(t)

	w := postCatchAll(t, h, "example.com", "")
	if w.Code == http.StatusSeeOther {
		t.Fatal("SetCatchAll should reject empty owner, but redirected")
	}
	if w.Code < 400 {
		t.Fatalf("SetCatchAll status = %d for empty owner, want 4xx", w.Code)
	}
}

// TestAdminClearCatchAll_OK verifies a successful clear redirects and removes the value.
func TestAdminClearCatchAll_OK(t *testing.T) {
	h, cs, mgr, _ := newCatchAllTestHandler(t)

	insertTestUser(t, mgr, "user-abc", "alice@example.com")

	// First set it.
	w := postCatchAll(t, h, "example.com", "user-abc")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("SetCatchAll status = %d", w.Code)
	}

	// Now clear it.
	w = deleteCatchAll(t, h, "example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("ClearCatchAll status = %d, want %d; body: %s", w.Code, http.StatusSeeOther, w.Body.String())
	}

	// Value must be gone from Pebble.
	_, ok, err := cs.Get("example.com")
	if err != nil {
		t.Fatalf("CatchAllStore.Get after clear: %v", err)
	}
	if ok {
		t.Error("catch-all still present after ClearCatchAll")
	}
}

// TestAdminClearCatchAll_Idempotent verifies clearing a domain with no catch-all is harmless.
func TestAdminClearCatchAll_Idempotent(t *testing.T) {
	h, _, _, _ := newCatchAllTestHandler(t)

	w := deleteCatchAll(t, h, "notset.example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("ClearCatchAll (nothing set) status = %d, want %d", w.Code, http.StatusSeeOther)
	}
}
