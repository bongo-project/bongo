package deliver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/pipeline"
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
	var metaBytes []byte
	if strings.HasPrefix(string(cmd.Key), "/__mailbox_deliver__/") {
		var payload struct {
			Meta []byte `json:"meta"`
		}
		if err := json.Unmarshal(cmd.Value, &payload); err != nil {
			t.Fatalf("unmarshal delivery payload: %v", err)
		}
		metaBytes = payload.Meta
	} else {
		metaBytes = cmd.Value
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(metaBytes, &meta); err != nil {
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
	if !strings.HasPrefix(key, "/__mailbox_deliver__/user-alice/INBOX") {
		t.Errorf("key = %q, want prefix /__mailbox_deliver__/user-alice/INBOX", key)
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

// TestDeliverIdempotent verifies that an envelope with MsgFlagDelivered already
// set is returned as "pass" without any Raft commands being applied. This
// protects against duplicate delivery during crash recovery.
func TestDeliverIdempotent(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
		},
	}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())

	rawMsg := minimalMessage("bob@example.com", "alice@example.com", "Already delivered", "body\r\n")
	env := &bongov1.QueueEnvelope{
		Id:         "env-idempotent",
		Sender:     "bob@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "idemhash",
		Flags:      pipeline.MsgFlagDelivered,
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

	// No Raft commands must have been issued: delivery was skipped entirely.
	if len(applier.commands) != 0 {
		t.Errorf("expected 0 Raft commands for already-delivered envelope, got %d", len(applier.commands))
	}
}

// mockQueuer records enqueued outbound messages.
type mockQueuer struct {
	messages []*bongov1.OutboundMessage
	err      error
}

func (m *mockQueuer) Enqueue(msg *bongov1.OutboundMessage) error {
	if m.err != nil {
		return m.err
	}
	m.messages = append(m.messages, msg)
	return nil
}

// mockPrefs returns fixed preferences by user ID.
type mockPrefs struct {
	prefs map[string]*bongov1.UserPreferences
}

func (m *mockPrefs) Get(userID string) *bongov1.UserPreferences {
	if p, ok := m.prefs[userID]; ok {
		return p
	}
	return &bongov1.UserPreferences{ForwardKeepCopy: true}
}

// TestDeliverForwardAddressesEnqueued verifies that when ForwardAddresses is
// set on the envelope, the deliver processor enqueues an outbound message.
func TestDeliverForwardAddressesEnqueued(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
		},
	}
	queuer := &mockQueuer{}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())
	proc.SetQueuer(queuer)

	rawMsg := minimalMessage("bob@example.com", "alice@example.com", "Hello", "Hi!\r\n")
	env := &bongov1.QueueEnvelope{
		Id:               "env-fwd",
		Sender:           "bob@example.com",
		Recipients:       []string{"alice@example.com"},
		ReceivedAt:       time.Now().Unix(),
		BlobHash:         "fwdhash",
		ForwardAddresses: []string{"external@other.com"},
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("result.Action = %q, want %q", result.Action, "pass")
	}

	// Local delivery should still happen.
	if len(applier.commands) != 1 {
		t.Fatalf("expected 1 raft command (local delivery), got %d", len(applier.commands))
	}

	// Forward should be enqueued.
	if len(queuer.messages) != 1 {
		t.Fatalf("expected 1 enqueued forward message, got %d", len(queuer.messages))
	}
	fwd := queuer.messages[0]
	if fwd.BlobHash != "fwdhash" {
		t.Errorf("forward blob hash = %q, want %q", fwd.BlobHash, "fwdhash")
	}
	if len(fwd.Recipients) != 1 || fwd.Recipients[0] != "external@other.com" {
		t.Errorf("forward recipients = %v, want [external@other.com]", fwd.Recipients)
	}
	if fwd.SenderEmail != "bob@example.com" {
		t.Errorf("forward sender = %q, want %q", fwd.SenderEmail, "bob@example.com")
	}
}

