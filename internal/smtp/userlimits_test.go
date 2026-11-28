package smtp

import (
	"strings"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func limitedSession(b *Backend, limits *bongov1.SendLimits) *Session {
	return &Session{
		backend: b, logger: b.logger, submission: true, authed: true,
		userID: "uid-alice@example.com", limits: limits,
	}
}

func sendExternal(t *testing.T, b *Backend, limits *bongov1.SendLimits) error {
	t.Helper()
	s := limitedSession(b, limits)
	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := s.Rcpt("outsider@example.net", nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
	return s.Data(strings.NewReader(authedMsg))
}

// A per-user messages-per-minute value replaces the global one, below or above
// it, and zero means the global value.
func TestPerUserMessagesPerMinute(t *testing.T) {
	tests := []struct {
		name    string
		global  int
		perUser uint32
		allowed int
	}{
		{"global only", 2, 0, 2},
		{"per user below global", 3, 1, 1},
		{"per user above global", 2, 4, 4},
		{"per user counts when global is off", 0, 2, 2},
		{"global off and no override", 0, 0, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _, _ := testBackend("alice@example.com")
			b.outboundRL = mail.NewRateLimiterWindow(tt.global, time.Minute)
			limits := &bongov1.SendLimits{MessagesPerMinute: tt.perUser}
			for i := 1; i <= tt.allowed; i++ {
				if err := sendExternal(t, b, limits); err != nil {
					t.Fatalf("message %d should pass: %v", i, err)
				}
			}
			if tt.allowed < 6 {
				err := sendExternal(t, b, limits)
				if code := smtpCode(err); code != 452 {
					t.Fatalf("message %d: err=%v code=%d, want 452", tt.allowed+1, err, code)
				}
			}
		})
	}
}

// The same limiter instance backs another path: units a web send recorded
// count against SMTP.
func TestMessageLimiterIsShared(t *testing.T) {
	b, _, _ := testBackend("alice@example.com")
	rl := mail.NewRateLimiterWindow(2, time.Minute)
	b.SetMessageLimiter(rl)
	if b.MessageLimiter() != rl {
		t.Fatal("SetMessageLimiter did not install the instance")
	}
	if !rl.AllowN("uid-alice@example.com", 2) {
		t.Fatal("setup")
	}
	if code := smtpCode(sendExternal(t, b, nil)); code != 452 {
		t.Fatalf("code = %d, want 452: units spent elsewhere must count", code)
	}
}

// The recipients-per-hour budget uses the per-user value in the RCPT peek and
// at DATA.
func TestPerUserRecipientsPerHour(t *testing.T) {
	b, _, _ := testBackend("alice@example.com")
	b.recipientRL = mail.NewRateLimiter(10)

	// Below global: two recipients only.
	s := limitedSession(b, &bongov1.SendLimits{RecipientsPerHour: 2})
	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatal(err)
	}
	for i, r := range []string{"a@example.net", "b@example.net"} {
		if err := s.Rcpt(r, nil); err != nil {
			t.Fatalf("rcpt %d: %v", i, err)
		}
	}
	if code := smtpCode(s.Rcpt("c@example.net", nil)); code != 452 {
		t.Fatalf("third recipient code = %d, want 452", code)
	}
	if err := s.Data(strings.NewReader(authedMsg)); err != nil {
		t.Fatalf("data: %v", err)
	}
	if got := b.recipientRL.RemainingLimit("uid-alice@example.com", 2); got != 0 {
		t.Errorf("remaining = %d, want 0", got)
	}

	// Above global: twelve recipients pass the 10-per-hour global budget.
	b2, _, _ := testBackend("alice@example.com")
	b2.recipientRL = mail.NewRateLimiter(10)
	s2 := limitedSession(b2, &bongov1.SendLimits{RecipientsPerHour: 20})
	if err := s2.Mail("alice@example.com", nil); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		if err := s2.Rcpt("r"+string(rune('a'+i))+"@example.net", nil); err != nil {
			t.Fatalf("rcpt %d: %v", i, err)
		}
	}
	if err := s2.Data(strings.NewReader(authedMsg)); err != nil {
		t.Fatalf("data above global: %v", err)
	}
}

// The per-message recipient cap: a per-user value above the global one is
// accepted, below it refused, with 452 4.5.3, and it restarts with each
// transaction.
func TestPerUserMaxRecipients(t *testing.T) {
	tests := []struct {
		name    string
		global  int
		perUser uint32
		allowed int
	}{
		{"global only", 3, 0, 3},
		{"above global", 3, 5, 5},
		{"below global", 3, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _, _ := testBackend("alice@example.com")
			b.maxRecipients = tt.global
			s := limitedSession(b, &bongov1.SendLimits{MaxRecipients: tt.perUser})
			if err := s.Mail("alice@example.com", nil); err != nil {
				t.Fatal(err)
			}
			for i := range tt.allowed {
				if err := s.Rcpt("r"+string(rune('a'+i))+"@example.net", nil); err != nil {
					t.Fatalf("rcpt %d should pass: %v", i+1, err)
				}
			}
			err := s.Rcpt("over@example.net", nil)
			if code := smtpCode(err); code != 452 {
				t.Fatalf("code = %d (%v), want 452", code, err)
			}
			if se, ok := err.(*gosmtp.SMTPError); !ok || se.EnhancedCode != (gosmtp.EnhancedCode{4, 5, 3}) {
				t.Errorf("error %#v lacks enhanced code 4.5.3", err)
			}
			// A new transaction starts the count again.
			s.Reset()
			if err := s.Mail("alice@example.com", nil); err != nil {
				t.Fatal(err)
			}
			if err := s.Rcpt("again@example.net", nil); err != nil {
				t.Fatalf("first recipient after reset: %v", err)
			}
		})
	}
}

// An unauthenticated or MX session never gets a per-user cap.
func TestMaxRecipientsGlobalForOthers(t *testing.T) {
	b, _, _ := testBackend("alice@example.com", "bob@example.com")
	b.maxRecipients = 2
	s := &Session{backend: b, logger: b.logger, limits: &bongov1.SendLimits{MaxRecipients: 9}}
	if got := s.effectiveMaxRecipients(); got != 9 {
		t.Fatalf("session helper uses limits only when set on the session: %d", got)
	}
	s.limits = nil
	if got := s.effectiveMaxRecipients(); got != 2 {
		t.Fatalf("no override = %d, want global 2", got)
	}
}
