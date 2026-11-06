package auth

import (
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// webAttempt runs both restrictive entry points on one credential and returns
// how many bcrypt comparisons each cost. They are separate calls so a second
// comparison in either one is counted on its own.
func (e *appEnv) webAttempt(email, password string) (authN, profileN int, authErr, profileErr error) {
	e.compare.Store(0)
	_, authErr = e.la.Authenticate(email, password)
	authN = int(e.compare.Load())

	e.compare.Store(0)
	_, _, profileErr = e.la.AuthenticateProfile(email, password)
	profileN = int(e.compare.Load())
	return authN, profileN, authErr, profileErr
}

// Web login and re-authentication must cost exactly one comparison on every
// branch, including for a password that looks like an app password, so the
// web path cannot be told apart from the protocol path by timing.
func TestWebPathCostsOneComparison(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "alice@example.com", "account-pw")
	e.user(t, "dis", "dis@example.com", "dis-pw")
	e.mutateProfile(t, "dis", func(p *bongov1.UserProfile) { p.Disabled = true })
	e.user(t, "bare", "bare@example.com", "bare-pw")
	// A user the index knows but whose profile is gone.
	e.user(t, "gone", "gone@example.com", "gone-pw")
	delete(e.kv.data, "/user/gone/profile")

	real := e.create(t, "u1", "phone")
	const shaped = "aaaaaa-aaaaaa-aaaaaa-aaaaaa"

	tests := []struct {
		name     string
		user     string
		password string
		wantOK   bool
	}{
		{"unknown user", "nobody@example.com", "x", false},
		{"unknown user, shaped", "nobody@example.com", shaped, false},
		{"known user, right password", "alice@example.com", "account-pw", true},
		{"known user, wrong password", "alice@example.com", "nope", false},
		{"known user, shaped wrong password", "alice@example.com", shaped, false},
		{"disabled user, right password", "dis@example.com", "dis-pw", false},
		{"disabled user, shaped", "dis@example.com", shaped, false},
		{"user with no records, shaped", "bare@example.com", shaped, false},
		{"profile missing", "gone@example.com", "gone-pw", false},
		{"real app password on the web path", "alice@example.com", real, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			an, pn, aerr, perr := e.webAttempt(tt.user, tt.password)
			if an != 1 {
				t.Errorf("Authenticate comparisons = %d, want exactly 1", an)
			}
			if pn != 1 {
				t.Errorf("AuthenticateProfile comparisons = %d, want exactly 1", pn)
			}
			if tt.wantOK {
				if aerr != nil || perr != nil {
					t.Errorf("errors = %v, %v, want success", aerr, perr)
				}
				return
			}
			if !errors.Is(aerr, ErrBadCredentials) || !errors.Is(perr, ErrBadCredentials) {
				t.Errorf("errors = %v, %v, want ErrBadCredentials", aerr, perr)
			}
		})
	}
	e.apps.Wait()
}

// An unknown user must cost the same as a known one, which needs the dummy
// hash at the same cost as a real account hash. This does not use newAppEnv:
// that lowers PasswordHashCost, and the point is the production value.
func TestDummyHashCostMatchesAccountCost(t *testing.T) {
	cost, err := bcrypt.Cost(dummyBcryptHash)
	if err != nil {
		t.Fatalf("dummy hash is not a bcrypt hash: %v", err)
	}
	if cost != PasswordHashCost {
		t.Errorf("dummy hash cost = %d, PasswordHashCost = %d: unknown users would be distinguishable by timing", cost, PasswordHashCost)
	}
}
