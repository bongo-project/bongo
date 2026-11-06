package imap

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/auth/authtest"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

// appSessionEnv is the revoke environment with app passwords wired through a
// record store that counts and can fail Get.
type appSessionEnv struct {
	*revokeEnv
	apps *auth.AppPasswords
	recs *authtest.Records
}

func newAppSessionEnv(t *testing.T) *appSessionEnv {
	t.Helper()
	e := newRevokeEnv(t)
	recs := &authtest.Records{AppPasswordRecords: store.NewAppPasswordStore(e.ps, touchApplier{e.ps})}
	apps := auth.NewAppPasswords(recs, nil)
	e.auth.SetAppPasswords(apps)
	return &appSessionEnv{revokeEnv: e, apps: apps, recs: recs}
}

func (e *appSessionEnv) create(t *testing.T, label string) (password, id string) {
	t.Helper()
	pw, info, err := e.apps.Create(pollTestUser, label)
	if err != nil {
		t.Fatal(err)
	}
	return pw, info.ID
}

// login runs the real Login, so the session records the credential the way
// production does (newRevokeSession sets the fields directly and would pass
// with the login assignment removed).
func (e *appSessionEnv) login(t *testing.T, password string) *session {
	t.Helper()
	s := &session{
		auth:        e.auth,
		mailboxes:   e.ms,
		authLimiter: authlimit.New(imapMaxAuthFailures, time.Minute),
		remoteIP:    net.ParseIP("203.0.113.7"),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := s.Login(pollTestUsername, password); err != nil {
		t.Fatalf("Login: %v", err)
	}
	e.apps.Wait() // let the detached last-used write finish before any revoke
	return s
}

func (e *appSessionEnv) dial(t *testing.T, addr, password string) *imapclient.Client {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Login(pollTestUsername, password).Wait(); err != nil {
		t.Fatalf("Login: %v", err)
	}
	e.apps.Wait()
	return c
}

func wantRevoked(t *testing.T, err error) {
	t.Helper()
	var ie *imap.Error
	if !errors.As(err, &ie) || ie.Type != imap.StatusResponseTypeNo || ie.Text != errAccountRevoked.Text {
		t.Fatalf("got %v, want NO account no longer available", err)
	}
}

// Every authenticated method refuses once the app password that opened the
// session is revoked.
func TestAppPasswordRevokeRefusesEveryMethod(t *testing.T) {
	for name, call := range authenticatedSessionMethods() {
		t.Run(name, func(t *testing.T) {
			e := newAppSessionEnv(t)
			pw, id := e.create(t, "phone")
			s := e.login(t, pw)
			if err := s.checkAccount(); err != nil {
				t.Fatalf("before revoke: %v", err)
			}
			if err := e.apps.Revoke(pollTestUser, id); err != nil {
				t.Fatal(err)
			}
			wantRevoked(t, call(s))
		})
	}
}

// Revoking a different credential leaves the session alone; an account
// password session neither reads the record nor notices the revoke.
func TestAppPasswordRevokeControls(t *testing.T) {
	t.Run("different credential revoked", func(t *testing.T) {
		e := newAppSessionEnv(t)
		pw, _ := e.create(t, "phone")
		_, other := e.create(t, "laptop")
		s := e.login(t, pw)
		if err := e.apps.Revoke(pollTestUser, other); err != nil {
			t.Fatal(err)
		}
		if err := s.checkAccount(); err != nil {
			t.Fatalf("session ended by an unrelated revoke: %v", err)
		}
	})
	t.Run("account password session", func(t *testing.T) {
		e := newAppSessionEnv(t)
		_, id := e.create(t, "phone")
		s := e.login(t, pollTestPassword)
		e.recs.Gets.Store(0)
		for range 3 {
			if err := s.checkAccount(); err != nil {
				t.Fatal(err)
			}
		}
		if got := e.recs.Gets.Load(); got != 0 {
			t.Fatalf("record Gets = %d for an account password session, want 0", got)
		}
		if err := e.apps.Revoke(pollTestUser, id); err != nil {
			t.Fatal(err)
		}
		e.recs.Gets.Store(0)
		if err := s.checkAccount(); err != nil {
			t.Fatalf("account password session ended by an app password revoke: %v", err)
		}
		if got := e.recs.Gets.Load(); got != 0 {
			t.Fatalf("record Gets = %d after the revoke, want 0", got)
		}
	})
	t.Run("RevokeAll ends app sessions only", func(t *testing.T) {
		e := newAppSessionEnv(t)
		pw1, _ := e.create(t, "phone")
		pw2, _ := e.create(t, "laptop")
		s1, s2, acct := e.login(t, pw1), e.login(t, pw2), e.login(t, pollTestPassword)
		if _, err := e.apps.RevokeAll(pollTestUser); err != nil {
			t.Fatal(err)
		}
		wantRevoked(t, s1.checkAccount())
		wantRevoked(t, s2.checkAccount())
		if err := acct.checkAccount(); err != nil {
			t.Fatalf("account password session: %v", err)
		}
	})
}

// A record read error is a temporary refusal that keeps the session, never a
// permanent one and never access.
func TestAppPasswordRecordReadErrorFailsClosed(t *testing.T) {
	e := newAppSessionEnv(t)
	pw, _ := e.create(t, "phone")
	s := e.login(t, pw)

	e.recs.Fail.Store(true)
	err := s.checkAccount()
	var ie *imap.Error
	if !errors.As(err, &ie) || ie.Type != imap.StatusResponseTypeNo || ie.Code != imap.ResponseCodeUnavailable {
		t.Fatalf("got %v, want NO [UNAVAILABLE]", err)
	}
	e.recs.Fail.Store(false)
	if err := s.checkAccount(); err != nil {
		t.Fatalf("session should recover once the store does: %v", err)
	}
}

// Through a real client: the next command after the revoke fails and the
// connection is closed.
func TestAppPasswordRevokeEndsRealSession(t *testing.T) {
	e := newAppSessionEnv(t)
	addr := startRevokeServer(t, e.revokeEnv)
	pw, id := e.create(t, "phone")
	c := e.dial(t, addr, pw)
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("NOOP before revoke: %v", err)
	}
	if err := e.apps.Revoke(pollTestUser, id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{UID: true}).Collect(); err == nil {
		t.Fatal("FETCH succeeded after the app password was revoked")
	}
	if err := c.Noop().Wait(); err == nil {
		t.Fatal("connection still usable after revoke")
	}
}

