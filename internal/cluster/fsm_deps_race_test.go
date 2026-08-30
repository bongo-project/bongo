package cluster

import (
	"context"
	"sync"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
	"google.golang.org/protobuf/proto"
)

// raceBlobEnqueuer is a no-op BlobEnqueuer for the concurrency test.
type raceBlobEnqueuer struct{}

func (raceBlobEnqueuer) Enqueue(hash string) {}

// TestFSMDependencySettersAreRaceFree covers a real data race: Agent.Run
// injects the search indexer, blob syncer and notifier AFTER handing the FSM to
// Raft, so Raft's apply goroutine can already be reading those fields while Run
// is still writing them.
//
// It surfaced as `SetBlobSyncer` racing with `runDerivedIndexes` in CI, and it
// is a production race, not a test artifact: the same interleaving happens on
// every real startup that applies Raft entries while Run is still wiring
// dependencies.
//
// Meaningful only under -race, which is how the suite runs.
func TestFSMDependencySettersAreRaceFree(t *testing.T) {
	env := newFSMTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meta := &bongov1.MailMeta{
		Subject:    "race",
		From:       "sender@example.com",
		RawMessage: &bongov1.BlobRef{Hash: "abc123", Size: 1},
	}
	val, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/race-user/mail/guid-1"),
		Value: val,
	}

	// The writer must still be writing WHILE the reader reads. A fixed
	// iteration count does not achieve that: three pointer writes take
	// microseconds while one updateDerivedIndexes does SQLite work in the
	// milliseconds, so the writer finishes long before the reader starts and
	// the two never overlap -- the detector then sees no concurrent access and
	// the test passes against unsynchronised fields. Driving the writer off the
	// reader's completion keeps them overlapping for the whole run.
	var wg sync.WaitGroup
	done := make(chan struct{})

	// Writer: the Agent.Run side, injecting dependencies.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			env.fsm.SetBlobSyncer(raceBlobEnqueuer{})
			env.fsm.SetSearchManager(nil)
			env.fsm.SetNotifier(nil)
			// StartIndexer assigns indexQueue, which the apply path reads on
			// every command. It was missed by the first pass of this fix and
			// raced in CI on its own, so it belongs in the same test. Repeat
			// calls after the first are no-ops, which is the point: the check
			// and the assignment must be atomic together.
			env.fsm.StartIndexer(ctx)
		}
	}()

	// Reader: the Raft apply side, reading them on every command.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < 200; i++ {
			env.fsm.updateDerivedIndexes(cmd)
		}
	}()

	wg.Wait()
}
