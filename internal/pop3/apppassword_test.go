package pop3

import (
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

type pebbleApplier struct{ ps *store.PebbleStore }

func (a pebbleApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	// Dispatch the touch as the FSM does; stored literally it would leave a
	// stray key and hide any negative test about it.
	if cmd.Op == bongov1.Op_OP_PUT && store.IsAppPasswordSyntheticCommand(cmd.Key) {
		return store.ApplyAppPasswordTouch(a.ps, cmd)
	}
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.ps.Delete(cmd.Key)
	}
	return a.ps.Set(cmd.Key, cmd.Value)
}

// POP3 USER/PASS accepts a valid app password and refuses a revoked one.
func TestPassAcceptsAppPassword(t *testing.T) {
	la, mb := newThrottleAuth(t, map[string]string{"a@example.com": "pw"})
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	apps := auth.NewAppPasswords(store.NewAppPasswordStore(ps, pebbleApplier{ps}), nil)
	la.SetAppPasswords(apps)

	appPW, info, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	if got := passAttempt(t, la, mb, nil, "a@example.com", appPW); !strings.HasPrefix(got, "+OK") {
		t.Errorf("app password: %q", got)
	}
	apps.Wait()
	if got := passAttempt(t, la, mb, nil, "a@example.com", "pw"); !strings.HasPrefix(got, "+OK") {
		t.Errorf("account password: %q", got)
	}
	if err := apps.Revoke("uid1", info.ID); err != nil {
		t.Fatal(err)
	}
	if got := passAttempt(t, la, mb, nil, "a@example.com", appPW); !strings.HasPrefix(got, "-ERR") {
		t.Errorf("revoked app password: %q", got)
	}
}
