package store

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newForwardTargetTestStore(t *testing.T) (*ForwardTargetStore, *PebbleStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	return NewForwardTargetStore(ps, &directApplier{store: ps}), ps
}

func TestForwardTargetStore_AllowedByState(t *testing.T) {
	fts, _ := newForwardTargetTestStore(t)
	for addr, state := range map[string]string{
		"pending@x.example":  ForwardStatePending,
		"verified@x.example": ForwardStateVerified,
		"old@x.example":      ForwardStateGrandfathered,
	} {
		if err := fts.SetState("u1", addr, state); err != nil {
			t.Fatalf("SetState: %v", err)
		}
	}
	tests := []struct {
		name, user, addr string
		want             bool
	}{
		{"pending is not allowed", "u1", "pending@x.example", false},
		{"verified is allowed", "u1", "verified@x.example", true},
		{"grandfathered is allowed", "u1", "old@x.example", true},
		{"missing record is not allowed", "u1", "none@x.example", false},
		{"another user's verified target is not allowed", "u2", "verified@x.example", false},
		{"lookup is case-insensitive", "u1", "VERIFIED@x.example", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fts.Allowed(tt.user, tt.addr)
			if err != nil || got != tt.want {
				t.Fatalf("Allowed = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestForwardTargetStore_ChallengeIsolatedFromSendAs(t *testing.T) {
	fts, ps := newForwardTargetTestStore(t)
	ch := &bongov1.AddressVerification{CodeHash: []byte("h"), ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := fts.PutChallenge("u1", "a@x.example", ch); err != nil {
		t.Fatalf("PutChallenge: %v", err)
	}
	if got, ok, err := fts.GetChallenge("u1", "a@x.example"); err != nil || !ok || string(got.CodeHash) != "h" {
		t.Fatalf("GetChallenge = %v, %v, %v", got, ok, err)
	}
	// The send-as challenge store and the global address index must not see it.
	if _, ok, _ := NewAddressVerifyStore(ps, &directApplier{store: ps}).Get("u1", "a@x.example"); ok {
		t.Error("forward challenge visible to the send-as verify store")
	}
	if err := fts.SetState("u1", "a@x.example", ForwardStateVerified); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if _, err := ps.Get([]byte("/system/address/a@x.example")); err == nil {
		t.Error("verifying a forward target created a global address record")
	}
	if err := fts.DeleteChallenge("u1", "a@x.example"); err != nil {
		t.Fatalf("DeleteChallenge: %v", err)
	}
	if _, ok, _ := fts.GetChallenge("u1", "a@x.example"); ok {
		t.Error("challenge still present after delete")
	}
}

func TestMigrateForwardTargets(t *testing.T) {
	fts, ps := newForwardTargetTestStore(t)
	setPrefs := func(user, fwd string) {
		t.Helper()
		if err := NewPreferenceStore(ps, &directApplier{store: ps}).Set(user, &bongov1.UserPreferences{ForwardAddress: fwd}); err != nil {
			t.Fatalf("Set prefs: %v", err)
		}
	}
	setPrefs("u1", "Ext@Other.example")
	setPrefs("u2", "") // no forward: no record
	setPrefs("u3", "local@here.example")
	setPrefs("u4", "keep@other.example")
	// u4 already verified: must not be downgraded.
	if err := fts.SetState("u4", "keep@other.example", ForwardStateVerified); err != nil {
		t.Fatal(err)
	}
	ruleVal := mustMarshal(t, &bongov1.FilterRule{Id: "r1", Action: &bongov1.FilterAction{Type: "forward", Target: "rule@other.example"}})
	moveVal := mustMarshal(t, &bongov1.FilterRule{Id: "r2", Action: &bongov1.FilterAction{Type: "move", Target: "Archive"}})
	if err := ps.Set([]byte("/user/u2/rule/r1"), ruleVal); err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/u2/rule/r2"), moveVal); err != nil {
		t.Fatal(err)
	}

	isLocal := func(a string) bool { return a == "local@here.example" }
	n, err := fts.MigrateForwardTargets(isLocal)
	if err != nil || n != 2 {
		t.Fatalf("Migrate = %d, %v; want 2", n, err)
	}
	checks := []struct {
		user, addr, state string
	}{
		{"u1", "ext@other.example", ForwardStateGrandfathered},
		{"u2", "rule@other.example", ForwardStateGrandfathered},
		{"u4", "keep@other.example", ForwardStateVerified},
		{"u3", "local@here.example", ""},
		{"u2", "archive", ""},
	}
	for _, c := range checks {
		rec, ok, _ := fts.Get(c.user, c.addr)
		got := ""
		if ok {
			got = rec.State
		}
		if got != c.state {
			t.Errorf("%s -> %s: state %q, want %q", c.user, c.addr, got, c.state)
		}
	}

	// Idempotent.
	if n, err := fts.MigrateForwardTargets(isLocal); err != nil || n != 0 {
		t.Fatalf("second Migrate = %d, %v; want 0", n, err)
	}
	gf, err := fts.ListGrandfathered()
	if err != nil || len(gf) != 2 {
		t.Fatalf("ListGrandfathered = %v, %v; want 2", gf, err)
	}
	if l, _ := fts.ListByUser("u1"); len(l) != 1 || l[0].Address != "ext@other.example" {
		t.Errorf("ListByUser(u1) = %v", l)
	}
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestForwardTargetStore_MigrationMarker(t *testing.T) {
	s, _ := newForwardTargetTestStore(t)
	if done, err := s.MigrationDone(); err != nil || done {
		t.Fatalf("fresh store: done=%v err=%v, want false, nil", done, err)
	}
	if err := s.MarkMigrationDone(); err != nil {
		t.Fatalf("MarkMigrationDone: %v", err)
	}
	if done, err := s.MigrationDone(); err != nil || !done {
		t.Fatalf("after mark: done=%v err=%v, want true, nil", done, err)
	}
}

// A forward that is not the rule's first action is grandfathered too, and a
// rule with a list but no field 6 is still read.
func TestMigrateForwardTargetsReadsEveryAction(t *testing.T) {
	fts, ps := newForwardTargetTestStore(t)
	rule := &bongov1.FilterRule{Id: "r1"}
	SetRuleActions(rule, []*bongov1.FilterAction{
		{Type: "flag"},
		{Type: "forward", Target: "second@other.example"},
		{Type: "forward", Target: "third@other.example"},
	})
	if err := ps.Set([]byte("/user/u1/rule/r1"), mustMarshal(t, rule)); err != nil {
		t.Fatal(err)
	}
	listOnly := &bongov1.FilterRule{Id: "r2", Actions: []*bongov1.FilterAction{{Type: "forward", Target: "listonly@other.example"}}}
	if err := ps.Set([]byte("/user/u1/rule/r2"), mustMarshal(t, listOnly)); err != nil {
		t.Fatal(err)
	}
	n, err := fts.MigrateForwardTargets(nil)
	if err != nil || n != 3 {
		t.Fatalf("Migrate = %d, %v; want 3", n, err)
	}
	for _, addr := range []string{"second@other.example", "third@other.example", "listonly@other.example"} {
		if rec, ok, _ := fts.Get("u1", addr); !ok || rec.State != ForwardStateGrandfathered {
			t.Errorf("%s not grandfathered", addr)
		}
	}
}
