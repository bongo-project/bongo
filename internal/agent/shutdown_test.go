package agent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/pipeline"
	bongopop3 "github.com/bongo-project/bongo/internal/pop3"
)

type nopBlobReader struct{}

func (nopBlobReader) ReadBlob(string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("x")), nil
}

// blockingStage signals entered, waits for release, then reads the store the
// way the deliver stage does and records any panic.
type blockingStage struct {
	a        *Agent
	log      *slog.Logger
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
	mu       sync.Mutex
	panicked any
	applyErr error
}

func (s *blockingStage) Name() string { return "blocker" }

func (s *blockingStage) Process(ctx context.Context, env *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	func() {
		defer func() {
			if r := recover(); r != nil {
				s.mu.Lock()
				s.panicked = r
				s.mu.Unlock()
			}
		}()
		_, _ = s.a.Store.Get([]byte("/user/none/profile"))
	}()
	// The deliver stage writes through Raft; shutting Raft down first would
	// fail exactly this call.
	err := s.a.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/test/shutdown-probe"), Value: []byte("x"),
	}, 5*time.Second)
	s.mu.Lock()
	s.applyErr = err
	s.mu.Unlock()
	s.log.Info("STAGE_RETURNED")
	return &bongov1.StageResult{Action: "pass"}, nil
}

func (s *blockingStage) panic() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.panicked
}

func (s *blockingStage) applyError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyErr
}

func newShutdownAgent(t *testing.T) (*Agent, *blockingStage, *syncBuffer, string) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	buf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, err := NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	for i := 0; i < 200 && !a.IsLeader(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.IsLeader() {
		t.Fatal("agent did not acquire leadership")
	}
	spoolDir := t.TempDir()
	spool, err := pipeline.NewSpoolStore(spoolDir, logger)
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	if err := spool.Enqueue(&bongov1.QueueEnvelope{Id: "msg-shutdown", Sender: "a@example.com", BlobHash: "deadbeef"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	stage := &blockingStage{a: a, log: logger, entered: make(chan struct{}), release: make(chan struct{})}
	a.PipelineRunner = pipeline.NewRunner(spool, nopBlobReader{}, []pipeline.Processor{stage}, logger,
		pipeline.WithWorkers(1), pipeline.WithPollInterval(10*time.Millisecond))
	return a, stage, buf, spoolDir
}

func startAndWaitEntered(t *testing.T, a *Agent, stage *blockingStage) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a.pipelineCancel = cancel
	a.startPipelineRunner(ctx)
	select {
	case <-stage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("stage never entered")
	}
}

func TestShutdownWaitsForInFlightPipelineMessage(t *testing.T) {
	a, stage, buf, _ := newShutdownAgent(t)
	startAndWaitEntered(t, a, stage)

	result := make(chan error, 1)
	go func() { result <- a.Shutdown() }()

	select {
	case err := <-result:
		close(stage.release)
		t.Fatalf("Shutdown returned (err=%v) while a message was in flight", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(stage.release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not return after release")
	}

	if p := stage.panic(); p != nil {
		t.Fatalf("stage panicked reading the store: %v", p)
	}
	if err := stage.applyError(); err != nil {
		t.Fatalf("Raft write after release failed (Raft shut down before the wait?): %v", err)
	}
	out := buf.String()
	stageIdx := strings.Index(out, "STAGE_RETURNED")
	stopIdx := strings.Index(out, "pipeline runner stopped")
	if stageIdx < 0 || stopIdx < 0 {
		t.Fatalf("missing log lines (stage=%d stopped=%d)", stageIdx, stopIdx)
	}
	if stopIdx < stageIdx {
		t.Error("'pipeline runner stopped' logged before the in-flight message finished")
	}
}

func TestShutdownGraceExceededLeavesEnvelopeAndStoresOpen(t *testing.T) {
	a, stage, _, spoolDir := newShutdownAgent(t)
	a.shutdownGrace = 200 * time.Millisecond
	// A non-nil POP3 server makes the retry after the grace timeout reach the
	// listener-close guard: closing it twice would panic on closeCh.
	a.POP3 = bongopop3.NewServer(nil, nil, nil, nil, nil, false, slog.New(slog.DiscardHandler))
	startAndWaitEntered(t, a, stage)
	t.Cleanup(func() {
		close(stage.release)
		<-a.pipelineDone
		// A second Shutdown, now that nothing is in flight, must complete.
		if err := a.Shutdown(); err != nil {
			t.Errorf("second Shutdown: %v", err)
		}
	})

	start := time.Now()
	err := a.Shutdown()
	if err == nil {
		t.Fatal("Shutdown returned nil after the grace expired")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Shutdown took %v, want it bounded by the grace", d)
	}
	if _, statErr := os.Stat(filepath.Join(spoolDir, "processing", "msg-shutdown.env")); statErr != nil {
		t.Errorf("envelope should remain in processing/: %v", statErr)
	}
	if _, getErr := a.Store.Get([]byte("/user/none/profile")); getErr != nil && strings.Contains(getErr.Error(), "closed") {
		t.Errorf("store was closed despite a running message: %v", getErr)
	}
	if applyErr := a.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/test/grace-probe"), Value: []byte("x"),
	}, 5*time.Second); applyErr != nil {
		t.Errorf("Raft was shut down despite a running message: %v", applyErr)
	}
}

func TestShutdownGraceErrorNamesRunningWorkers(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	a.shutdownGrace = 200 * time.Millisecond
	release := make(chan struct{})
	a.goWorker("stuck-worker", func() { <-release })
	a.goWorker("finished-worker", func() {})
	t.Cleanup(func() {
		close(release)
		a.bgWG.Wait()
		if err := a.Shutdown(); err != nil {
			t.Errorf("second Shutdown: %v", err)
		}
	})

	err = a.Shutdown()
	if err == nil {
		t.Fatal("Shutdown returned nil with a stuck worker")
	}
	if !strings.Contains(err.Error(), "stuck-worker") {
		t.Errorf("error does not name the running worker: %v", err)
	}
	if strings.Contains(err.Error(), "finished-worker") {
		t.Errorf("error names a worker that already returned: %v", err)
	}
}

func TestShutdownWaitsForBackgroundWorker(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	release := make(chan struct{})
	finished := make(chan struct{})
	a.goWorker("test-worker", func() {
		<-release
		close(finished)
	})

	result := make(chan error, 1)
	go func() { result <- a.Shutdown() }()
	select {
	case <-result:
		close(release)
		t.Fatal("Shutdown returned while a background worker was running")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not return")
	}
	<-finished
}

// Run returned early without cancelling its ctx (integration helpers do this):
// Shutdown must still stop the real workers well inside the grace and close
// everything, which needs the orphan-GC and usage cancels and worker tracking.
func TestShutdownStopsRealWorkersWithoutCtxCancel(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.ControlAddr = freeControlAddr(t)
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	a.shutdownGrace = 5 * time.Second

	// ctx is deliberately never cancelled: stopping the HTTP server makes Run
	// return through its own Shutdown, like the integration helpers do.
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()
	// The dev agent binds fixed ports; if the test fails before Run returns,
	// stop it so later dev-agent tests do not hit "address already in use".
	runDrained := false
	t.Cleanup(func() {
		if runDrained {
			return
		}
		_ = a.Shutdown()
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("Run did not return during cleanup")
		}
	})
	for i := 0; i < 200 && !a.Ready(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent not ready")
	}
	if a.orphanGCCancel == nil || a.usageCancel == nil {
		t.Fatal("orphan GC / usage recompute lack their own cancel")
	}
	// Queue worker, reminder checker, blob sync, orphan GC, usage recompute.
	if n := a.bgStarted.Load(); n < 5 {
		t.Fatalf("only %d workers tracked through goWorker, want at least 5", n)
	}

	start := time.Now()
	if err := a.httpServer.Close(); err != nil {
		t.Fatalf("closing http server: %v", err)
	}
	select {
	case err := <-runDone:
		runDrained = true
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("Shutdown took %v, workers were not cancelled", d)
	}
}

