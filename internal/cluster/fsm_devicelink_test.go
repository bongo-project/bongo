package cluster

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// The FSM must route a device link consume to the conditional handler: stored
// as a literal key the link would never be marked used, and a refusal must come
// back as the sentinel (so the caller can tell who won) without an Error log.
func TestFSMDeviceLinkConsumeIsConditional(t *testing.T) {
	env := newFSMTestEnv(t)
	var logs bytes.Buffer
	env.fsm.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, hash, err := store.NewDeviceToken()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.DeviceLinkKey("u1", hash)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := proto.Marshal(&bongov1.AppPasswordTicket{Label: "x", ExpiresAt: 2_000})
	applyOK(t, env, 1, bongov1.Op_OP_PUT, string(key), rec)

	consume := func(at int64) *bongov1.RaftCommand {
		cmd, err := store.BuildAppPasswordTicketConsumeCommand("u1", hash, at)
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	read := func() *bongov1.AppPasswordTicket {
		raw, err := env.pebble.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		var got bongov1.AppPasswordTicket
		if err := proto.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		return &got
	}

	// Expired: refused, nothing changes.
	if r, _ := env.fsm.Apply(makeProtoLog(2, consume(2_000))).(error); !errors.Is(r, store.ErrTicketRefused) {
		t.Fatalf("expired consume = %v, want ErrTicketRefused", r)
	}
	if read().UsedAt != 0 {
		t.Fatal("an expired consume changed the record")
	}

	// A good one wins; the record keeps everything else.
	if r := env.fsm.Apply(makeProtoLog(3, consume(1_999))); r != nil {
		t.Fatalf("first consume = %v", r)
	}
	if got := read(); got.UsedAt != 1_999 || got.Label != "x" || got.ExpiresAt != 2_000 {
		t.Fatalf("record after consume = %v", got)
	}

	// The second loses, with the sentinel, and does not move used_at.
	if r, _ := env.fsm.Apply(makeProtoLog(4, consume(1_500))).(error); !errors.Is(r, store.ErrTicketRefused) {
		t.Fatalf("second consume = %v, want ErrTicketRefused", r)
	}
	// Replaying the winning entry is a no-op too.
	if r, _ := env.fsm.Apply(makeProtoLog(3, consume(1_999))).(error); r != nil {
		t.Fatalf("replay = %v", r)
	}
	if read().UsedAt != 1_999 {
		t.Error("a later consume moved used_at")
	}

	if _, err := env.pebble.Get([]byte("/__apppassword_ticket__/consume")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("consume stored as a literal key: %v", err)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a refusal was logged as an error:\n%s", logs.String())
	}
	// The plain Put of the record logs its key (the hash, never the token); the
	// consume must add nothing beyond that single line.
	if n := strings.Count(logs.String(), hash); n != 1 {
		t.Errorf("the hash appears %d times in the log, want 1 (the record Put):\n%s", n, logs.String())
	}
}
