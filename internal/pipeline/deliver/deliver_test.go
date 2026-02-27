package deliver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// mockApplier records all commands applied through it.
type mockApplier struct {
	commands []*bongov1.RaftCommand
	err      error
}

func (m *mockApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if m.err != nil {
		return m.err
	}
	m.commands = append(m.commands, cmd)
	return nil
}

// mockResolver returns fixed user IDs for known addresses.
type mockResolver struct {
	mapping map[string]string
	err     error
}

func (m *mockResolver) ResolveRecipient(email string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if id, ok := m.mapping[email]; ok {
		return id, nil
	}
	return "", fmt.Errorf("recipient not found: %s", email)
}

// minimalMessage builds a minimal RFC 5322 message with the given fields.
func minimalMessage(from, to, subject, body string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n")
	sb.WriteString("Message-Id: <test@example.com>\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// unmarshalMailMeta decodes a MailMeta from the value of a RaftCommand.
func unmarshalMailMeta(t *testing.T, cmd *bongov1.RaftCommand) *bongov1.MailMeta {
	t.Helper()
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(cmd.Value, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}
	return &meta
}

// TestDeliverBasic verifies that a normal message is delivered to a user and
// that the MailMeta fields are populated correctly.
func TestDeliverBasic(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
		},
	}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())

	rawMsg := minimalMessage("bob@example.com", "alice@example.com", "Hello", "Hi Alice!\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-001",
		Sender:     "bob@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "abc123",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if result.Action != "pass" {
		t.Errorf("result.Action = %q, want %q", result.Action, "pass")
	}
	if result.Stage != "deliver" {
		t.Errorf("result.Stage = %q, want %q", result.Stage, "deliver")
	}

	if len(applier.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(applier.commands))
	}

	key := string(applier.commands[0].Key)
	if !strings.HasPrefix(key, "/user/user-alice/mail/") {
		t.Errorf("key = %q, want prefix /user/user-alice/mail/", key)
	}

	meta := unmarshalMailMeta(t, applier.commands[0])
	if meta.Subject != "Hello" {
		t.Errorf("meta.Subject = %q, want %q", meta.Subject, "Hello")
	}
	if meta.From != "bob@example.com" {
		t.Errorf("meta.From = %q, want %q", meta.From, "bob@example.com")
	}
	if meta.RawMessage == nil || meta.RawMessage.Hash != "abc123" {
		t.Errorf("meta.RawMessage.Hash = %q, want %q", meta.RawMessage.GetHash(), "abc123")
	}
	if meta.RawMessage.ContentType != "message/rfc822" {
		t.Errorf("meta.RawMessage.ContentType = %q, want %q", meta.RawMessage.ContentType, "message/rfc822")
	}
}

// TestDeliverSpamToJunk verifies that an envelope with target_mailbox "Junk"
// routes the message to Junk and that the TargetMailbox field is set.
func TestDeliverSpamToJunk(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
		},
	}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())

	rawMsg := minimalMessage("spammer@bad.example", "alice@example.com", "Win a prize!", "Click here.\r\n")
	env := &bongov1.QueueEnvelope{
		Id:            "env-junk",
		Sender:        "spammer@bad.example",
		Recipients:    []string{"alice@example.com"},
		ReceivedAt:    time.Now().Unix(),
		BlobHash:      "spamhash",
		TargetMailbox: "Junk",
		Stages: []*bongov1.StageResult{
			{
				Stage:  "antispam",
				Action: "tag",
				Score:  0.92,
			},
		},
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("result.Action = %q, want %q", result.Action, "pass")
	}

	if len(applier.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(applier.commands))
	}

	meta := unmarshalMailMeta(t, applier.commands[0])
	if meta.TargetMailbox != "Junk" {
		t.Errorf("meta.TargetMailbox = %q, want %q", meta.TargetMailbox, "Junk")
	}
}

