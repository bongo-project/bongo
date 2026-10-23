package pop3

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
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

// The epoch is the one of the profile the password was checked against, and
// PASS reads no account state of its own, so a reset in between ends the
// session at its next command.
func TestPassTakesEpochFromVerifiedProfile(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })

	a := &epochRaceAuth{
		p1: &bongov1.UserProfile{PasswordHash: []byte("hash-old")},
		p2: &bongov1.UserProfile{PasswordHash: []byte("hash-new")},
	}
	serverConn, clientConn := net.Pipe()
	s := newSession(serverConn, a, store.NewMailboxStore(ps, nil), ps, nil, nil, false, true, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { s.handle(); close(done) }()

	tc := &testPOP3{conn: clientConn, reader: bufio.NewReader(clientConn), t: t}
	tc.expectOK() // greeting
	tc.send("USER u@example.com")
	tc.expectOK()
	tc.send("PASS old-pass")
	tc.expectOK()

	tc.send("STAT")
	if line := tc.expectErr(); line != "-ERR account no longer available" {
		t.Fatalf("STAT after reset = %q", line)
	}
	<-done
	_, _ = io.ReadAll(clientConn)

	if want := auth.ProtocolEpoch(a.p1); s.epoch != want {
		t.Fatalf("epoch = %q, want the verified profile's %q", s.epoch, want)
	}
	// Only the per-command check may read account state, never PASS.
	if a.stateCalls != 1 {
		t.Fatalf("AccountState calls = %d, want exactly the one from STAT", a.stateCalls)
	}
}

func TestPassRefusedWithoutUsableProfile(t *testing.T) {
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
			got := passAttempt(t, tt.a, nil, nil, "u@example.com", "pw")
			if got != "-ERR authentication failed\r\n" {
				t.Fatalf("PASS = %q, want refusal", got)
			}
		})
	}
}

// A 2FA account with a bumped web session generation must stay logged in: the
// login epoch uses the protocol formula, which ignores both.
func TestPassKeepsSessionForTOTPAccount(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })

	p := &bongov1.UserProfile{
		PasswordHash:      []byte("hash"),
		TotpSecret:        []byte("totp-secret"),
		TotpEnabled:       true,
		SessionGeneration: 7,
	}
	a := &epochRaceAuth{p1: p, p2: p}
	serverConn, clientConn := net.Pipe()
	s := newSession(serverConn, a, store.NewMailboxStore(ps, nil), ps, nil, nil, false, true, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { s.handle(); close(done) }()

	tc := &testPOP3{conn: clientConn, reader: bufio.NewReader(clientConn), t: t}
	tc.expectOK() // greeting
	tc.send("USER u@example.com")
	tc.expectOK()
	tc.send("PASS pw")
	tc.expectOK()
	tc.send("STAT")
	tc.expectOK()
	tc.send("QUIT")
	tc.expectOK()
	<-done
	_, _ = io.ReadAll(clientConn)

	if want := auth.ProtocolEpoch(p); s.epoch != want {
		t.Fatalf("epoch = %q, want %q", s.epoch, want)
	}
}