// TestDeliverAccountForwarding verifies that account-level forwarding preferences
// trigger forwarding and that forward_keep_copy controls local delivery.
func TestDeliverAccountForwarding(t *testing.T) {
	tests := []struct {
		name          string
		keepCopy      bool
		wantLocalCmds int
		wantForwards  int
	}{
		{"keep_copy_true", true, 1, 1},
		{"keep_copy_false", false, 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applier := &mockApplier{}
			resolver := &mockResolver{
				mapping: map[string]string{
					"alice@example.com": "user-alice",
				},
			}
			queuer := &mockQueuer{}
			prefs := &mockPrefs{
				prefs: map[string]*bongov1.UserPreferences{
					"user-alice": {
						ForwardAddress:  "external@other.com",
						ForwardKeepCopy: tt.keepCopy,
					},
				},
			}

			proc := NewDeliverProcessor(applier, resolver, newTestLogger())
			proc.SetQueuer(queuer)
			proc.SetPreferences(prefs)

			rawMsg := minimalMessage("bob@example.com", "alice@example.com", "Hello", "Hi!\r\n")
			env := &bongov1.QueueEnvelope{
				Id:         "env-acct-fwd",
				Sender:     "bob@example.com",
				Recipients: []string{"alice@example.com"},
				ReceivedAt: time.Now().Unix(),
				BlobHash:   "acctfwdhash",
			}

			_, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}

			if len(applier.commands) != tt.wantLocalCmds {
				t.Errorf("local delivery commands = %d, want %d", len(applier.commands), tt.wantLocalCmds)
			}
			if len(queuer.messages) != tt.wantForwards {
				t.Errorf("forwarded messages = %d, want %d", len(queuer.messages), tt.wantForwards)
			}
		})
	}
}

// TestDeliverNoForwardWithoutQueuer verifies that forwarding is silently skipped
// when no queuer is set (graceful degradation).
func TestDeliverNoForwardWithoutQueuer(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{
		mapping: map[string]string{
			"alice@example.com": "user-alice",
		},
	}

	proc := NewDeliverProcessor(applier, resolver, newTestLogger())
	// No queuer set.

	rawMsg := minimalMessage("bob@example.com", "alice@example.com", "Hello", "Hi!\r\n")
	env := &bongov1.QueueEnvelope{
		Id:               "env-no-queuer",
		Sender:           "bob@example.com",
		Recipients:       []string{"alice@example.com"},
		ReceivedAt:       time.Now().Unix(),
		BlobHash:         "noqhash",
		ForwardAddresses: []string{"external@other.com"},
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(rawMsg))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("result.Action = %q, want %q", result.Action, "pass")
	}
	// Local delivery should still succeed.
	if len(applier.commands) != 1 {
		t.Errorf("expected 1 command, got %d", len(applier.commands))
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

func TestCountReceivedHeaders(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{"none", "From: a@b.com\r\nSubject: hi\r\n\r\nbody", 0},
		{"one", "Received: from x by y; date\r\nFrom: a@b.com\r\n\r\nbody", 1},
		{
			"three with fold",
			"Received: from a\r\n by b; d1\r\nReceived: from c by d; d2\r\nReceived: from e by f; d3\r\nFrom: a@b.com\r\n\r\nbody",
			3,
		},
		{"ignores received-spf", "Received-SPF: pass\r\nReceived: from x by y; d\r\nFrom: a@b\r\n\r\nb", 1},
		{"stops at body", "Received: from x by y; d\r\n\r\nReceived: not-a-header\r\n", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countReceivedHeaders([]byte(tt.raw)); got != tt.want {
				t.Errorf("countReceivedHeaders = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestForwardTargetsDropsSelfForward(t *testing.T) {
	resolver := &mockResolver{mapping: map[string]string{
		"alice@example.com": "user-alice",
		"alias@example.com": "user-alice", // same account as the recipient
		"bob@external.net":  "user-bob",
	}}
	proc := NewDeliverProcessor(nil, resolver, newTestLogger())

	// Recipient alice forwards to her own alias (loop) and to an external addr.
	got := proc.forwardTargets(
		[]string{"alias@example.com", "bob@external.net", "bob@external.net"},
		[]string{"alice@example.com"},
	)

	want := []string{"bob@external.net"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("forwardTargets = %v, want %v (self-forward alias should be dropped, dupes collapsed)", got, want)
	}
}
