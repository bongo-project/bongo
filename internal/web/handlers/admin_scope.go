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
	return profile, true
}
