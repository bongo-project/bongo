package antivirus

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/pipeline"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// These tests drive a real ClamAVProcessor through pipeline.Runner rather
// than calling Process directly, because the behaviour under test —
// quarantine vs error vs deliver — is runner behaviour, not processor
// behaviour: the processor only ever returns a StageResult, never a Go
// error, and it is the runner that decides which spool directory a
// StageResult's Action routes to (see runner.go, processMessage). A
// processor-only test cannot distinguish "the fix works" from "the fix
// silently regressed back to the old spool/error path", because both would
// look identical from Process()'s return value.
//
// processMessage itself is unexported, so these tests drive the public
// Run(ctx) entry point with a short poll interval and poll the spool
// directories (via the exported ReadEnvelope) for the outcome, the same
// pattern runner_test.go's TestRunnerGracefulShutdown uses in-package.

const dummyRFC5322 = "From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: test\r\n\r\nHello, world.\r\n"

// integrationBlobReader implements pipeline.BlobReader, returning a fixed
// RFC 5322 message body for any hash.
type integrationBlobReader struct{}

func (integrationBlobReader) ReadBlob(_ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader([]byte(dummyRFC5322))), nil
}

func newIntegrationSpool(t *testing.T) *pipeline.SpoolStore {
	t.Helper()
	spool, err := pipeline.NewSpoolStore(t.TempDir(), silentLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	return spool
}

func enqueueTestEnvelope(t *testing.T, spool *pipeline.SpoolStore, id string) *bongov1.QueueEnvelope {
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

// inSpoolDir reports whether id has an envelope file in the given spool
// subdirectory, using only the SpoolStore's exported ReadEnvelope — the
// runner package's own internal-field test helper (fileExistsIn) is
// unavailable here across the package boundary.
func inSpoolDir(spool *pipeline.SpoolStore, dir, id string) bool {
	_, err := spool.ReadEnvelope(dir, id)
	return err == nil
}

// unreachableSocketPath returns a unix socket path with no listener, inside
// a short os.MkdirTemp directory (not t.TempDir(), whose path embeds the
// full subtest name) so the 108-byte sun_path limit is never at risk.
func unreachableSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "av")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "absent.sock")
}

