package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"

	"github.com/bongo-project/bongo/internal/security"
)

const csrfCtxKey contextKey = "csrfToken"

const (
	csrfHeaderName = "X-CSRF-Token"
	csrfFieldName  = "_csrf"
	csrfPurpose    = "bongo/csrf-token/v1"

	// csrfPeekLimit bounds how much of a multipart body is read to find the
	// leading _csrf part. csrfFieldMaxBytes bounds that part's value.
	csrfPeekLimit     = 64 << 10
	csrfFieldMaxBytes = 256
	// csrfFormMaxBytes caps a url-encoded form body read by the middleware.
	// It matches net/http's own ParseForm limit, so it adds no new exposure.
	csrfFormMaxBytes = 10 << 20
)

// csrfToken derives a stateless, session-bound CSRF token from the authenticated
// user ID. It is unforgeable without the server secret and unreadable to a
// cross-origin attacker (the session cookie is HttpOnly and never exposed
// cross-origin), so requiring it to be echoed on mutating requests proves
// same-origin provenance as defence-in-depth on top of RequireSameOrigin.
func csrfToken(secret []byte, userID string) string {
	mac := hmac.New(sha256.New, security.DeriveKey(secret, csrfPurpose))
	mac.Write([]byte(userID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// InjectCSRF stores the current user's CSRF token in the request context so
// templates can render it into the page (hx-headers on <body>, so every HTMX
// mutation sends it automatically). Must run after RequireAuth.
func InjectCSRF(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if userID, _ := UserFromContext(r.Context()); userID != "" {
				ctx := context.WithValue(r.Context(), csrfCtxKey, csrfToken(secret, userID))
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CSRFTokenFromContext returns the CSRF token for the current request, or "".
func CSRFTokenFromContext(ctx context.Context) string {
	v, _ := ctx.Value(csrfCtxKey).(string)
	return v
}

// RequireCSRF enforces the token on state-changing requests. The expected token
// is recomputed from the authenticated user and must be echoed in the
// X-CSRF-Token header (HTMX sends it via the hx-headers rendered on <body>).
// When the header is present the body is never touched. Native <form>
// submissions cannot set a header, so they carry a hidden _csrf field instead:
//   - url-encoded forms are parsed with the standard 10MB ParseForm cap;
//   - multipart forms are NOT parsed here (that would buffer up to 32MB, spill
//     the rest to unbounded temp files and make every handler's MaxBytesReader
//     moot). Only the first part is peeked, within csrfPeekLimit, and must be
//     _csrf; the consumed bytes are replayed so the handler sees the full body.
//     Forms must therefore render _csrf as their first field.
//
// Safe methods and unauthenticated requests (which rely on RequireSameOrigin)
// pass through.
//
// enforce gates the check: it is disabled in dev mode (mirroring
// AllowInsecureAuth), where the local single-user server has no CSRF exposure
// and non-browser test clients cannot run the HTMX that supplies the header.
func RequireCSRF(secret []byte, enforce bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enforce {
				next.ServeHTTP(w, r)
				return
			}
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				next.ServeHTTP(w, r)
				return
			}

			userID, _ := UserFromContext(r.Context())
			if userID == "" {
				next.ServeHTTP(w, r)
				return
			}

			want := csrfToken(secret, userID)
			got := r.Header.Get(csrfHeaderName)
			if got == "" {
				got = formCSRFToken(w, r)
			}
			if got == "" || !hmac.Equal([]byte(got), []byte(want)) {
				http.Error(w, "Forbidden: invalid CSRF token", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