// TestShutdownIsIdempotent runs a dev agent with POP3 live and calls Shutdown
// twice, as Run's own deferred teardown and an integration helper both do. The
// second call used to close the POP3 server's channel again and panic.
func TestShutdownIsIdempotent(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.ControlAddr = freeControlAddr(t)
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	a.shutdownGrace = 5 * time.Second

	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()
	for i := 0; i < 200 && !a.Ready(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		_ = a.Shutdown()
		t.Fatal("agent not ready")
	}
	if a.POP3 == nil {
		_ = a.Shutdown()
		t.Fatal("POP3 server not running; the test would not exercise the double close")
	}

	if err := a.Shutdown(); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := a.Shutdown(); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return")
	}
}

// TestShutdownGraceTimeoutIsNotTerminal pins the retry semantics: a grace
// timeout must not mark shutdown complete, so a later call still waits for the
// workers (and reports the same error) until they finish, and only then
// tears the stores down.
func TestShutdownGraceTimeoutIsNotTerminal(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	a.shutdownGrace = 100 * time.Millisecond
	release := make(chan struct{})
	a.goWorker("stuck-worker", func() { <-release })
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		a.bgWG.Wait()
		_ = a.Shutdown()
	})

	for i := range 2 {
		err := a.Shutdown()
		if err == nil {
			t.Fatalf("Shutdown call %d returned nil while a worker was still running", i+1)
		}
		if !strings.Contains(err.Error(), "stuck-worker") {
			t.Errorf("Shutdown call %d error does not name the worker: %v", i+1, err)
		}
		if a.shutdownDone {
			t.Fatalf("shutdownDone set after grace timeout on call %d", i+1)
		}
	}

	close(release)
	a.bgWG.Wait()
	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown after the worker finished: %v", err)
	}
	if !a.shutdownDone {
		t.Error("shutdownDone not set after a completed teardown")
	}
}
