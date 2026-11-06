package auth

import (
	"errors"
	"sync/atomic"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// countingRecords counts Get calls and can fail them. It is the in-package twin
// of authtest.Records, which this package cannot import without a cycle.
type countingRecords struct {
	AppPasswordRecords
	fail atomic.Bool
	gets atomic.Int32
}

func (c *countingRecords) Get(userID, id string) (*bongov1.AppPassword, bool, error) {
	c.gets.Add(1)
	if c.fail.Load() {
		return nil, false, errors.New("record unreadable")
	}
	return c.AppPasswordRecords.Get(userID, id)
}

// AuthenticateProtocol reports which app password opened the session, and ""
// for the account password, including an account password that merely has the
// app password shape (the parsed id there names no record, and returning it
// would end the session on its next command).
func TestAuthenticateProtocolReturnsCredentialID(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	pw, info, err := e.apps.Create("u1", "phone")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		password string
		wantID   string
		wantErr  bool
	}{
		{"app password", pw, info.ID, false},
		{"account password", "pw", "", false},
		{"wrong password", "nope", "", true},
		{"wrong app password", pw[:6] + "-aaaaaa-aaaaaa-aaaaaa", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, got, err := e.la.AuthenticateProtocol("a@example.com", tt.password)
			if (err != nil) != tt.wantErr || got != tt.wantID {
				t.Fatalf("got id %q err %v, want id %q err %v", got, err, tt.wantID, tt.wantErr)
			}
		})
	}

	t.Run("shaped account password", func(t *testing.T) {
		const shaped = "abcdef-ghjkmn-pqrstu-vwxyza"
		e2 := newAppEnv(t)
		e2.user(t, "u2", "b@example.com", shaped)
		_, profile, got, err := e2.la.AuthenticateProtocol("b@example.com", shaped)
		if err != nil || got != "" {
			t.Fatalf("got id %q err %v, want empty and success", got, err)
		}
		st, err := e2.la.AccountState("u2", got)
		if err != nil || !st.Live(ProtocolEpoch(profile)) {
			t.Fatalf("session bound to a nonexistent record: %+v %v", st, err)
		}
	})
}

func TestAccountStateAppPassword(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	_, info, err := e.apps.Create("u1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	rec := &countingRecords{AppPasswordRecords: e.recs}
	e.la.SetAppPasswords(NewAppPasswords(rec, nil))

	tests := []struct {
		name        string
		userID      string
		id          string
		fail        bool
		wantErr     bool
		wantRevoked bool
		wantMissing bool
		wantGets    int32
	}{
		{name: "account password does no read", userID: "u1", id: ""},
		{name: "record found", userID: "u1", id: info.ID, wantGets: 1},
		{name: "record absent", userID: "u1", id: "zzzzzz", wantRevoked: true, wantGets: 1},
		{name: "read error", userID: "u1", id: info.ID, fail: true, wantErr: true, wantGets: 1},
		{name: "profile missing skips the record read", userID: "gone", id: info.ID, wantMissing: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec.gets.Store(0)
			rec.fail.Store(tt.fail)
			st, err := e.la.AccountState(tt.userID, tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if st.AppPasswordRevoked != tt.wantRevoked || st.Missing != tt.wantMissing {
				t.Errorf("state = %+v, want revoked %v missing %v", st, tt.wantRevoked, tt.wantMissing)
			}
			if got := rec.gets.Load(); got != tt.wantGets {
				t.Errorf("record Gets = %d, want %d", got, tt.wantGets)
			}
		})
	}

	t.Run("no app password service fails closed", func(t *testing.T) {
		bare := NewLocalAuth(e.kv, e.mm)
		st, err := bare.AccountState("u1", info.ID)
		if err != nil || !st.AppPasswordRevoked {
			t.Fatalf("state = %+v err %v, want revoked", st, err)
		}
	})
}

// Revoke-all ends every open app password session while the account password
// is unchanged, and leaves an account password session alone. This stands for
// the admin Revoke all route; the password change box also reaches RevokeAll,
// but a password change alone moves the epoch, so it cannot be isolated.
func TestRevokeAllEndsOpenSessions(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	pw1 := e.create(t, "u1", "phone")
	pw2 := e.create(t, "u1", "laptop")

	type sess struct {
		id    string
		epoch string
	}
	open := func(pw string) sess {
		t.Helper()
		_, p, id, err := e.la.AuthenticateProtocol("a@example.com", pw)
		if err != nil {
			t.Fatal(err)
		}
		return sess{id, ProtocolEpoch(p)}
	}
	s1, s2, acct := open(pw1), open(pw2), open("pw")
	e.apps.Wait()

	live := func(s sess) bool {
		t.Helper()
		st, err := e.la.AccountState("u1", s.id)
		if err != nil {
			t.Fatal(err)
		}
		return st.Live(s.epoch)
	}
	if !live(s1) || !live(s2) || !live(acct) {
		t.Fatal("sessions not live before the revoke")
	}
	if n, err := e.apps.RevokeAll("u1"); err != nil || n != 2 {
		t.Fatalf("RevokeAll = %d, %v", n, err)
	}
	if live(s1) || live(s2) {
		t.Error("app password session survived RevokeAll")
	}
	if !live(acct) {
		t.Error("account password session ended by RevokeAll")
	}
}
