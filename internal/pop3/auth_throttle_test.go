package pop3

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

type mapKeyGetter map[string][]byte

func (m mapKeyGetter) Get(key []byte) ([]byte, error) {
	v, ok := m[string(key)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return v, nil
}

// newThrottleAuth builds a real LocalAuth with the given email -> password
// accounts, plus an empty mailbox store so a successful PASS can complete.
func newThrottleAuth(t *testing.T, accounts map[string]string) (*auth.LocalAuth, *store.MailboxStore) {
	t.Helper()
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mm.Close() })

	pebble, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { pebble.Close() })

	kv := mapKeyGetter{}
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
	return auth.NewLocalAuth(kv, mm), store.NewMailboxStore(pebble, nil)
}

// passAttempt runs one USER/PASS exchange on a fresh session from a fixed IP
// and reports the first response line to PASS.
func passAttempt(t *testing.T, a authenticator, mb *store.MailboxStore, lim *authlimit.Limiter, user, pass string) string {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	s := newSession(serverConn, a, mb, nil, nil, nil, false, true, lim, slog.New(slog.NewTextHandler(io.Discard, nil)))
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := clientConn.Read(buf)
		got <- string(buf[:n])
		_, _ = io.Copy(io.Discard, clientConn)
	}()
	s.username = user
	s.handlePass(pass)
	select {
	case line := <-got:
		return line
	case <-time.After(2 * time.Second):
		t.Fatal("no response from handlePass")
		return ""
	}
}

// A success as one account must not reset the per-IP counter for guesses at
// another account.
func TestPassOtherAccountSuccessDoesNotClearThrottle(t *testing.T) {
	a, mb := newThrottleAuth(t, map[string]string{"attacker@example.com": "mine", "victim@example.com": "secret"})
	lim := authlimit.New(pop3MaxAuthFailures, time.Minute)

	for range pop3MaxAuthFailures - 1 {
		passAttempt(t, a, mb, lim, "victim@example.com", "guess")
	}
	if got := passAttempt(t, a, mb, lim, "attacker@example.com", "mine"); got[:3] != "+OK" {
		t.Fatalf("attacker's own login should succeed, got %q", got)
	}
	passAttempt(t, a, mb, lim, "victim@example.com", "guess")
	// Correct password is refused once the IP is throttled.
	if got := passAttempt(t, a, mb, lim, "victim@example.com", "secret"); got[:4] != "-ERR" {
		t.Fatalf("IP should be throttled after attacker success, got %q", got)
	}
}

// Positive control: a user's own success forgives their own earlier typos.
func TestPassOwnSuccessForgivesOwnFailures(t *testing.T) {
	a, mb := newThrottleAuth(t, map[string]string{"alice@example.com": "right"})
	lim := authlimit.New(pop3MaxAuthFailures, time.Minute)

	for range pop3MaxAuthFailures - 1 {
		passAttempt(t, a, mb, lim, "Alice@example.com", "typo")
	}
	if got := passAttempt(t, a, mb, lim, "alice@example.com", "right"); got[:3] != "+OK" {
		t.Fatalf("login should succeed, got %q", got)
	}
	for range pop3MaxAuthFailures - 1 {
		passAttempt(t, a, mb, lim, "alice@example.com", "typo")
	}
	if lim.Blocked("pipe") {
		t.Fatal("own failures should be forgiven; IP must not be blocked")
	}
}
