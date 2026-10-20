package handlers

import (
	"net/http"

	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// requireGlobalAdmin checks that the request scope is global (i.e., the caller
// is a global administrator). If not, it writes 403 Forbidden and returns false.
// Call this at the top of handlers that are global-only endpoints.
func requireGlobalAdmin(w http.ResponseWriter, scope middleware.AdminScope) bool {
	if scope.Global {
		return true
	}
	http.Error(w, "Forbidden", http.StatusForbidden)
	return false
}

// requireDomainAccess checks that the caller has administrative access to the
// given domain. If not, it writes 403 Forbidden and returns false.
// Call this at the top of domain-scoped mutation handlers.
func requireDomainAccess(w http.ResponseWriter, scope middleware.AdminScope, domain string) bool {
	if scope.HasDomain(domain) {
		return true
	}
	http.Error(w, "Forbidden", http.StatusForbidden)
	return false
}

// requireUserAccess loads the profile for userID and verifies the caller has
// access to the user's primary domain.
//
// Zero-leak semantics: a domain-admin gets 403 for both out-of-scope AND
// nonexistent target users, so the response reveals nothing about whether the
// user exists; a global admin gets an informative 404 instead.
//
// Returns (profile, true) when access is granted, (nil, false) when access is
// denied or the profile cannot be loaded (the HTTP response has already been
// written in that case).
func (h *AdminHandler) requireUserAccess(w http.ResponseWriter, scope middleware.AdminScope, userID string) (*bongov1.UserProfile, bool) {
	profile, err := h.loadProfile(userID)
	if err != nil {
		if scope.Global {
			// Global admins get an informative 404.
			http.Error(w, "user not found", http.StatusNotFound)
		} else {
			// Domain-admins get a 403 with no existence hint.
			http.Error(w, "Forbidden", http.StatusForbidden)
		}
		return nil, false
	}
	domain := domainFromEmail(profile.Email)
	if !scope.HasDomain(domain) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil, false
	}
	if !scope.Global && !h.targetWithinScope(profile, userID, scope) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil, false
	}
	return profile, true
}

// targetWithinScope reports whether a domain-admin may manage the target: the
// target must not be a global admin, and every domain the target administers
// (owned or granted) must lie inside the caller's scope. Fails closed when the
// target's scope cannot be resolved.
func (h *AdminHandler) targetWithinScope(profile *bongov1.UserProfile, userID string, scope middleware.AdminScope) bool {
	if profile.IsAdmin {
		return false
	}
	if h.authorizedDomainsFn == nil {
		return false
	}
	// false on purpose: the global-admin case is the explicit IsAdmin check above.
	domains, err := h.authorizedDomainsFn(userID, false)
	if err != nil {
		h.logger.Warn("admin scope: resolving target domains failed", "user_id", userID, "error", err)
		return false
	}
	for _, d := range domains {
		if !scope.HasDomain(d) {
			return false
		}
	}
	return true
}
