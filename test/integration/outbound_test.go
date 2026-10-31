//go:build integration

package integration

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	smtplib "net/smtp"
	"net/url"
	"strings"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// mockExternalSMTPBackend captures messages delivered to the mock server.
type mockExternalSMTPBackend struct {
	received chan []byte
}

func (b *mockExternalSMTPBackend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	return &mockExternalSMTPSession{backend: b}, nil
}

type mockExternalSMTPSession struct {
	backend    *mockExternalSMTPBackend
	from       string
	recipients []string
}

func (s *mockExternalSMTPSession) AuthPlain(_, _ string) error { return nil }
func (s *mockExternalSMTPSession) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}
func (s *mockExternalSMTPSession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	s.recipients = append(s.recipients, to)
	return nil
}
func (s *mockExternalSMTPSession) Data(r io.Reader) error {
	data, _ := io.ReadAll(r)
	s.backend.received <- data
	return nil
}
func (s *mockExternalSMTPSession) Reset()        {}
func (s *mockExternalSMTPSession) Logout() error { return nil }

func mockExternalSMTP(t *testing.T) (addr string, received chan []byte) {
	t.Helper()
	backend := &mockExternalSMTPBackend{received: make(chan []byte, 10)}

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

	return l.Addr().String(), backend.received
}

// mockRejectSMTPBackend rejects all recipients with 550.
type mockRejectSMTPBackend struct{}

func (b *mockRejectSMTPBackend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	return &mockRejectSMTPSession{}, nil
}

type mockRejectSMTPSession struct{}

func (s *mockRejectSMTPSession) AuthPlain(_, _ string) error { return nil }
func (s *mockRejectSMTPSession) Mail(_ string, _ *gosmtp.MailOptions) error {
	return &gosmtp.SMTPError{Code: 550, Message: "rejected"}
}
func (s *mockRejectSMTPSession) Rcpt(_ string, _ *gosmtp.RcptOptions) error { return nil }
func (s *mockRejectSMTPSession) Data(_ io.Reader) error                     { return nil }
func (s *mockRejectSMTPSession) Reset()                                     {}
func (s *mockRejectSMTPSession) Logout() error                              { return nil }

func startDevAgent(t *testing.T, cfg *config.Config) *agent.Agent {
	t.Helper()
	a, _, _ := sharedStartAgent(t, cfg)
	return a
}

func TestOutboundRelay(t *testing.T) {
	mockAddr, received := mockExternalSMTP(t)

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.SMTP.Relay.Enabled = true
	cfg.SMTP.Relay.Host = mockAddr

	a := startDevAgent(t, cfg)

	// Create sender user.
	userID, err := auth.CreateUser(a, a.Metadata, "sender@localhost", "Sender", "pass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Store raw message as blob.
	rawMsg := []byte("From: sender@localhost\r\nTo: bob@external.com\r\nSubject: outbound test\r\n\r\nHello external world")
	hash, err := a.Blobs.Put(rawMsg)
	if err != nil {
		t.Fatalf("blob put: %v", err)
	}

	// Enqueue outbound message.
	msg := &bongov1.OutboundMessage{
		Id:          "test-outbound-1",
		SenderId:    userID,
		SenderEmail: "sender@localhost",
		Recipients:  []string{"bob@external.com"},
		BlobHash:    hash,
		MessageGuid: "<test-1@bongo>",
	}
	if err := a.QueueStore.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Wait for the queue worker to process.
	select {
	case data := <-received:
		if !strings.Contains(string(data), "outbound test") {
			t.Error("received message does not contain expected subject")
		}
		t.Log("message received by mock SMTP server")
	case <-time.After(30 * time.Second):
		t.Fatal("message not received by mock SMTP server within timeout")
	}
}

func TestWebUIComposeExternalRecipientRelaysOutbound(t *testing.T) {
	mockAddr, received := mockExternalSMTP(t)

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.SMTP.Relay.Enabled = true
	cfg.SMTP.Relay.Host = mockAddr

	startDevAgent(t, cfg)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	resp, err := client.PostForm(baseURL+"/login", url.Values{
		"email":    {"admin@localhost"},
		"password": {"admin"},
	})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}

	resp, err = client.PostForm(baseURL+"/compose/send", url.Values{
		"send_id": {composeSendID()},
		"to":      {"bob@external.com"},
		"subject": {"webui outbound relay"},
		"body":    {"This message should leave via the outbound relay."},
	})
	if err != nil {
		t.Fatalf("POST /compose/send: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compose status = %d, want 200", resp.StatusCode)
	}

	select {
	case data := <-received:
		if !strings.Contains(string(data), "webui outbound relay") {
			t.Fatalf("received message does not contain expected subject:\n%s", string(data))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("message composed in Web UI was not received by mock SMTP server")
	}
}

func TestBounceGeneration(t *testing.T) {
	// Start a mock SMTP that rejects all mail.
	srv := gosmtp.NewServer(&mockRejectSMTPBackend{})
	srv.Addr = "127.0.0.1:0"
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mock smtp listen: %v", err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	rejectAddr := l.Addr().String()

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.SMTP.Relay.Enabled = true
	cfg.SMTP.Relay.Host = rejectAddr

	a := startDevAgent(t, cfg)

	userID, err := auth.CreateUser(a, a.Metadata, "sender@localhost", "Sender", "pass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if a.Mailboxes != nil {
		a.Mailboxes.EnsureDefaultMailboxes(userID)
	}
	time.Sleep(500 * time.Millisecond)

	rawMsg := []byte("From: sender@localhost\r\nTo: fail@external.com\r\nSubject: will bounce\r\n\r\nThis should bounce")
	hash, _ := a.Blobs.Put(rawMsg)

	// Enqueue already past the default 5d lifetime so it bounces on its next failure.
	msg := &bongov1.OutboundMessage{
		Id:           "test-bounce-1",
		SenderId:     userID,
		SenderEmail:  "sender@localhost",
		Recipients:   []string{"fail@external.com"},
		BlobHash:     hash,
		AttemptCount: 3,
		CreatedAt:    time.Now().Add(-6 * 24 * time.Hour).Unix(),
		MessageGuid:  "<test-bounce@bongo>",
	}
	if err := a.QueueStore.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Wait for queue worker to process and generate bounce.
	time.Sleep(15 * time.Second)

	// Check that bounce appeared in sender's INBOX.
	mailPrefix := fmt.Sprintf("/user/%s/mail/", userID)
	iter, err := a.Store.PrefixScan([]byte(mailPrefix))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}

	bounceFound := false
	for iter.Next() {
		val, _ := iter.Value()
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			continue
		}
		if strings.Contains(meta.Subject, "Undelivered") {
			bounceFound = true
			t.Logf("bounce found: subject=%q from=%q", meta.Subject, meta.From)
			break
		}
	}
	iter.Close()

	if !bounceFound {
		t.Error("bounce message not found in sender's mailbox")
	}
}

