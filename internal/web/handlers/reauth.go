package handlers

import (
	"encoding/base32"
	"sync"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ReauthLimiter is the slice of the shared login throttle that re-authentication
// uses. It shares the per-IP budget with /login, so repeated mistyped
// passwords on the settings page count against the same window.
type ReauthLimiter interface {
	RecordFailure(ip, account string)
	Succeed(ip, account string)
	IsLimited(ip string) bool
	Window() time.Duration
}

// userLocks serialises read-modify-write of a user's profile across the login
// and re-authentication handlers on this node, so two concurrent submissions
// of one TOTP code cannot both pass before either records it. It does not
// cover a second node or a follower that has not yet applied the previous
// write; a Raft compare-and-set would be needed for that.
var userLocks sync.Map // userID -> *sync.Mutex

// lockUser takes the per-user lock and returns its unlock function.
func lockUser(userID string) func() {
	m, _ := userLocks.LoadOrStore(userID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// consumeSecondFactor checks code as a TOTP code newer than the last one used,
// or else as a recovery code, and applies the consumption to profile: the TOTP
// step is advanced, or the recovery code removed. The caller must persist the
// profile and must refuse the request if that write fails.
func consumeSecondFactor(profile *bongov1.UserProfile, code string, now time.Time) (ok, usedRecovery bool) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(profile.TotpSecret)
	if step, valid := auth.ValidateTOTPStep(secret, code, now, profile.TotpLastStep); valid {
		profile.TotpLastStep = step
		return true, false
	}

	matched, idx := auth.ValidateRecoveryCode(profile.RecoveryCodes, code)
	if !matched {
		return false, false
	}
	remaining := make([]string, 0, len(profile.RecoveryCodes)-1)
	for i, c := range profile.RecoveryCodes {
		if i != idx {
			remaining = append(remaining, c)
		}
	}
	profile.RecoveryCodes = remaining
	return true, true
}