// TestDeliverMultipleRecipients verifies that a message with multiple
// recipients results in one Raft command per resolved recipient.
func TestDeliverMultipleRecipients(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
			"bob@example.com":   "user-bob",
		},
	}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())

	rawMsg := minimalMessage("carol@example.com", "alice@example.com, bob@example.com", "Group mail", "Hello team!\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-multi",
		Sender:     "carol@example.com",
		Recipients: []string{"alice@example.com", "bob@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "multihash",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("result.Action = %q, want %q", result.Action, "pass")
	}

	if len(applier.commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(applier.commands))
	}

	// Collect keys and check both users got a copy.
	keys := make(map[string]bool)
	for _, cmd := range applier.commands {
		key := string(cmd.Key)
		if strings.Contains(key, "user-alice") {
			keys["alice"] = true
		} else if strings.Contains(key, "user-bob") {
			keys["bob"] = true
		}
	}
	if !keys["alice"] {
		t.Error("no command for user-alice")
	}
	if !keys["bob"] {
		t.Error("no command for user-bob")
	}

	// GUIDs must be distinct.
	metas := make([]*bongov1.MailMeta, 0, 2)
	for _, cmd := range applier.commands {
		metas = append(metas, unmarshalMailMeta(t, cmd))
	}
	if metas[0].Guid == metas[1].Guid {
		t.Errorf("expected distinct GUIDs per recipient, both are %q", metas[0].Guid)
	}
}

// TestDeliverPreservesSpamScore verifies that the spam_score from the antispam
// StageResult is propagated to the stored MailMeta.
func TestDeliverPreservesSpamScore(t *testing.T) {
	const wantScore float32 = 0.75

	applier := &mockApplier{}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
		},
	}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())

	rawMsg := minimalMessage("x@example.com", "alice@example.com", "Scored mail", "body\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-score",
		Sender:     "x@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "scorehash",
		Stages: []*bongov1.StageResult{
			{
				Stage:  "antispam",
				Action: "pass",
				Score:  wantScore,
			},
		},
	}

	_, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if len(applier.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(applier.commands))
	}

	meta := unmarshalMailMeta(t, applier.commands[0])
	if meta.SpamScore != wantScore {
		t.Errorf("meta.SpamScore = %v, want %v", meta.SpamScore, wantScore)
	}
}

// TestDeliverUnknownRecipientSkipped verifies that an unknown recipient is
// skipped without error and no Raft command is emitted for that address.
func TestDeliverUnknownRecipientSkipped(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
		},
	}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())

	rawMsg := minimalMessage("x@example.com", "alice@example.com", "test", "body\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-skip",
		Sender:     "x@example.com",
		Recipients: []string{"alice@example.com", "unknown@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "skiphash",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("result.Action = %q, want %q", result.Action, "pass")
	}

	// Only alice should have a command (unknown recipient is silently skipped).
	if len(applier.commands) != 1 {
		t.Errorf("expected 1 command (alice only), got %d", len(applier.commands))
	}
}

// TestDeliverRaftError verifies that a Raft apply failure is returned as an error.
func TestDeliverRaftError(t *testing.T) {
	applier := &mockApplier{
		err: io.ErrUnexpectedEOF, // any non-nil error
	}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
		},
	}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())

	rawMsg := minimalMessage("x@example.com", "alice@example.com", "test", "body\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-raft-err",
		Sender:     "x@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "errhash",
	}

	_, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
	if err == nil {
		t.Fatal("expected error from Process when Raft fails, got nil")
	}
}

// TestDeliverSPFResult verifies that the spf_result from the envelope is
// preserved in the stored MailMeta.
func TestDeliverSPFResult(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
		},
	}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())

	rawMsg := minimalMessage("x@example.com", "alice@example.com", "SPF test", "body\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-spf",
		Sender:     "x@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "spfhash",
		SpfResult:  "pass",
	}

	_, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	meta := unmarshalMailMeta(t, applier.commands[0])
	if meta.SpfResult != "pass" {
		t.Errorf("meta.SpfResult = %q, want %q", meta.SpfResult, "pass")
	}
}
