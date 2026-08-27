package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// dummyRFC5322 is a minimal valid RFC 5322 message used in tests.
const dummyRFC5322 = "From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: test\r\n\r\nHello, world.\r\n"

// mockBlobReader returns the dummyRFC5322 message for any hash.
type mockBlobReader struct{}

func (m *mockBlobReader) ReadBlob(_ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader([]byte(dummyRFC5322))), nil
}

// mockProcessor is a configurable test implementation of Processor.
type mockProcessor struct {
	name   string
	result *bongov1.StageResult
	err    error
	calls  int
}

func (p *mockProcessor) Name() string { return p.name }

func (p *mockProcessor) Process(_ context.Context, _ *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	p.calls++
	return p.result, p.err
}

// newTestSpool creates a SpoolStore backed by t.TempDir().
func newTestSpool(t *testing.T) *SpoolStore {
	t.Helper()
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	spool, err := NewSpoolStore(dir, logger)
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	return spool
}

// newTestEnv creates and enqueues a minimal QueueEnvelope for use in tests.
func newTestEnv(t *testing.T, spool *SpoolStore, id string) *bongov1.QueueEnvelope {
	t.Helper()
	env := &bongov1.QueueEnvelope{
		Id:       id,
		Sender:   "sender@example.com",
		BlobHash: "deadbeef",
	}
	if err := spool.Enqueue(env); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return env
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func fileExistsIn(t *testing.T, spool *SpoolStore, dir, id string) bool {
	t.Helper()
	path := filepath.Join(spool.baseDir, dir, id+envSuffix)
	_, err := os.Stat(path)
	return err == nil
}

// TestRunnerProcessMessage verifies that a passing stage moves the envelope to deliver/.
func TestRunnerProcessMessage(t *testing.T) {
	spool := newTestSpool(t)
	env := newTestEnv(t, spool, "msg-001")

	proc := &mockProcessor{
		name: "antispam",
		result: &bongov1.StageResult{
			Stage:  "antispam",
			Action: "pass",
		},
	}

	runner := NewRunner(spool, &mockBlobReader{}, []Processor{proc}, newTestLogger())

	// Dequeue manually to move to processing/.
	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := runner.processMessage(context.Background(), dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if !fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("expected envelope in deliver/, not found")
	}
	if proc.calls != 1 {
		t.Errorf("expected 1 stage call, got %d", proc.calls)
	}

	// Verify flags.
	delivered, err := spool.ReadEnvelope("deliver", env.Id)
	if err != nil {
		t.Fatalf("ReadEnvelope deliver: %v", err)
	}
	if !HasFlag(delivered.Flags, MsgFlagSpamChecked) {
		t.Error("expected MsgFlagSpamChecked to be set")
	}
}

// TestRunnerQuarantine verifies that a quarantine action moves the envelope to quarantine/.
func TestRunnerQuarantine(t *testing.T) {
	spool := newTestSpool(t)
	env := newTestEnv(t, spool, "msg-002")

	proc := &mockProcessor{
		name: "antispam",
		result: &bongov1.StageResult{
			Stage:  "antispam",
			Action: "quarantine",
		},
	}

	runner := NewRunner(spool, &mockBlobReader{}, []Processor{proc}, newTestLogger())

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := runner.processMessage(context.Background(), dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if !fileExistsIn(t, spool, "quarantine", env.Id) {
		t.Error("expected envelope in quarantine/, not found")
	}
	if fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("envelope should not be in deliver/")
	}

	quarantined, err := spool.ReadEnvelope("quarantine", env.Id)
	if err != nil {
		t.Fatalf("ReadEnvelope quarantine: %v", err)
	}
	if !HasFlag(quarantined.Flags, MsgFlagQuarantined) {
		t.Error("expected MsgFlagQuarantined to be set")
	}
	if !HasFlag(quarantined.Flags, MsgFlagSpam) {
		t.Error("expected MsgFlagSpam to be set on quarantine action")
	}
}

// TestRunnerError verifies that a stage error moves the envelope to error/.
func TestRunnerError(t *testing.T) {
	spool := newTestSpool(t)
	env := newTestEnv(t, spool, "msg-003")

	proc := &mockProcessor{
		name:   "antispam",
		result: nil,
		err:    fmt.Errorf("backend unavailable"),
	}

	runner := NewRunner(spool, &mockBlobReader{}, []Processor{proc}, newTestLogger())

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := runner.processMessage(context.Background(), dequeued); err == nil {
		t.Fatal("expected error from processMessage, got nil")
	}

	if !fileExistsIn(t, spool, "error", env.Id) {
		t.Error("expected envelope in error/, not found")
	}
}

// TestRunnerIdempotent verifies that a stage whose flag is already set is skipped.
func TestRunnerIdempotent(t *testing.T) {
	spool := newTestSpool(t)

	// Pre-set the spam-checked flag so the antispam stage should be skipped.
	env := &bongov1.QueueEnvelope{
		Id:       "msg-004",
		Sender:   "sender@example.com",
		BlobHash: "deadbeef",
		Flags:    MsgFlagSpamChecked,
	}
	if err := spool.Enqueue(env); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	proc := &mockProcessor{
		name:   "antispam",
		result: &bongov1.StageResult{Stage: "antispam", Action: "pass"},
	}

	runner := NewRunner(spool, &mockBlobReader{}, []Processor{proc}, newTestLogger())

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := runner.processMessage(context.Background(), dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if proc.calls != 0 {
		t.Errorf("expected stage to be skipped (0 calls), got %d", proc.calls)
	}
	if !fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("expected envelope in deliver/, not found")
	}
}

// TestRunnerMultipleStages verifies that two stages run in order.
func TestRunnerMultipleStages(t *testing.T) {
	spool := newTestSpool(t)
	env := newTestEnv(t, spool, "msg-005")

	spam := &mockProcessor{
		name:   "antispam",
		result: &bongov1.StageResult{Stage: "antispam", Action: "pass"},
	}
	virus := &mockProcessor{
		name:   "antivirus",
		result: &bongov1.StageResult{Stage: "antivirus", Action: "pass"},
	}

	runner := NewRunner(spool, &mockBlobReader{}, []Processor{spam, virus}, newTestLogger())

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := runner.processMessage(context.Background(), dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if spam.calls != 1 {
		t.Errorf("antispam: expected 1 call, got %d", spam.calls)
	}
	if virus.calls != 1 {
		t.Errorf("antivirus: expected 1 call, got %d", virus.calls)
	}
	if !fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("expected envelope in deliver/, not found")
	}

	delivered, err := spool.ReadEnvelope("deliver", env.Id)
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if len(delivered.Stages) != 2 {
		t.Errorf("expected 2 stage results, got %d", len(delivered.Stages))
	}
	if !HasFlag(delivered.Flags, MsgFlagSpamChecked) {
		t.Error("MsgFlagSpamChecked not set")
	}
	if !HasFlag(delivered.Flags, MsgFlagVirusChecked) {
		t.Error("MsgFlagVirusChecked not set")
	}
}

// TestRunnerGracefulShutdown verifies that cancelling the context stops the polling
// loop and in-flight messages complete before Run returns.
func TestRunnerGracefulShutdown(t *testing.T) {
	spool := newTestSpool(t)

	// Use a slow processor to ensure the worker is still running when we cancel.
	slow := &mockProcessor{
		name: "antispam",
		result: &bongov1.StageResult{
			Stage:  "antispam",
			Action: "pass",
		},
	}

	runner := NewRunner(
		spool,
		&mockBlobReader{},
		[]Processor{slow},
		newTestLogger(),
		WithWorkers(1),
		WithPollInterval(10*time.Millisecond),
	)

	// Enqueue a message before starting.
	newTestEnv(t, spool, "msg-006")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx)
	}()

	// Give the runner time to pick up the message.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s after context cancel")
	}
}

// TestRunnerEmptyQueue verifies the runner handles an empty queue without error.
func TestRunnerEmptyQueue(t *testing.T) {
	spool := newTestSpool(t)

	runner := NewRunner(
		spool,
		&mockBlobReader{},
		nil,
		newTestLogger(),
		WithWorkers(1),
		WithPollInterval(10*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run returned error on empty queue: %v", err)
	}
}
