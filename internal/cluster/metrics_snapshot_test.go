package cluster

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/bongo-project/bongo/internal/observability"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestIndexBacklogGaugeReturnsToZeroAfterBurst applies many deferrable
// commands while the worker runs, so the apply goroutine's increments and the
// worker's decrements interleave freely. With Set(len(queue)) from both sides
// a stale value could stick; with Inc and Dec the gauge must end where it
// began. Whether the worker keeps up varies, so the blocked-worker test is
// what proves the enqueue side moves the gauge.
func TestIndexBacklogGaugeReturnsToZeroAfterBurst(t *testing.T) {
	env := newFSMTestEnv(t)
	start := testutil.ToFloat64(observability.SearchIndexBacklog)

	ctx, cancel := context.WithCancel(context.Background())
	env.fsm.StartIndexer(ctx)
	t.Cleanup(func() {
		cancel()
		env.fsm.StopIndexer()
	})

	for i := range 200 {
		env.fsm.Apply(mailPutLog(t, uint64(i+1), "user-b", fmt.Sprintf("g%d", i)))
	}
	if !env.fsm.WaitIdle(30 * time.Second) {
		t.Fatal("backlog did not drain")
	}
	if got := testutil.ToFloat64(observability.SearchIndexBacklog); got != start {
		t.Fatalf("backlog gauge = %v after drain, want %v", got, start)
	}
}

// snapshotPhaseSamples reads the sample count of one bongo_raft_snapshot_seconds
// phase from the registry.
func snapshotPhaseSamples(t *testing.T, phase string) uint64 {
	t.Helper()
	families, err := observability.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != "bongo_raft_snapshot_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "phase" && l.GetValue() == phase {
					return m.GetHistogram().GetSampleCount()
				}
			}
		}
	}
	t.Fatalf("no bongo_raft_snapshot_seconds{phase=%q}", phase)
	return 0
}

// TestSnapshotMetrics checks a snapshot observes both phases and reports the
// size of what the sink received.
func TestSnapshotMetrics(t *testing.T) {
	env := newFSMTestEnv(t)
	for i := range 20 {
		env.fsm.Apply(makeProtoLog(uint64(i+1), &bongov1.RaftCommand{
			Op: bongov1.Op_OP_PUT, Key: []byte(fmt.Sprintf("/k/%d", i)), Value: []byte("value"),
		}))
	}
	cp0 := snapshotPhaseSamples(t, "checkpoint")
	ps0 := snapshotPhaseSamples(t, "persist")
	observability.RaftSnapshotBytes.Set(0)

	snap, err := env.fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	defer snap.Release()
	var buf bytes.Buffer
	if err := snap.Persist(&bufferSink{buf: &buf}); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	if got := snapshotPhaseSamples(t, "checkpoint"); got != cp0+1 {
		t.Errorf("checkpoint samples = %d, want %d", got, cp0+1)
	}
	if got := snapshotPhaseSamples(t, "persist"); got != ps0+1 {
		t.Errorf("persist samples = %d, want %d", got, ps0+1)
	}
	if got := testutil.ToFloat64(observability.RaftSnapshotBytes); buf.Len() == 0 || got != float64(buf.Len()) {
		t.Errorf("snapshot bytes gauge = %v, sink received %d", got, buf.Len())
	}
}
