package smtp

import (
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/auth/authtest"
	"github.com/bongo-project/bongo/internal/store"
)

// appSessionEnv is the revoke backend with app passwords read through a
// counting record store.
type appSessionEnv struct {
	b    *Backend
	d    *fakeSplitDeliverer
	apps *auth.AppPasswords
	recs *authtest.Records
}

func newAppSessionEnv(t *testing.T) *appSessionEnv {
	t.Helper()
	b, ps := revokeBackend(t)
	putSMTPProfile(t, ps, "pw", false)
	d := &fakeSplitDeliverer{}
	b.deliverer = d
	b.resolver = &fakeResolver{local: map[string]bool{"local@example.com": true}}
	b.relayQueue = &fakeRelayQueue{}
	b.senderPolicy = &fakePolicy{extra: map[string]bool{"u@example.com": true}}

	recs := &authtest.Records{AppPasswordRecords: store.NewAppPasswordStore(ps, touchApplier{ps})}
	apps := auth.NewAppPasswords(recs, nil)
	b.auth.(*auth.LocalAuth).SetAppPasswords(apps)
	return &appSessionEnv{b: b, d: d, apps: apps, recs: recs}
}

func (e *appSessionEnv) create(t *testing.T, label string) (password, id string) {
	t.Helper()
	pw, info, err := e.apps.Create("uid1", label)
	if err != nil {
		t.Fatal(err)
	}
	return pw, info.ID
}

// authed runs the real AuthPlain, so the session records the credential the
// way production does.
func (e *appSessionEnv) authed(t *testing.T, password string) *Session {
	t.Helper()
	s := newSessionForIP(e.b, "203.0.113.9")
	if err := s.AuthPlain("u@example.com", password); err != nil {
		t.Fatalf("AuthPlain: %v", err)
	}
	e.apps.Wait() // let the detached last-used write finish before any revoke
	return s
}

func (e *appSessionEnv) mailRcpt(t *testing.T, s *Session) {
	t.Helper()
	if err := s.Mail("u@example.com", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := s.Rcpt("local@example.com", nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
}

// Revoking the credential that opened the session is refused at MAIL, RCPT and
// DATA, each with the revoke landing just before that step, and nothing is
// queued.
func TestAppPasswordRevokeRefusedAtEachStep(t *testing.T) {
	steps := []struct {
		name string
		pre  func(t *testing.T, e *appSessionEnv, s *Session)
		step func(s *Session) error
	}{
		{"MAIL", func(*testing.T, *appSessionEnv, *Session) {}, func(s *Session) error { return s.Mail("u@example.com", nil) }},
		{"RCPT", func(t *testing.T, e *appSessionEnv, s *Session) {
			if err := s.Mail("u@example.com", nil); err != nil {
				t.Fatal(err)
			}
		}, func(s *Session) error { return s.Rcpt("local@example.com", nil) }},
		{"DATA", func(t *testing.T, e *appSessionEnv, s *Session) { e.mailRcpt(t, s) },
			func(s *Session) error { return s.Data(strings.NewReader(revokeMsg)) }},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			e := newAppSessionEnv(t)
			pw, id := e.create(t, "phone")
			s := e.authed(t, pw)
			st.pre(t, e, s)
			if err := s.checkAccountLive(st.name); err != nil {
				t.Fatalf("positive control: %v", err)
			}
			if err := e.apps.Revoke("uid1", id); err != nil {
				t.Fatal(err)
			}
			err := st.step(s)
			if code := smtpCode(err); code != 535 {
				t.Fatalf("%s after revoke = code %d (%v), want 535", st.name, code, err)
			}
			if len(e.d.localCalls) != 0 || e.d.stored != 0 {
				t.Fatalf("queued %d, stored %d after revoke; want 0 and 0", len(e.d.localCalls), e.d.stored)
			}
		})
	}
}

func TestAppPasswordRevokeControlsSMTP(t *testing.T) {
	t.Run("different credential revoked", func(t *testing.T) {
		e := newAppSessionEnv(t)
		pw, _ := e.create(t, "phone")
		_, other := e.create(t, "laptop")
		s := e.authed(t, pw)
		if err := e.apps.Revoke("uid1", other); err != nil {
			t.Fatal(err)
		}
		e.mailRcpt(t, s)
		if err := s.Data(strings.NewReader(revokeMsg)); err != nil {
			t.Fatalf("Data after an unrelated revoke: %v", err)
		}
		if len(e.d.localCalls) != 1 {
			t.Fatalf("%d enqueues, want 1", len(e.d.localCalls))
		}
	})
	t.Run("account password session does no record read", func(t *testing.T) {
		e := newAppSessionEnv(t)
		_, id := e.create(t, "phone")
		s := e.authed(t, "pw")
		if err := e.apps.Revoke("uid1", id); err != nil {
			t.Fatal(err)
		}
		e.recs.Gets.Store(0)
		e.mailRcpt(t, s)
		if err := s.Data(strings.NewReader(revokeMsg)); err != nil {
			t.Fatalf("Data for an account password session after a revoke: %v", err)
		}
		if got := e.recs.Gets.Load(); got != 0 {
			t.Fatalf("record Gets = %d, want 0", got)
		}
	})
	t.Run("RevokeAll", func(t *testing.T) {
		e := newAppSessionEnv(t)
		pw, _ := e.create(t, "phone")
		app, acct := e.authed(t, pw), e.authed(t, "pw")
		if _, err := e.apps.RevokeAll("uid1"); err != nil {
			t.Fatal(err)
		}
		if code := smtpCode(app.Mail("u@example.com", nil)); code != 535 {
			t.Fatalf("app session MAIL = code %d, want 535", code)
		}
		if err := acct.Mail("u@example.com", nil); err != nil {
			t.Fatalf("account password session MAIL: %v", err)
		}
	})
}

// A record read error is a temporary 451 on MAIL, never the permanent
// 535 and never access, and the session recovers once reads do.
func TestAppPasswordRecordReadErrorFailsClosedSMTP(t *testing.T) {
	e := newAppSessionEnv(t)
	pw, _ := e.create(t, "phone")
	s := e.authed(t, pw)

	e.recs.Fail.Store(true)
	if code := smtpCode(s.Mail("u@example.com", nil)); code != 451 {
		t.Fatalf("MAIL with record read error = code %d, want 451", code)
	}
	e.recs.Fail.Store(false)
	if err := s.Mail("u@example.com", nil); err != nil {
		t.Fatalf("MAIL after recovery: %v", err)
	}
}

// Reset runs between messages on one connection and must keep the credential,
// or the session would become immune to a revoke after the first message.
func TestAppPasswordSurvivesResetSMTP(t *testing.T) {
	e := newAppSessionEnv(t)
	pw, id := e.create(t, "phone")
	s := e.authed(t, pw)
	e.mailRcpt(t, s)
	s.Reset()
	if err := e.apps.Revoke("uid1", id); err != nil {
		t.Fatal(err)
	}
	if code := smtpCode(s.Mail("u@example.com", nil)); code != 535 {
		t.Fatalf("MAIL after Reset and revoke = code %d, want 535", code)
	}
}
