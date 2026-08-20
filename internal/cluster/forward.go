package cluster

import (
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// ErrNotLeader is returned when a write is attempted on a non-leader node.
var ErrNotLeader = errors.New("not the raft leader")

// LeaderInfo contains the leader's address for forwarding.
type LeaderInfo struct {
	Address raft.ServerAddress
	ID      raft.ServerID
}

// ApplyCommand applies a protobuf command through Raft consensus.
//
// IMPORTANT: timeout is NOT an upper bound on this call. hashicorp/raft uses
// the value passed to Apply only to bound how long it will wait to ENQUEUE the
// log entry; the following future.Error() then waits with no deadline until the
// entry is committed and the FSM has applied it. A slow FSM.Apply therefore
// blocks every caller for as long as it takes, whatever timeout says.
//
// This is not merely pedantic: a 5s value at the store.DeliverMail call site
// read as a guarantee, and an 85s FSM apply (opening a user's derived-index
// handles) stalled mail delivery while appearing bounded. Derived indexing has
// since moved off the apply path (FSM.StartIndexer), but the semantics here are
// unchanged, so keep FSM.Apply fast.
//
// A real deadline is deliberately NOT enforced by abandoning future.Error():
// the entry may still commit afterwards, so returning an error would tell the
// caller a write failed that in fact succeeded, and a retry would duplicate it.
func ApplyCommand(r *raft.Raft, cmd *bongov1.RaftCommand, timeout time.Duration) error {
	if r.State() != raft.Leader {
		addr, id := r.LeaderWithID()
		return &NotLeaderError{
			LeaderAddr: addr,
			LeaderID:   id,
		}
	}

	data, err := proto.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("encoding command: %w", err)
	}

	future := r.Apply(data, timeout)
	if err := future.Error(); err != nil {
		return fmt.Errorf("raft apply: %w", err)
	}

	// Check the FSM response for errors.
	resp := future.Response()
	if err, ok := resp.(error); ok {
		return fmt.Errorf("fsm apply: %w", err)
	}

	return nil
}

// NotLeaderError is returned when a write is sent to a non-leader node.
type NotLeaderError struct {
	LeaderAddr raft.ServerAddress
	LeaderID   raft.ServerID
}

func (e *NotLeaderError) Error() string {
	if e.LeaderAddr != "" {
		return fmt.Sprintf("not the raft leader, leader is %s at %s", e.LeaderID, e.LeaderAddr)
	}
	return "not the raft leader, leader unknown"
}

func (e *NotLeaderError) Is(target error) bool {
	return target == ErrNotLeader
}
