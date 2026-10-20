package middleware

import (
	"net/http"

	"github.com/bongo-project/bongo/internal/security"
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

// RequireBlobSyncAuth validates node-to-node blob pulls. It uses a key distinct
// from RequireInternalAuth, so control-plane signatures do not open blob routes
// and vice versa.
func RequireBlobSyncAuth(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !security.VerifyBlobSyncRequest(r, secret) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
