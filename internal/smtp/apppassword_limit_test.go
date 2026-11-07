package smtp

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// See the IMAP test of the same name: a success proves only the secret used.
func TestAppPasswordLoginForgivesNoGuesses(t *testing.T) {
	la, apps := realAppAuth(t)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	b := throttleTestBackend(nil)
	b.auth = la
	const ip = "203.0.113.55"
	for i := range maxAuthFailures - 1 {
		if code := smtpCode(newSessionForIP(b, ip).AuthPlain("a@example.com", fmt.Sprintf("guess-%d", i))); code != 535 {
			t.Fatalf("guess %d: %d", i, code)
		}
	}
	if err := newSessionForIP(b, ip).AuthPlain("a@example.com", pw); err != nil {
		t.Fatalf("app password below the limit: %v", err)
	}
	apps.Wait()
	_ = newSessionForIP(b, ip).AuthPlain("a@example.com", "one-more")
	if code := smtpCode(newSessionForIP(b, ip).AuthPlain("a@example.com", "pw")); code != 454 {
		t.Fatalf("right account password once locked = %d, want 454", code)
	}
}

func TestSwappedFieldsDoNotLogTheAppPassword(t *testing.T) {
	la, apps := realAppAuth(t)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	b := throttleTestBackend(nil)
	b.auth = la
	var buf bytes.Buffer
	b.logger = slog.New(slog.NewTextHandler(&buf, nil))
	// One past the limit, so the throttled line is logged as well.
	for range maxAuthFailures + 1 {
		_ = newSessionForIP(b, "203.0.113.56").AuthPlain(pw, "a@example.com")
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
