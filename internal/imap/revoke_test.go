package imap

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

// revokeEnv is a real LocalAuth over the same Pebble store the session reads,
// so revocation is driven exactly as production does it: by rewriting or
// deleting the profile key.
type revokeEnv struct {
	ps   *store.PebbleStore
	ms   *store.MailboxStore
	auth *auth.LocalAuth
}

func (e *revokeEnv) putProfile(t *testing.T, password string, disabled bool) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(&bongov1.UserProfile{Email: pollTestUsername, PasswordHash: hash, Disabled: disabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Set([]byte(fmt.Sprintf("/user/%s/profile", pollTestUser)), data); err != nil {
		t.Fatal(err)
	}
}

// disableProfile flips only Disabled on the stored profile, keeping the
// password hash (and so the session epoch) untouched, so the case exercises
// the Disabled check and nothing else.
func (e *revokeEnv) disableProfile(t *testing.T) {
	t.Helper()
	key := []byte(fmt.Sprintf("/user/%s/profile", pollTestUser))
	data, err := e.ps.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	p.Disabled = true
	if data, err = proto.Marshal(&p); err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Set(key, data); err != nil {
		t.Fatal(err)
	}
}

func newRevokeEnv(t *testing.T) *revokeEnv {
	t.Helper()
	ms, ps := newPollTestStore(t, "INBOX")
	deliverPollTestMessage(t, ms, "INBOX", "guid-1")

	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	if _, err := mm.System.DB().Exec(
		"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, 'U', 1, 0)",
		pollTestUser, pollTestUsername); err != nil {
		t.Fatal(err)
	}
	e := &revokeEnv{ps: ps, ms: ms, auth: auth.NewLocalAuth(ps, mm)}
	e.putProfile(t, pollTestPassword, false)
	return e
}

// revocations are the three account changes that must end a live session.
var revocations = []struct {
	name   string
	revoke func(t *testing.T, e *revokeEnv)
}{
	{"disabled", func(t *testing.T, e *revokeEnv) { e.disableProfile(t) }},
	{"password reset", func(t *testing.T, e *revokeEnv) { e.putProfile(t, "a-new-password", false) }},
	{"deleted", func(t *testing.T, e *revokeEnv) {
		if err := e.ps.Delete([]byte(fmt.Sprintf("/user/%s/profile", pollTestUser))); err != nil {
			t.Fatal(err)
		}
	}},
}

func newRevokeSession(t *testing.T, e *revokeEnv) *session {
	t.Helper()
	s := &session{
		auth:        e.auth,
		mailboxes:   e.ms,
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		authLimiter: nil,
	}
	// Bypass the limiter; Login's own job is covered separately.
	userID, err := e.auth.Authenticate(pollTestUsername, pollTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	st, err := e.auth.AccountState(userID)
	if err != nil {
		t.Fatal(err)
	}
	s.userID, s.epoch = userID, st.Epoch
	return s
}

// authenticatedSessionMethods lists every session method that must call
// checkAccount, so revocation and store-error tests enumerate the same set.
func authenticatedSessionMethods() map[string]func(s *session) error {
	return map[string]func(s *session) error{
		"Select": func(s *session) error { _, err := s.Select("INBOX", nil); return err },
		"Create": func(s *session) error { return s.Create("X", nil) },
		"Delete": func(s *session) error { return s.Delete("INBOX") },
		"Rename": func(s *session) error { return s.Rename("INBOX", "Y", nil) },
		"Subscribe": func(s *session) error {
			return s.Subscribe("INBOX")
		},
		"Unsubscribe": func(s *session) error { return s.Unsubscribe("INBOX") },
		"List":        func(s *session) error { return s.List(nil, "", []string{"*"}, nil) },
		"Status": func(s *session) error {
			_, err := s.Status("INBOX", &imap.StatusOptions{NumMessages: true})
			return err
		},
		"Append":   func(s *session) error { _, err := s.Append("INBOX", nil, nil); return err },
		"Poll":     func(s *session) error { return s.Poll(nil, true) },
		"Idle":     func(s *session) error { return s.Idle(nil, make(chan struct{})) },
		"Unselect": func(s *session) error { return s.Unselect() },
		"Expunge":  func(s *session) error { return s.Expunge(nil, nil) },
		"Search":   func(s *session) error { _, err := s.Search(0, &imap.SearchCriteria{}, nil); return err },
		"Fetch":    func(s *session) error { return s.Fetch(nil, imap.SeqSetNum(1), &imap.FetchOptions{}) },
		"Store":    func(s *session) error { return s.Store(nil, imap.SeqSetNum(1), &imap.StoreFlags{}, nil) },
		"Copy":     func(s *session) error { _, err := s.Copy(imap.SeqSetNum(1), "INBOX"); return err },
		"Move":     func(s *session) error { return s.Move(nil, imap.SeqSetNum(1), "INBOX") },
	}
}

// Every authenticated session method must refuse after revocation. A method
// missing its checkAccount call is a bypass, so this enumerates all of them.
func TestSessionMethodsRefuseAfterRevocation(t *testing.T) {
	methods := authenticatedSessionMethods()

	for _, rv := range revocations {
		for name, call := range methods {
			t.Run(rv.name+"/"+name, func(t *testing.T) {
				e := newRevokeEnv(t)
				s := newRevokeSession(t, e)

				// Positive control: the same session is healthy before revocation.
				if err := s.checkAccount(); err != nil {
					t.Fatalf("checkAccount before revocation = %v", err)
				}

				rv.revoke(t, e)

				err := call(s)
				var ie *imap.Error
				if !errors.As(err, &ie) || ie.Type != imap.StatusResponseTypeNo || ie.Text != errAccountRevoked.Text {
					t.Fatalf("%s after %s = %v, want NO account no longer available", name, rv.name, err)
				}
			})
		}
	}
}

// A store read error refuses the command with a temporary failure rather than
// granting access on state it could not verify.
func TestCheckAccountStoreErrorFailsClosed(t *testing.T) {
	e := newRevokeEnv(t)
	s := newRevokeSession(t, e)
	s.auth = errState{}
	err := s.checkAccount()
	var ie *imap.Error
	if !errors.As(err, &ie) || ie.Type != imap.StatusResponseTypeNo || ie.Code != imap.ResponseCodeUnavailable {
		t.Fatalf("checkAccount with store error = %v, want NO [UNAVAILABLE]", err)
	}
}

// flakyState is a real LocalAuth whose AccountState fails while fail is set.
type flakyState struct {
	*auth.LocalAuth
	fail atomic.Bool
}

func (f *flakyState) AccountState(userID string) (auth.AccountState, error) {
	if f.fail.Load() {
		return auth.AccountState{}, errors.New("pebble unavailable")
	}
	return f.LocalAuth.AccountState(userID)
}

// Through a real client: while the store errors every authenticated command
// gets NO [UNAVAILABLE], and the connection survives (no BYE) and recovers.
func TestIMAPStoreErrorRefusesCommandsThroughRealSession(t *testing.T) {
	commands := map[string]func(c *imapclient.Client) error{
		"STATUS": func(c *imapclient.Client) error {
			_, err := c.Status("INBOX", &imap.StatusOptions{NumMessages: true}).Wait()
			return err
		},
		"FETCH": func(c *imapclient.Client) error {
			_, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{UID: true}).Collect()
			return err
		},
		"LIST":   func(c *imapclient.Client) error { _, err := c.List("", "*", nil).Collect(); return err },
		"SEARCH": func(c *imapclient.Client) error { _, err := c.Search(&imap.SearchCriteria{}, nil).Wait(); return err },
	}
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			e := newRevokeEnv(t)
			flaky := &flakyState{LocalAuth: e.auth}
			addr := startRevokeServerWithAuth(t, e, flaky)
			c := dialPollTestClient(t, addr, nil)
			if _, err := c.Select("INBOX", nil).Wait(); err != nil {
				t.Fatalf("SELECT: %v", err)
			}
			if err := cmd(c); err != nil {
				t.Fatalf("%s while healthy: %v", name, err)
			}

			flaky.fail.Store(true)
			err := cmd(c)
			var ie *imap.Error
			if !errors.As(err, &ie) || ie.Type != imap.StatusResponseTypeNo || ie.Code != imap.ResponseCodeUnavailable {
				t.Fatalf("%s with store error = %v, want NO [UNAVAILABLE]", name, err)
			}

			flaky.fail.Store(false)
			if err := cmd(c); err != nil {
				t.Fatalf("%s after store recovery: %v (connection should have survived)", name, err)
			}
		})
	}
}

