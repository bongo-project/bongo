package deliver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestDeliverExpiredContextWritesNothing pins the pre-write budget check: a
// context that has already ended must not reach Raft, and a live context (the
// control) must.
func TestDeliverExpiredContextWritesNothing(t *testing.T) {
	newProc := func() (*DeliverProcessor, *mockApplier) {
		applier := &mockApplier{}
		resolver := &mockResolver{mapping: map[string]string{
			"alice@example.com": "user-alice",
			"carol@example.com": "user-carol",
		}}
		return NewDeliverProcessor(applier, resolver, newTestLogger()), applier
	}
	newEnv := func() *bongov1.QueueEnvelope {
		return &bongov1.QueueEnvelope{
			Id:         "env-budget",
			Sender:     "bob@example.com",
			Recipients: []string{"alice@example.com", "carol@example.com"},
			ReceivedAt: time.Now().Unix(),
			BlobHash:   "abc123",
		}
	}
	raw := minimalMessage("bob@example.com", "alice@example.com", "Budget", "Hi\r\n")

	proc, applier := newProc()
	if _, err := proc.Process(context.Background(), newEnv(), strings.NewReader(raw)); err != nil {
		t.Fatalf("live context: %v", err)
	}
	if len(applier.commands) != 2 {
		t.Fatalf("control delivered %d commands, want 2", len(applier.commands))
	}

	proc, applier = newProc()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := proc.Process(ctx, newEnv(), strings.NewReader(raw))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want one wrapping context.DeadlineExceeded", err)
	}
	if len(applier.commands) != 0 {
		t.Errorf("expired context still applied %d commands", len(applier.commands))
	}
}
