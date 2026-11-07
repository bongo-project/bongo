package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/bongo-project/bongo/internal/observability"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestIndexBacklogGaugeTracksQueue holds the worker inside one index call and
// checks the gauge counts only what is still waiting behind it, then returns
// to zero once the worker drains.
func TestIndexBacklogGaugeTracksQueue(t *testing.T) {
	env := newFSMTestEnv(t)
	idx := newSlowIndexer()
	env.fsm.SetSearchManager(idx)

	ctx, cancel := context.WithCancel(context.Background())
	env.fsm.StartIndexer(ctx)
	released := false
	release := func() {
		if !released {
			released = true
			close(idx.release)
		}
	}
	t.Cleanup(func() {
		release()
		cancel()
		env.fsm.StopIndexer()
	})

	env.fsm.Apply(mailPutLog(t, 1, "user-m", "g1"))
	deadline := time.Now().Add(5 * time.Second)
	for idx.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("worker never picked up the first item")
		}
		time.Sleep(time.Millisecond)
	}
	// The worker is blocked now, so nothing dequeues until release.
	env.fsm.Apply(mailPutLog(t, 2, "user-m", "g2"))
	env.fsm.Apply(mailPutLog(t, 3, "user-m", "g3"))

	if got := testutil.ToFloat64(observability.SearchIndexBacklog); got != 2 {
		t.Fatalf("backlog gauge = %v with two items waiting, want 2", got)
	}

	release()
	if !env.fsm.WaitIdle(10 * time.Second) {
		t.Fatal("backlog did not drain")
	}
	// WaitIdle returns when the last item finishes; the worker sets the gauge
	// right after dequeuing, which is earlier, so no extra wait is needed.
	if got := testutil.ToFloat64(observability.SearchIndexBacklog); got != 0 {
		t.Fatalf("backlog gauge = %v after drain, want 0", got)
	}
}

// TestApplyCommandObservesRaftApplySeconds checks the histogram counts leader
// writes, including one the FSM rejects, since the caller waited for both.
func TestApplyCommandObservesRaftApplySeconds(t *testing.T) {
	node, _ := newLeaderNode(t)
	n0 := raftApplySamples(t)

	if err := ApplyCommand(node.Raft, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/test/apply-seconds"),
		Value: []byte("v"),
	}, 5*time.Second); err != nil {
		t.Fatalf("ApplyCommand: %v", err)
	}
	if got := raftApplySamples(t); got != n0+1 {
		t.Fatalf("sample count = %d, want %d", got, n0+1)
	}

	if err := ApplyCommand(node.Raft, &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_UNSPECIFIED,
		Key: []byte("/test/apply-seconds-err"),
	}, 5*time.Second); err == nil {
		t.Fatal("expected an FSM error")
	}
	if got := raftApplySamples(t); got != n0+2 {
		t.Fatalf("sample count after FSM error = %d, want %d", got, n0+2)
	}
}

func raftApplySamples(t *testing.T) uint64 {
	t.Helper()
	var m dto.Metric
	if err := observability.RaftApplySeconds.Write(&m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

func fsmApplySamples(t *testing.T) uint64 {
	t.Helper()
	var m dto.Metric
	if err := observability.RaftFSMApplySeconds.Write(&m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

// TestApplyCommandObservesFSMApplyAndLag checks the FSM timer counts one sample
// per applied command, and that the lag gauge, wired to the real node, settles
// at zero once a write has been applied.
func TestApplyCommandObservesFSMApplyAndLag(t *testing.T) {
	node, _ := newLeaderNode(t)
	clear := observability.SetRaftIndexSource(func() (uint64, uint64) {
		return node.Raft.LastIndex(), node.Raft.AppliedIndex()
	})
	defer clear()

	n0 := fsmApplySamples(t)
	const writes = 5
	for i := range writes {
		if err := ApplyCommand(node.Raft, &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/test/fsm-apply-%d", i)),
			Value: []byte("v"),
		}, 5*time.Second); err != nil {
			t.Fatalf("ApplyCommand: %v", err)
		}
	}
	if got := fsmApplySamples(t); got != n0+writes {
		t.Fatalf("fsm apply samples = %d, want %d", got, n0+writes)
	}

	deadline := time.Now().Add(5 * time.Second)
	for testutil.ToFloat64(observability.RaftCommitLagEntries) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("lag = %v after writes were applied, want 0", testutil.ToFloat64(observability.RaftCommitLagEntries))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
