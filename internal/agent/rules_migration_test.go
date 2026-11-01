package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/rules"
	"github.com/bongo-project/bongo/internal/store"
)

var rulesMarkerKey = []byte("/system/migrations/rules-first-match-v1")

// recordingApplier records every command key in order and applies it to
// Pebble. failOn, if non-zero, fails the Nth call (1-based) without applying.
type recordingApplier struct {
	ps     *store.PebbleStore
	keys   atomic.Pointer[[]string]
	calls  atomic.Int32
	failOn int32
}

func (r *recordingApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	n := r.calls.Add(1)
	if r.failOn != 0 && n == r.failOn {
		return errors.New("simulated raft failure")
	}
	var ks []string
	if p := r.keys.Load(); p != nil {
		ks = append(ks, *p...)
	}
	ks = append(ks, string(cmd.Key))
	r.keys.Store(&ks)
	if cmd.Op == bongov1.Op_OP_DELETE {
		return r.ps.Delete(cmd.Key)
	}
	// Without this a grant command would be stored as a literal key and every
	// negative test on grants would pass vacuously.
	if store.IsACLSyntheticCommand(cmd.Key) {
		return store.ApplyACLCommand(r.ps, cmd)
	}
	return r.ps.Set(cmd.Key, cmd.Value)
}

func (r *recordingApplier) recorded() []string {
	if p := r.keys.Load(); p != nil {
		return *p
	}
	return nil
}

func newRulesMigrationAgent(t *testing.T) (*Agent, *recordingApplier, *store.PebbleStore) {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	app := &recordingApplier{ps: ps}
	a := &Agent{
		Rules:  rules.NewRuleStore(ps, app),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return a, app, ps
}

func seedLegacyRule(t *testing.T, ps *store.PebbleStore, user, id string, priority int32) {
	t.Helper()
	data, err := proto.Marshal(&bongov1.FilterRule{
		Id: id, Priority: priority, Enabled: true,
		Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "x"}},
		Action:     &bongov1.FilterAction{Type: "copy", Target: "T"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+user+"/rule/"+id), data); err != nil {
		t.Fatal(err)
	}
}

func ruleMigrated(t *testing.T, a *Agent, user, id string) bool {
	t.Helper()
	list, err := a.Rules.List(user)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if r.Id == id {
			return r.StopProcessing && len(r.Actions) == 1
		}
	}
	t.Fatalf("rule %s not found", id)
	return false
}

func hasMarker(t *testing.T, ps *store.PebbleStore) bool {
	t.Helper()
	_, err := ps.Get(rulesMarkerKey)
	if err == nil {
		return true
	}
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	t.Fatal(err)
	return false
}

func always(v bool) func() bool { return func() bool { return v } }

func TestMigrateRulesFirstMatchPass_Follower(t *testing.T) {
	a, app, ps := newRulesMigrationAgent(t)
	seedLegacyRule(t, ps, "u1", "a", 1)
	if a.migrateRulesFirstMatchPass(always(false)) {
		t.Error("follower reported done")
	}
	if n := app.calls.Load(); n != 0 {
		t.Errorf("follower applied %d commands", n)
	}
	if ruleMigrated(t, a, "u1", "a") || hasMarker(t, ps) {
		t.Error("follower changed state")
	}
}

func TestMigrateRulesFirstMatchPass_LeaderWithMarker(t *testing.T) {
	a, app, ps := newRulesMigrationAgent(t)
	seedLegacyRule(t, ps, "u1", "a", 1)
	if err := ps.Set(rulesMarkerKey, []byte("done")); err != nil {
		t.Fatal(err)
	}
	if !a.migrateRulesFirstMatchPass(always(true)) {
		t.Error("marker present but pass not done")
	}
	if n := app.calls.Load(); n != 0 {
		t.Errorf("applied %d commands despite marker", n)
	}
	if ruleMigrated(t, a, "u1", "a") {
		t.Error("rule migrated despite marker")
	}
}

func TestMigrateRulesFirstMatchPass_LeaderMigratesMarkerLast(t *testing.T) {
	a, app, ps := newRulesMigrationAgent(t)
	seedLegacyRule(t, ps, "u1", "a", 1)
	seedLegacyRule(t, ps, "u1", "b", 2)
	seedLegacyRule(t, ps, "u2", "c", 1)
	if !a.migrateRulesFirstMatchPass(always(true)) {
		t.Fatal("pass not done")
	}
	for _, c := range []struct{ u, id string }{{"u1", "a"}, {"u1", "b"}, {"u2", "c"}} {
		if !ruleMigrated(t, a, c.u, c.id) {
			t.Errorf("rule %s not migrated", c.id)
		}
	}
	keys := app.recorded()
	if len(keys) != 4 {
		t.Fatalf("recorded %d commands, want 4: %v", len(keys), keys)
	}
	if keys[len(keys)-1] != string(rulesMarkerKey) {
		t.Errorf("last command %q, want marker", keys[len(keys)-1])
	}
	for _, k := range keys[:3] {
		if k == string(rulesMarkerKey) {
			t.Errorf("marker written before rules: %v", keys)
		}
	}
}

