package store

import (
	"errors"
	"testing"

	"github.com/cockroachdb/pebble"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newQueueCommandStore(t *testing.T) *PebbleStore {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return ps
}

// TestApplyQueueCommandMovesAtomically is the happy path: after the command the
// message lives at the new key and nowhere else.
func TestApplyQueueCommandMovesAtomically(t *testing.T) {
	ps := newQueueCommandStore(t)

	oldKey := []byte("/queue/pending/00000000000000000100-msg1")
	newKey := []byte("/queue/pending/00000000000000000200-msg1")
	if err := ps.Set(oldKey, []byte("v1")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd, err := BuildQueueMoveCommand("msg1", oldKey, newKey, []byte("v2"))
	if err != nil {
		t.Fatalf("BuildQueueMoveCommand: %v", err)
	}
	if err := ApplyQueueCommand(ps, cmd); err != nil {
		t.Fatalf("ApplyQueueCommand: %v", err)
	}

	if v, err := ps.Get(oldKey); err == nil && v != nil {
		t.Errorf("old key still present after move: %q", v)
	}
	got, err := ps.Get(newKey)
	if err != nil || string(got) != "v2" {
		t.Errorf("new key = %q err %v, want v2", got, err)
	}
}

// TestApplyQueueCommandLosesNothingOnCrash is the reason this command type
// exists.
//
// The three queue transitions (retry backoff, bounce, retry-now) used to be two
// separate ApplyCommand calls: delete the old key, then put the new one.
// Consensus can accept the first and reject the second, and a leader can die
// between them -- either way the message was gone from the queue and written
// back nowhere. Silent mail loss, no error returned, nothing left to retry
// from.
//
// A crash is simulated by substituting commitSyncBatch with one that populates
// the batch exactly as production does and then discards it. The message must
// still be readable at its ORIGINAL key: the transition simply did not happen,
// and the caller's error tells it so.
func TestApplyQueueCommandLosesNothingOnCrash(t *testing.T) {
	ps := newQueueCommandStore(t)

	oldKey := []byte("/queue/pending/00000000000000000100-msg1")
	newKey := []byte("/queue/bounced/msg1")
	if err := ps.Set(oldKey, []byte("original")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	orig := commitSyncBatch
	t.Cleanup(func() { commitSyncBatch = orig })
	commitSyncBatch = func(ps *PebbleStore, fn func(batch *pebble.Batch) error) error {
		batch := ps.db.NewBatch()
		defer batch.Close()
		if err := fn(batch); err != nil {
			return err
		}
		return errors.New("simulated crash before commit")
	}

	cmd, err := BuildQueueMoveCommand("msg1", oldKey, newKey, []byte("bounced"))
	if err != nil {
		t.Fatalf("BuildQueueMoveCommand: %v", err)
	}
	if err := ApplyQueueCommand(ps, cmd); err == nil {
		t.Fatal("ApplyQueueCommand reported success despite a failed commit")
	}

	// The message must still be queued. If the delete had been applied
	// separately from the put -- the old two-command shape -- it would be gone
	// from both keys and unrecoverable.
	got, err := ps.Get(oldKey)
	if err != nil || string(got) != "original" {
		t.Errorf("message lost from its original key after a failed transition: got %q err %v; "+
			"a crash mid-transition must leave the message queued, not delete it", got, err)
	}
	if v, err := ps.Get(newKey); err == nil && v != nil {
		t.Errorf("new key was written despite the commit failing: %q", v)
	}
}

// TestBuildQueueMoveCommandRejectsIncompleteInput pins the guards. A command
// with no new key would apply as a bare delete, which is the exact mail-loss
// shape this type replaced.
func TestBuildQueueMoveCommandRejectsIncompleteInput(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		newKey []byte
	}{
		{"missing id", "", []byte("/queue/pending/x")},
		{"missing new key", "msg1", nil},
		{"empty new key", "msg1", []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildQueueMoveCommand(tt.id, []byte("/queue/pending/old"), tt.newKey, []byte("v")); err == nil {
				t.Error("expected an error; a move with no destination deletes the message")
			}
		})
	}
}

// TestApplyQueueCommandRejectsForeignKey stops the FSM dispatch being fed a key
// it does not own.
func TestApplyQueueCommandRejectsForeignKey(t *testing.T) {
	ps := newQueueCommandStore(t)
	if err := ApplyQueueCommand(ps, mustQueueCmd(t)); err != nil {
		t.Fatalf("sanity: valid command failed: %v", err)
	}
	bad := mustQueueCmd(t)
	bad.Key = []byte("/user/u1/mail/guid")
	if err := ApplyQueueCommand(ps, bad); err == nil {
		t.Error("a non-queue key was accepted by ApplyQueueCommand")
	}
}

func mustQueueCmd(t *testing.T) *bongov1.RaftCommand {
	t.Helper()
	cmd, err := BuildQueueMoveCommand("msg1", nil, []byte("/queue/pending/00000000000000000001-msg1"), []byte("v"))
	if err != nil {
		t.Fatalf("BuildQueueMoveCommand: %v", err)
	}
	return cmd
}
