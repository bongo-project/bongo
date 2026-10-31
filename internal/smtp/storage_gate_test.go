package smtp

import (
	"errors"
	"testing"

	gosmtp "github.com/emersion/go-smtp"
)

type fakeGate struct{ low bool }

func (g *fakeGate) Low() bool { return g.low }

func TestMailRefusedWhenStorageLow(t *testing.T) {
	tests := []struct {
		name       string
		submission bool
		authed     bool
		low        bool
		from       string
		wantCode   int
	}{
		{"mx low", false, false, true, "a@remote.example", 452},
		{"mx ok", false, false, false, "a@remote.example", 0},
		{"submission authed low", true, true, true, "me@example.com", 452},
		{"submission authed ok", true, true, false, "me@example.com", 0},
		{"submission unauthenticated low keeps 530", true, false, true, "me@example.com", 530},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _, _ := testBackend("staff@example.com")
			b.SetStorageGate(&fakeGate{low: tt.low})
			s := &Session{backend: b, logger: b.logger, submission: tt.submission, authed: tt.authed}
			if tt.authed {
				s.userID = "uid-me@example.com"
			}
			err := s.Mail(tt.from, nil)
			if got := smtpCode(err); got != tt.wantCode {
				t.Fatalf("Mail() = %v (code %d), want code %d", err, got, tt.wantCode)
			}
		})
	}
}

func TestMailStorageLowEnhancedCode(t *testing.T) {
	b, _, _ := testBackend("staff@example.com")
	b.SetStorageGate(&fakeGate{low: true})
	s := &Session{backend: b, logger: b.logger}
	err := s.Mail("a@remote.example", nil)
	var se *gosmtp.SMTPError
	if !errors.As(err, &se) {
		t.Fatalf("Mail() = %v, want *SMTPError", err)
	}
	if se.Code != 452 || se.EnhancedCode != (gosmtp.EnhancedCode{4, 3, 1}) || se.Message != "Insufficient system storage" {
		t.Fatalf("Mail() = %+v", se)
	}
}

func TestMailNilGateAccepts(t *testing.T) {
	b, _, _ := testBackend("staff@example.com")
	b.SetStorageGate(nil)
	s := &Session{backend: b, logger: b.logger}
	if err := s.Mail("a@remote.example", nil); err != nil {
		t.Fatalf("Mail() = %v", err)
	}
}
