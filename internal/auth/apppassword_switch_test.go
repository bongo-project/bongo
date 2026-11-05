package auth

import (
	"errors"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestCreateRefusesTheEleventhAppPassword(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "alice@example.com", "pw")
	e.user(t, "u2", "bob@example.com", "pw")
	for i := range MaxAppPasswordsPerUser {
		if _, _, err := e.apps.Create("u1", "device "+string(rune('a'+i))); err != nil {
			t.Fatalf("create %d: %v", i+1, err)
		}
	}

	before, _ := e.apps.List("u1")
	pw, _, err := e.apps.Create("u1", "eleventh")
	if !errors.Is(err, ErrAppPasswordLimit) || pw != "" {
		t.Fatalf("11th create = %q, %v; want ErrAppPasswordLimit", pw, err)
	}
	after, _ := e.apps.List("u1")
	if len(after) != len(before) || len(after) != MaxAppPasswordsPerUser {
		t.Errorf("credentials %d -> %d", len(before), len(after))
	}

	// The cap is per user, and revoking one frees a slot.
	if _, _, err := e.apps.Create("u2", "bob"); err != nil {
		t.Errorf("other user refused: %v", err)
	}
	if err := e.apps.Revoke("u1", after[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.apps.Create("u1", "replacement"); err != nil {
		t.Errorf("create after revoke: %v", err)
	}
}

func TestCreateLabelErrorBeatsLimit(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "alice@example.com", "pw")
	for i := range MaxAppPasswordsPerUser {
		e.create(t, "u1", "d"+string(rune('a'+i)))
	}
	if _, _, err := e.apps.Create("u1", ""); !errors.Is(err, ErrInvalidAppPasswordLabel) {
		t.Errorf("got %v, want the label error first", err)
	}
}

func TestCreateListFailureRefuses(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "alice@example.com", "pw")
	e.apps.records = failingList{e.apps.records}
	if _, _, err := e.apps.Create("u1", "x"); err == nil || errors.Is(err, ErrAppPasswordLimit) {
		t.Errorf("err = %v, want a list failure", err)
	}
}

type failingList struct{ AppPasswordRecords }

func (failingList) List(string) ([]*bongov1.AppPassword, error) { return nil, errors.New("boom") }

// With the switch on and 2FA on, the account password fails on the protocol
// path and an app password works, and every branch still costs exactly one
// bcrypt comparison.
func TestRequireAppPasswordsSwitch(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "alice@example.com", "account-pw")
	e.user(t, "u2", "bob@example.com", "bob-pw")
	good := e.create(t, "u1", "phone")
	noRecord := "aaaaaa-aaaaaa-aaaaaa-aaaaaa"
	if good[:6] == "aaaaaa" {
		noRecord = "bbbbbb-bbbbbb-bbbbbb-bbbbbb"
	}
	e.mutateProfile(t, "u1", func(p *bongov1.UserProfile) { p.TotpEnabled, p.RequireAppPasswords = true, true })

	tests := []struct {
		name, user, password string
		ok                   bool
	}{
		{"account password refused", "alice@example.com", "account-pw", false},
		{"app password accepted", "alice@example.com", good, true},
		{"wrong password", "alice@example.com", "nope", false},
		{"shaped, no record", "alice@example.com", noRecord, false},
		{"unknown user", "nobody@example.com", "account-pw", false},
		{"other account unaffected", "bob@example.com", "bob-pw", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, n, err := e.attempt(tt.user, tt.password)
			if n != 1 {
				t.Errorf("bcrypt comparisons = %d, want exactly 1", n)
			}
			if tt.ok != (err == nil && id != "") {
				t.Errorf("id=%q err=%v, want ok=%v", id, err, tt.ok)
			}
			if !tt.ok && !errors.Is(err, ErrBadCredentials) {
				t.Errorf("err = %v, want ErrBadCredentials", err)
			}
		})
	}

	// The refusal is the same error value a wrong password gets.
	_, _, refused := e.la.AuthenticateProtocol("alice@example.com", "account-pw")
	_, _, wrong := e.la.AuthenticateProtocol("alice@example.com", "nope")
	if refused != wrong {
		t.Errorf("refusal %v differs from wrong password %v", refused, wrong)
	}

	// Web login and re-authentication keep the account password.
	if _, _, err := e.la.AuthenticateProfile("alice@example.com", "account-pw"); err != nil {
		t.Errorf("AuthenticateProfile with the switch on: %v", err)
	}
	if _, err := e.la.Authenticate("alice@example.com", "account-pw"); err != nil {
		t.Errorf("Authenticate with the switch on: %v", err)
	}

	// Switch off: both work.
	e.mutateProfile(t, "u1", func(p *bongov1.UserProfile) { p.RequireAppPasswords = false })
	for _, pw := range []string{"account-pw", good} {
		if id, _, err := e.attempt("alice@example.com", pw); err != nil || id != "u1" {
			t.Errorf("switch off, %q: id=%q err=%v", pw, id, err)
		}
	}

	// A flag left behind without 2FA does not lock anyone out.
	e.mutateProfile(t, "u1", func(p *bongov1.UserProfile) { p.RequireAppPasswords, p.TotpEnabled = true, false })
	if id, _, err := e.attempt("alice@example.com", "account-pw"); err != nil || id != "u1" {
		t.Errorf("flag without 2FA: id=%q err=%v", id, err)
	}
}

func TestProtocolEpochFollowsEnforcement(t *testing.T) {
	off := ProtocolEpoch(&bongov1.UserProfile{PasswordHash: []byte("hash")})
	flagOnly := ProtocolEpoch(&bongov1.UserProfile{PasswordHash: []byte("hash"), RequireAppPasswords: true})
	on := ProtocolEpoch(&bongov1.UserProfile{PasswordHash: []byte("hash"), RequireAppPasswords: true, TotpEnabled: true})
	if off != flagOnly {
		t.Error("a flag that is not enforced changed the epoch")
	}
	if on == off {
		t.Error("enforcing the switch left the epoch unchanged, so open sessions survive")
	}
	if off != ProtocolEpoch(&bongov1.UserProfile{PasswordHash: []byte("hash"), TotpEnabled: true}) {
		t.Error("2FA alone changed the protocol epoch")
	}
}
