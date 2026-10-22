package agent

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

type davKeyGetter map[string][]byte

func (m davKeyGetter) Get(key []byte) ([]byte, error) {
	v, ok := m[string(key)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return v, nil
}

func newDAVTestAuth(t *testing.T, accounts map[string]string) *auth.LocalAuth {
	t.Helper()
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mm.Close() })

	kv := davKeyGetter{}
	i := 0
	for email, pw := range accounts {
		i++
		id := fmt.Sprintf("uid%d", i)
		hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		data, err := proto.Marshal(&bongov1.UserProfile{Email: email, PasswordHash: hash})
		if err != nil {
			t.Fatal(err)
		}
		kv[fmt.Sprintf("/user/%s/profile", id)] = data
		if _, err := mm.System.DB().Exec(
			"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, ?, 1, 0)",
			id, email, email,
		); err != nil {
			t.Fatal(err)
		}
	}
	return auth.NewLocalAuth(kv, mm)
}

func davAttempt(a *auth.LocalAuth, lim *authlimit.Limiter, user, pass string) int {
	r := httptest.NewRequest(http.MethodGet, "/dav/", nil)
	r.RemoteAddr = "203.0.113.50:4321"
	r.SetBasicAuth(user, pass)
	w := httptest.NewRecorder()
	davBasicAuth(w, r, a, lim, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	return w.Code
}

// A success as another account must not reset the per-IP counter.
func TestDAVBasicAuthOtherAccountSuccessDoesNotClearThrottle(t *testing.T) {
	a := newDAVTestAuth(t, map[string]string{"attacker@example.com": "mine", "victim@example.com": "secret"})
	lim := authlimit.New(davMaxAuthFailures, time.Minute)

	for range davMaxAuthFailures - 1 {
		if code := davAttempt(a, lim, "victim@example.com", "guess"); code != http.StatusUnauthorized {
			t.Fatalf("guess status = %d, want 401", code)
		}
	}
	if code := davAttempt(a, lim, "attacker@example.com", "mine"); code != http.StatusOK {
		t.Fatalf("attacker's own login status = %d, want 200", code)
	}
	if code := davAttempt(a, lim, "victim@example.com", "guess"); code != http.StatusTooManyRequests {
		t.Fatalf("fifth guess status = %d, want 429", code)
	}
	if code := davAttempt(a, lim, "victim@example.com", "secret"); code != http.StatusTooManyRequests {
		t.Fatalf("throttled IP status = %d, want 429", code)
	}
}

// Positive control: a user's own success forgives their own earlier typos.
func TestDAVBasicAuthOwnSuccessForgivesOwnFailures(t *testing.T) {
	a := newDAVTestAuth(t, map[string]string{"alice@example.com": "right"})
	lim := authlimit.New(davMaxAuthFailures, time.Minute)

	for range davMaxAuthFailures - 1 {
		davAttempt(a, lim, "Alice@example.com", "typo")
	}
	if code := davAttempt(a, lim, "alice@example.com", "right"); code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", code)
	}
	for range davMaxAuthFailures - 1 {
		if code := davAttempt(a, lim, "alice@example.com", "typo"); code != http.StatusUnauthorized {
			t.Fatalf("typo status = %d, want 401 (own failures forgiven)", code)
		}
	}
}
