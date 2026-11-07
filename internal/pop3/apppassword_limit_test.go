package pop3

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

func limitAppAuth(t *testing.T) (*auth.LocalAuth, *store.MailboxStore, *auth.AppPasswords, string) {
	t.Helper()
	la, mb := newThrottleAuth(t, map[string]string{"a@example.com": "pw"})
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	apps := auth.NewAppPasswords(store.NewAppPasswordStore(ps, pebbleApplier{ps}), nil)
	la.SetAppPasswords(apps)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	return la, mb, apps, pw
}

// See the IMAP test of the same name: a success proves only the secret used.
func TestAppPasswordLoginForgivesNoGuesses(t *testing.T) {
	la, mb, apps, pw := limitAppAuth(t)
	lim := authlimit.New(pop3MaxAuthFailures, time.Minute)
	for i := range pop3MaxAuthFailures - 1 {
		if got := passAttempt(t, la, mb, lim, "a@example.com", fmt.Sprintf("guess-%d", i)); !strings.Contains(got, "authentication failed") {
			t.Fatalf("guess %d: %q", i, got)
		}
	}
	if got := passAttempt(t, la, mb, lim, "a@example.com", pw); !strings.HasPrefix(got, "+OK") {
		t.Fatalf("app password below the limit: %q", got)
	}
	apps.Wait()
	_ = passAttempt(t, la, mb, lim, "a@example.com", "one-more")
	if got := passAttempt(t, la, mb, lim, "a@example.com", "pw"); !strings.Contains(got, "too many") {
		t.Fatalf("right account password once locked = %q, want too many", got)
	}
}

func TestSwappedFieldsDoNotLogTheAppPassword(t *testing.T) {
	la, mb, _, pw := limitAppAuth(t)
	var buf bytes.Buffer
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	go func() { _, _ = io.Copy(io.Discard, clientConn) }()
	s := newSession(serverConn, la, mb, nil, nil, nil, false, true,
		authlimit.New(pop3MaxAuthFailures, time.Minute), slog.New(slog.NewTextHandler(&buf, nil)))
	// One past the limit, so the throttled line is logged as well.
	for range pop3MaxAuthFailures + 1 {
		s.username = pw
		s.handlePass("a@example.com")
	}
	if strings.Contains(buf.String(), pw) || strings.Contains(buf.String(), pw[7:13]) {
		t.Fatalf("log holds the app password: %s", buf.String())
	}
	for _, want := range []string{"auth failed", "auth throttled"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("%q was not logged: %s", want, buf.String())
		}
	}
}
