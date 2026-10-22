package pop3

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

func putProfile(t *testing.T, ps *store.PebbleStore, password string, disabled bool) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(&bongov1.UserProfile{Email: "u@example.com", PasswordHash: hash, Disabled: disabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/uid1/profile"), data); err != nil {
		t.Fatal(err)
	}
}

// disableProfile flips only Disabled, keeping the password hash (and so the
// session epoch) untouched, so the case exercises the Disabled check alone.
func disableProfile(t *testing.T, ps *store.PebbleStore) {
	t.Helper()
	data, err := ps.Get([]byte("/user/uid1/profile"))
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
	if err := ps.Set([]byte("/user/uid1/profile"), data); err != nil {
		t.Fatal(err)
	}
}

// A live POP3 session must end on the next command once the account is
// disabled, deleted or has its password reset.
func TestPOP3SessionEndsAfterRevocation(t *testing.T) {
	tests := []struct {
		name   string
		revoke func(t *testing.T, ps *store.PebbleStore)
	}{
		{"disabled", func(t *testing.T, ps *store.PebbleStore) { disableProfile(t, ps) }},
		{"password reset", func(t *testing.T, ps *store.PebbleStore) { putProfile(t, ps, "new-pw", false) }},
		{"deleted", func(t *testing.T, ps *store.PebbleStore) {
			if err := ps.Delete([]byte("/user/uid1/profile")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			serverConn, clientConn := net.Pipe()
			s := newSession(serverConn, auth.NewLocalAuth(ps, mm), store.NewMailboxStore(ps, nil), ps, nil, nil, false, true, nil,
				slog.New(slog.NewTextHandler(io.Discard, nil)))
			done := make(chan struct{})
			go func() { s.handle(); close(done) }()

			tc := &testPOP3{conn: clientConn, reader: bufio.NewReader(clientConn), t: t}
			tc.expectOK() // greeting
			tc.send("USER u@example.com")
			tc.expectOK()
			tc.send("PASS pw")
			tc.expectOK()
			// Positive control: the session works while the account is healthy.
			tc.send("STAT")
			tc.expectOK()

			tt.revoke(t, ps)

			tc.send("STAT")
			if line := tc.expectErr(); line != "-ERR account no longer available" {
				t.Fatalf("STAT after %s = %q", tt.name, line)
			}
			<-done // server closed the connection
			if _, err := io.ReadAll(clientConn); err != nil {
				t.Logf("read after close: %v", err)
			}
		})
	}
}

func TestPOP3LoginRefusedForDisabledProfile(t *testing.T) {
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
	putProfile(t, ps, "pw", true)

	got := passAttempt(t, auth.NewLocalAuth(ps, mm), store.NewMailboxStore(ps, nil), nil, "u@example.com", "pw")
	if got != "-ERR authentication failed\r\n" {
		t.Fatalf("PASS for disabled profile = %q", got)
	}
}

// pop3DirectApplier applies Raft commands straight to Pebble.
type pop3DirectApplier struct{ ps *store.PebbleStore }

func (a *pop3DirectApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && store.IsMailboxSyntheticCommand(cmd.Key) {
		_, err := store.ApplyMailboxCommand(a.ps, cmd)
		return err
	}
	if cmd.Op == bongov1.Op_OP_PUT {
		return a.ps.Set(cmd.Key, cmd.Value)
	}
	return a.ps.Delete(cmd.Key)
}

// DELE marks a message, the account is revoked, then QUIT must be refused and
// must not apply the mark: a revoked user cannot get a deletion through the
// UPDATE state.
func TestPOP3QuitDoesNotApplyDeletionsForRevokedAccount(t *testing.T) {
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
	meta := &bongov1.MailMeta{
		Guid:       "g1",
		Subject:    "s",
		ReceivedAt: time.Now().Unix(),
		RawMessage: &bongov1.BlobRef{Hash: "abc", Size: 10},
	}
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

	serverConn, clientConn := net.Pipe()
	s := newSession(serverConn, auth.NewLocalAuth(ps, mm), ms, ps, nil, nil, false, true, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { s.handle(); close(done) }()

	tc := &testPOP3{conn: clientConn, reader: bufio.NewReader(clientConn), t: t}
	tc.expectOK()
	tc.send("USER u@example.com")
	tc.expectOK()
	tc.send("PASS pw")
	tc.expectOK()
	tc.send("DELE 1") // positive control: the mark is accepted while healthy
	tc.expectOK()

	putProfile(t, ps, "pw", true)

	tc.send("QUIT")
	if line := tc.expectErr(); line != "-ERR account no longer available" {
		t.Fatalf("QUIT after revocation = %q", line)
	}
	<-done

	msgs, err := ms.ListMessages("uid1", "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("INBOX has %d messages after revoked QUIT, want 1 (deletion must not apply)", len(msgs))
	}
}
