package imap

import (
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
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

// IMAP LOGIN accepts an app password and resolves it to the owning user, then
// refuses it once revoked and once the source address is locked out.
func TestLoginAcceptsAppPassword(t *testing.T) {
	la, apps := realAppAuth(t)
	pw, info, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	lim := authlimit.New(imapMaxAuthFailures, time.Minute)
	const ip = "203.0.113.50"

	s := newThrottleSession(la, lim, ip)
	if err := s.Login("a@example.com", pw); err != nil {
		t.Fatalf("app password login: %v", err)
	}
	if s.userID != "uid1" {
		t.Errorf("userID = %q, want uid1", s.userID)
	}
	apps.Wait()
	if err := newThrottleSession(la, lim, ip).Login("a@example.com", "pw"); err != nil {
		t.Errorf("account password: %v", err)
	}

	if err := apps.Revoke("uid1", info.ID); err != nil {
		t.Fatal(err)
	}
	if err := newThrottleSession(la, lim, ip).Login("a@example.com", pw); err == nil {
		t.Fatal("revoked app password accepted")
	}
}

// Wrong app passwords count against the lockout, after which even a valid
// one is refused.
func TestWrongAppPasswordsLockOut(t *testing.T) {
	la, apps := realAppAuth(t)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	wrong := pw[:6] + "-aaaaaa-aaaaaa-aaaaaa"
	lim := authlimit.New(imapMaxAuthFailures, time.Minute)
	const ip = "203.0.113.51"
	for range imapMaxAuthFailures {
		if err := newThrottleSession(la, lim, ip).Login("a@example.com", wrong); err == nil {
			t.Fatal("wrong app password accepted")
		}
	}
	if err := newThrottleSession(la, lim, ip).Login("a@example.com", pw); err == nil {
		t.Fatal("valid app password accepted while locked out")
	}
}
