package cluster

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// The FSM must route a group command to the composite handler (stored as a
// literal key nothing would be created) and hand a refusal back as a sentinel
// without an Error log line.
func TestFSMGroupCommandRefusalIsReturnedNotLogged(t *testing.T) {
	env := newFSMTestEnv(t)
	var logs bytes.Buffer
	env.fsm.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	create := func(id string) *bongov1.RaftCommand {
		cmd, err := store.BuildGroupCommand(store.GroupRequest{Op: store.GroupOpCreate, At: 10, Group: &bongov1.Group{
			Id: id, Address: "team@example.com", Domain: "example.com",
			PostingPolicy: "anyone", MemberIds: []string{"u1"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	if r := env.fsm.Apply(makeProtoLog(1, create("g1"))); r != nil {
		t.Fatalf("first create = %v", r)
	}
	if r, _ := env.fsm.Apply(makeProtoLog(2, create("g2"))).(error); !errors.Is(r, store.ErrGroupAddressTaken) {
		t.Fatalf("second create = %v, want ErrGroupAddressTaken", r)
	}
	for _, k := range []string{"/system/group/g1", "/system/groupmember/u1/g1", "/system/address/team@example.com"} {
		if _, err := env.pebble.Get([]byte(k)); err != nil {
			t.Errorf("%s missing: %v", k, err)
		}
	}
	if _, err := env.pebble.Get([]byte("/system/group/g2")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("loser left a group key: %v", err)
	}
	if _, err := env.pebble.Get([]byte("/__group__/create")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("command stored as a literal key: %v", err)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a refusal was logged as an error:\n%s", logs.String())
	}

	// A payload that does not decode is a real failure and is logged.
	bad := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/__group__/create"), Value: []byte("{")}
	if r, _ := env.fsm.Apply(makeProtoLog(3, bad)).(error); r == nil {
		t.Fatal("malformed payload accepted")
	}
	if !strings.Contains(logs.String(), "level=ERROR") {
		t.Error("a malformed payload was not logged")
	}
}
