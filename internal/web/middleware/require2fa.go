package middleware

import (
	"net/http"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// ProfileLookup reads keys from the store to check system settings and user profiles.
type ProfileLookup interface {
	GetKey(key []byte) ([]byte, error)
}

// Require2FA returns middleware that redirects users without 2FA enabled to
// the setup page when the system-wide require_2fa policy is active.
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

			// User does not have 2FA enabled. Check if current path is exempt.
			path := r.URL.Path
			if strings.HasPrefix(path, "/settings/security") ||
				strings.HasPrefix(path, "/logout") ||
				strings.HasPrefix(path, "/login") ||
				strings.HasPrefix(path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}

			// Redirect to security settings with a flash message.
			http.Redirect(w, r, "/settings/security?flash=Your+administrator+requires+two-factor+authentication.+Please+set+it+up+to+continue.", http.StatusSeeOther)
		})
	}
}
