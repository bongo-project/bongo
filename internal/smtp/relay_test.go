package smtp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-go/bongo-go/internal/config"
)

// mockRelayBackend captures delivered messages for test assertions.
type mockRelayBackend struct {
	messages     chan mockRelayMessage
	rejectTo     string // reject this recipient with 550
	tempFail     bool   // return 421 for all
	requireAuth  bool   // require authentication
	authUser     string // expected username
	authPass     string // expected password
	lastAuthUser string // captured from last session
	lastAuthPass string // captured from last session
}

type mockRelayMessage struct {
	From       string
	Recipients []string
	Data       []byte
}

func (b *mockRelayBackend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	return &mockRelaySession{backend: b}, nil
}

type mockRelaySession struct {
	backend    *mockRelayBackend
	from       string
	recipients []string
}

func (s *mockRelaySession) AuthMechanisms() []string {
	return []string{"PLAIN"}
}

func (s *mockRelaySession) Auth(mech string) (sasl.Server, error) {
	if mech != "PLAIN" {
		return nil, fmt.Errorf("unsupported mechanism: %s", mech)
	}
	return sasl.NewPlainServer(func(identity, username, password string) error {
		s.backend.lastAuthUser = username
		s.backend.lastAuthPass = password
		if s.backend.requireAuth {
			if username != s.backend.authUser || password != s.backend.authPass {
				return fmt.Errorf("authentication failed")
			}
		}
		return nil
	}), nil
}

func (s *mockRelaySession) Mail(from string, opts *gosmtp.MailOptions) error {
	if s.backend.tempFail {
		return &gosmtp.SMTPError{Code: 421, Message: "try again later"}
	}
	s.from = from
	return nil
}

func (s *mockRelaySession) Rcpt(to string, opts *gosmtp.RcptOptions) error {
	if s.backend.rejectTo != "" && strings.EqualFold(to, s.backend.rejectTo) {
		return &gosmtp.SMTPError{Code: 550, Message: "user unknown"}
	}
	s.recipients = append(s.recipients, to)
	return nil
}

func (s *mockRelaySession) Data(r io.Reader) error {
	data, _ := io.ReadAll(r)
	s.backend.messages <- mockRelayMessage{
		From:       s.from,
		Recipients: s.recipients,
		Data:       data,
	}
	return nil
}

func (s *mockRelaySession) Reset()        {}
func (s *mockRelaySession) Logout() error { return nil }

func startMockSMTP(t *testing.T, backend *mockRelayBackend) string {
	t.Helper()
	srv := gosmtp.NewServer(backend)
	srv.Addr = "127.0.0.1:0"
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mock smtp listen: %v", err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l.Addr().String()
}

func TestRelaySmartRelay(t *testing.T) {
	backend := &mockRelayBackend{messages: make(chan mockRelayMessage, 10)}
	addr := startMockSMTP(t, backend)

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: test\r\n\r\nHello")
	permFails, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(permFails) != 0 {
		t.Errorf("unexpected permanent failures: %v", permFails)
	}

	msg := <-backend.messages
	if msg.From != "alice@example.com" {
		t.Errorf("from = %q, want alice@example.com", msg.From)
	}
	if len(msg.Recipients) != 1 || msg.Recipients[0] != "bob@external.com" {
		t.Errorf("recipients = %v, want [bob@external.com]", msg.Recipients)
	}
}

func TestRelayPermanentReject(t *testing.T) {
	backend := &mockRelayBackend{
		messages: make(chan mockRelayMessage, 10),
		rejectTo: "bad@external.com",
	}
	addr := startMockSMTP(t, backend)

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bad@external.com\r\nSubject: test\r\n\r\nHello")
	permFails, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bad@external.com"}, rawMsg)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(permFails) != 1 || permFails[0] != "bad@external.com" {
		t.Errorf("permanentFails = %v, want [bad@external.com]", permFails)
	}
}

func TestRelayTempFail(t *testing.T) {
	backend := &mockRelayBackend{
		messages: make(chan mockRelayMessage, 10),
		tempFail: true,
	}
	addr := startMockSMTP(t, backend)

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: test\r\n\r\nHello")
	_, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err == nil {
		t.Error("expected temp error, got nil")
	}
}

