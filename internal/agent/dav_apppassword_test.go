package agent

import (
	"net/http"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

type davPebbleApplier struct{ ps *store.PebbleStore }

func (a davPebbleApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
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

// CalDAV and CardDAV Basic accept a valid app password and refuse a revoked
// one.
func TestDAVBasicAuthAcceptsAppPassword(t *testing.T) {
	a := newDAVTestAuth(t, map[string]string{"alice@example.com": "pw"})
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	apps := auth.NewAppPasswords(store.NewAppPasswordStore(ps, davPebbleApplier{ps}), nil)
	a.SetAppPasswords(apps)
	lim := authlimit.New(davMaxAuthFailures, time.Minute)

	appPW, info, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	if code := davAttempt(a, lim, "alice@example.com", appPW); code != http.StatusOK {
		t.Errorf("app password: %d", code)
	}
	apps.Wait()
	if code := davAttempt(a, lim, "alice@example.com", "pw"); code != http.StatusOK {
		t.Errorf("account password: %d", code)
	}
	if err := apps.Revoke("uid1", info.ID); err != nil {
		t.Fatal(err)
	}
	if code := davAttempt(a, lim, "alice@example.com", appPW); code != http.StatusUnauthorized {
		t.Errorf("revoked app password: %d", code)
	}
}
