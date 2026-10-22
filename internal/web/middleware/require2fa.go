package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const require2FAKey contextKey = "require2FA"

// ProfileLookup reads keys from the store to check system settings and user profiles.
type ProfileLookup interface {
	GetKey(key []byte) ([]byte, error)
}

// Require2FAFromContext returns true when the system requires 2FA and the
// current user has not yet enabled it. Templates use this to show a
// persistent warning banner.
func Require2FAFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(require2FAKey).(bool)
	return v
}

// Require2FA returns middleware that redirects users without 2FA enabled to
// the setup page when the system-wide require_2fa policy is active.
// It also injects a context flag so templates can show a persistent banner.
//
// It fails closed: when the policy or the user's profile cannot be read or
// decoded, the request is refused with 503 rather than waved through. Only a
// missing policy key means "no policy" (the admin toggle deletes the key to
// switch the policy off).
func Require2FA(profiles ProfileLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if system-wide 2FA is enforced.
			data, err := profiles.GetKey([]byte("/system/settings/require_2fa"))
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				slog.Warn("require 2fa: policy read failed", "error", err)
				http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			if err != nil || string(data) != "1" {
				// No policy or not enforced, pass through.
				next.ServeHTTP(w, r)
				return
			}

			// Get current user from context.
			userID, _ := UserFromContext(r.Context())
			if userID == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Check if user already has 2FA enabled.
			profileKey := []byte("/user/" + userID + "/profile")
			profileData, err := profiles.GetKey(profileKey)
			if err != nil {
				// RequireAuth has already revoked sessions whose profile is
				// gone, so even not-found here is an anomaly: deny.
				slog.Warn("require 2fa: profile read failed", "user_id", userID, "error", err)
				http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
				return
			}

			var profile bongov1.UserProfile
			if err := proto.Unmarshal(profileData, &profile); err != nil {
				slog.Warn("require 2fa: profile decode failed", "user_id", userID, "error", err)
				http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
				return
			}

			if profile.TotpEnabled {
				next.ServeHTTP(w, r)
				return
			}

			// User needs 2FA. Inject context flag for persistent banner.
			ctx := context.WithValue(r.Context(), require2FAKey, true)
			r = r.WithContext(ctx)

			// Allow exempt paths (security settings, logout, login, static).
			path := r.URL.Path
			if strings.HasPrefix(path, "/settings/security") ||
				strings.HasPrefix(path, "/logout") ||
				strings.HasPrefix(path, "/login") ||
				strings.HasPrefix(path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}

			// Redirect to security settings.
			http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
		})
	}
}
