package auth

import (
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/store"
)

// fsmApplier routes commands through a real cluster.FSM so FSM side effects
// (users row, GAL) happen as they do on the leader.
type fsmApplier struct {
	fsm        *cluster.FSM
	ps         *store.PebbleStore
	idx        atomic.Uint64
	profileKey string
}

func (a *fsmApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT {
		a.profileKey = string(cmd.Key)
	}
	b, err := proto.Marshal(cmd)
	if err != nil {
		return err
	}
	resp := a.fsm.Apply(&raft.Log{Index: a.idx.Add(1), Term: 1, Type: raft.LogCommand, Data: b})
	if e, ok := resp.(error); ok {
		return e
	}
	return nil
}

func (a *fsmApplier) GetKey(k []byte) ([]byte, error) { return a.ps.Get(k) }

func TestCreateUserCompensationLeavesNoUsersRowViaFSM(t *testing.T) {
	dir := t.TempDir()
	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	fsm := cluster.NewFSM(ps, bs, mm, slog.New(slog.NewTextHandler(io.Discard, nil)))
	applier := &fsmApplier{fsm: fsm, ps: ps}

	// The FSM's INSERT succeeds; CreateUser's own upsert hits ON CONFLICT DO
	// UPDATE and trips this trigger, forcing compensation. The FSM's DELETE
	// does not fire it.
	if _, err := mm.System.DB().Exec(`CREATE TRIGGER fail_upsert BEFORE UPDATE ON users BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}

	_, err = CreateUser(applier, mm, "bob@example.com", "Bob", "secret123", false)
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrAccountExists) {
		t.Fatalf("unexpected ErrAccountExists: %v", err)
	}
	if applier.idx.Load() != 2 || applier.profileKey == "" {
		t.Fatalf("expected PUT and DELETE through the FSM, got %d applies", applier.idx.Load())
	}
	var n int
	if err := mm.System.DB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("users rows after compensation = %d, want 0", n)
	}
	if _, err := ps.Get([]byte(applier.profileKey)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("profile key still present, err = %v", err)
	}
}
