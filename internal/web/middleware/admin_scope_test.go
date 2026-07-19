package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// noopHandler is a trivial next handler that records it was called.
type noopHandler struct{ called bool }

func (h *noopHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
}

// makeRequest builds a request whose context has userID, email, and isAdmin injected
// (as RequireAuth would do).
func makeRequest(userID string, isAdmin bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/admin", nil)
	ctx := WithUserContext(r.Context(), userID, userID+"@example.com")
	ctx = context.WithValue(ctx, adminKey, isAdmin)
	r = r.WithContext(ctx)
	return r
}

// TestRequireAdminOrDomainAdmin_GlobalAdmin verifies that a global admin is allowed
// and receives a Global scope.
func TestRequireAdminOrDomainAdmin_GlobalAdmin(t *testing.T) {
	next := &noopHandler{}
	authFn := func(userID string, isGlobalAdmin bool) ([]string, error) {
		// Should not be called for global admins.
		t.Error("authorizedFn called for global admin")
		return nil, nil
	}
	mw := RequireAdminOrDomainAdmin(authFn)(next)

	r := makeRequest("admin1", true)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !next.called {
		t.Error("next handler not called")
	}
}

// TestRequireAdminOrDomainAdmin_DomainAdmin verifies that a user with ≥1
// authorized domain is allowed and receives a domain scope.
func TestRequireAdminOrDomainAdmin_DomainAdmin(t *testing.T) {
	next := &noopHandler{}
	authFn := func(userID string, isGlobalAdmin bool) ([]string, error) {
		return []string{"d1.example.com"}, nil
	}
	mw := RequireAdminOrDomainAdmin(authFn)(next)

	r := makeRequest("domainadmin1", false)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !next.called {
		t.Error("next handler not called")
	}
}

// TestRequireAdminOrDomainAdmin_RegularUser verifies that a user with no
// authorized domains is denied.
func TestRequireAdminOrDomainAdmin_RegularUser(t *testing.T) {
	next := &noopHandler{}
	authFn := func(userID string, isGlobalAdmin bool) ([]string, error) {
		return nil, nil // no domains
	}
	mw := RequireAdminOrDomainAdmin(authFn)(next)

	r := makeRequest("regular1", false)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if next.called {
		t.Error("next handler should not have been called")
	}
}

// TestRequireAdminOrDomainAdmin_StoreError verifies that a store error causes
// a 403 (fail-closed).
func TestRequireAdminOrDomainAdmin_StoreError(t *testing.T) {
	next := &noopHandler{}
	authFn := func(userID string, isGlobalAdmin bool) ([]string, error) {
		return nil, http.ErrNoCookie // any error
	}
	mw := RequireAdminOrDomainAdmin(authFn)(next)

	r := makeRequest("user1", false)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// TestRequireAdminOrDomainAdmin_ScopeInjectedForGlobal verifies scope is
// injected as Global=true for a global admin.
func TestRequireAdminOrDomainAdmin_ScopeInjectedForGlobal(t *testing.T) {
	var capturedScope AdminScope
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedScope = AdminScopeFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	authFn := func(userID string, isGlobalAdmin bool) ([]string, error) {
		return nil, nil
	}
	mw := RequireAdminOrDomainAdmin(authFn)(next)

	r := makeRequest("admin1", true)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if !capturedScope.Global {
		t.Error("scope.Global should be true for global admin")
	}
	if !capturedScope.HasDomain("any.domain.com") {
		t.Error("global scope should have access to any domain")
	}
}

// TestRequireAdminOrDomainAdmin_ScopeInjectedForDomainAdmin verifies scope is
// injected with the correct Domains set for a domain-admin.
func TestRequireAdminOrDomainAdmin_ScopeInjectedForDomainAdmin(t *testing.T) {
	var capturedScope AdminScope
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedScope = AdminScopeFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	authFn := func(userID string, isGlobalAdmin bool) ([]string, error) {
		return []string{"d1.example.com", "d2.example.com"}, nil
	}
	mw := RequireAdminOrDomainAdmin(authFn)(next)

	r := makeRequest("domainadmin1", false)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if capturedScope.Global {
		t.Error("scope.Global should be false for domain-admin")
	}
	if !capturedScope.HasDomain("d1.example.com") {
		t.Error("scope should include d1.example.com")
	}
	if !capturedScope.HasDomain("d2.example.com") {
		t.Error("scope should include d2.example.com")
	}
	if capturedScope.HasDomain("d3.example.com") {
		t.Error("scope should NOT include d3.example.com")
	}
}
