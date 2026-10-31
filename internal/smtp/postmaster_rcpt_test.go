package smtp

import (
	"fmt"
	"io"
	"log/slog"
	"testing"

	gosmtp "github.com/emersion/go-smtp"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

func smtpEnhanced(err error) string {
	if se, ok := err.(*gosmtp.SMTPError); ok {
		return fmt.Sprintf("%d.%d.%d", se.EnhancedCode[0], se.EnhancedCode[1], se.EnhancedCode[2])
	}
	return ""
}

// postmaster@ and abuse@ are accepted on a hosted domain with a live owner,
// whatever the casing. With no target they get 550 5.1.1 on every session,
// authenticated or not: not 5.7.1 "Relaying denied", and never relayed out.
func TestRcptPostmasterAndAbuse(t *testing.T) {
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

	data, err := proto.Marshal(&bongov1.UserProfile{Email: "alice@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"/user/alice/profile":              string(data),
		"/system/domain/example.com":       "enabled",
		"/system/domain/example.com/owner": "alice",
		"/system/domain/ownerless.example": "enabled",
	} {
		if err := ps.Set([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}

	b := &Backend{
		resolver: store.NewRecipientResolver(nil, ps, mm.System),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	for _, authed := range []bool{false, true} {
		for _, to := range []string{"<Postmaster@example.com>", "ABUSE@Example.COM"} {
			s := &Session{backend: b, logger: b.logger, authed: authed}
			if err := s.Rcpt(to, nil); err != nil || len(s.localRcpts) != 1 || len(s.externalRcpts) != 0 {
				t.Errorf("authed=%v RCPT %s = %v, local=%v ext=%v; want accepted locally", authed, to, err, s.localRcpts, s.externalRcpts)
			}
		}
		for _, to := range []string{"postmaster@ownerless.example", "abuse@ownerless.example"} {
			s := &Session{backend: b, logger: b.logger, authed: authed}
			err := s.Rcpt(to, nil)
			if smtpCode(err) != 550 || smtpEnhanced(err) != "5.1.1" {
				t.Errorf("authed=%v RCPT %s = %v, want 550 5.1.1", authed, to, err)
			}
			if len(s.externalRcpts) != 0 || len(s.localRcpts) != 0 {
				t.Errorf("authed=%v RCPT %s was queued", authed, to)
			}
		}
		// Not hosted here: relay rules are unchanged.
		s := &Session{backend: b, logger: b.logger, authed: authed}
		err := s.Rcpt("postmaster@elsewhere.example", nil)
		if authed && (err != nil || len(s.externalRcpts) != 1) {
			t.Errorf("authed relay of postmaster@elsewhere = %v", err)
		}
		if !authed && smtpEnhanced(err) != "5.7.1" {
			t.Errorf("unauthenticated postmaster@elsewhere = %v, want 5.7.1", err)
		}
	}
}
