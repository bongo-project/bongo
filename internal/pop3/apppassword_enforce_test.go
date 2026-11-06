package pop3

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

// flaggedPOP3 is a real LocalAuth over one account (uid1, "u@example.com",
// password "pw") whose two-factor flag and app-password-only switch the test
// can flip, with the Pebble store the sessions read.
type flaggedPOP3 struct {
	la       *auth.LocalAuth
	apps     *auth.AppPasswords
	mailbox  *store.MailboxStore
	ps       *store.PebbleStore
	mm       *store.MetadataManager
	passHash []byte
}

func newFlaggedPOP3(t *testing.T) *flaggedPOP3 {
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
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	f := &flaggedPOP3{ps: ps, mm: mm, passHash: hash, mailbox: store.NewMailboxStore(ps, nil)}
	f.setFlags(t, false, false)
	f.la = auth.NewLocalAuth(ps, mm)
	f.apps = auth.NewAppPasswords(store.NewAppPasswordStore(ps, pebbleApplier{ps}), nil)
	f.la.SetAppPasswords(f.apps)
	return f
}

func (f *flaggedPOP3) setFlags(t *testing.T, totp, require bool) {
	t.Helper()
	data, err := proto.Marshal(&bongov1.UserProfile{Email: "u@example.com", PasswordHash: f.passHash, TotpEnabled: totp, RequireAppPasswords: require})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ps.Set([]byte("/user/uid1/profile"), data); err != nil {
		t.Fatal(err)
	}
}

// connect starts a session over a pipe and logs in; the reply to PASS is
// returned for the caller to judge.
func (f *flaggedPOP3) connect(t *testing.T, password string) (*testPOP3, string) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	s := newSession(serverConn, f.la, f.mailbox, f.ps, nil, nil, false, true, authlimit.New(pop3MaxAuthFailures, time.Minute),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	go s.handle()
	t.Cleanup(func() { _ = clientConn.Close() })
	tc := &testPOP3{conn: clientConn, reader: bufio.NewReader(clientConn), t: t}
	tc.expectOK() // greeting
	tc.send("USER u@example.com")
	tc.expectOK()
	tc.send("PASS " + password)
	line, err := tc.reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the PASS reply: %v", err)
	}
	return tc, strings.TrimRight(line, "\r\n")
}

// POP3 PASS honours the per-account switch.
func TestPassHonoursRequireAppPasswords(t *testing.T) {
	f := newFlaggedPOP3(t)
	pw, _, err := f.apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		totp, require bool
		password      string
		wantOK        bool
	}{
		{"switch off, account password", true, false, "pw", true},
		{"switch on with 2FA, account password", true, true, "pw", false},
		{"switch on with 2FA, app password", true, true, pw, true},
		{"switch on without 2FA, account password", false, true, "pw", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.setFlags(t, tt.totp, tt.require)
			lim := authlimit.New(pop3MaxAuthFailures, time.Minute)
			got := passAttempt(t, f.la, f.mailbox, lim, "u@example.com", tt.password)
			if tt.wantOK && !strings.HasPrefix(got, "+OK") {
				t.Errorf("PASS = %q, want +OK", got)
			}
			if !tt.wantOK && !strings.HasPrefix(got, "-ERR") {
				t.Errorf("PASS = %q, want -ERR", got)
			}
		})
	}
	f.apps.Wait()
}

// Turning the switch on ends a session opened with the account password on its
// next command, and the app password then connects again.
func TestSwitchOnEndsAccountPasswordPOP3Session(t *testing.T) {
	f := newFlaggedPOP3(t)
	pw, _, err := f.apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	f.setFlags(t, true, false)

	tc, reply := f.connect(t, "pw")
	if !strings.HasPrefix(reply, "+OK") {
		t.Fatalf("PASS = %q", reply)
	}
	// Control: a real command works before the switch moves.
	tc.send("STAT")
	tc.expectOK()

	f.setFlags(t, true, true)
	tc.send("STAT")
	if line := tc.expectErr(); line != "-ERR account no longer available" {
		t.Fatalf("STAT after the switch = %q", line)
	}

	again, reply := f.connect(t, pw)
	if !strings.HasPrefix(reply, "+OK") {
		t.Fatalf("app password after the switch: %q", reply)
	}
	again.send("STAT")
	again.expectOK()
	f.apps.Wait()
}

// Wrong app passwords count as failures: a lookup id that exists with a wrong
// secret locks the address out, after which even the valid credential is
// refused. A limiter that skipped failures for app-password-shaped input would
// pass every plain-password lockout test.
func TestPOP3WrongAppPasswordsLockTheAddressOut(t *testing.T) {
	f := newFlaggedPOP3(t)
	pw, _, err := f.apps.Create("uid1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	lim := authlimit.New(pop3MaxAuthFailures, time.Minute)
	wrong := pw[:6] + "-aaaaaa-aaaaaa-aaaaaa"
	for range pop3MaxAuthFailures {
		if got := passAttempt(t, f.la, f.mailbox, lim, "u@example.com", wrong); !strings.HasPrefix(got, "-ERR") {
			t.Fatalf("a wrong app password was accepted: %q", got)
		}
	}
	got := passAttempt(t, f.la, f.mailbox, lim, "u@example.com", pw)
	if !strings.HasPrefix(got, "-ERR") || !strings.Contains(strings.ToLower(got), "too many") {
		t.Fatalf("the valid app password was not throttled after %d wrong ones: %q", pop3MaxAuthFailures, got)
	}
}
