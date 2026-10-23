package middleware

import (
	"context"
	"net/http"
	"strings"
)

// AdminScope holds the computed authorization scope for an admin request.
// Injected into the context by RequireAdminOrDomainAdmin and read by handlers
// via AdminScopeFromContext.
type AdminScope struct {
	// Global is true when the caller is a global administrator.
	// When Global is true, Domains is not populated and all resources are accessible.
	Global bool

	// Domains is the set of lower-cased domain names the caller may administer.
	// Only meaningful when Global is false.
	Domains map[string]bool
}

// HasDomain reports whether the caller has administrative access to domain d.
// Global admins always return true. Domain-admins return true only for
// domains in their Domains set.
func (s AdminScope) HasDomain(d string) bool {
	if s.Global {
		return true
	}
	return s.Domains[strings.ToLower(d)]
}

const adminScopeKey contextKey = "adminScope"

// RequireAdminOrDomainAdmin is the admin-group gate, and replaces RequireAdmin
// for that purpose -- RequireAdmin still exists and is still exported, so mount
// this one on admin-group routes. It admits global admins
// (isAdmin=true from RequireAuth context) and users who administer at least one
// domain (authorizedFn returns ≥1 domain); regular users and store errors both
// get 403 (fail-closed). When RequireAuth could not read the account state
// (SessionStateUnavailable) every caller, global or domain admin, gets 503
// before authorizedFn is consulted, so an unreadable profile never downgrades
// a demoted admin to domain scope or grants a stale one. It injects an AdminScope into the request context so
// downstream handlers can enforce per-resource access without re-querying the
// store.
//
// authorizedFn is typically DomainAdminStore.AuthorizedDomains. It MUST NOT be
// called for global admins (isGlobalAdmin=true) as that would scan all Pebble
// domains unnecessarily; the middleware already implements this optimization,
// so this is a property it guarantees rather than an obligation on the caller.
func RequireAdminOrDomainAdmin(authorizedFn func(userID string, isGlobalAdmin bool) ([]string, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if SessionStateUnavailable(r.Context()) {
				http.Error(w, "account state unavailable", http.StatusServiceUnavailable)
				return
			}
			isAdmin := AdminFromContext(r.Context())
			userID, _ := UserFromContext(r.Context())

			scope := AdminScope{Global: isAdmin}

			if !isAdmin {
				// Non-global: check whether they administer any domains.
				if authorizedFn == nil {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				domains, err := authorizedFn(userID, false)
				if err != nil {
					// Fail closed: scope computation error → deny.
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				if len(domains) == 0 {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				scope.Domains = make(map[string]bool, len(domains))
				for _, d := range domains {
					scope.Domains[strings.ToLower(d)] = true
				}
			}

			ctx := context.WithValue(r.Context(), adminScopeKey, scope)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminScopeFromContext retrieves the AdminScope from the context.
// Returns a zero AdminScope (non-global, no domains) if not set.
func AdminScopeFromContext(ctx context.Context) AdminScope {
	s, _ := ctx.Value(adminScopeKey).(AdminScope)
	return s
}

// WithAdminScopeContext injects an AdminScope into a context.
// This is intended for use in tests; production code should use the
// RequireAdminOrDomainAdmin middleware instead.
func WithAdminScopeContext(ctx context.Context, scope AdminScope) context.Context {
	return context.WithValue(ctx, adminScopeKey, scope)
}
