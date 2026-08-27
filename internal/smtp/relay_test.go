package smtp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/config"
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

	// rejectDotlessHELO simulates a strict receiver enforcing RFC 5321
	// section 4.1.1.1: any EHLO/HELO argument without a dot is refused before
	// a session is even created.
	rejectDotlessHELO bool
	// capturedHELO records the EHLO/HELO argument from the connection that
	// produced the most recent session. go-smtp populates Conn.Hostname()
	// from the EHLO/HELO command before calling NewSession, and NewSession
	// fires only on the first EHLO of a connection, so this is by
	// construction the pre-STARTTLS EHLO name.
	//
	// Written on the go-smtp server goroutine and read from the test
	// goroutine, so it is mutex-guarded: tests that assert on it without
	// first receiving from b.messages (e.g. the rejection cases, where no
	// message is ever delivered) have no other happens-before edge.
	mu           sync.Mutex
	capturedHELO string
}

func (b *mockRelayBackend) setCapturedHELO(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.capturedHELO = s
}

// helo returns the EHLO/HELO argument captured from the most recent session.
func (b *mockRelayBackend) helo() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capturedHELO
}

type mockRelayMessage struct {
	From       string
	Recipients []string
	Data       []byte
}

func (b *mockRelayBackend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	// c.Hostname() is populated from the EHLO/HELO command before NewSession
	// is invoked (go-smtp conn.go handleGreet), and NewSession fires only on
	// the first EHLO of the connection, so this is the pre-STARTTLS EHLO name.
	b.setCapturedHELO(c.Hostname())
	if b.rejectDotlessHELO && !strings.Contains(c.Hostname(), ".") {
		return nil, &gosmtp.SMTPError{Code: 501, Message: "HELO/EHLO argument must be a fully-qualified domain name"}
	}
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

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "mail.example.test", slog.Default())

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

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "mail.example.test", slog.Default())

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

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "mail.example.test", slog.Default())

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
	}, "mail.example.test", slog.Default())

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
	}, "mail.example.test", slog.Default())

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
	}, "mail.example.test", slog.Default())

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
	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "mail.example.test", slog.Default())

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

// TestRelaySendsQualifiedHELO verifies that Relay.sendToHost issues the
// configured HELO name on the wire instead of net/smtp's hardcoded
// "localhost", and that this happens before STARTTLS (the mock has no TLS
// config, so NewSession only ever sees the pre-STARTTLS EHLO).
func TestRelaySendsQualifiedHELO(t *testing.T) {
	backend := &mockRelayBackend{messages: make(chan mockRelayMessage, 10)}
	addr := startMockSMTP(t, backend)

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "mail.example.test", slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: helo test\r\n\r\nHello")
	permFails, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(permFails) != 0 {
		t.Errorf("unexpected permanent failures: %v", permFails)
	}
	<-backend.messages

	if got := backend.helo(); got != "mail.example.test" {
		t.Errorf("captured EHLO = %q, want %q", got, "mail.example.test")
	}
	if backend.helo() == "localhost" {
		t.Error("EHLO command sent the literal localhost")
	}
}

// TestRelaySucceedsAgainstServerRejectingDotlessHELO exercises the exact
// acceptance scenario: a receiving server enforcing RFC 5321 4.1.1.1
// (rejecting any HELO/EHLO argument without a dot, e.g. Postfix
// reject_non_fqdn_helo_hostname) must still accept delivery once the relay
// sends a fully-qualified HELO name.
func TestRelaySucceedsAgainstServerRejectingDotlessHELO(t *testing.T) {
	backend := &mockRelayBackend{
		messages:          make(chan mockRelayMessage, 10),
		rejectDotlessHELO: true,
	}
	addr := startMockSMTP(t, backend)

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "mail.example.test", slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: strict helo test\r\n\r\nHello")
	permFails, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err != nil {
		t.Fatalf("Deliver against a dotless-HELO-rejecting server: %v", err)
	}
	if len(permFails) != 0 {
		t.Errorf("unexpected permanent failures: %v", permFails)
	}
}

// TestRelayFailsWithDotlessHELO is the regression guard for the bug this
// fix addresses: against a server rejecting dotless HELO names, a Relay
// carrying an unqualified HELO name (as every outbound connection did before,
// which always sent the hardcoded "localhost") is rejected outright and
// delivery fails -- it must not be silently swallowed as a permanent
// per-recipient failure.
func TestRelayFailsWithDotlessHELO(t *testing.T) {
	backend := &mockRelayBackend{
		messages:          make(chan mockRelayMessage, 10),
		rejectDotlessHELO: true,
	}
	addr := startMockSMTP(t, backend)

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "workstation", slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: dotless helo test\r\n\r\nHello")
	permFails, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err == nil {
		t.Fatal("expected delivery to fail against a server rejecting a dotless HELO, got nil error")
	}
	if len(permFails) != 0 {
		t.Errorf("unexpected permanent failures: %v", permFails)
	}
	if got := backend.helo(); got != "workstation" {
		t.Errorf("captured EHLO = %q, want %q", got, "workstation")
	}
}

// TestRelayFallsBackToAddressLiteralWhenHELONameEmpty verifies that a Relay
// constructed with an empty heloName (ResolveHELOName could not derive a
// qualified name) sends an RFC 5321 4.1.3 address literal instead of
// net/smtp's hardcoded "localhost" -- the "never send localhost" guarantee
// must hold even in the no-configuration fallback path.
func TestRelayFallsBackToAddressLiteralWhenHELONameEmpty(t *testing.T) {
	backend := &mockRelayBackend{messages: make(chan mockRelayMessage, 10)}
	addr := startMockSMTP(t, backend)

	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "", slog.Default())

	rawMsg := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: fallback helo test\r\n\r\nHello")
	permFails, err := relay.Deliver(context.Background(), "alice@example.com", []string{"bob@external.com"}, rawMsg)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(permFails) != 0 {
		t.Errorf("unexpected permanent failures: %v", permFails)
	}
	<-backend.messages

	if got := backend.helo(); got == "localhost" || got == "" {
		t.Errorf("captured EHLO = %q, must never be empty or the literal localhost", got)
	}
	if got := backend.helo(); !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
		t.Errorf("captured EHLO = %q, want an address literal in brackets", got)
	}
}
