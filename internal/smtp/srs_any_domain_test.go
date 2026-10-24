package smtp

import (
	"io"
	"log/slog"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/srs"
)

// TestRcptSRSAnyDomainNoResolver is the agent-role matrix: no resolver and no
// hosted-domain list, so the HMAC check alone decides every SRS-shaped
// recipient, whatever its domain.
func TestRcptSRSAnyDomainNoResolver(t *testing.T) {
	now := time.Unix(20000*86400+3600, 0).UTC()
	c := srs.NewWithClock([]byte("0123456789abcdef0123456789abcdef"), 17, func() time.Time { return now })
	valid, err := c.Forward("bob@example.org", "anywhere.example")
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.Forward("bob@example.org", "first.example")
	if err != nil {
		t.Fatal(err)
	}
	valid1, err := c.Forward(first, "second.example")
	if err != nil {
		t.Fatal(err)
	}

	b := &Backend{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	b.SetSRSAnyDomain(c)
	if b.HostedDomainConfigured() {
		t.Fatal("SetSRSAnyDomain must not configure a hosted-domain function")
	}

	tests := []struct {
		name string
		from string
		to   string
		want int // 0 = accepted
	}{
		{"valid SRS0 null sender", "", valid, 0},
		{"valid SRS1 null sender", "", valid1, 0},
		{"valid SRS non-null sender", "mallory@evil.example", valid, 550},
		{"wrong hash null sender", "", flipHash(valid), 550},
		{"wrong hash non-null sender", "x@y.example", flipHash(valid), 550},
		{"wrong hash SRS1", "", flipHash(valid1), 550},
		{"ordinary recipient", "x@y.example", "anyone@anywhere.example", 0},
		{"no-relay shaped address stays ordinary", "x@y.example", srs.NoRelayLocal + "@anywhere.example", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{backend: b, logger: b.logger, from: tt.from}
			err := s.Rcpt(tt.to, nil)
			if tt.want == 0 {
				if err != nil || len(s.localRcpts) != 1 {
					t.Fatalf("Rcpt = %v, local=%v; want accepted", err, s.localRcpts)
				}
				return
			}
			if code := smtpCode(err); code != tt.want {
				t.Fatalf("Rcpt code = %d (%v), want %d", code, err, tt.want)
			}
			se, ok := err.(*gosmtp.SMTPError)
			if !ok || se.EnhancedCode != (gosmtp.EnhancedCode{5, 1, 1}) || se.Message != "Mailbox unavailable" {
				t.Errorf("refusal %#v, want the server role's 550 5.1.1 Mailbox unavailable", err)
			}
			if len(s.localRcpts) != 0 {
				t.Fatalf("refused recipient was queued: %v", s.localRcpts)
			}
		})
	}
}
