package smtp

import (
	"fmt"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/store"
)

// The MX cap stays global; the submission listener accepts up to the ceiling
// so a per-user cap above the global one can be honoured by the session.
func TestNewServerRecipientCaps(t *testing.T) {
	tests := []struct {
		name       string
		global     int
		wantMX     int
		wantSubmit int
	}{
		{"default below ceiling", 100, 100, store.MaxRecipientsCeiling},
		{"global above ceiling", 5000, 5000, 5000},
		{"unset falls back to 100", 0, 100, store.MaxRecipientsCeiling},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.SMTPConfig{Port: 2525, SubmissionPort: 2587, Limits: config.SMTPLimits{MaxRecipients: tt.global}}
			backend := NewBackend(nil, nil, testLogger())
			srv := NewServer(cfg, "", backend, true, testLogger())
			if srv.mx.MaxRecipients != tt.wantMX {
				t.Errorf("mx.MaxRecipients = %d, want %d", srv.mx.MaxRecipients, tt.wantMX)
			}
			if srv.submission.MaxRecipients != tt.wantSubmit {
				t.Errorf("submission.MaxRecipients = %d, want %d", srv.submission.MaxRecipients, tt.wantSubmit)
			}
			if backend.maxRecipients != tt.wantMX {
				t.Errorf("backend.maxRecipients = %d, want %d", backend.maxRecipients, tt.wantMX)
			}
		})
	}
}

// A limiter installed with SetMessageLimiter survives NewServer; without one
// NewServer builds a default from the configured global.
func TestNewServerKeepsInjectedMessageLimiter(t *testing.T) {
	cfg := &config.SMTPConfig{Port: 2525, SubmissionPort: 2587, Limits: config.SMTPLimits{OutboundMessagesPerMinute: 7}}

	injected := mail.NewRateLimiterWindow(3, time.Minute)
	b := NewBackend(nil, nil, testLogger())
	b.SetMessageLimiter(injected)
	NewServer(cfg, "", b, true, testLogger())
	if b.MessageLimiter() != injected {
		t.Fatal("NewServer replaced the injected message limiter")
	}

	b2 := NewBackend(nil, nil, testLogger())
	NewServer(cfg, "", b2, true, testLogger())
	if b2.MessageLimiter() == nil || b2.MessageLimiter().Limit() != 7 {
		t.Fatalf("default limiter = %v, want one with limit 7", b2.MessageLimiter())
	}
}

// smtp.limits.max_recipients of 0 or below has always meant the default of
// 100, never unlimited. The submission listener now accepts up to the ceiling,
// so the session must still stop a user with no own cap at 100, as before.
func TestSubmissionUnsetGlobalCapStaysAtDefault(t *testing.T) {
	for _, global := range []int{0, -5} {
		b, _, _ := testBackend("alice@example.com")
		b.connLimit = netx.NewPerIPLimiter(0)
		cfg := &config.SMTPConfig{Port: 2525, SubmissionPort: 2587, Limits: config.SMTPLimits{MaxRecipients: global}}
		NewServer(cfg, "", b, true, testLogger())

		s := limitedSession(b, nil)
		if err := s.Mail("alice@example.com", nil); err != nil {
			t.Fatal(err)
		}
		for i := range 100 {
			if err := s.Rcpt(fmt.Sprintf("r%d@example.net", i), nil); err != nil {
				t.Fatalf("global %d: rcpt %d should pass: %v", global, i+1, err)
			}
		}
		if code := smtpCode(s.Rcpt("over@example.net", nil)); code != 452 {
			t.Fatalf("global %d: rcpt 101 code = %d, want 452", global, code)
		}
	}
}
