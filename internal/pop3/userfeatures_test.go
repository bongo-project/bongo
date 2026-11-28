package pop3

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func setDisabledFeatures(t *testing.T, ps *store.PebbleStore, features ...string) {
	t.Helper()
	data, err := ps.Get([]byte("/user/uid1/profile"))
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	p.DisabledFeatures = features
	if data, err = proto.Marshal(&p); err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/uid1/profile"), data); err != nil {
		t.Fatal(err)
	}
}

func featureEnv(t *testing.T) (*store.PebbleStore, *auth.LocalAuth, *store.MailboxStore) {
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
	return ps, auth.NewLocalAuth(ps, mm), store.NewMailboxStore(ps, nil)
}

// A POP3 login with pop3 switched off gets the reply of a wrong password.
func TestPOP3LoginFeatureDisabledMatchesWrongPassword(t *testing.T) {
	ps, a, mb := featureEnv(t)
	setDisabledFeatures(t, ps, store.FeaturePOP3)
	disabled := passAttempt(t, a, mb, nil, "u@example.com", "pw")
	wrong := passAttempt(t, a, mb, nil, "u@example.com", "nope")
	if disabled != wrong || disabled != "-ERR authentication failed\r\n" {
		t.Fatalf("disabled = %q, wrong = %q", disabled, wrong)
	}
	// Another protocol being off does not matter.
	setDisabledFeatures(t, ps, store.FeatureIMAP)
	if got := passAttempt(t, a, mb, nil, "u@example.com", "pw"); got == wrong {
		t.Fatalf("login refused with only imap disabled: %q", got)
	}
}

// A live session ends on the next command once pop3 is switched off.
func TestPOP3SessionEndsWhenFeatureDisabled(t *testing.T) {
	ps, a, mb := featureEnv(t)
	serverConn, clientConn := net.Pipe()
	s := newSession(serverConn, a, mb, ps, nil, nil, false, true, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { s.handle(); close(done) }()

	tc := &testPOP3{conn: clientConn, reader: bufio.NewReader(clientConn), t: t}
	tc.expectOK()
	tc.send("USER u@example.com")
	tc.expectOK()
	tc.send("PASS pw")
	tc.expectOK()
	tc.send("STAT")
	tc.expectOK()

	setDisabledFeatures(t, ps, store.FeaturePOP3)

	tc.send("STAT")
	if line := tc.expectErr(); line != "-ERR account no longer available" {
		t.Fatalf("STAT after switch = %q", line)
	}
	<-done
	_, _ = io.ReadAll(clientConn)
}
