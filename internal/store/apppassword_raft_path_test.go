package store

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// capturingApplier records every command and applies none of them. A store
// that also wrote Pebble directly would then show a record the applier never
// saw, and a store that skipped the applier would record nothing.
type capturingApplier struct {
	mu   sync.Mutex
	cmds []*bongov1.RaftCommand
}

func (c *capturingApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cmds = append(c.cmds, cmd)
	return nil
}

func (c *capturingApplier) commands() []*bongov1.RaftCommand {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*bongov1.RaftCommand(nil), c.cmds...)
}

func newCapturingAppPasswordEnv(t *testing.T) (*AppPasswordStore, *PebbleStore, *capturingApplier) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	ap := &capturingApplier{}
	return NewAppPasswordStore(ps, ap), ps, ap
}

// Create and revoke must go through the Raft applier so every replica sees
// them; a direct Pebble write would change one node only.
func TestAppPasswordPutGoesThroughApplier(t *testing.T) {
	s, ps, ap := newCapturingAppPasswordEnv(t)
	rec := &bongov1.AppPassword{Id: "abcdef", Label: "phone", CreatedAt: 42, PasswordHash: []byte("h")}
	if err := s.Put("u1", rec); err != nil {
		t.Fatal(err)
	}

	cmds := ap.commands()
	if len(cmds) != 1 {
		t.Fatalf("applied commands = %d, want exactly 1", len(cmds))
	}
	cmd := cmds[0]
	if cmd.Op != bongov1.Op_OP_PUT || string(cmd.Key) != "/user/u1/apppassword/abcdef" {
		t.Errorf("command = %v %q", cmd.Op, cmd.Key)
	}
	var got bongov1.AppPassword
	if err := proto.Unmarshal(cmd.Value, &got); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(&got, rec) {
		t.Errorf("command value = %v, want %v", &got, rec)
	}
	// The applier persists nothing, so a direct write is the only way a
	// record could be here.
	if _, err := ps.Get(AppPasswordKey("u1", "abcdef")); !errors.Is(err, ErrNotFound) {
		t.Errorf("record visible without the applier applying it: %v", err)
	}
}

func TestAppPasswordDeleteGoesThroughApplier(t *testing.T) {
	s, ps, ap := newCapturingAppPasswordEnv(t)
	key := AppPasswordKey("u1", "abcdef")
	seed := []byte("seed")
	if err := ps.Set(key, seed); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("u1", "abcdef"); err != nil {
		t.Fatal(err)
	}

	cmds := ap.commands()
	if len(cmds) != 1 {
		t.Fatalf("applied commands = %d, want exactly 1", len(cmds))
	}
	if cmds[0].Op != bongov1.Op_OP_DELETE || !bytes.Equal(cmds[0].Key, key) {
		t.Errorf("command = %v %q", cmds[0].Op, cmds[0].Key)
	}
	// Only the applier deletes, and this one does not.
	got, err := ps.Get(key)
	if err != nil || !bytes.Equal(got, seed) {
		t.Errorf("key removed without the applier applying it: %q %v", got, err)
	}
}
