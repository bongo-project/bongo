package smtp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
)

// runSASL drives a sasl.Server to completion using a fixed username/password,
// answering each challenge the way a real client would.
func runSASL(t *testing.T, srv sasl.Server, mech, username, password string) error {
	t.Helper()

	var response []byte
	if mech == sasl.Plain {
		// PLAIN sends everything as the initial response.
		response = []byte("\x00" + username + "\x00" + password)
	}

	for range 5 {
		challenge, done, err := srv.Next(response)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		switch string(challenge) {
		case "Username:":
			response = []byte(username)
		case "Password:":
			response = []byte(password)
		default:
			t.Fatalf("unexpected challenge %q", challenge)
		}
	}
	t.Fatal("SASL exchange did not complete")
	return nil
}

func TestAuthMechanismsOnlyOnSubmission(t *testing.T) {
	b := throttleTestBackend(nil)

	sub := &Session{backend: b, logger: b.logger, submission: true}
	if mechs := sub.AuthMechanisms(); len(mechs) != 2 || mechs[0] != sasl.Plain || mechs[1] != sasl.Login {
		t.Errorf("submission AuthMechanisms() = %v, want [PLAIN LOGIN]", mechs)
	}

	mx := &Session{backend: b, logger: b.logger, submission: false}
	if mechs := mx.AuthMechanisms(); len(mechs) != 0 {
		t.Errorf("MX AuthMechanisms() = %v, want none", mechs)
	}
}

func TestSessionImplementsAuthSession(t *testing.T) {
	var _ gosmtp.AuthSession = &Session{}
}

func TestAuthPlainMechanismAuthenticates(t *testing.T) {
	b := throttleTestBackend(nil) // Authenticate succeeds
	b.auth = &fakeAuth{userID: "user-1"}
	s := newSessionForIP(b, "203.0.113.5")

	srv, err := s.Auth(sasl.Plain)
	if err != nil {
		t.Fatalf("Auth(PLAIN): %v", err)
	}
	if err := runSASL(t, srv, sasl.Plain, "login@example.com", "secret"); err != nil {
		t.Fatalf("PLAIN exchange failed: %v", err)
	}
	if !s.authed || s.userID != "user-1" {
		t.Errorf("session not authenticated after PLAIN (authed=%v userID=%q)", s.authed, s.userID)
	}
}

func TestAuthLoginMechanismAuthenticates(t *testing.T) {
	b := throttleTestBackend(nil)
	b.auth = &fakeAuth{userID: "user-1"}
	s := newSessionForIP(b, "203.0.113.6")

	srv, err := s.Auth(sasl.Login)
	if err != nil {
		t.Fatalf("Auth(LOGIN): %v", err)
	}
	if err := runSASL(t, srv, sasl.Login, "login@example.com", "secret"); err != nil {
		t.Fatalf("LOGIN exchange failed: %v", err)
	}
	if !s.authed || s.userID != "user-1" {
		t.Errorf("session not authenticated after LOGIN (authed=%v userID=%q)", s.authed, s.userID)
	}
}

func TestAuthLoginRejectsBadCredentials(t *testing.T) {
	b := throttleTestBackend(errors.New("bad credentials"))
	s := newSessionForIP(b, "203.0.113.7")

	srv, _ := s.Auth(sasl.Login)
	if err := runSASL(t, srv, sasl.Login, "login@example.com", "wrong"); err == nil {
		t.Fatal("LOGIN with bad credentials should fail")
	}
	if s.authed {
		t.Error("session should not be authenticated after failed LOGIN")
	}
}

func TestAuthUnsupportedMechanism(t *testing.T) {
	b := throttleTestBackend(nil)
	s := newSessionForIP(b, "203.0.113.8")
	if _, err := s.Auth("CRAM-MD5"); err != gosmtp.ErrAuthUnsupported {
		t.Errorf("Auth(CRAM-MD5) err = %v, want ErrAuthUnsupported", err)
	}
}

func TestAuthRefusedOnMXSession(t *testing.T) {
	b := throttleTestBackend(nil)
	mx := &Session{backend: b, logger: b.logger, submission: false}
	if _, err := mx.Auth(sasl.Plain); err != gosmtp.ErrAuthUnsupported {
		t.Errorf("Auth on MX session err = %v, want ErrAuthUnsupported", err)
	}
}

// AUTH failures reach the client verbatim through go-smtp, so a backend's
// internal error text (here a secret-looking string) must never be relayed.
func TestAuthFailureDoesNotLeakBackendError(t *testing.T) {
	const secret = "parsing profile: proto: cannot parse invalid wire-format data hunter2-hash"
	tests := []struct {
		name     string
		authErr  error
		wantCode int
		wantEnh  gosmtp.EnhancedCode
	}{
		{"bad credentials", auth.ErrBadCredentials, 535, gosmtp.EnhancedCode{5, 7, 8}},
		{"wrapped bad credentials", fmt.Errorf("lookup: %w", auth.ErrBadCredentials), 535, gosmtp.EnhancedCode{5, 7, 8}},
		{"internal error", errors.New(secret), 454, gosmtp.EnhancedCode{4, 7, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := throttleTestBackend(tt.authErr)
			s := newSessionForIP(b, "203.0.113.20")

			err := s.AuthPlain("bob@example.com", "pw")
			se, ok := err.(*gosmtp.SMTPError)
			if !ok {
				t.Fatalf("AuthPlain err = %T (%v), want *gosmtp.SMTPError", err, err)
			}
			if se.Code != tt.wantCode || se.EnhancedCode != tt.wantEnh {
				t.Errorf("got %d %v, want %d %v", se.Code, se.EnhancedCode, tt.wantCode, tt.wantEnh)
			}
			for _, leak := range []string{"hunter2", "parsing profile", "proto", "lookup"} {
				if strings.Contains(se.Error(), leak) {
					t.Errorf("client-visible error %q leaks %q", se.Error(), leak)
				}
			}
			if s.authed {
				t.Error("session must not be authenticated")
			}
		})
	}
}
