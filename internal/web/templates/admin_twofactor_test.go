package templates

import (
	"strings"
	"testing"
)

// The Reset two-factor action is offered only for an account with 2FA on,
// and never for a resource, which has no sign-in.
func TestAdminUserFormResetTwoFactor(t *testing.T) {
	const action = `hx-post="/admin/users/1/reset-2fa"`
	tests := []struct {
		name string
		user AdminUserInfo
		want bool
	}{
		{"2FA on", AdminUserInfo{TwoFactorEnabled: true}, true},
		{"2FA off", AdminUserInfo{}, false},
		{"resource", AdminUserInfo{TwoFactorEnabled: true, Kind: "resource"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := tt.user
			u.UserID, u.Email, u.PrimaryEmail = "1", "a@x.test", "a@x.test"
			html := renderAdmin(t, AdminUserForm(&u, nil, false, "admin@x.test", ""))
			if got := strings.Contains(html, action); got != tt.want {
				t.Errorf("reset action shown = %v, want %v", got, tt.want)
			}
		})
	}
}