func TestAppPasswordRevokeControlRealSession(t *testing.T) {
	e := newAppSessionEnv(t)
	addr := startRevokeServer(t, e.revokeEnv)
	pw, _ := e.create(t, "phone")
	_, other := e.create(t, "laptop")
	c := e.dial(t, addr, pw)
	if err := e.apps.Revoke(pollTestUser, other); err != nil {
		t.Fatal(err)
	}
	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("NOOP after revoking a different credential: %v", err)
	}
}

// IDLE with a mailbox selected ends on the recheck timer.
func TestAppPasswordRevokeEndsIdle(t *testing.T) {
	old := idleRecheckInterval
	idleRecheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { idleRecheckInterval = old })

	e := newAppSessionEnv(t)
	addr := startRevokeServer(t, e.revokeEnv)
	pw, id := e.create(t, "phone")
	c := e.dial(t, addr, pw)
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	idle, err := c.Idle()
	if err != nil {
		t.Fatalf("IDLE: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- idle.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("IDLE ended while the credential was live: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := e.apps.Revoke(pollTestUser, id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("IDLE did not end after the revoke")
	}
	if err := c.Noop().Wait(); err == nil {
		t.Fatal("connection still usable after IDLE revoke")
	}
}

// IDLE before any SELECT has its own loop; the entry check covers an IDLE
// issued after the revoke.
func TestAppPasswordRevokeEndsIdleWithoutSelect(t *testing.T) {
	old := idleRecheckInterval
	idleRecheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { idleRecheckInterval = old })

	e := newAppSessionEnv(t)
	pw, id := e.create(t, "phone")
	s := e.login(t, pw)
	stop := make(chan struct{})
	defer close(stop)
	done := make(chan error, 1)
	go func() { done <- s.Idle(nil, stop) }()
	select {
	case err := <-done:
		t.Fatalf("IDLE ended while the credential was live: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := e.apps.Revoke(pollTestUser, id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		wantRevoked(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("IDLE without SELECT did not end after the revoke")
	}
}

// An IDLE issued after the revoke ends at once from the entry check, not on
// the next ticker.
func TestAppPasswordIdleEntryCheckIsPrompt(t *testing.T) {
	old := idleRecheckInterval
	idleRecheckInterval = time.Hour
	t.Cleanup(func() { idleRecheckInterval = old })

	e := newAppSessionEnv(t)
	pw, id := e.create(t, "phone")
	s := e.login(t, pw)
	if err := e.apps.Revoke(pollTestUser, id); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Idle(nil, make(chan struct{})) }()
	select {
	case err := <-done:
		wantRevoked(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("IDLE after the revoke did not end at entry")
	}
}
