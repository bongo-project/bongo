package cluster

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// The FSM must route the touch command to the conditional handler: stored as
// a literal key it would never update the record, and a literal Put of the
// whole record could resurrect a revoked credential.
func TestFSMAppPasswordTouchIsConditional(t *testing.T) {
	env := newFSMTestEnv(t)
	key := store.AppPasswordKey("u1", "abcdef")
	rec, _ := proto.Marshal(&bongov1.AppPassword{Id: "abcdef", Label: "x", PasswordHash: []byte("h")})
	touch, err := store.BuildAppPasswordTouchCommand("u1", "abcdef", 1_700_006_400+99)
	if err != nil {
		t.Fatal(err)
	}
	apply := func(idx uint64, cmd *bongov1.RaftCommand) {
		t.Helper()
		if err, ok := env.fsm.Apply(makeProtoLog(idx, cmd)).(error); ok && err != nil {
			t.Fatal(err)
		}
	}

	applyOK(t, env, 1, bongov1.Op_OP_PUT, string(key), rec)
	apply(2, touch)
	apply(3, touch) // replay is a no-op
	raw, err := env.pebble.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	var got bongov1.AppPassword
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.LastUsedAt != 1_700_006_400 || got.Label != "x" {
		t.Errorf("record after touch = %v", &got)
	}

	applyOK(t, env, 4, bongov1.Op_OP_DELETE, string(key), nil)
	apply(5, touch)
	if _, err := env.pebble.Get(key); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("touch after revoke brought the record back: %v", err)
	}
	if _, err := env.pebble.Get([]byte("/__apppassword_touch__/touch")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("touch stored as a literal key: %v", err)
	}
}
