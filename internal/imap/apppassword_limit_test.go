package imap

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/authlimit"
)

// A success proves only the secret that was used. An app password login must
// not forgive failed guesses, or a holder of one could guess the account
// password without limit.
func TestAppPasswordLoginForgivesNoGuesses(t *testing.T) {
	la, apps := realAppAuth(t)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	lim := authlimit.New(imapMaxAuthFailures, time.Minute)
	const ip = "203.0.113.99"
	for i := range imapMaxAuthFailures - 1 {
		err := newThrottleSession(la, lim, ip).Login("a@example.com", fmt.Sprintf("guess-%d", i))
		if err == nil || errors.Is(err, errAuthRateLimited) {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	if err := newThrottleSession(la, lim, ip).Login("a@example.com", pw); err != nil {
		t.Fatalf("app password below the limit: %v", err)
	}
	apps.Wait()
	if err := newThrottleSession(la, lim, ip).Login("a@example.com", "one-more"); !errors.Is(err, errAuthRateLimited) {
		t.Fatalf("the guess that reaches the limit = %v, want rate limited", err)
	}
	if err := newThrottleSession(la, lim, ip).Login("a@example.com", "pw"); !errors.Is(err, errAuthRateLimited) {
		t.Fatalf("right account password once locked = %v, want rate limited", err)
	}
}

// The account password still forgives its own failures, so a user who mistypes
// and then gets it right is not left closer to a lockout.
func TestAccountPasswordLoginStillForgivesGuesses(t *testing.T) {
	la, _ := realAppAuth(t)
	lim := authlimit.New(imapMaxAuthFailures, time.Minute)
	const ip = "203.0.113.98"
	for range 3 {
		for i := range imapMaxAuthFailures - 1 {
			_ = newThrottleSession(la, lim, ip).Login("a@example.com", fmt.Sprintf("typo-%d", i))
		}
		if err := newThrottleSession(la, lim, ip).Login("a@example.com", "pw"); err != nil {
			t.Fatalf("account password: %v", err)
		}
	}
}

// A client with its fields swapped sends the app password as the user name.
func TestSwappedFieldsDoNotLogTheAppPassword(t *testing.T) {
	la, apps := realAppAuth(t)
	pw, _, err := apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	lim := authlimit.New(imapMaxAuthFailures, time.Minute)
	// One past the limit, so the throttled line is logged as well.
	for range imapMaxAuthFailures + 1 {
		s := newThrottleSession(la, lim, "203.0.113.70")
		s.logger = slog.New(slog.NewTextHandler(&buf, nil))
		_ = s.Login(pw, "a@example.com")
	}
	if strings.Contains(buf.String(), pw) || strings.Contains(buf.String(), pw[7:13]) {
		t.Fatalf("log holds the app password: %s", buf.String())
	}
	for _, want := range []string{"login failed", "login throttled"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("%q was not logged: %s", want, buf.String())
		}
	}

	buf.Reset()
	s := newThrottleSession(la, lim, "203.0.113.71")
	s.logger = slog.New(slog.NewTextHandler(&buf, nil))
	_ = s.Login("a@example.com", "wrong")
	if !strings.Contains(buf.String(), "a@example.com") {
		t.Fatalf("an ordinary user name is no longer logged: %s", buf.String())
	}
}
