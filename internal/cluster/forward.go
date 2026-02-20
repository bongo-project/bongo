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
