package smtp

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
)

// domainResolver wraps fakeResolver with the two domain behaviours it cannot
// model: a catch-all domain accepts any local part, a disabled domain answers
// ErrRecipientUnavailable. The real resolver is covered in the store package.
type domainResolver struct {
	fakeResolver
	catchAll string
	disabled string
}

func (r *domainResolver) ResolveRecipient(email string) (string, error) {
	clean := strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(email), ">"), "<"))
	switch {
	case strings.HasSuffix(clean, "@"+r.catchAll):
		return "uid-catchall", nil
	case strings.HasSuffix(clean, "@"+r.disabled):
		return "", store.ErrRecipientUnavailable
	}
	return r.fakeResolver.ResolveRecipient(email)
}

func TestRcptUnknownLocalPartOnHostedDomain(t *testing.T) {
	hosted := map[string]bool{
		"hosted.example": true, "alias.example": true,
		"catchall.example": true, "disabled.example": true,
	}
	refused := "5.1.1"
	accepted := ""
	tests := []struct {
		name   string
		authed bool
		to     string
		want   string // empty = accepted
		ext    bool   // accepted as external relay
	}{
		{"unauthenticated unknown", false, "unknown@hosted.example", refused, false},
		{"authenticated unknown", true, "unknown@hosted.example", refused, false},
		{"mixed case and brackets", true, "<Unknown@HOSTED.example>", refused, false},
		{"alias domain unknown", true, "unknown@alias.example", refused, false},
		{"disabled domain", true, "anyone@disabled.example", refused, false},
		{"catch-all accepted", true, "anyone@catchall.example", accepted, false},
		{"known local accepted", true, "known@hosted.example", accepted, false},
		{"non-hosted authenticated relays", true, "friend@external.example", accepted, true},
		{"non-hosted unauthenticated denied", false, "friend@external.example", "5.7.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _, relay := testBackend("known@hosted.example", "alice@example.com")
			b.resolver = &domainResolver{
				fakeResolver: *b.resolver.(*fakeResolver),
				catchAll:     "catchall.example", disabled: "disabled.example",
			}
			b.SetHostedDomain(func(d string) bool { return hosted[d] })
			b.recipientRL = mail.NewRateLimiter(7)
			s := &Session{backend: b, logger: b.logger, submission: tt.authed, authed: tt.authed}
			if tt.authed {
				s.userID = "uid-alice@example.com"
				if err := s.Mail("alice@example.com", nil); err != nil {
					t.Fatalf("MAIL: %v", err)
				}
			}

			err := s.Rcpt(tt.to, nil)
			if tt.want == accepted {
				if err != nil {
					t.Fatalf("Rcpt = %v, want accepted", err)
				}
				if tt.ext != (len(s.externalRcpts) == 1) {
					t.Fatalf("external=%v, want external=%v", s.externalRcpts, tt.ext)
				}
				return
			}
			if smtpCode(err) != 550 || smtpEnhanced(err) != tt.want {
				t.Fatalf("Rcpt = %v, want 550 %v", err, tt.want)
			}
			if len(s.localRcpts) != 0 || len(s.externalRcpts) != 0 {
				t.Fatalf("refused recipient queued: local=%v ext=%v", s.localRcpts, s.externalRcpts)
			}
			if tt.authed {
				if err := s.Rcpt("alice@example.com", nil); err != nil {
					t.Fatalf("valid local rcpt: %v", err)
				}
				if err := s.Data(strings.NewReader(authedMsg)); err != nil {
					t.Fatalf("Data: %v", err)
				}
				if got := b.recipientRL.Remaining(s.userID); got != 7 {
					t.Errorf("budget remaining = %d, want 7 (refusal must not spend budget)", got)
				}
			}
			if len(relay.enqueued) != 0 {
				t.Errorf("relay enqueued after refusal: %d", len(relay.enqueued))
			}
		})
	}
}

// An exhausted budget must not turn the permanent refusal into a 452: the
// hosted-domain check runs before the budget peek.
func TestRcptHostedRefusalBeforeBudget(t *testing.T) {
	b, _, _ := testBackend("alice@example.com")
	b.SetHostedDomain(func(d string) bool { return d == "hosted.example" })
	b.recipientRL = mail.NewRateLimiter(1)
	if !b.recipientRL.AllowN("uid-alice@example.com", 1) {
		t.Fatal("could not exhaust budget")
	}
	s := &Session{backend: b, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		submission: true, authed: true, userID: "uid-alice@example.com"}
	err := s.Rcpt("unknown@hosted.example", nil)
	if smtpCode(err) != 550 || smtpEnhanced(err) != "5.1.1" {
		t.Fatalf("Rcpt = %v, want 550 5.1.1", err)
	}
	// Positive control: the budget really is exhausted for a relay.
	if err := s.Rcpt("friend@external.example", nil); smtpCode(err) != 452 {
		t.Fatalf("control Rcpt = %v, want 452", err)
	}
}
