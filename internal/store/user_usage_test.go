package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newUsageStore(t *testing.T) (*PebbleStore, *directApplier) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	return ps, &directApplier{store: ps}
}

func TestUserUsageRoundTrip(t *testing.T) {
	ps, ap := newUsageStore(t)
	for _, id := range []string{"u", "v"} {
		if err := ps.Set([]byte("/user/"+id+"/profile"), []byte("p")); err != nil {
			t.Fatal(err)
		}
	}
	cmds, err := BuildUserUsageCommands(map[string]int64{"u": 42, "v": -5, "nop": 7})
	if err != nil || len(cmds) != 1 {
		t.Fatalf("build: %v, %d", err, len(cmds))
	}
	if err := ap.ApplyCommand(cmds[0], 0); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int64{"u": 42, "v": 0, "nop": 0} {
		if got, err := GetUserUsage(ps, id); err != nil || got != want {
			t.Errorf("%s: got %d, %v; want %d", id, got, err, want)
		}
	}
	// directApplier must dispatch, or the command lands as a literal key.
	if _, err := ps.Get([]byte("/__user_usage__/0")); !errors.Is(err, ErrNotFound) {
		t.Errorf("literal command key stored: %v", err)
	}
	if _, err := ps.Get(UserUsageKey("u")); err != nil {
		t.Errorf("usage key missing: %v", err)
	}
	if _, err := ps.Get(UserUsageKey("nop")); !errors.Is(err, ErrNotFound) {
		t.Errorf("usage written for a user with no profile: %v", err)
	}
}

func TestBuildUserUsageCommandsChunking(t *testing.T) {
	tests := []struct {
		n, want int
	}{{0, 0}, {1, 1}, {MaxUserUsageEntries, 1}, {MaxUserUsageEntries + 1, 2}}
	for _, tt := range tests {
		m := make(map[string]int64, tt.n)
		for i := range tt.n {
			m[string(rune('a'))+string(rune(i/70000+'a'))+itoa(i)] = 1
		}
		cmds, err := BuildUserUsageCommands(m)
		if err != nil {
			t.Fatal(err)
		}
		if len(cmds) != tt.want {
			t.Errorf("n=%d: %d commands, want %d", tt.n, len(cmds), tt.want)
		}
	}
}

func itoa(i int) string {
	b := []byte{}
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b) + "x"
}

func TestPlanUserUsageRefusesBadInput(t *testing.T) {
	get := func([]byte) ([]byte, error) { return []byte("p"), nil }
	for _, id := range []string{"", "a/b", "..", "a\x00b", "x..y"} {
		// Built by hand: BuildUserUsageCommands drops these ids on the leader,
		// and this checks the FSM backstop.
		payload, err := json.Marshal(userUsagePayload{Users: []userUsageEntry{{ID: id, Bytes: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(userUsagePrefix + "0"), Value: payload}
		if _, err := PlanUserUsage(get, cmd); err == nil {
			t.Errorf("id %q accepted", id)
		}
	}
	big := &bongov1.RaftCommand{Key: []byte(userUsagePrefix + "0")}
	m := make(map[string]int64)
	for i := range MaxUserUsageEntries + 1 {
		m[itoa(i+1)] = 1
	}
	// Build chunks, then merge into one oversize payload by hand.
	cmds, _ := BuildUserUsageCommands(m)
	var p userUsagePayload
	for _, c := range cmds {
		var q userUsagePayload
		if err := jsonUnmarshal(c.Value, &q); err != nil {
			t.Fatal(err)
		}
		p.Users = append(p.Users, q.Users...)
	}
	big.Value = jsonMarshal(p)
	if _, err := PlanUserUsage(get, big); err == nil {
		t.Error("oversized payload accepted")
	}
	if _, err := PlanUserUsage(get, &bongov1.RaftCommand{Key: []byte("/other")}); err == nil {
		t.Error("non-usage key accepted")
	}
}

func TestGetUserUsageDecoding(t *testing.T) {
	ps, _ := newUsageStore(t)
	huge := make([]byte, 8)
	binary.LittleEndian.PutUint64(huge, math.MaxUint64)
	tests := []struct {
		name string
		val  []byte
		want int64
	}{
		{"missing", nil, 0},
		{"short", []byte{1, 2}, 0},
		{"huge clamps", huge, math.MaxInt64},
		{"normal", encodeUsage(1500), 1500},
	}
	for i, tt := range tests {
		id := "u" + itoa(i)
		if tt.val != nil {
			if err := ps.Set(UserUsageKey(id), tt.val); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := GetUserUsage(ps, id); err != nil || got != tt.want {
			t.Errorf("%s: got %d, %v; want %d", tt.name, got, err, tt.want)
		}
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func jsonMarshal(v any) []byte            { b, _ := json.Marshal(v); return b }

// One stray user id must not block every other user's figure: the leader drops
// it before building the command, so the FSM never refuses the whole pass.
func TestBuildUserUsageCommandsDropsInvalidIDs(t *testing.T) {
	cmds, err := BuildUserUsageCommands(map[string]int64{"good": 10, "a..b": 5, "": 7, "x\x00y": 3})
	if err != nil {
		t.Fatalf("BuildUserUsageCommands: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1", len(cmds))
	}
	var p userUsagePayload
	if err := json.Unmarshal(cmds[0].Value, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(p.Users) != 1 || p.Users[0].ID != "good" || p.Users[0].Bytes != 10 {
		t.Fatalf("payload users = %+v, want only good=10", p.Users)
	}
	if _, err := PlanUserUsage(func([]byte) ([]byte, error) { return []byte("x"), nil }, cmds[0]); err != nil {
		t.Fatalf("PlanUserUsage refused the filtered command: %v", err)
	}
}
