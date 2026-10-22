package deliver

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestDeliverDeepMIMEIsFast requires delivery of a message that costs enmime
// seconds to parse to finish quickly with the headers intact.
func TestDeliverDeepMIMEIsFast(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}
	proc := NewDeliverProcessor(applier, resolver, newTestLogger())
	env := &bongov1.QueueEnvelope{
		Id:         "env-deep",
		Sender:     "bob@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   "abc123",
	}

	start := time.Now()
	if _, err := proc.Process(context.Background(), env, bytes.NewReader(testutil.DeepUnclosedMultipart(24))); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Process took %v on a deeply nested message, want < 1s", elapsed)
	}
	if len(applier.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(applier.commands))
	}
	if got := unmarshalMailMeta(t, applier.commands[0]).Subject; got != "deep nesting" {
		t.Errorf("Subject = %q, want %q", got, "deep nesting")
	}
}
