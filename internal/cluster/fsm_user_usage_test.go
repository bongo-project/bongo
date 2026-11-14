package cluster

import (
	"errors"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The FSM must route the usage command to the conditional handler: stored as a
// literal key it would record nothing, and an unconditional write would bring
// back a deleted user's usage key.
func TestFSMUserUsageIsConditionalOnProfile(t *testing.T) {
	env := newFSMTestEnv(t)
	cmds, err := store.BuildUserUsageCommands(map[string]int64{"u1": 1234, "gone": 99})
	if err != nil || len(cmds) != 1 {
		t.Fatalf("build: %v, %d commands", err, len(cmds))
	}
	applyOK(t, env, 1, bongov1.Op_OP_PUT, "/user/u1/profile", []byte("p"))
	if err, ok := env.fsm.Apply(makeProtoLog(2, cmds[0])).(error); ok && err != nil {
		t.Fatal(err)
	}

	if got, err := store.GetUserUsage(env.pebble, "u1"); err != nil || got != 1234 {
		t.Errorf("u1 usage = %d, %v; want 1234", got, err)
	}
	if _, err := env.pebble.Get(store.UserUsageKey("gone")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("usage written for a user with no profile: %v", err)
	}
	if _, err := env.pebble.Get([]byte("/__user_usage__/0")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("command stored as a literal key: %v", err)
	}
}
