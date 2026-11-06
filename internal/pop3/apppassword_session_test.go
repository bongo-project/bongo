package pop3

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/auth/authtest"
	"github.com/bongo-project/bongo/internal/store"
)

// appSessionEnv is a real LocalAuth with one account (uid1, password "pw"),
// one INBOX message and app passwords read through a counting record store.
type appSessionEnv struct {
	ps   *store.PebbleStore
	ms   *store.MailboxStore
	la   *auth.LocalAuth
	apps *auth.AppPasswords
	recs *authtest.Records
}

func newAppSessionEnv(t *testing.T) *appSessionEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	if _, err := mm.System.DB().Exec(
		"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES ('uid1', 'u@example.com', 'U', 1, 0)"); err != nil {
		t.Fatal(err)
	}
	putProfile(t, ps, "pw", false)

	ms := store.NewMailboxStore(ps, &pop3DirectApplier{ps: ps})
	for _, mb := range []string{"INBOX", "Trash"} {
		if err := ms.CreateMailbox("uid1", mb); err != nil {
			t.Fatal(err)
		}
	}
	meta := &bongov1.MailMeta{Guid: "g1", Subject: "s", ReceivedAt: time.Now().Unix(), RawMessage: &bongov1.BlobRef{Hash: "abc", Size: 10}}
	if _, err := ms.DeliverMessage("uid1", "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/uid1/mail/g1"), data); err != nil {
		t.Fatal(err)
	}

	la := auth.NewLocalAuth(ps, mm)
	recs := &authtest.Records{AppPasswordRecords: store.NewAppPasswordStore(ps, pebbleApplier{ps})}
	apps := auth.NewAppPasswords(recs, nil)
	la.SetAppPasswords(apps)
	return &appSessionEnv{ps: ps, ms: ms, la: la, apps: apps, recs: recs}
}

func (e *appSessionEnv) create(t *testing.T, label string) (password, id string) {
	t.Helper()
	pw, info, err := e.apps.Create("uid1", label)
	if err != nil {
		t.Fatal(err)
	}
	return pw, info.ID
}

// open logs in through USER and PASS on a real session with the given
// password and returns the client side. The session is also returned so the
// test can swap its authenticator.
func (e *appSessionEnv) open(t *testing.T, password string) (*testPOP3, *session, chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	s := newSession(serverConn, e.la, e.ms, e.ps, nil, nil, false, true, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { s.handle(); close(done) }()
	_ = clientConn.SetDeadline(time.Now().Add(10 * time.Second))
	t.Cleanup(func() { clientConn.Close() })

	tc := &testPOP3{conn: clientConn, reader: bufio.NewReader(clientConn), t: t}
	tc.expectOK()
	tc.send("USER u@example.com")
	tc.expectOK()
	tc.send("PASS " + password)
	tc.expectOK()
	e.apps.Wait() // let the detached last-used write finish before any revoke
	return tc, s, done
}

func (e *appSessionEnv) messageCount(t *testing.T) int {
	t.Helper()
	msgs, err := e.ms.ListMessages("uid1", "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	return len(msgs)
}

func waitClosed(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connection was not closed")
	}
}

// Every authenticated command is refused after the credential that opened the
// session is revoked, and a pending DELE is not applied by QUIT.
func TestAppPasswordRevokeEndsPOP3Session(t *testing.T) {
	for _, command := range []string{"STAT", "LIST", "RETR 1", "TOP 1 0", "DELE 1", "RSET", "UIDL", "NOOP", "QUIT"} {
		t.Run(command, func(t *testing.T) {
			e := newAppSessionEnv(t)
			pw, id := e.create(t, "phone")
			tc, _, done := e.open(t, pw)
			tc.send("DELE 1")
			tc.expectOK()
			tc.send("STAT") // positive control: the credential is live
			tc.expectOK()

			if err := e.apps.Revoke("uid1", id); err != nil {
				t.Fatal(err)
			}

			tc.send(command)
			if line := tc.expectErr(); line != "-ERR account no longer available" {
				t.Fatalf("%s after revoke = %q", command, line)
			}
			waitClosed(t, done)
			if got := e.messageCount(t); got != 1 {
				t.Fatalf("INBOX has %d messages, want 1 (the DELE mark must not apply)", got)
			}
		})
	}
}

func TestAppPasswordRevokeControlsPOP3(t *testing.T) {
	t.Run("different credential revoked", func(t *testing.T) {
		e := newAppSessionEnv(t)
		pw, _ := e.create(t, "phone")
		_, other := e.create(t, "laptop")
		tc, _, _ := e.open(t, pw)
		if err := e.apps.Revoke("uid1", other); err != nil {
			t.Fatal(err)
		}
		tc.send("STAT")
		tc.expectOK()
	})
	t.Run("account password session does no record read", func(t *testing.T) {
		e := newAppSessionEnv(t)
		_, id := e.create(t, "phone")
		tc, _, _ := e.open(t, "pw")
		e.recs.Gets.Store(0)
		tc.send("STAT")
		tc.expectOK()
		if err := e.apps.Revoke("uid1", id); err != nil {
			t.Fatal(err)
		}
		e.recs.Gets.Store(0)
		tc.send("STAT")
		tc.expectOK()
		if got := e.recs.Gets.Load(); got != 0 {
			t.Fatalf("record Gets = %d, want 0", got)
		}
	})
	t.Run("RevokeAll", func(t *testing.T) {
		e := newAppSessionEnv(t)
		pw, _ := e.create(t, "phone")
		tc, _, done := e.open(t, pw)
		acct, _, _ := e.open(t, "pw")
		if _, err := e.apps.RevokeAll("uid1"); err != nil {
			t.Fatal(err)
		}
		tc.send("STAT")
		tc.expectErr()
		waitClosed(t, done)
		acct.send("STAT")
		acct.expectOK()
	})
}

// A record read error is a temporary refusal that closes the session without
// applying DELE marks, not the permanent one and not access.
func TestAppPasswordRecordReadErrorFailsClosedPOP3(t *testing.T) {
	e := newAppSessionEnv(t)
	pw, _ := e.create(t, "phone")
	tc, _, done := e.open(t, pw)
	tc.send("DELE 1")
	tc.expectOK()

	e.recs.Fail.Store(true)
	tc.send("QUIT")
	if line := tc.expectErr(); !strings.HasPrefix(line, "-ERR [SYS/TEMP]") {
		t.Fatalf("QUIT with record read error = %q, want -ERR [SYS/TEMP]", line)
	}
	waitClosed(t, done)
	if got := e.messageCount(t); got != 1 {
		t.Fatalf("INBOX has %d messages, want 1", got)
	}
}
