package smtp

import (
	"testing"
	"time"

	"github.com/emersion/go-sasl"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

// touchApplier writes to Pebble the way the FSM does, including the
// last-used command; stored literally it would leave a stray key.
type touchApplier struct{ ps *store.PebbleStore }

func (a touchApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && store.IsAppPasswordSyntheticCommand(cmd.Key) {
		return store.ApplyAppPasswordTouch(a.ps, cmd)
	}
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.ps.Delete(cmd.Key)
	}
	return a.ps.Set(cmd.Key, cmd.Value)
}

// realAppAuth builds a real LocalAuth with one account (uid1, password "pw")
// and an app password service over the same Pebble store.
func realAppAuth(t *testing.T) (*auth.LocalAuth, *auth.AppPasswords) {
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
	raw, err := proto.Marshal(&bongov1.UserProfile{Email: "a@example.com", PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/uid1/profile"), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := mm.System.DB().Exec("INSERT INTO users (id, username, display_name, enabled, created_at) VALUES ('uid1','a@example.com','a',1,0)"); err != nil {
		t.Fatal(err)
	}
	la := auth.NewLocalAuth(ps, mm)
	apps := auth.NewAppPasswords(store.NewAppPasswordStore(ps, touchApplier{ps}), nil)
	la.SetAppPasswords(apps)
	return la, apps
}

// SMTP AUTH PLAIN and LOGIN accept an app password and resolve it to the
// owning user, then refuse it once revoked.
func TestAuthAcceptsAppPassword(t *testing.T) {
	la, apps := realAppAuth(t)
	pw, info, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	b := throttleTestBackend(nil)
	b.auth = la
	const ip = "203.0.113.52"

	s := newSessionForIP(b, ip)
	if err := s.AuthPlain("a@example.com", pw); err != nil {
		t.Fatalf("PLAIN: %v", err)
	}
	if s.userID != "uid1" {
		t.Errorf("PLAIN userID = %q, want uid1", s.userID)
	}

	s = newSessionForIP(b, ip)
	srv, err := s.Auth(sasl.Login)
	if err != nil {
		t.Fatal(err)
	}
	var done bool
	for _, resp := range [][]byte{nil, []byte("a@example.com"), []byte(pw)} {
		if _, done, err = srv.Next(resp); err != nil {
			t.Fatalf("LOGIN: %v", err)
		}
	}
	if !done || s.userID != "uid1" {
		t.Errorf("LOGIN done=%v userID=%q, want done and uid1", done, s.userID)
	}
	apps.Wait()

	if err := apps.Revoke("uid1", info.ID); err != nil {
		t.Fatal(err)
	}
	if code := smtpCode(newSessionForIP(b, ip).AuthPlain("a@example.com", pw)); code != 535 {
		t.Errorf("revoked app password: SMTP code %d, want 535", code)
	}
}

// Wrong app passwords count against the lockout, after which even a valid one
// is throttled.
func TestWrongAppPasswordsThrottle(t *testing.T) {
	la, apps := realAppAuth(t)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	b := throttleTestBackend(nil)
	b.auth = la
	s := newSessionForIP(b, "203.0.113.53")
	wrong := pw[:6] + "-aaaaaa-aaaaaa-aaaaaa"
	for range maxAuthFailures {
		if code := smtpCode(s.AuthPlain("a@example.com", wrong)); code != 535 {
			t.Fatalf("wrong app password: code %d, want 535", code)
		}
	}
	if code := smtpCode(s.AuthPlain("a@example.com", pw)); code != 454 {
		t.Errorf("valid app password after lockout: code %d, want 454", code)
	}
}
