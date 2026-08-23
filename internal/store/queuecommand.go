package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cockroachdb/pebble"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// queueMovePrefix marks a synthetic queue-move command, following the same
// convention as the mailbox prefixes above.
const queueMovePrefix = "/__queue_move__/"

// queueMovePayload carries one atomic queue transition: drop the message from
// the key it currently occupies and write it at its new key.
//
// Keys are passed as opaque bytes rather than rebuilt here. The queue package
// owns its key layout (/queue/pending/{ts}-{id}, /queue/bounced/{id}); this
// package only guarantees the two writes land together.
type queueMovePayload struct {
	OldKey []byte `json:"old_key"`
	NewKey []byte `json:"new_key"`
	Value  []byte `json:"value"`
}

// BuildQueueMoveCommand builds the synthetic command that moves a queued
// message from oldKey to newKey with the given serialised value.
//
// This exists because the queue's retry, bounce and retry-now transitions were
// each two separate ApplyCommand calls -- delete the old key, then put the new
// one. A crash or a Raft rejection between the two removed the message from the
// queue without writing it back anywhere: silent mail loss, no error, nothing
// to retry. That is exactly the case CLAUDE.md's guardrail on multi-step Raft
// mutations warns about, and the fix it names first is a composite command.
//
// oldKey may be empty for a transition that only writes (a fresh enqueue).
func BuildQueueMoveCommand(id string, oldKey, newKey, value []byte) (*bongov1.RaftCommand, error) {
	if id == "" {
		return nil, fmt.Errorf("queue move requires a message id")
	}
	if len(newKey) == 0 {
		return nil, fmt.Errorf("queue move requires a new key")
	}
	payload, err := json.Marshal(queueMovePayload{
		OldKey: oldKey,
		NewKey: newKey,
		Value:  value,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling queue move payload: %w", err)
	}
	return &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(queueMovePrefix + id),
		Value: payload,
	}, nil
}

// IsQueueSyntheticCommand reports whether the key represents a synthetic queue
// operation handled directly by the FSM.
func IsQueueSyntheticCommand(key []byte) bool {
	return strings.HasPrefix(string(key), queueMovePrefix)
}

// ApplyQueueCommand applies a synthetic queue command to the local Pebble
// store. The delete and the put share one batch committed with pebble.Sync, so
// a crash either leaves the message wholly at its old key (and the transition
// is retried) or wholly at its new one. It is never at neither.
func ApplyQueueCommand(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	if !IsQueueSyntheticCommand(cmd.Key) {
		return fmt.Errorf("not a synthetic queue command")
	}

	var payload queueMovePayload
	if err := json.Unmarshal(cmd.Value, &payload); err != nil {
		return fmt.Errorf("parsing queue move payload: %w", err)
	}
	if len(payload.NewKey) == 0 {
		return fmt.Errorf("queue move payload missing new key")
	}

	return commitSyncBatch(ps, func(batch *pebble.Batch) error {
		// Delete first, then write. Within one batch the order does not affect
		// the committed result, but it does when old and new keys are equal --
		// a retry that lands in the same backoff second, say. Deleting first
		// means the put wins rather than the message vanishing.
		if len(payload.OldKey) > 0 {
			if err := batch.Delete(payload.OldKey, nil); err != nil {
				return fmt.Errorf("deleting old queue key: %w", err)
			}
		}
		if err := batch.Set(payload.NewKey, payload.Value, nil); err != nil {
			return fmt.Errorf("writing new queue key: %w", err)
		}
		return nil
	})
}