// NOOP has no handler, only the post-command Poll. During a store error it
// must send no mailbox updates and must not drop the connection.
func TestIMAPNoopDuringStoreErrorSendsNoUpdates(t *testing.T) {
	e := newRevokeEnv(t)
	flaky := &flakyState{LocalAuth: e.auth}
	addr := startRevokeServerWithAuth(t, e, flaky)
	c := dialPollTestClient(t, addr, nil)
	sel, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	before := sel.NumMessages

	flaky.fail.Store(true)
	deliverPollTestMessage(t, e.ms, "INBOX", "guid-2")
	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("NOOP during store error: %v", err)
	}
	if got := c.Mailbox().NumMessages; got != before {
		t.Fatalf("NumMessages = %d during store error, want %d (no updates)", got, before)
	}

	flaky.fail.Store(false)
	if err := c.Noop().Wait(); err != nil {
		t.Fatal(err)
	}
	if got := c.Mailbox().NumMessages; got != before+1 {
		t.Fatalf("NumMessages = %d after recovery, want %d", got, before+1)
	}
}

// IDLE issued during a store error is refused by the entry check rather than
// pushing mailbox counts on state it could not verify, and the connection
// survives.
func TestIMAPIdleDuringStoreErrorRefused(t *testing.T) {
	e := newRevokeEnv(t)
	flaky := &flakyState{LocalAuth: e.auth}
	addr := startRevokeServerWithAuth(t, e, flaky)
	c := dialPollTestClient(t, addr, nil)
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}

	flaky.fail.Store(true)
	idle, err := c.Idle()
	if err == nil {
		// go-imap reports the handler's error only after the client ends
		// the IDLE with DONE; an accepted IDLE would still be idling here.
		time.Sleep(200 * time.Millisecond)
		done := make(chan error, 1)
		go func() {
			_ = idle.Close()
			done <- idle.Wait()
		}()
		select {
		case err = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("IDLE did not end")
		}
	}
	if err == nil {
		t.Fatal("IDLE ended without an error during a store error")
	}
	var ie *imap.Error
	if !errors.As(err, &ie) || ie.Type != imap.StatusResponseTypeNo || ie.Code != imap.ResponseCodeUnavailable {
		t.Fatalf("IDLE with store error = %v, want NO [UNAVAILABLE]", err)
	}

	flaky.fail.Store(false)
	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("connection should survive: %v", err)
	}
}

