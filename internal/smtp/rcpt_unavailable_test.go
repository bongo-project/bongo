package smtp

import (
	"io"
	"log/slog"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// A disabled or deleted local recipient, or any recipient on a disabled
// domain, must be refused with a permanent 550 on
// every session. On an authenticated one the resolver error would otherwise
// fall into the relay branch and the mail would leave for our own MX.
func TestRcptRefusesUnavailableLocalRecipient(t *testing.T) {
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

	put := func(id, email string, disabled bool) {
		t.Helper()
		data, err := proto.Marshal(&bongov1.UserProfile{Email: email, Disabled: disabled})
		if err != nil {
			t.Fatal(err)
		}
		if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
			t.Fatal(err)
		}
		if _, err := mm.System.DB().Exec(
			"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, 'U', 1, 0)", id, email); err != nil {
			t.Fatal(err)
		}
	}
	put("live", "live@example.com", false)
	put("off", "off@example.com", true)
	// A live account on a domain an administrator has disabled.
	put("ondisabled", "user@disabled.example", false)
	if err := ps.Set([]byte("/system/domain/disabled.example"), []byte("disabled")); err != nil {
		t.Fatal(err)
	}
	// Deleted: users row survives on this node but the profile is gone.
	if _, err := mm.System.DB().Exec(
		"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES ('gone', 'gone@example.com', 'U', 1, 0)"); err != nil {
		t.Fatal(err)
	}

	b := &Backend{
		resolver: store.NewRecipientResolver(nil, ps, mm.System),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	for _, authed := range []bool{false, true} {
		for _, to := range []string{"off@example.com", "gone@example.com", "user@disabled.example"} {
			s := &Session{backend: b, logger: b.logger, authed: authed}
			if code := smtpCode(s.Rcpt(to, nil)); code != 550 {
				t.Errorf("authed=%v RCPT %s = %d, want 550", authed, to, code)
			}
			if len(s.externalRcpts) != 0 || len(s.localRcpts) != 0 {
				t.Errorf("authed=%v RCPT %s was queued (ext=%v local=%v)", authed, to, s.externalRcpts, s.localRcpts)
			}
		}
		// Positive control: a live local recipient is accepted.
		s := &Session{backend: b, logger: b.logger, authed: authed}
		if err := s.Rcpt("live@example.com", nil); err != nil || len(s.localRcpts) != 1 {
			t.Errorf("authed=%v live RCPT = %v, local=%v", authed, err, s.localRcpts)
		}
	}
}
