package imap

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

// flaggedAppAuth is realAppAuth with the profile's two-factor flag and its
// app-password-only switch under the test's control. realAppAuth hides its
// store, so the flags cannot be flipped through it.
func flaggedAppAuth(t *testing.T) (la *auth.LocalAuth, apps *auth.AppPasswords, setFlags func(totp, require bool)) {
	t.Helper()
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	setFlags = func(totp, require bool) {
		t.Helper()
		raw, err := proto.Marshal(&bongov1.UserProfile{Email: "a@example.com", PasswordHash: hash, TotpEnabled: totp, RequireAppPasswords: require})
		if err != nil {
			t.Fatal(err)
		}
		if err := ps.Set([]byte("/user/uid1/profile"), raw); err != nil {
			t.Fatal(err)
		}
	}
	setFlags(false, false)
	if _, err := mm.System.DB().Exec("INSERT INTO users (id, username, display_name, enabled, created_at) VALUES ('uid1','a@example.com','a',1,0)"); err != nil {
		t.Fatal(err)
	}
	la = auth.NewLocalAuth(ps, mm)
	apps = auth.NewAppPasswords(store.NewAppPasswordStore(ps, touchApplier{ps}), nil)
	la.SetAppPasswords(apps)
	return la, apps, setFlags
}

// IMAP LOGIN honours the per-account switch: with two-factor and the switch on
// the account password is refused and an app password accepted, and a switch
// left on without two-factor is not enforced.
func TestLoginHonoursRequireAppPasswords(t *testing.T) {
	la, apps, setFlags := flaggedAppAuth(t)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		totp, require bool
		password      string
		wantOK        bool
	}{
		{"switch off, account password", true, false, "pw", true},
		{"switch on with 2FA, account password", true, true, "pw", false},
		{"switch on with 2FA, app password", true, true, pw, true},
		{"switch on without 2FA, account password", false, true, "pw", true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setFlags(tt.totp, tt.require)
			lim := authlimit.New(imapMaxAuthFailures, time.Minute)
			err := newThrottleSession(la, lim, "203.0.113."+strconv.Itoa(10+i)).Login("a@example.com", tt.password)
			if tt.wantOK && err != nil {
				t.Errorf("Login = %v, want success", err)
			}
			if !tt.wantOK && err == nil {
				t.Error("Login succeeded, want a refusal")
			}
		})
	}
	apps.Wait()
}

// Turning the switch on ends a session that was opened with the account
// password, on the next command; the app password then connects again.
func TestSwitchOnEndsAccountPasswordSession(t *testing.T) {
	la, apps, setFlags := flaggedAppAuth(t)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	setFlags(true, false)
	lim := authlimit.New(imapMaxAuthFailures, time.Minute)

	s := newThrottleSession(la, lim, "203.0.113.61")
	if err := s.Login("a@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	// Control: the session is live before the switch moves.
	if err := s.checkAccount(); err != nil {
		t.Fatalf("session not live before the switch: %v", err)
	}

	setFlags(true, true)
	if err := s.checkAccount(); !errors.Is(err, errAccountRevoked) {
		t.Fatalf("checkAccount after the switch = %v, want errAccountRevoked", err)
	}

	again := newThrottleSession(la, lim, "203.0.113.62")
	if err := again.Login("a@example.com", pw); err != nil {
		t.Fatalf("app password after the switch: %v", err)
	}
	if err := again.checkAccount(); err != nil {
		t.Errorf("reconnected session not live: %v", err)
	}
	apps.Wait()
}
