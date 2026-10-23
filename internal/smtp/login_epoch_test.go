package smtp

import (
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// epochRaceAuth models a password reset landing between the credential check
// and any later read: AuthenticateProfile verifies against p1 while
// AccountState already reports p2.
type epochRaceAuth struct {
	p1, p2     *bongov1.UserProfile
	err        error
	stateCalls int
}

func (a *epochRaceAuth) AuthenticateProfile(string, string) (string, *bongov1.UserProfile, error) {
	if a.err != nil {
		return "", nil, a.err
	}
	return "uid1", a.p1, nil
}

func (a *epochRaceAuth) AccountState(string) (auth.AccountState, error) {
	a.stateCalls++
	return auth.AccountState{Epoch: auth.ProtocolEpoch(a.p2)}, nil
}

func epochBackend(a *epochRaceAuth) *Backend {
	b := throttleTestBackend(nil)
	b.auth = a
	b.authFail = authlimit.New(maxAuthFailures, authFailWindow)
	return b
}

// The epoch is the one of the profile the password was checked against, and
// AUTH reads no account state of its own, so a reset in between ends the
// session at its next MAIL.
func TestAuthPlainTakesEpochFromVerifiedProfile(t *testing.T) {
	a := &epochRaceAuth{
		p1: &bongov1.UserProfile{PasswordHash: []byte("hash-old")},
		p2: &bongov1.UserProfile{PasswordHash: []byte("hash-new")},
	}
	s := newSessionForIP(epochBackend(a), "203.0.113.31")
	if err := s.AuthPlain("u@example.com", "old-pass"); err != nil {
		t.Fatalf("AuthPlain: %v", err)
	}
	if want := auth.ProtocolEpoch(a.p1); s.epoch != want {
		t.Fatalf("epoch = %q, want the verified profile's %q", s.epoch, want)
	}
	if a.stateCalls != 0 {
		t.Fatalf("AuthPlain made %d AccountState calls, want 0", a.stateCalls)
	}
	if code := smtpCode(s.Mail("u@example.com", nil)); code != 535 {
		t.Fatalf("MAIL after reset = code %d, want 535", code)
	}
}

func TestAuthPlainRefusedWithoutUsableProfile(t *testing.T) {
	tests := []struct {
		name     string
		a        *epochRaceAuth
		wantCode int
	}{
		{"bad credentials", &epochRaceAuth{err: auth.ErrBadCredentials}, 535},
		{"nil profile", &epochRaceAuth{}, 535},
		{"disabled profile", &epochRaceAuth{p1: &bongov1.UserProfile{PasswordHash: []byte("h"), Disabled: true}}, 535},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.a.p2 = &bongov1.UserProfile{PasswordHash: []byte("h")}
			s := newSessionForIP(epochBackend(tt.a), "203.0.113.32")
			if code := smtpCode(s.AuthPlain("u@example.com", "pw")); code != tt.wantCode {
				t.Fatalf("AuthPlain code = %d, want %d", code, tt.wantCode)
			}
			if s.authed || s.userID != "" {
				t.Fatalf("refused AUTH left authed=%v userID=%q", s.authed, s.userID)
			}
		})
	}
}

// A 2FA account with a bumped web session generation must stay logged in: the
// login epoch uses the protocol formula, which ignores both.
func TestAuthPlainKeepsSessionForTOTPAccount(t *testing.T) {
	p := &bongov1.UserProfile{
		PasswordHash:      []byte("hash"),
		TotpSecret:        []byte("totp-secret"),
		TotpEnabled:       true,
		SessionGeneration: 7,
	}
	a := &epochRaceAuth{p1: p, p2: p}
	s := newSessionForIP(epochBackend(a), "203.0.113.33")
	if err := s.AuthPlain("u@example.com", "pw"); err != nil {
		t.Fatalf("AuthPlain: %v", err)
	}
	if want := auth.ProtocolEpoch(p); s.epoch != want {
		t.Fatalf("epoch = %q, want %q", s.epoch, want)
	}
	if err := s.checkAccountLive("MAIL"); err != nil {
		t.Fatalf("per-command check refused a live 2FA session: %v", err)
	}
}
