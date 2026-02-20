package smtp

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type mockApplier struct {
	commands []*bongov1.RaftCommand
}

func (m *mockApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	m.commands = append(m.commands, cmd)
	return nil
}

type mockResolver struct {
	users map[string]string
}

func (m *mockResolver) ResolveRecipient(email string) (string, error) {
	email = strings.TrimPrefix(email, "<")
	email = strings.TrimSuffix(email, ">")
	email = strings.TrimSpace(email)
	if id, ok := m.users[email]; ok {
		return id, nil
	}
	return "", &recipientNotFoundError{email}
}

type recipientNotFoundError struct {
	email string
}

func (e *recipientNotFoundError) Error() string {
	return "recipient not found: " + e.email
}

func TestDeliveryPipeline(t *testing.T) {
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	applier := &mockApplier{}
	resolver := &mockResolver{
		users: map[string]string{
			"alice@example.com": "user-1",
		},
	}

	pipeline := NewDeliveryPipeline(applier, blobs, resolver, testLogger())

	msg := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: Hello\r\nMessage-Id: <test@example.com>\r\n\r\nHello Alice!\r\n"

	err = pipeline.Deliver("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	// Verify a command was applied.
	if len(applier.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(applier.commands))
	}

	cmd := applier.commands[0]
	if cmd.Op != bongov1.Op_OP_PUT {
		t.Errorf("op = %v, want OP_PUT", cmd.Op)
	}

	// Parse the key.
	key := string(cmd.Key)
	if !strings.HasPrefix(key, "/user/user-1/mail/") {
		t.Errorf("key = %q, want prefix /user/user-1/mail/", key)
	}

	// Parse the protobuf value.
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(cmd.Value, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}

	if meta.Subject != "Hello" {
		t.Errorf("subject = %q, want %q", meta.Subject, "Hello")
	}
	if meta.From != "bob@example.com" {
		t.Errorf("from = %q, want %q", meta.From, "bob@example.com")
	}
	if meta.RawMessage == nil || meta.RawMessage.Hash == "" {
		t.Error("expected raw message blob ref")
	}

	// Verify blob was stored.
	if !blobs.Exists(meta.RawMessage.Hash) {
		t.Error("raw message blob not found in store")
	}
}

func TestDeliveryWithAttachment(t *testing.T) {
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	applier := &mockApplier{}
	resolver := &mockResolver{
		users: map[string]string{
			"alice@example.com": "user-1",
		},
	}

	pipeline := NewDeliveryPipeline(applier, blobs, resolver, testLogger())

	// MIME message with attachment.
	msg := "From: bob@example.com\r\n" +
		"To: alice@example.com\r\n" +
		"Subject: With Attachment\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"boundary\"\r\n" +
		"\r\n" +
		"--boundary\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"See attached.\r\n" +
		"--boundary\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"JVBER\r\n" +
		"--boundary--\r\n"

	err = pipeline.Deliver("bob@example.com", []string{"alice@example.com"}, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	if len(applier.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(applier.commands))
	}

	var meta bongov1.MailMeta
	if err := proto.Unmarshal(applier.commands[0].Value, &meta); err != nil {
		t.Fatal(err)
	}

	if len(meta.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(meta.Attachments))
	}
	if meta.Attachments[0].ContentType != "application/pdf" {
		t.Errorf("attachment content type = %q, want %q", meta.Attachments[0].ContentType, "application/pdf")
	}
}

func TestDeliveryUnknownRecipient(t *testing.T) {
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	applier := &mockApplier{}
	resolver := &mockResolver{users: map[string]string{}}

	pipeline := NewDeliveryPipeline(applier, blobs, resolver, testLogger())

	msg := "From: bob@example.com\r\nTo: nobody@example.com\r\nSubject: Test\r\n\r\nBody\r\n"
	err = pipeline.Deliver("bob@example.com", []string{"nobody@example.com"}, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("expected no error for unknown recipient, got: %v", err)
	}

	// No commands should be applied for unknown recipients.
	if len(applier.commands) != 0 {
		t.Errorf("expected 0 commands for unknown recipient, got %d", len(applier.commands))
	}
}
