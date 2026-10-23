package smtp

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

func revokeBackend(t *testing.T) (*Backend, *store.PebbleStore) {
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
	// SQLite says enabled = 1 throughout; only the Pebble profile changes.
	if _, err := mm.System.DB().Exec(
		"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES ('uid1', 'u@example.com', 'U', 1, 0)"); err != nil {
		t.Fatal(err)
	}
	return &Backend{
		auth:     auth.NewLocalAuth(ps, mm),
		authFail: authlimit.New(maxAuthFailures, authFailWindow),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, ps
}

func putSMTPProfile(t *testing.T, ps *store.PebbleStore, password string, disabled bool) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	data, err := proto.Marshal(&bongov1.UserProfile{Email: "u@example.com", PasswordHash: hash, Disabled: disabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/uid1/profile"), data); err != nil {
		t.Fatal(err)
	}
}

// disableSMTPProfile flips only Disabled, keeping the password hash (and so the
// session epoch) untouched, so the case exercises the Disabled check alone.
func disableSMTPProfile(t *testing.T, ps *store.PebbleStore) {
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

func TestSMTPAuthRefusedForDisabledProfile(t *testing.T) {
	b, ps := revokeBackend(t)

	putSMTPProfile(t, ps, "pw", false)
	if err := newSessionForIP(b, "203.0.113.9").AuthPlain("u@example.com", "pw"); err != nil {
		t.Fatalf("positive control: AUTH for enabled profile = %v", err)
	}

	putSMTPProfile(t, ps, "pw", true)
	if err := newSessionForIP(b, "203.0.113.9").AuthPlain("u@example.com", "pw"); err == nil {
		t.Fatal("AUTH succeeded for a disabled profile with sqlite enabled=1")
	}
}

func TestSMTPMailRefusedAfterRevocation(t *testing.T) {
	tests := []struct {
		name   string
		revoke func(t *testing.T, ps *store.PebbleStore)
	}{
		{"disabled", func(t *testing.T, ps *store.PebbleStore) { disableSMTPProfile(t, ps) }},
		{"password reset", func(t *testing.T, ps *store.PebbleStore) { putSMTPProfile(t, ps, "new", false) }},
		{"deleted", func(t *testing.T, ps *store.PebbleStore) {
			if err := ps.Delete([]byte("/user/uid1/profile")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, ps := revokeBackend(t)
			putSMTPProfile(t, ps, "pw", false)
			s := newSessionForIP(b, "203.0.113.9")
			if err := s.AuthPlain("u@example.com", "pw"); err != nil {
				t.Fatal(err)
			}
			if err := s.checkAccountLive("MAIL"); err != nil {
				t.Fatalf("positive control: %v", err)
			}
			tt.revoke(t, ps)
			if code := smtpCode(s.Mail("u@example.com", nil)); code != 535 {
				t.Fatalf("MAIL after %s = code %d, want 535", tt.name, code)
			}
		})
	}
}

// revokeDataSession builds an authenticated submission session over real
// account state with a recording deliverer, then runs MAIL and RCPT.
func revokeDataSession(t *testing.T) (*Session, *store.PebbleStore, *fakeSplitDeliverer) {
	t.Helper()
	b, ps := revokeBackend(t)
	putSMTPProfile(t, ps, "pw", false)
	d := &fakeSplitDeliverer{}
	b.deliverer = d
	b.resolver = &fakeResolver{local: map[string]bool{"local@example.com": true}}
	b.relayQueue = &fakeRelayQueue{}
	b.senderPolicy = &fakePolicy{extra: map[string]bool{"u@example.com": true}}
	s := newSessionForIP(b, "203.0.113.9")
	if err := s.AuthPlain("u@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := s.Mail("u@example.com", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := s.Rcpt("local@example.com", nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
	return s, ps, d
}

const revokeMsg = "From: u@example.com\r\nSubject: hi\r\n\r\nbody"

func TestSMTPDataRefusedAfterRevocation(t *testing.T) {
	tests := []struct {
		name   string
		revoke func(t *testing.T, ps *store.PebbleStore)
	}{
		{"disabled", func(t *testing.T, ps *store.PebbleStore) { disableSMTPProfile(t, ps) }},
		{"password reset", func(t *testing.T, ps *store.PebbleStore) { putSMTPProfile(t, ps, "new", false) }},
		{"deleted", func(t *testing.T, ps *store.PebbleStore) {
			if err := ps.Delete([]byte("/user/uid1/profile")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Positive control: the same flow without revocation queues once.
			s, _, d := revokeDataSession(t)
			if err := s.Data(strings.NewReader(revokeMsg)); err != nil {
				t.Fatalf("positive control: Data = %v", err)
			}
			if len(d.localCalls) != 1 {
				t.Fatalf("positive control: %d enqueues, want 1", len(d.localCalls))
			}

			s, ps, d := revokeDataSession(t)
			tt.revoke(t, ps)
			if code := smtpCode(s.Data(strings.NewReader(revokeMsg))); code != 535 {
				t.Fatalf("DATA after %s = code %d, want 535", tt.name, code)
			}
			if len(d.localCalls) != 0 || d.stored != 0 {
				t.Fatalf("revoked DATA queued %d, stored %d blobs; want 0 and 0", len(d.localCalls), d.stored)
			}
		})
	}
}

func TestSMTPRcptRefusedAfterRevocation(t *testing.T) {
	b, ps := revokeBackend(t)
	putSMTPProfile(t, ps, "pw", false)
	b.resolver = &fakeResolver{local: map[string]bool{"local@example.com": true}}
	b.senderPolicy = &fakePolicy{extra: map[string]bool{"u@example.com": true}}
	s := newSessionForIP(b, "203.0.113.9")
	if err := s.AuthPlain("u@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := s.Mail("u@example.com", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("local@example.com", nil); err != nil {
		t.Fatalf("positive control: Rcpt = %v", err)
	}
	disableSMTPProfile(t, ps)
	if code := smtpCode(s.Rcpt("local@example.com", nil)); code != 535 {
		t.Fatalf("RCPT after disable = code %d, want 535", code)
	}
}
