package cluster

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// newLeaderNode brings up a single-node dev Raft cluster and waits for it to
// elect itself leader, so ApplyCommand takes the real apply path rather than
// the not-leader early return.
func newLeaderNode(t *testing.T) (*RaftNode, *store.PebbleStore) {
	t.Helper()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mm.Close() })

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.Name = "apply-error-test"

	node, err := NewRaftNode(cfg, ps, bs, mm, logger)
	if err != nil {
		t.Fatalf("NewRaftNode: %v", err)
	}
	t.Cleanup(func() { node.Shutdown() })

	if _, err := node.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("WaitForLeader: %v", err)
	}
	return node, ps
}

// TestApplyCommandSurfacesFSMError is the durability guarantee that a caller is
// never told a Raft write succeeded when the FSM rejected it.
//
// The entry commits to the Raft log either way -- that is what makes this
// dangerous. future.Error() is nil because consensus succeeded; only
// future.Response() carries the FSM's verdict. Drop that check and every write
// path in the server (mail delivery, profile updates, queue mutations) silently
// reports success on a failed Pebble mutation.
//
// An Op_UNSPECIFIED command is the smallest way to reach a real FSM error: it
// marshals as valid protobuf, so it replicates normally, but applyEntry
// declines to treat it as a proto command and the legacy JSON fallback then
// fails to parse the protobuf bytes.
func TestApplyCommandSurfacesFSMError(t *testing.T) {
	node, _ := newLeaderNode(t)

	err := ApplyCommand(node.Raft, &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_UNSPECIFIED,
		Key: []byte("/test/fsm-error"),
	}, 5*time.Second)

	if err == nil {
		t.Fatal("ApplyCommand returned nil for a command the FSM rejected: " +
			"the caller was told a write succeeded when it did not")
	}
	if !strings.Contains(err.Error(), "fsm apply") {
		t.Errorf("error = %q, want it to name the FSM as the source", err)
	}
}

// TestApplyCommandSucceedsForValidCommand is the control. Without it,
// TestApplyCommandSurfacesFSMError would still pass if ApplyCommand were
// changed to return an error unconditionally, which is the wrong fix for the
// same symptom.
func TestApplyCommandSucceedsForValidCommand(t *testing.T) {
	node, ps := newLeaderNode(t)

	key := []byte("/test/valid-put")
	if err := ApplyCommand(node.Raft, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   key,
		Value: []byte("hello"),
	}, 5*time.Second); err != nil {
		t.Fatalf("ApplyCommand for a valid PUT: %v", err)
	}

	got, err := ps.Get(key)
	if err != nil {
		t.Fatalf("Get after apply: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("stored value = %q, want %q", got, "hello")
	}
}

// TestFSMReplaySkipIsConsistentWithStoredData covers the replay-skip mechanism:
// the watermark advances on a successful apply, and an entry at an index at or
// below it is skipped rather than re-executed.
//
// Scope note, because the name matters here. This does NOT pin the statement
// ORDER inside FSM.Apply (applyEntry before setAppliedIndex). Swapping those
// two lines leaves this test green, and deliberately so: when applyEntry
// succeeds the order is unobservable. It costs an entry only if the process
// dies between the two statements -- the watermark is durable, the write is
// not, and the replay-skip asserted below then drops the entry silently.
//
// That ordering is covered separately by TestFSMAppliedIndexNotAdvancedOnCrash,
// which injects a panic at the FSM boundary. What this test guarantees is the
// other half: if the skip itself regresses, or the watermark stops advancing,
// it is caught here.
func TestFSMReplaySkipIsConsistentWithStoredData(t *testing.T) {
	node, ps := newLeaderNode(t)

	key := []byte("/test/applied-index")
	if err := ApplyCommand(node.Raft, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   key,
		Value: []byte("v1"),
	}, 5*time.Second); err != nil {
		t.Fatalf("ApplyCommand: %v", err)
	}

	// The entry is durable, so the watermark must have advanced past 0.
	idx := node.FSM.appliedIndex()
	if idx == 0 {
		t.Fatal("appliedIndex is still 0 after a successful apply")
	}

	// The value must be readable. If setAppliedIndex ran before applyEntry and
	// applyEntry then failed, the watermark would be ahead of the data and this
	// is what notices.
	got, err := ps.Get(key)
	if err != nil || string(got) != "v1" {
		t.Fatalf("value at the applied index is missing: got %q err %v", got, err)
	}

	// Re-applying the same index must be skipped, not re-executed -- that skip
	// is what makes the ordering matter in the first place. A watermark that
	// advanced before the write would cause a crashed entry to be skipped here
	// and lost outright.
	data, err := proto.Marshal(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   key,
		Value: []byte("v2"),
	})
	if err != nil {
		t.Fatalf("marshal replay command: %v", err)
	}
	if replayed := node.FSM.Apply(&raft.Log{Index: idx, Data: data}); replayed != nil {
		t.Errorf("replay of index %d returned %v, want nil (skipped)", idx, replayed)
	}
	got, err = ps.Get(key)
	if err != nil || string(got) != "v1" {
		t.Errorf("replay of an already-applied index overwrote the value: got %q, want v1", got)
	}
}

// TestFSMAppliedIndexNotAdvancedOnCrash pins the statement order in FSM.Apply:
// applyEntry first, setAppliedIndex second.
//
// This is the one guarantee in the sweep that no observation of normal
// operation can distinguish -- when applyEntry succeeds, either order produces
// identical state. It is only visible if the process dies in the gap, so the
// crash is simulated by making applyEntry panic.
//
// Correct order: the panic happens before the watermark is touched, the entry
// is replayed on restart, nothing is lost. Reversed: the watermark already
// claims the entry is durable, the replay-skip at the top of Apply drops it,
// and the write is gone with no error logged anywhere.
func TestFSMAppliedIndexNotAdvancedOnCrash(t *testing.T) {
	node, _ := newLeaderNode(t)
	fsm := node.FSM

	before := fsm.appliedIndex()

	orig := applyEntryFn
	t.Cleanup(func() { applyEntryFn = orig })
	applyEntryFn = func(f *FSM, l *raft.Log) interface{} {
		panic("simulated crash inside applyEntry")
	}

	data, err := proto.Marshal(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/test/crash-window"),
		Value: []byte("v"),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected the simulated crash to propagate")
			}
		}()
		fsm.Apply(&raft.Log{Index: before + 500, Data: data})
	}()

	if got := fsm.appliedIndex(); got != before {
		t.Errorf("applied index moved from %d to %d despite the entry never being written: "+
			"setAppliedIndex runs before applyEntry, so a crash in this window loses the entry "+
			"silently (the replay-skip in Apply will decline to re-apply it)", before, got)
	}
}
