package smtp

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-go/bongo-go/internal/config"
)

// mockRelayBackend captures delivered messages for test assertions.
type mockRelayBackend struct {
	messages chan mockRelayMessage
	rejectTo string // reject this recipient with 550
	tempFail bool   // return 421 for all
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

func (s *mockRelaySession) AuthPlain(username, password string) error { return nil }
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
