package middleware

import (
	"net/http"

	"github.com/bongo-go/bongo-go/internal/security"
)

// RequireInternalAuth validates internal control-plane requests.
func RequireInternalAuth(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !security.VerifyInternalRequest(r, secret) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
