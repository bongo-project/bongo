package smtp

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/store"
)

// unavailableStub resolves nothing: every address belongs to a disabled
// account or a disabled domain.
type unavailableStub struct{}

func (unavailableStub) ResolveRecipient(email string) (string, error) {
	return "", fmt.Errorf("resolve recipient: %s: %w", email, store.ErrRecipientUnavailable)
}

// An unavailable local recipient (disabled account or disabled domain) is a
// permanent 550 5.1.1, and an authenticated session must not relay it out to
// our own MX instead.
func TestRcptUnavailableRecipientRefused(t *testing.T) {
	b := &Backend{
		resolver: unavailableStub{},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, authed := range []bool{false, true} {
		t.Run(fmt.Sprintf("authed=%v", authed), func(t *testing.T) {
			s := &Session{backend: b, logger: b.logger, authed: authed, from: "a@example.org"}
			err := s.Rcpt("user@off.example", nil)
			var smtpErr *gosmtp.SMTPError
			if !errors.As(err, &smtpErr) || smtpErr.Code != 550 || smtpErr.EnhancedCode != (gosmtp.EnhancedCode{5, 1, 1}) {
				t.Fatalf("Rcpt err = %v, want 550 5.1.1", err)
			}
			if len(s.localRcpts) != 0 || len(s.externalRcpts) != 0 {
				t.Fatalf("recipient queued: local=%v external=%v", s.localRcpts, s.externalRcpts)
			}
		})
	}
}