type errState struct{}

func (errState) Authenticate(string, string) (string, error) { return "", errors.New("n/a") }
func (errState) AccountState(string) (auth.AccountState, error) {
	return auth.AccountState{}, errors.New("pebble unavailable")
}

func startRevokeServer(t *testing.T, e *revokeEnv) string {
	t.Helper()
	return startRevokeServerWithAuth(t, e, e.auth)
}

func startRevokeServerWithAuth(t *testing.T, e *revokeEnv, a profileAuthenticator) string {
	t.Helper()
	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	notifier := NewNotifier()
	e.ms.SetNotifier(NewStoreNotifier(notifier))
	srv := NewServer(a, e.ps, blobs, e.ms, nil, nil, nil, notifier, nil, true,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := srv.Listen("127.0.0.1:0", "", nil, false, nil); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv.PlainAddr()
}

// A real client keeps working after revocation today; after the fix the next
// command fails and the server closes the connection.
func TestRevokedIMAPSessionEndsOnNextCommand(t *testing.T) {
	commands := map[string]func(c *imapclient.Client) error{
		"NOOP": func(c *imapclient.Client) error { return c.Noop().Wait() },
		"FETCH": func(c *imapclient.Client) error {
			_, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{UID: true}).Collect()
			return err
		},
		"LIST":   func(c *imapclient.Client) error { _, err := c.List("", "*", nil).Collect(); return err },
		"SEARCH": func(c *imapclient.Client) error { _, err := c.Search(&imap.SearchCriteria{}, nil).Wait(); return err },
	}
	for _, rv := range revocations {
		for name, cmd := range commands {
			t.Run(rv.name+"/"+name, func(t *testing.T) {
				e := newRevokeEnv(t)
				addr := startRevokeServer(t, e)
				c := dialPollTestClient(t, addr, nil)
				if _, err := c.Select("INBOX", nil).Wait(); err != nil {
					t.Fatalf("SELECT: %v", err)
				}
				// Positive control.
				if err := cmd(c); err != nil {
					t.Fatalf("%s before revocation: %v", name, err)
				}

				rv.revoke(t, e)

				if err := cmd(c); err == nil {
					t.Fatalf("%s succeeded after %s", name, rv.name)
				}
				// The connection is gone: even a NOOP cannot get through.
				if err := c.Noop().Wait(); err == nil {
					t.Fatal("connection still usable after revocation")
				}
			})
		}
	}
}

func TestRevokedIMAPLoginRefused(t *testing.T) {
	e := newRevokeEnv(t)
	e.putProfile(t, pollTestPassword, true)
	addr := startRevokeServer(t, e)
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Login(pollTestUsername, pollTestPassword).Wait(); err == nil {
		t.Fatal("login succeeded for a disabled profile")
	}
}

func TestRevokedIMAPIdleEnds(t *testing.T) {
	old := idleRecheckInterval
	idleRecheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { idleRecheckInterval = old })

	for _, rv := range revocations {
		t.Run(rv.name, func(t *testing.T) {
			e := newRevokeEnv(t)
			addr := startRevokeServer(t, e)
			c := dialPollTestClient(t, addr, nil)
			if _, err := c.Select("INBOX", nil).Wait(); err != nil {
				t.Fatal(err)
			}
			idle, err := c.Idle()
			if err != nil {
				t.Fatalf("IDLE: %v", err)
			}

			// Still idling after several ticks while the account is healthy.
			done := make(chan error, 1)
			go func() { done <- idle.Wait() }()
			select {
			case err := <-done:
				t.Fatalf("IDLE ended while account healthy: %v", err)
			case <-time.After(300 * time.Millisecond):
			}

			rv.revoke(t, e)

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("IDLE did not end after revocation")
			}
			if err := c.Noop().Wait(); err == nil {
				t.Fatal("connection still usable after IDLE revocation")
			}
		})
	}
}

