package middleware

import (
	"context"
	"net/http"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
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
func Require2FA(profiles ProfileLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if system-wide 2FA is enforced.
			data, err := profiles.GetKey([]byte("/system/settings/require_2fa"))
			if err != nil || string(data) != "1" {
				// Not enforced, pass through.
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
				// Cannot load profile, pass through.
				next.ServeHTTP(w, r)
				return
			}

			var profile bongov1.UserProfile
			if err := proto.Unmarshal(profileData, &profile); err != nil {
				next.ServeHTTP(w, r)
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