// runToCompletion starts runner.Run against spool and blocks until id
// reaches deliver/, quarantine/, or error/ (or the deadline expires), then
// cancels the runner and waits for Run to return cleanly.
func runToCompletion(t *testing.T, spool *pipeline.SpoolStore, procs []pipeline.Processor, id string) {
	t.Helper()

	runner := pipeline.NewRunner(spool, integrationBlobReader{}, procs, silentLogger(),
		pipeline.WithWorkers(1),
		pipeline.WithPollInterval(10*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if inSpoolDir(spool, "deliver", id) || inSpoolDir(spool, "quarantine", id) || inSpoolDir(spool, "error", id) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancellation")
	}
}

// TestRunnerQuarantinesOnScannerFailure asserts that an unreachable clamd
// with the default (quarantine) policy lands the message in quarantine/, and
// explicitly NOT in error/ or deliver/. Before the wiring fix, a stage error
// took the runner's error/ path — a directory nothing reads or surfaces in
// any UI, silently stranding inbound mail. This test would have failed
// against that behaviour, which is what makes it a real regression guard
// rather than a vacuous one.
func TestRunnerQuarantinesOnScannerFailure(t *testing.T) {
	spool := newIntegrationSpool(t)
	env := enqueueTestEnvelope(t, spool, "msg-quarantine")

	proc := NewClamAVProcessor(
		WithSocket(unreachableSocketPath(t)),
		WithTimeout(2*time.Second),
		WithLogger(silentLogger()),
	)

	runToCompletion(t, spool, []pipeline.Processor{proc}, env.Id)

	if !inSpoolDir(spool, "quarantine", env.Id) {
		t.Error("expected envelope in quarantine/, not found")
	}
	if inSpoolDir(spool, "error", env.Id) {
		t.Error("envelope must not be in error/ — a scanner outage is not a pipeline fault, it is a scan verdict")
	}
	if inSpoolDir(spool, "deliver", env.Id) {
		t.Error("envelope must not be in deliver/ — default policy is quarantine, not deliver-unscanned")
	}
}

// TestRunnerAcceptsOnScannerFailureWhenConfigured proves on_failure=accept
// delivers the message unscanned rather than quarantining it.
func TestRunnerAcceptsOnScannerFailureWhenConfigured(t *testing.T) {
	spool := newIntegrationSpool(t)
	env := enqueueTestEnvelope(t, spool, "msg-accept")

	proc := NewClamAVProcessor(
		WithSocket(unreachableSocketPath(t)),
		WithTimeout(2*time.Second),
		WithLogger(silentLogger()),
		WithOnFailure(OnFailureAccept),
	)

	runToCompletion(t, spool, []pipeline.Processor{proc}, env.Id)

	if !inSpoolDir(spool, "deliver", env.Id) {
		t.Error("expected envelope in deliver/ under on_failure=accept")
	}
	if inSpoolDir(spool, "quarantine", env.Id) {
		t.Error("envelope must not be in quarantine/ under on_failure=accept")
	}
}

// TestRunnerRescanRoundTrip proves quarantine-for-scanner-failure is actually
// recoverable, which is the entire justification for choosing quarantine over
// deliver-unscanned as the default. A message is quarantined by an
// unreachable clamd, drained via pipeline.PrepareForRescan and re-enqueued,
// then re-run against a live mock clamd — and must actually be scanned the
// second time (not skipped by the idempotency check, proven via the mock's
// connection WaitGroup) and reach deliver/.
func TestRunnerRescanRoundTrip(t *testing.T) {
	spool := newIntegrationSpool(t)
	env := enqueueTestEnvelope(t, spool, "msg-rescan")

	// Pass 1: clamd unreachable, default policy quarantines.
	unreachable := NewClamAVProcessor(
		WithSocket(unreachableSocketPath(t)),
		WithTimeout(2*time.Second),
		WithLogger(silentLogger()),
	)
	runToCompletion(t, spool, []pipeline.Processor{unreachable}, env.Id)

	if !inSpoolDir(spool, "quarantine", env.Id) {
		t.Fatalf("expected envelope in quarantine/ after pass 1")
	}

	quarantined, err := spool.ReadEnvelope("quarantine", env.Id)
	if err != nil {
		t.Fatalf("ReadEnvelope quarantine: %v", err)
	}

	// Drain: clear scanning flags/results and re-enqueue to incoming/.
	pipeline.PrepareForRescan(quarantined)
	if pipeline.HasFlag(quarantined.Flags, pipeline.MsgFlagVirusChecked) {
		t.Fatal("PrepareForRescan did not clear MsgFlagVirusChecked; rescan would be skipped as already-checked")
	}
	if err := spool.Remove("quarantine", quarantined.Id); err != nil {
		t.Fatalf("Remove quarantine: %v", err)
	}
	if err := spool.Enqueue(quarantined); err != nil {
		t.Fatalf("Enqueue for rescan: %v", err)
	}

	// Pass 2: live mock clamd, clean verdict — prove the stage actually ran
	// again (not skipped) via the mock's connection WaitGroup.
	addr, wg := mockClamd(t, "stream: OK")
	host, port := splitHostPort(t, addr)
	live := NewClamAVProcessor(
		WithTCP(host, port),
		WithTimeout(5*time.Second),
		WithLogger(silentLogger()),
	)
	runToCompletion(t, spool, []pipeline.Processor{live}, env.Id)
	waitOrFail(t, wg, "clamd was never connected to on the rescan pass — the antivirus stage was skipped instead of re-running")

	if !inSpoolDir(spool, "deliver", env.Id) {
		t.Error("expected envelope in deliver/ after rescan against a live clamd")
	}
	if inSpoolDir(spool, "quarantine", env.Id) {
		t.Error("envelope must not remain in quarantine/ after a successful rescan")
	}
}

// waitOrFail waits for the mock clamd's connection WaitGroup with a bound.
//
// A bare wg.Wait() here is a trap: the WaitGroup is only released when the
// mock's Accept returns, and if the stage never connects — the exact
// regression these tests guard — Accept blocks until the t.Cleanup listener
// close, which runs only AFTER the test body. The failure then surfaces as a
// package-wide test-binary timeout panic minutes later, rather than as the
// assertion that actually failed. Bounding it keeps a regression legible.
func waitOrFail(t *testing.T, wg *sync.WaitGroup, msg string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal(msg)
	}
}
