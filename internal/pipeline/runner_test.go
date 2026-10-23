package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// waitForLog polls buf until a line with the given msg appears and returns it.
// It fails the test if none appears before the timeout.
func waitForLog(t *testing.T, buf *lockedBuffer, msg string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, l := range logLines(t, buf) {
			if l["msg"] == msg {
				return l
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q line within %v in logs:\n%s", msg, timeout, buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// assertIdentity checks that line carries the envIdentity attributes for env
// (the standard two-recipient test envelope) and, when wantErrSubstr is not
// empty, an error attribute containing it.
func assertIdentity(t *testing.T, line map[string]any, env *bongov1.QueueEnvelope, wantErrSubstr string) {
	t.Helper()
	if line["id"] != env.Id || line["sender"] != env.Sender {
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
	if wantErrSubstr != "" {
		s, _ := line["error"].(string)
		if !strings.Contains(s, wantErrSubstr) {
			t.Errorf("error = %q, want substring %q", s, wantErrSubstr)
		}
	}
}

// runUntil drives the real Run loop until the envelope appears in dir and the
// line with msg has been logged, then stops it, waits for Run to return and
// hands back that line.
func runUntil(t *testing.T, spool *SpoolStore, stage Processor, buf *lockedBuffer, dir, id, msg string) map[string]any {
	t.Helper()
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{stage}, jsonLogger(buf),
		WithWorkers(1), WithPollInterval(10*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !fileExistsIn(t, spool, dir, id) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !fileExistsIn(t, spool, dir, id) {
		t.Fatalf("envelope never reached %s/", dir)
	}
	return waitForLog(t, buf, msg, 5*time.Second)
}

func TestRunnerTerminalLogsCarryIdentity(t *testing.T) {
	tests := []struct {
		name      string
		result    *bongov1.StageResult
		err       error
		dir       string
		msg       string
		wantStage bool
		wantErr   string
	}{
		{"pass", &bongov1.StageResult{Stage: "antispam", Action: "pass"}, nil, "deliver", "pipeline: message ready for delivery", false, ""},
		{"quarantine", &bongov1.StageResult{Stage: "antispam", Action: "quarantine"}, nil, "quarantine", "pipeline: message quarantined", true, ""},
		{"reject", &bongov1.StageResult{Stage: "antispam", Action: "reject"}, nil, "quarantine", "pipeline: message quarantined", true, ""},
		{"stage error", nil, fmt.Errorf("boom"), "error", "pipeline: process message failed", false, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spool := newTestSpool(t)
			env := identityEnv("msg-" + tt.name)
			if err := spool.Enqueue(env); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			buf := &lockedBuffer{}
			line := runUntil(t, spool, &mockProcessor{name: "antispam", result: tt.result, err: tt.err}, buf, tt.dir, env.Id, tt.msg)

			assertIdentity(t, line, env, tt.wantErr)
			if tt.wantStage && (line["stage"] != "antispam" || line["action"] != tt.result.Action) {
				t.Errorf("stage/action = %v/%v", line["stage"], line["action"])
			}
		})
	}
}

// identityEnv is the standard two-recipient envelope used by identity tests.
func identityEnv(id string) *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Id:         id,
		Sender:     "sender@example.com",
		Recipients: []string{"a@example.com", "b@example.com"},
		BlobHash:   "deadbeef",
	}
}

// errBlobReader fails every read.
type errBlobReader struct{}

func (errBlobReader) ReadBlob(_ string) (io.ReadCloser, error) {
	return nil, errors.New("blob gone")
}

// TestRunnerFailureLogsCarryIdentity injects spool failures through
// filesystem state (and a failing BlobReader) and asserts each failure-path
// log line carries the envelope identity. processMessage is called directly
// so each case yields exactly one line with the target msg even though three
// sites share "pipeline: move to error failed".
func TestRunnerFailureLogsCarryIdentity(t *testing.T) {
	const moveMsg = "pipeline: move to error failed"
	tests := []struct {
		name    string
		blobs   BlobReader
		result  *bongov1.StageResult
		stErr   error
		prepare func(t *testing.T, spool *SpoolStore, env *bongov1.QueueEnvelope)
		msg     string
		wantErr string
		wantRet bool
	}{
		{
			// The envelope was never written to processing/, so the move fails.
			name: "blob read error", blobs: errBlobReader{},
			msg: moveMsg, wantErr: "spool: move", wantRet: true,
		},
		{
			name: "stage error", blobs: &mockBlobReader{}, stErr: errors.New("boom"),
			msg: moveMsg, wantErr: "spool: move", wantRet: true,
		},
		{
			// Removing processing/ makes both the post-stage write and the move fail.
			name: "write after stage", blobs: &mockBlobReader{},
			result: &bongov1.StageResult{Stage: "antispam", Action: "pass"},
			prepare: func(t *testing.T, spool *SpoolStore, _ *bongov1.QueueEnvelope) {
				if err := os.RemoveAll(filepath.Join(spool.baseDir, "processing")); err != nil {
					t.Fatal(err)
				}
			},
			msg: moveMsg, wantErr: "spool: move", wantRet: true,
		},
		{
			// A directory squats on WriteEnvelope's temp path (tmpPrefix+id+tmpSuffix),
			// so the write fails with EISDIR while the real envelope still moves.
			name: "write before quarantine", blobs: &mockBlobReader{},
			result: &bongov1.StageResult{Stage: "antispam", Action: "quarantine"},
			prepare: func(t *testing.T, spool *SpoolStore, env *bongov1.QueueEnvelope) {
				if err := spool.WriteEnvelope("processing", env); err != nil {
					t.Fatal(err)
				}
				tmp := filepath.Join(spool.baseDir, "processing", tmpPrefix+env.Id+tmpSuffix)
				if err := os.Mkdir(tmp, 0o750); err != nil {
					t.Fatal(err)
				}
			},
			msg: "pipeline: write envelope before quarantine failed", wantErr: "spool: write temp",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spool := newTestSpool(t)
			env := identityEnv("msg-fail")
			if tt.prepare != nil {
				tt.prepare(t, spool, env)
			}
			buf := &lockedBuffer{}
			stage := &mockProcessor{name: "antispam", result: tt.result, err: tt.stErr}
			runner := NewRunner(spool, tt.blobs, []Processor{stage}, jsonLogger(buf))

			err := runner.processMessage(context.Background(), env)
			if (err != nil) != tt.wantRet {
				t.Fatalf("processMessage error = %v, want error: %v", err, tt.wantRet)
			}
			var matches []map[string]any
			for _, l := range logLines(t, buf) {
				if l["msg"] == tt.msg {
					matches = append(matches, l)
				}
			}
			if len(matches) != 1 {
				t.Fatalf("got %d %q lines, want 1:\n%s", len(matches), tt.msg, buf.String())
			}
			assertIdentity(t, matches[0], env, tt.wantErr)
			if tt.name == "write before quarantine" && !fileExistsIn(t, spool, "quarantine", env.Id) {
				t.Error("expected envelope in quarantine/ despite the failed write")
			}
		})
	}
}

// blockingProcessor blocks in Process until release is closed.
type blockingProcessor struct {
	name    string
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingProcessor) Name() string { return p.name }

func (p *blockingProcessor) Process(_ context.Context, _ *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	p.once.Do(func() { close(p.started) })
	<-p.release
	return &bongov1.StageResult{Stage: p.name, Action: "pass"}, nil
}

// TestRunnerShutdownReturnFailureLogsIdentity fills the single worker and its
// buffer so a third envelope is blocked handing off, breaks incoming/, then
// cancels so the return-to-incoming move fails.
func TestRunnerShutdownReturnFailureLogsIdentity(t *testing.T) {
	spool := newTestSpool(t)
	for _, id := range []string{"msg-a", "msg-b", "msg-c"} {
		if err := spool.Enqueue(identityEnv(id)); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	stage := &blockingProcessor{name: "antispam", started: make(chan struct{}), release: make(chan struct{})}
	buf := &lockedBuffer{}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{stage}, jsonLogger(buf),
		WithWorkers(1), WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = runner.Run(ctx) }()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(stage.release) }) }
	t.Cleanup(func() {
		cancel()
		release()
		<-done
	})

	select {
	case <-stage.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never started")
	}
	// msg-c is dequeued (renamed into processing/) and blocked on the full work channel.
	deadline := time.Now().Add(5 * time.Second)
	for !fileExistsIn(t, spool, "processing", "msg-c") {
		if time.Now().After(deadline) {
			t.Fatal("msg-c never reached processing/")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.RemoveAll(filepath.Join(spool.baseDir, "incoming")); err != nil {
		t.Fatal(err)
	}
	cancel()
	line := waitForLog(t, buf, "pipeline: failed to return envelope on shutdown", 5*time.Second)
	release()
	<-done
	assertIdentity(t, line, identityEnv("msg-c"), "spool: move")
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

// firstCallBlockingProcessor blocks only its first Process call; later calls
// return at once so an unfixed runner fails the test instead of hanging it.
type firstCallBlockingProcessor struct {
	name    string
	started chan struct{}
	release chan struct{}
	calls   sync.Mutex
	n       int
}

func (p *firstCallBlockingProcessor) Name() string { return p.name }

func (p *firstCallBlockingProcessor) Process(_ context.Context, _ *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	p.calls.Lock()
	p.n++
	first := p.n == 1
	p.calls.Unlock()
	if first {
		close(p.started)
		<-p.release
	}
	return &bongov1.StageResult{Stage: p.name, Action: "pass"}, nil
}

// TestRunnerShutdownReturnsUnstartedEnvelopes cancels with one envelope in
// flight, one buffered in the work channel and one held by the poll loop. The
// in-flight one completes; the other two must go back to incoming/ rather than
// run through the stages against the shutdown grace.
func TestRunnerShutdownReturnsUnstartedEnvelopes(t *testing.T) {
	spool := newTestSpool(t)
	ids := []string{"msg-a", "msg-b", "msg-c"}
	for _, id := range ids {
		if err := spool.Enqueue(identityEnv(id)); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	stage := &firstCallBlockingProcessor{name: "antispam", started: make(chan struct{}), release: make(chan struct{})}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{stage}, newTestLogger(),
		WithWorkers(1), WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = runner.Run(ctx) }()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(stage.release) }) }
	t.Cleanup(func() {
		cancel()
		release()
		<-done
	})

	select {
	case <-stage.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never started")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		all := true
		for _, id := range ids {
			all = all && fileExistsIn(t, spool, "processing", id)
		}
		if all {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("envelopes never all reached processing/")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}

	var delivered, incoming int
	for _, id := range ids {
		var where []string
		for _, dir := range []string{"incoming", "processing", "deliver", "error", "quarantine"} {
			if fileExistsIn(t, spool, dir, id) {
				where = append(where, dir)
			}
		}
		if len(where) != 1 {
			t.Fatalf("%s is in %v, want exactly one directory", id, where)
		}
		switch where[0] {
		case "deliver":
			delivered++
		case "incoming":
			incoming++
		default:
			t.Fatalf("%s ended in %s", id, where[0])
		}
	}
	if delivered != 1 || incoming != 2 {
		t.Fatalf("delivered=%d incoming=%d, want 1 and 2", delivered, incoming)
	}
}