func TestRateLimiting(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.SMTP.Limits.FloodThreshold = 3

	a := startDevAgent(t, cfg)

	// Verify rate limiter is configured.
	if a.RateLimiter == nil {
		t.Fatal("rate limiter not initialized")
	}

	// Allow 3 messages.
	for i := 0; i < 3; i++ {
		if !a.RateLimiter.Allow("test@localhost") {
			t.Fatalf("message %d should be allowed", i+1)
		}
	}

	// 4th should be rejected.
	if a.RateLimiter.Allow("test@localhost") {
		t.Error("4th message should be rate limited")
	}

	t.Log("rate limiting test passed")
}

func TestSPFInbound(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true
	cfg.Pipeline.PollInterval = "50ms" // deliver promptly

	a := startDevAgent(t, cfg)

	// Create recipient user.
	userID, err := auth.CreateUser(a, a.Metadata, "recipient@localhost", "Recipient", "pass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if a.Mailboxes != nil {
		a.Mailboxes.EnsureDefaultMailboxes(userID)
	}
	time.Sleep(500 * time.Millisecond)

	// Send inbound email via net/smtp.
	smtpAddr := a.SMTP.MXAddr()
	msg := "From: external@example.com\r\n" +
		"To: recipient@localhost\r\n" +
		"Subject: SPF test\r\n" +
		"Message-ID: <spf-test@example.com>\r\n" +
		"\r\n" +
		"Testing SPF header injection\r\n"

	err = smtplib.SendMail(smtpAddr, nil, "external@example.com", []string{"recipient@localhost"}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	// Delivery runs on the pipeline's poll loop, so a fixed sleep races it:
	// under -race load the message landed after the check ran. Poll for the
	// header instead.
	mailPrefix := fmt.Sprintf("/user/%s/mail/", userID)
	hasSPF := func() bool {
		iter, err := a.Store.PrefixScan([]byte(mailPrefix))
		if err != nil {
			t.Fatalf("PrefixScan: %v", err)
		}
		defer iter.Close()
		for iter.Next() {
			val, _ := iter.Value()
			var meta bongov1.MailMeta
			if err := proto.Unmarshal(val, &meta); err != nil {
				continue
			}
			if meta.RawMessage == nil || meta.RawMessage.Hash == "" {
				continue
			}
			rc, err := a.Blobs.Get(meta.RawMessage.Hash)
			if err != nil {
				continue
			}
			rawData, _ := io.ReadAll(rc)
			rc.Close()
			if strings.Contains(string(rawData), "Received-SPF:") {
				return true
			}
		}
		return false
	}

	deadline := time.Now().Add(15 * time.Second)
	for !hasSPF() {
		if time.Now().After(deadline) {
			t.Fatal("Received-SPF header not found in delivered message")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Log("Received-SPF header found in inbound message")
}
