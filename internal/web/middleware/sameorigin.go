package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// RequireSameOrigin rejects cross-site mutating requests for cookie-auth flows.
func RequireSameOrigin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				next.ServeHTTP(w, r)
				return
			}

			if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(r, origin) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			if referer := r.Header.Get("Referer"); referer != "" && !sameOrigin(r, referer) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func sameOrigin(r *http.Request, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