// TestRunnerShutdownDrainsAllBufferedEnvelopes pins that a worker keeps
// draining the work channel after cancellation. Three workers block in
// Process while three more envelopes sit in the channel buffer; once Run
// returns, none may be left in processing/ (a worker that stopped at the
// first cancelled envelope would strand the rest until RecoverCrashed).
func TestRunnerShutdownDrainsAllBufferedEnvelopes(t *testing.T) {
	spool := newTestSpool(t)
	var ids []string
	for i := range 8 {
		id := fmt.Sprintf("msg-%d", i)
		ids = append(ids, id)
		if err := spool.Enqueue(identityEnv(id)); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	stage := &blockingProcessor{name: "antispam", started: make(chan struct{}), release: make(chan struct{})}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{stage}, newTestLogger(),
		WithWorkers(1), WithPollInterval(10*time.Millisecond))
	// With the default depth (the worker count) every worker moves one
	// buffered envelope before it could return, so the drain loop is only
	// observable with a deeper buffer.
	runner.queueDepth = 4

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = runner.Run(ctx) }()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(stage.release) }) }
	t.Cleanup(func() {
		cancel()
		release()
		<-done
	})

	// One in flight plus four buffered means at least five are in processing/.
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := 0
		for _, id := range ids {
			if fileExistsIn(t, spool, "processing", id) {
				n++
			}
		}
		if n >= 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d envelopes reached processing/, want at least 5", n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}

	var delivered, incoming int
	for _, id := range ids {
		var where []string
		for _, dir := range []string{"incoming", "processing", "deliver", "error", "quarantine"} {
			if fileExistsIn(t, spool, dir, id) {
				where = append(where, dir)
			}
		}
		if len(where) != 1 {
			t.Fatalf("%s is in %v, want exactly one directory", id, where)
		}
		switch where[0] {
		case "deliver":
			delivered++
		case "incoming":
			incoming++
		default:
			t.Fatalf("%s ended in %s, want deliver or incoming", id, where[0])
		}
	}
	if delivered > 1 || delivered+incoming != len(ids) {
		t.Fatalf("delivered=%d incoming=%d, want at most 1 delivered and the rest back in incoming/", delivered, incoming)
	}
}
