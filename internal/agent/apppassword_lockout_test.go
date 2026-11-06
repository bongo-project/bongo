package agent

import (
	"net/http"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

// Wrong app passwords count towards the DAV lockout, after which even the
// valid credential gets 429. A limiter that skipped failures for
// app-password-shaped input would pass every plain-password lockout test, so
// only this one proves the case.
func TestDAVWrongAppPasswordsLockTheAddressOut(t *testing.T) {
	a := newDAVTestAuth(t, map[string]string{"alice@example.com": "pw"})
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	apps := auth.NewAppPasswords(store.NewAppPasswordStore(ps, davPebbleApplier{ps}), nil)
	a.SetAppPasswords(apps)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}

	wrong := pw[:6] + "-aaaaaa-aaaaaa-aaaaaa"
	lim := authlimit.New(davMaxAuthFailures, time.Minute)
	for i := range davMaxAuthFailures - 1 {
		if c := davAttempt(a, lim, "alice@example.com", wrong); c != http.StatusUnauthorized {
			t.Fatalf("wrong app password %d = %d, want 401", i+1, c)
		}
	}
	if c := davAttempt(a, lim, "alice@example.com", wrong); c != http.StatusTooManyRequests {
		t.Fatalf("the failure that reaches the limit = %d, want 429", c)
	}
	if c := davAttempt(a, lim, "alice@example.com", pw); c != http.StatusTooManyRequests {
		t.Fatalf("valid app password while locked out = %d, want 429", c)
	}
	apps.Wait()
}
