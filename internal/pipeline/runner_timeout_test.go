package pipeline

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ctxStage is a Processor whose behaviour is supplied by a function.
type ctxStage struct {
	name string
	fn   func(ctx context.Context) (*bongov1.StageResult, error)
}

func (s *ctxStage) Name() string { return s.name }

func (s *ctxStage) Process(ctx context.Context, _ *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	return s.fn(ctx)
}

// TestRunnerMessageTimeoutMovesToError pins the per-message budget: a stage that
// blocks until its context ends must be released at the deadline, the envelope
// must land in error/, and the log must say the budget was the cause.
func TestRunnerMessageTimeoutMovesToError(t *testing.T) {
	spool := newTestSpool(t)
	env := newTestEnv(t, spool, "msg-slow")

	stage := &ctxStage{name: "antispam", fn: func(ctx context.Context) (*bongov1.StageResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	buf := &lockedBuffer{}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{stage}, jsonLogger(buf),
		WithMessageTimeout(100*time.Millisecond))

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- runner.processMessage(context.Background(), dequeued) }()

	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("processMessage still blocked 2s after a 100ms message budget")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want one wrapping context.DeadlineExceeded", err)
	}
	if !fileExistsIn(t, spool, "error", env.Id) {
		t.Error("envelope not moved to error/")
	}
	if fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("timed-out envelope must not reach deliver/")
	}
	line := waitForLog(t, buf, "pipeline: message exceeded time budget", time.Second)
	if line["id"] != env.Id {
		t.Errorf("budget log id = %v, want %q", line["id"], env.Id)
	}
}

// TestRunnerMessageTimeoutDoesNotAffectQuickMessages is the paired control: the
// same budget leaves a fast stage alone, and the deadline is shared across
// stages rather than reset for each.
func TestRunnerMessageTimeoutDoesNotAffectQuickMessages(t *testing.T) {
	spool := newTestSpool(t)
	env := newTestEnv(t, spool, "msg-fast")

	var deadlines []time.Time
	mk := func(name string) *ctxStage {
		return &ctxStage{name: name, fn: func(ctx context.Context) (*bongov1.StageResult, error) {
			d, ok := ctx.Deadline()
			if !ok {
				t.Error("stage context has no deadline")
			}
			deadlines = append(deadlines, d)
			return &bongov1.StageResult{Stage: name, Action: "pass"}, nil
		}}
	}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{mk("antispam"), mk("rules")}, newTestLogger(),
		WithMessageTimeout(time.Minute))

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if err := runner.processMessage(context.Background(), dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	if !fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("envelope not in deliver/")
	}
	if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
		t.Errorf("deadlines %v: want one shared per-message deadline", deadlines)
	}
}

// TestRunnerShutdownDoesNotCancelInFlightMessage pins that cancelling the Run
// context (shutdown) does not reach the stage, so an in-flight message still
// completes and is not pushed into error/.
func TestRunnerShutdownDoesNotCancelInFlightMessage(t *testing.T) {
	spool := newTestSpool(t)
	env := newTestEnv(t, spool, "msg-shutdown")

	var stageCtxErr error
	stage := &ctxStage{name: "antispam", fn: func(ctx context.Context) (*bongov1.StageResult, error) {
		stageCtxErr = ctx.Err()
		return &bongov1.StageResult{Stage: "antispam", Action: "pass"}, nil
	}}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{stage}, newTestLogger())

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runner.processMessage(runCtx, dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	if stageCtxErr != nil {
		t.Errorf("stage saw ctx error %v after shutdown, want nil", stageCtxErr)
	}
	if !fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("in-flight message not delivered after shutdown signal")
	}
}

func TestWithMessageTimeoutIgnoresNonPositive(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		r := NewRunner(nil, nil, nil, newTestLogger(), WithMessageTimeout(d))
		if r.messageTimeout != defaultMessageTimeout {
			t.Errorf("WithMessageTimeout(%v) set %v, want default %v", d, r.messageTimeout, defaultMessageTimeout)
		}
	}
	if defaultMessageTimeout != 2*time.Minute {
		t.Errorf("default message timeout = %v, want 2m", defaultMessageTimeout)
	}
}