func TestRelayAuthSuccess(t *testing.T) {
	backend := &mockRelayBackend{
		messages:    make(chan mockRelayMessage, 10),
		requireAuth: true,
		authUser:    "apikey",
		authPass:    "secret123",
	}
	addr := startMockSMTP(t, backend)

	// The mock server does not advertise STARTTLS, so AllowInsecureAuth must
	// be set to permit credentials over the plain test connection.
	relay := NewRelay(&config.SMTPRelay{
		Enabled:           true,
		Host:              addr,
		Username:          "apikey",
		Password:          "secret123",
		AllowInsecureAuth: true,
	}, slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: auth test\r\n\r\nHello")
	permFails, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(permFails) != 0 {
		t.Errorf("unexpected permanent failures: %v", permFails)
	}

	msg := <-backend.messages
	if msg.From != "alice@example.com" {
		t.Errorf("from = %q, want alice@example.com", msg.From)
	}

	// Verify credentials were sent.
	if backend.lastAuthUser != "apikey" {
		t.Errorf("auth username = %q, want %q", backend.lastAuthUser, "apikey")
	}
	if backend.lastAuthPass != "secret123" {
		t.Errorf("auth password = %q, want %q", backend.lastAuthPass, "secret123")
	}
}

func TestRelayAuthFailure(t *testing.T) {
	backend := &mockRelayBackend{
		messages:    make(chan mockRelayMessage, 10),
		requireAuth: true,
		authUser:    "correct-user",
		authPass:    "correct-pass",
	}
	addr := startMockSMTP(t, backend)

	// AllowInsecureAuth must be true so the guard passes and the server can
	// reject the wrong credentials (the mock has no STARTTLS).
	relay := NewRelay(&config.SMTPRelay{
		Enabled:           true,
		Host:              addr,
		Username:          "wrong-user",
		Password:          "wrong-pass",
		AllowInsecureAuth: true,
	}, slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: auth fail\r\n\r\nHello")
	_, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	if !strings.Contains(err.Error(), "AUTH") {
		t.Errorf("error = %q, expected to contain 'AUTH'", err.Error())
	}
}

// TestRelayAuthRefusedOverPlaintext verifies that credentials are never sent
// when STARTTLS did not complete and AllowInsecureAuth is false (the default).
func TestRelayAuthRefusedOverPlaintext(t *testing.T) {
	backend := &mockRelayBackend{
		messages:    make(chan mockRelayMessage, 10),
		requireAuth: false, // server does not check auth
	}
	addr := startMockSMTP(t, backend)

	// No AllowInsecureAuth -- the guard must refuse.
	relay := NewRelay(&config.SMTPRelay{
		Enabled:  true,
		Host:     addr,
		Username: "apikey",
		Password: "secret123",
		// AllowInsecureAuth defaults to false
	}, slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: guard test\r\n\r\nHello")
	_, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err == nil {
		t.Fatal("expected guard error, got nil")
	}
	if !strings.Contains(err.Error(), "unencrypted") {
		t.Errorf("error = %q, expected to mention 'unencrypted'", err.Error())
	}
	// Credentials must not have reached the server.
	if backend.lastAuthUser != "" || backend.lastAuthPass != "" {
		t.Errorf("credentials were transmitted despite guard: user=%q pass=%q",
			backend.lastAuthUser, backend.lastAuthPass)
	}
}

func TestRelayNoAuthWhenNotConfigured(t *testing.T) {
	backend := &mockRelayBackend{
		messages: make(chan mockRelayMessage, 10),
	}
	addr := startMockSMTP(t, backend)

	// No username/password set -- auth should be skipped.
	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: no auth\r\n\r\nHello")
	permFails, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(permFails) != 0 {
		t.Errorf("unexpected permanent failures: %v", permFails)
	}

	<-backend.messages

	// Auth should not have been attempted -- lastAuthUser stays empty.
	if backend.lastAuthUser != "" {
		t.Errorf("auth was attempted (user=%q) when no credentials configured", backend.lastAuthUser)
	}
}
