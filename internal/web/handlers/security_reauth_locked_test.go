package handlers

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// countingAuth accepts or rejects every password and counts how often it is
// asked. The re-auth path must not consult it at all.
type countingAuth struct {
	accept bool
	calls  atomic.Int32
}

func (a *countingAuth) Authenticate(string, string) (string, error) {
	a.calls.Add(1)
	if a.accept {
		return reauthUserID, nil
	}
	return "", auth.ErrBadCredentials
}

// The current password is compared with the profile loaded under the user
// lock, not with whatever a separate Authenticate read saw. A fake that accepts
// every password stands in for the stale read: if it were consulted, the old
// password would pass against a profile that now holds a different hash.
func TestReauthComparesAgainstLockedProfile(t *testing.T) {
	for _, ep := range reauthEndpoints {
		for _, twoFA := range []bool{false, true} {
			name := ep.name
			if twoFA {
				name += "/2fa"
			}
			t.Run(name, func(t *testing.T) {
				env := newReauthEnv(t, twoFA, 1)
				stale := &countingAuth{accept: true}
				env.security.authenticator = stale

				// The profile as an admin reset left it: the hash is for new-pass.
				p := env.store.profile(t)
				hash, err := bcrypt.GenerateFromPassword([]byte("new-pass"), bcrypt.MinCost)
				if err != nil {
					t.Fatal(err)
				}
				p.PasswordHash = hash
				env.store.put(t, p)
				before, err := env.store.GetKey([]byte("/user/" + reauthUserID + "/profile"))
				if err != nil {
					t.Fatal(err)
				}

				rr := env.call(ep, "old-pass", totpNowIf(t, twoFA))
				if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/settings/security?flash=Invalid+password" {
					t.Fatalf("got %d to %q, want 303 to the invalid-password flash", rr.Code, rr.Header().Get("Location"))
				}
				if env.store.writes != 0 {
					t.Errorf("%d writes after a wrong current password", env.store.writes)
				}
				after, _ := env.store.GetKey([]byte("/user/" + reauthUserID + "/profile"))
				if !proto.Equal(mustProfile(t, before), mustProfile(t, after)) {
					t.Error("stored profile changed")
				}
				if n := stale.calls.Load(); n != 0 {
					t.Errorf("Authenticate called %d times, want 0", n)
				}
				// max=1: exactly the one recorded failure is enough to limit.
				if !env.limiter.IsLimited("203.0.113.50") {
					t.Error("no limiter failure recorded")
				}
			})
		}
	}
}

// Exactly one failure per bad attempt: with a budget of two, one bad attempt
// leaves the client unthrottled.
func TestReauthWrongPasswordRecordsOneFailure(t *testing.T) {
	for _, ep := range reauthEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			env := newReauthEnv(t, false, 2)
			env.call(ep, "wrong", "")
			if env.limiter.IsLimited("203.0.113.50") {
				t.Error("one bad attempt consumed more than one failure")
			}
		})
	}
}

// Positive control: the correct password succeeds even when the authenticator
// rejects everything, so the authenticator is not what decides.
func TestReauthSucceedsWithoutAuthenticator(t *testing.T) {
	for _, ep := range reauthEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			env := newReauthEnv(t, false, 5)
			rejecting := &countingAuth{}
			env.security.authenticator = rejecting

			before := env.store.profile(t)
			rr := env.call(ep, "right", "")
			if rr.Code != http.StatusSeeOther {
				t.Fatalf("got %d, want 303", rr.Code)
			}
			if loc := rr.Header().Get("Location"); strings.Contains(loc, "Invalid") {
				t.Fatalf("correct password refused: %q", loc)
			}
			if ep.name != "disable-2fa" && !ep.changed(before, env.store.profile(t)) {
				t.Error("profile not altered by a correct password")
			}
			if n := rejecting.calls.Load(); n != 0 {
				t.Errorf("Authenticate called %d times, want 0", n)
			}
		})
	}
}

// Positive control with 2FA on: a correct password and code really disable it.
func TestReauthDisableTOTPSucceedsWithoutAuthenticator(t *testing.T) {
	env := newReauthEnv(t, true, 5)
	env.security.authenticator = &countingAuth{}
	ep := reauthEndpoints[1]
	before := env.store.profile(t)
	rr := env.call(ep, "right", totpNow(t))
	if rr.Code != http.StatusSeeOther || strings.Contains(rr.Header().Get("Location"), "Invalid") {
		t.Fatalf("got %d to %q", rr.Code, rr.Header().Get("Location"))
	}
	if !ep.changed(before, env.store.profile(t)) {
		t.Error("2FA not disabled")
	}
}

// A disabled account with the correct password is refused like a wrong one.
func TestReauthRefusesDisabledProfile(t *testing.T) {
	for _, ep := range reauthEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			env := newReauthEnv(t, false, 1)
			p := env.store.profile(t)
			p.Disabled = true
			env.store.put(t, p)
			env.store.writes = 0

			rr := env.call(ep, "right", "")
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/settings/security?flash=Invalid+password" {
				t.Fatalf("got %d to %q, want 303 to the invalid-password flash", rr.Code, rr.Header().Get("Location"))
			}
			if env.store.writes != 0 {
				t.Errorf("%d writes for a disabled account", env.store.writes)
			}
			if !env.limiter.IsLimited("203.0.113.50") {
				t.Error("no limiter failure recorded")
			}
		})
	}
}

func totpNowIf(t *testing.T, twoFA bool) string {
	if !twoFA {
		return ""
	}
	return totpNow(t)
}

func mustProfile(t *testing.T, raw []byte) *bongov1.UserProfile {
	t.Helper()
	var p bongov1.UserProfile
	if err := proto.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}
