package rules

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// directApplier applies PUT and DELETE straight to Pebble. failAfter, when
// positive, makes the call after that many successful writes fail, to
// simulate a crash part-way through a migration.
type directApplier struct {
	pebble    *store.PebbleStore
	writes    int
	failAfter int
}

func (a *directApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if a.failAfter > 0 && a.writes >= a.failAfter {
		return errors.New("simulated crash")
	}
	a.writes++
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.pebble.Delete(cmd.Key)
	}
	return a.pebble.Set(cmd.Key, cmd.Value)
}

func newMigrationStore(t *testing.T) (*RuleStore, *directApplier) {
	t.Helper()
	pebble, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pebble.Close() })
	app := &directApplier{pebble: pebble}
	return NewRuleStore(pebble, app), app
}

// putLegacy writes a rule the way a pre-release node did: field 6 only.
func putLegacy(t *testing.T, rs *RuleStore, user, id string, priority int32, target string) {
	t.Helper()
	rule := &bongov1.FilterRule{
		Id: id, Priority: priority, Enabled: true,
		Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "x"}},
		Action:     &bongov1.FilterAction{Type: "copy", Target: target},
	}
	data, err := proto.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.pebble.Set(ruleKey(user, id), data); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateFirstMatch(t *testing.T) {
	rs, app := newMigrationStore(t)
	putLegacy(t, rs, "u1", "a", 1, "A")
	putLegacy(t, rs, "u1", "b", 2, "B")
	putLegacy(t, rs, "u2", "c", 1, "C")

	// A rule saved by the new form: both fields, stop_processing off.
	newRule := &bongov1.FilterRule{Id: "new", Priority: 3, Enabled: true}
	SetActions(newRule, []*bongov1.FilterAction{{Type: "flag"}, {Type: "copy", Target: "N"}})
	if err := rs.Save("u1", newRule); err != nil {
		t.Fatal(err)
	}

	app.writes = 0
	n, err := rs.MigrateFirstMatch()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || app.writes != 3 {
		t.Fatalf("migrated %d rules with %d writes, want 3 and 3", n, app.writes)
	}

	for _, tc := range []struct{ user, id, target string }{{"u1", "a", "A"}, {"u1", "b", "B"}, {"u2", "c", "C"}} {
		got, err := rs.Get(tc.user, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if !got.StopProcessing {
			t.Errorf("%s: stop_processing not set", tc.id)
		}
		acts := Actions(got)
		if len(got.Actions) != 1 || len(acts) != 1 || acts[0].Target != tc.target || got.Action.Target != tc.target {
			t.Errorf("%s: actions = %v, action = %v", tc.id, got.Actions, got.Action)
		}
	}

	untouched, err := rs.Get("u1", "new")
	if err != nil {
		t.Fatal(err)
	}
	if untouched.StopProcessing || len(untouched.Actions) != 2 {
		t.Errorf("rule saved by the new form was changed: stop=%v actions=%d", untouched.StopProcessing, len(untouched.Actions))
	}

	// A rerun writes nothing.
	app.writes = 0
	if n, err := rs.MigrateFirstMatch(); err != nil || n != 0 || app.writes != 0 {
		t.Errorf("rerun: n=%d writes=%d err=%v, want all zero", n, app.writes, err)
	}
}

// TestMigrateFirstMatchSameDeliveryBeforeAndAfter: two overlapping legacy
// rules. Before the release only the first fired; after migration the engine
// must pick the same rule.
func TestMigrateFirstMatchSameDeliveryBeforeAndAfter(t *testing.T) {
	rs, _ := newMigrationStore(t)
	putLegacy(t, rs, "u1", "first", 1, "A")
	putLegacy(t, rs, "u1", "second", 2, "B")
	meta := &bongov1.MailMeta{Subject: "x"}

	if _, err := rs.MigrateFirstMatch(); err != nil {
		t.Fatal(err)
	}
	list, err := rs.List("u1")
	if err != nil {
		t.Fatal(err)
	}
	got := NewEngine().MatchingRules(list, &Message{Meta: meta})
	if len(got) != 1 || got[0].Id != "first" {
		t.Fatalf("matched %d rules, want only \"first\"", len(got))
	}
}

func TestMigrateFirstMatchResumesAfterCrash(t *testing.T) {
	rs, app := newMigrationStore(t)
	putLegacy(t, rs, "u1", "a", 1, "A")
	putLegacy(t, rs, "u1", "b", 2, "B")
	putLegacy(t, rs, "u1", "c", 3, "C")

	app.failAfter = 2
	n, err := rs.MigrateFirstMatch()
	if err == nil || n != 2 {
		t.Fatalf("crash run: n=%d err=%v, want 2 and an error", n, err)
	}
	done, err := rs.FirstMatchMigrationDone()
	if err != nil || done {
		t.Fatalf("marker after a crash: done=%v err=%v", done, err)
	}

	app.failAfter = 0
	app.writes = 0
	n, err = rs.MigrateFirstMatch()
	if err != nil || n != 1 || app.writes != 1 {
		t.Fatalf("resume: n=%d writes=%d err=%v, want 1 rule", n, app.writes, err)
	}
	for _, id := range []string{"a", "b", "c"} {
		r, _ := rs.Get("u1", id)
		if !r.StopProcessing {
			t.Errorf("%s not migrated after resume", id)
		}
	}
}

func TestFirstMatchMarker(t *testing.T) {
	rs, _ := newMigrationStore(t)
	if done, err := rs.FirstMatchMigrationDone(); err != nil || done {
		t.Fatalf("fresh store: done=%v err=%v", done, err)
	}
	if err := rs.MarkFirstMatchMigrationDone(); err != nil {
		t.Fatal(err)
	}
	if done, err := rs.FirstMatchMigrationDone(); err != nil || !done {
		t.Fatalf("after mark: done=%v err=%v", done, err)
	}
	if _, err := rs.pebble.Get([]byte("/system/migrations/rules-first-match-v1")); err != nil {
		t.Errorf("marker key missing: %v", err)
	}
}

// TestListOrderIsDeterministicOnTies: equal priorities resolve by creation
// time, then ID, on every read.
func TestListOrderIsDeterministicOnTies(t *testing.T) {
	rs, _ := newMigrationStore(t)
	for _, r := range []*bongov1.FilterRule{
		{Id: "z", Priority: 1, CreatedAt: 10},
		{Id: "b", Priority: 1, CreatedAt: 20},
		{Id: "a", Priority: 1, CreatedAt: 20},
		{Id: "first", Priority: 0, CreatedAt: 99},
	} {
		if err := rs.Save("u1", r); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"first", "z", "a", "b"}
	for range 5 {
		list, err := rs.List("u1")
		if err != nil {
			t.Fatal(err)
		}
		for i, id := range want {
			if list[i].Id != id {
				t.Fatalf("order = %v at %d, want %v", list[i].Id, i, want)
			}
		}
	}
}

// TestReorderKeepsActions: Reorder loads, changes the priority and saves; the
// actions list must survive.
func TestReorderKeepsActions(t *testing.T) {
	rs, _ := newMigrationStore(t)
	r := &bongov1.FilterRule{Id: "r", Priority: 5}
	SetActions(r, []*bongov1.FilterAction{{Type: "flag"}, {Type: "copy", Target: "X"}})
	if err := rs.Save("u1", r); err != nil {
		t.Fatal(err)
	}
	if err := rs.Reorder("u1", []string{"r"}); err != nil {
		t.Fatal(err)
	}
	got, _ := rs.Get("u1", "r")
	if len(got.Actions) != 2 || got.Priority != 1 {
		t.Errorf("after reorder: actions=%d priority=%d", len(got.Actions), got.Priority)
	}
}