func TestMigrateRulesFirstMatchPass_FailureLeavesMarkerUnsetThenResumes(t *testing.T) {
	a, app, ps := newRulesMigrationAgent(t)
	seedLegacyRule(t, ps, "u1", "a", 1)
	seedLegacyRule(t, ps, "u1", "b", 2)
	seedLegacyRule(t, ps, "u1", "c", 3)
	app.failOn = 2
	if a.migrateRulesFirstMatchPass(always(true)) {
		t.Fatal("pass reported done after a failed PUT")
	}
	if hasMarker(t, ps) {
		t.Fatal("marker written despite failure")
	}
	app.failOn = 0
	if !a.migrateRulesFirstMatchPass(always(true)) {
		t.Fatal("second pass not done")
	}
	for _, id := range []string{"a", "b", "c"} {
		if !ruleMigrated(t, a, "u1", id) {
			t.Errorf("rule %s not migrated after resume", id)
		}
	}
	keys := app.recorded()
	if keys[len(keys)-1] != string(rulesMarkerKey) {
		t.Errorf("marker not last: %v", keys)
	}
}

func TestMigrateRulesFirstMatchPass_MarkerWriteFailureNotDone(t *testing.T) {
	a, app, ps := newRulesMigrationAgent(t)
	seedLegacyRule(t, ps, "u1", "a", 1)
	app.failOn = 2 // the marker PUT
	if a.migrateRulesFirstMatchPass(always(true)) {
		t.Error("done reported although the marker write failed")
	}
	if hasMarker(t, ps) {
		t.Error("marker present")
	}
}

func TestRunRulesFirstMatchMigration_BecomesLeaderLater(t *testing.T) {
	a, app, ps := newRulesMigrationAgent(t)
	seedLegacyRule(t, ps, "u1", "a", 1)

	var leader atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		a.runRulesFirstMatchMigration(ctx, 5*time.Millisecond, leader.Load)
	}()

	// Let several ticks pass as a follower; nothing may be applied. The wait
	// only lets ticks elapse, it is not a synchronisation point.
	time.Sleep(60 * time.Millisecond)
	if n := app.calls.Load(); n != 0 {
		t.Fatalf("applied %d commands while follower", n)
	}
	select {
	case <-exited:
		t.Fatal("loop exited while still follower and unmarked")
	default:
	}

	leader.Store(true)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not exit after migration")
	}
	if !hasMarker(t, ps) || !ruleMigrated(t, a, "u1", "a") {
		t.Error("migration did not run after gaining leadership")
	}
}

func TestRunRulesFirstMatchMigration_StopsOnContext(t *testing.T) {
	a, _, _ := newRulesMigrationAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		a.runRulesFirstMatchMigration(ctx, 5*time.Millisecond, always(false))
	}()
	cancel()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("loop ignored context cancellation")
	}
}

func TestMigrateRulesFirstMatch_RunWiring(t *testing.T) {
	// An interval far longer than the test means the worker cannot tick, find
	// the work done and exit before it is checked. The loop's own behaviour is
	// covered by TestRunRulesFirstMatchMigration_BecomesLeaderLater.
	old := rulesFirstMatchInterval
	rulesFirstMatchInterval = time.Hour
	t.Cleanup(func() { rulesFirstMatchInterval = old })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := blobGCLeaderTestAgentSeeded(t, logger, func(a *Agent) {
		seedLegacyRule(t, a.Store, "wire", "boot", 1)
	})

	// Startup pass: migrated and marked by Run before it reports ready.
	if !ruleMigrated(t, a, "wire", "boot") {
		t.Error("rule not migrated by Run")
	}
	if !hasMarker(t, a.Store) {
		t.Fatal("marker not set by Run")
	}

	// Run also starts the worker that retries on later leadership changes.
	a.bgMu.Lock()
	live := a.bgLive["rules-first-match"]
	a.bgMu.Unlock()
	if live != 1 {
		t.Errorf("rules-first-match workers running = %d, want 1", live)
	}
}
