package smtp

import (
	"strconv"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
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

// SMTP AUTH honours the per-account switch.
func TestAuthHonoursRequireAppPasswords(t *testing.T) {
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
			b := throttleTestBackend(nil)
			b.auth = la
			err := newSessionForIP(b, "203.0.113."+strconv.Itoa(70+i)).AuthPlain("a@example.com", tt.password)
			if tt.wantOK && err != nil {
				t.Errorf("AuthPlain = %v, want success", err)
			}
			if !tt.wantOK && smtpCode(err) != 535 {
				t.Errorf("AuthPlain = %v, want 535", err)
			}
		})
	}
	apps.Wait()
}

// Turning the switch on ends a submission session opened with the account
// password: the next MAIL is answered 535. The app password then authenticates
// a new session. Decision 17 names IMAP and POP3; the code covers SMTP too.
func TestSwitchOnEndsAccountPasswordSubmission(t *testing.T) {
	la, apps, setFlags := flaggedAppAuth(t)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	setFlags(true, false)
	b := throttleTestBackend(nil)
	b.auth = la

	s := newSessionForIP(b, "203.0.113.80")
	if err := s.AuthPlain("a@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	// Control: the live check passes before the switch moves, so the 535
	// below is the switch's doing.
	if err := s.checkAccountLive("MAIL"); err != nil {
		t.Fatalf("session not live before the switch: %v", err)
	}

	setFlags(true, true)
	if code := smtpCode(s.Mail("a@example.com", nil)); code != 535 {
		t.Fatalf("MAIL after the switch = %d, want 535", code)
	}

	again := newSessionForIP(b, "203.0.113.81")
	if err := again.AuthPlain("a@example.com", pw); err != nil {
		t.Fatalf("app password after the switch: %v", err)
	}
	if err := again.checkAccountLive("MAIL"); err != nil {
		t.Errorf("reconnected session not live: %v", err)
	}
	apps.Wait()
}
