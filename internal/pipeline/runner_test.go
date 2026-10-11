package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
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

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func jsonLogger(buf *lockedBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func logLines(t *testing.T, buf *lockedBuffer) []map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(buf.String())))
	var out []map[string]any
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode log line: %v", err)
		}
		out = append(out, m)
	}
	return out
}

// runUntil drives the real Run loop until the envelope appears in dir, then
// stops it and waits for Run to return so the log buffer is safe to read.
func runUntil(t *testing.T, spool *SpoolStore, stage Processor, logger *slog.Logger, dir, id string) {
	t.Helper()
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{stage}, logger,
		WithWorkers(1), WithPollInterval(10*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for !fileExistsIn(t, spool, dir, id) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// Give the worker time to emit the terminal line after the move.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	if !fileExistsIn(t, spool, dir, id) {
		t.Fatalf("envelope never reached %s/", dir)
	}
}

func TestRunnerTerminalLogsCarryIdentity(t *testing.T) {
	tests := []struct {
		name      string
		result    *bongov1.StageResult
		err       error
		dir       string
		msg       string
		wantStage bool
		wantErr   bool
	}{
		{"pass", &bongov1.StageResult{Stage: "antispam", Action: "pass"}, nil, "deliver", "pipeline: message ready for delivery", false, false},
		{"quarantine", &bongov1.StageResult{Stage: "antispam", Action: "quarantine"}, nil, "quarantine", "pipeline: message quarantined", true, false},
		{"reject", &bongov1.StageResult{Stage: "antispam", Action: "reject"}, nil, "quarantine", "pipeline: message quarantined", true, false},
		{"stage error", nil, fmt.Errorf("boom"), "error", "pipeline: process message failed", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spool := newTestSpool(t)
			rcpts := []string{"a@example.com", "b@example.com"}
			env := &bongov1.QueueEnvelope{Id: "msg-" + tt.name, Sender: "sender@example.com", Recipients: rcpts, BlobHash: "deadbeef"}
			if err := spool.Enqueue(env); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			buf := &lockedBuffer{}
			runUntil(t, spool, &mockProcessor{name: "antispam", result: tt.result, err: tt.err}, jsonLogger(buf), tt.dir, env.Id)

			var line map[string]any
			for _, l := range logLines(t, buf) {
				if l["msg"] == tt.msg {
					line = l
				}
			}
			if line == nil {
				t.Fatalf("no %q line in logs:\n%s", tt.msg, buf.String())
			}
			if line["id"] != env.Id || line["sender"] != "sender@example.com" {
				t.Errorf("id/sender wrong: %v", line)
			}
			got, _ := line["recipients"].([]any)
			if !reflect.DeepEqual(got, []any{"a@example.com", "b@example.com"}) {
				t.Errorf("recipients = %v", line["recipients"])
			}
			if line["recipient_count"] != float64(2) {
				t.Errorf("recipient_count = %v", line["recipient_count"])
			}
			if _, ok := line["subject"]; ok {
				t.Error("subject must not be logged")
			}
			if tt.wantStage && (line["stage"] != "antispam" || line["action"] != tt.result.Action) {
				t.Errorf("stage/action = %v/%v", line["stage"], line["action"])
			}
			if s, _ := line["error"].(string); tt.wantErr && s == "" {
				t.Errorf("error missing: %v", line)
			}
		})
	}
}

func TestRunnerLogCapsRecipients(t *testing.T) {
	env := &bongov1.QueueEnvelope{Id: "x", Sender: "s@example.com"}
	for i := range 25 {
		env.Recipients = append(env.Recipients, fmt.Sprintf("r%d@example.com", i))
	}
	buf := &lockedBuffer{}
	jsonLogger(buf).Info("t", envIdentity(env)...)
	lines := logLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	if got := lines[0]["recipients"].([]any); len(got) != 20 {
		t.Errorf("len(recipients) = %d, want 20", len(got))
	}
	if lines[0]["recipient_count"] != float64(25) {
		t.Errorf("recipient_count = %v", lines[0]["recipient_count"])
	}
	if len(env.Recipients) != 25 {
		t.Error("envelope recipients mutated")
	}
}