// RFC 2177 allows IDLE in the authenticated state, before any SELECT. That
// loop has its own ticker, so it needs its own coverage.
func TestRevokedIMAPIdleWithoutSelectEnds(t *testing.T) {
	old := idleRecheckInterval
	idleRecheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { idleRecheckInterval = old })

	for _, rv := range revocations {
		t.Run(rv.name, func(t *testing.T) {
			e := newRevokeEnv(t)
			s := newRevokeSession(t, e)
			stop := make(chan struct{})
			defer close(stop)

			done := make(chan error, 1)
			go func() { done <- s.Idle(nil, stop) }()

			select {
			case err := <-done:
				t.Fatalf("IDLE ended while account healthy: %v", err)
			case <-time.After(300 * time.Millisecond):
			}

			rv.revoke(t, e)

			select {
			case err := <-done:
				var ie *imap.Error
				if !errors.As(err, &ie) || ie.Text != errAccountRevoked.Text {
					t.Fatalf("IDLE ended with %v, want account revoked", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("IDLE without SELECT did not end after revocation")
			}
		})
	}
}

// An IDLE issued after revocation must end at once from the entry check, not
// wait for the next ticker.
func TestRevokedIMAPIdleEntryCheckIsPrompt(t *testing.T) {
	old := idleRecheckInterval
	idleRecheckInterval = time.Hour
	t.Cleanup(func() { idleRecheckInterval = old })

	e := newRevokeEnv(t)
	s := newRevokeSession(t, e)
	e.putProfile(t, pollTestPassword, true)

	done := make(chan error, 1)
	go func() { done <- s.Idle(nil, make(chan struct{})) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("IDLE returned nil for a revoked account")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("IDLE after revocation did not end promptly")
	}
}

func (e errState) AuthenticateProfile(username, password string) (string, *bongov1.UserProfile, error) {
	id, err := e.Authenticate(username, password)
	if err != nil {
		return "", nil, err
	}
	return id, &bongov1.UserProfile{}, nil
}

// Every authenticated method must answer a store error with the temporary
// NO [UNAVAILABLE], never success and never the permanent revoked error.
func TestSessionMethodsRefuseOnStoreError(t *testing.T) {
	for name, call := range authenticatedSessionMethods() {
		if name == "Poll" {
			// Poll deliberately returns nil on a store error (a non-nil
			// return would kill the connection); that is covered by
			// TestIMAPNoopDuringStoreErrorSendsNoUpdates.
			continue
		}
		t.Run(name, func(t *testing.T) {
			e := newRevokeEnv(t)
			s := newRevokeSession(t, e)
			s.auth = errState{}

			err := call(s)
			var ie *imap.Error
			if !errors.As(err, &ie) || ie.Type != imap.StatusResponseTypeNo ||
				ie.Code != imap.ResponseCodeUnavailable || ie.Text != errAccountStateUnavailable.Text {
				t.Fatalf("%s with store error = %v, want NO [UNAVAILABLE]", name, err)
			}
		})
	}
}

// IDLE started while the store is healthy must end with the temporary failure
// once the store starts erroring. Both recheck loops have their own ticker.
func TestIMAPIdleEndsWhenStoreStartsErroring(t *testing.T) {
	old := idleRecheckInterval
	idleRecheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { idleRecheckInterval = old })

	for _, tc := range []struct {
		name     string
		selected bool
	}{
		{"no mailbox selected", false},
		{"mailbox selected", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRevokeEnv(t)
			s := newRevokeSession(t, e)
			flaky := &flakyState{LocalAuth: e.auth}
			s.auth = flaky
			if tc.selected {
				s.selectedMailbox = "INBOX"
				s.notifier = NewNotifier()
			}
			stop := make(chan struct{})
			defer close(stop)

			done := make(chan error, 1)
			go func() { done <- s.Idle(nil, stop) }()

			select {
			case err := <-done:
				t.Fatalf("IDLE ended while store healthy: %v", err)
			case <-time.After(300 * time.Millisecond):
			}

			flaky.fail.Store(true)

			select {
			case err := <-done:
				var ie *imap.Error
				if !errors.As(err, &ie) || ie.Code != imap.ResponseCodeUnavailable {
					t.Fatalf("IDLE ended with %v, want NO [UNAVAILABLE]", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("IDLE did not end after the store started erroring")
			}
		})
	}
}
