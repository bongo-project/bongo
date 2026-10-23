package imap

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapserver"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// epochRaceAuth models a password reset landing between the credential check
// and any later read: AuthenticateProfile verifies against p1 while
// AccountState already reports p2.
type epochRaceAuth struct {
	p1, p2      *bongov1.UserProfile
	err         error
	stateCalls  int
	authProfile bool
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

func newEpochSession(a profileAuthenticator) *session {
	return &session{
		auth:        a,
		authLimiter: authlimit.New(imapMaxAuthFailures, time.Minute),
		remoteIP:    net.ParseIP("203.0.113.30"),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// The epoch is the one of the profile the password was checked against, and
// LOGIN reads no account state of its own, so a reset in between ends the
// session at its next command.
func TestLoginTakesEpochFromVerifiedProfile(t *testing.T) {
	a := &epochRaceAuth{
		p1: &bongov1.UserProfile{PasswordHash: []byte("hash-old")},
		p2: &bongov1.UserProfile{PasswordHash: []byte("hash-new")},
	}
	s := newEpochSession(a)
	if err := s.Login("u@example.com", "old-pass"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if want := auth.ProtocolEpoch(a.p1); s.epoch != want {
		t.Fatalf("epoch = %q, want the verified profile's %q", s.epoch, want)
	}
	if a.stateCalls != 0 {
		t.Fatalf("Login made %d AccountState calls, want 0", a.stateCalls)
	}
	if _, err := s.Select("INBOX", nil); !errors.Is(err, errAccountRevoked) {
		t.Fatalf("Select after reset = %v, want errAccountRevoked", err)
	}
}

func TestLoginRefusedWithoutUsableProfile(t *testing.T) {
	tests := []struct {
		name string
		a    *epochRaceAuth
	}{
		{"bad credentials", &epochRaceAuth{err: auth.ErrBadCredentials}},
		{"nil profile", &epochRaceAuth{}},
		{"disabled profile", &epochRaceAuth{p1: &bongov1.UserProfile{PasswordHash: []byte("h"), Disabled: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.a.p2 = &bongov1.UserProfile{PasswordHash: []byte("h")}
			s := newEpochSession(tt.a)
			if err := s.Login("u@example.com", "pw"); !errors.Is(err, imapserver.ErrAuthFailed) {
				t.Fatalf("Login = %v, want ErrAuthFailed", err)
			}
			if s.userID != "" || s.epoch != "" {
				t.Fatalf("refused login left state userID=%q epoch=%q", s.userID, s.epoch)
			}
		})
	}
}

// A 2FA account with a bumped web session generation must stay logged in: the
// login epoch uses the protocol formula, which ignores both.
func TestLoginKeepsSessionForTOTPAccount(t *testing.T) {
	p := &bongov1.UserProfile{
		PasswordHash:      []byte("hash"),
		TotpSecret:        []byte("totp-secret"),
		TotpEnabled:       true,
		SessionGeneration: 7,
	}
	a := &epochRaceAuth{p1: p, p2: p}
	s := newEpochSession(a)
	if err := s.Login("u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if want := auth.ProtocolEpoch(p); s.epoch != want {
		t.Fatalf("epoch = %q, want %q", s.epoch, want)
	}
	if err := s.checkAccount(); err != nil {
		t.Fatalf("per-command check refused a live 2FA session: %v", err)
	}
}
