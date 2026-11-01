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

	"github.com/bongo-project/bongo/internal/store"
)

var grantsMarkerKey = []byte("/system/migrations/calendar-grants-v1")

func newGrantsMigrationAgent(t *testing.T) (*Agent, *recordingApplier, *store.PebbleStore) {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	app := &recordingApplier{ps: ps}
	a := &Agent{
		Store:  ps,
		ACL:    store.NewACLStore(ps, app),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return a, app, ps
}

func seedLegacyCalendar(t *testing.T, ps *store.PebbleStore, owner, guid string, with ...string) {
	t.Helper()
	data, err := proto.Marshal(&bongov1.Calendar{Guid: guid, Name: guid, SharedWith: with})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+owner+"/calendar/"+guid), data); err != nil {
		t.Fatal(err)
	}
}

func legacyShares(t *testing.T, ps *store.PebbleStore, owner, guid string) int {
	t.Helper()
	raw, err := ps.Get([]byte("/user/" + owner + "/calendar/" + guid))
	if err != nil {
		t.Fatal(err)
	}
	var cal bongov1.Calendar
	if err := proto.Unmarshal(raw, &cal); err != nil {
		t.Fatal(err)
	}
	return len(cal.SharedWith)
}

func hasGrantsMarker(t *testing.T, ps *store.PebbleStore) bool {
	t.Helper()
	_, err := ps.Get(grantsMarkerKey)
	if err == nil {
		return true
	}
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	t.Fatal(err)
	return false
}

func TestMigrateCalendarGrantsPass_Follower(t *testing.T) {
	a, app, ps := newGrantsMigrationAgent(t)
	seedLegacyCalendar(t, ps, "alice", "c1", "bob")
	if a.migrateCalendarGrantsPass(always(false)) {
		t.Error("follower reported done")
	}
	if n := app.calls.Load(); n != 0 {
		t.Errorf("follower applied %d commands", n)
	}
	if legacyShares(t, ps, "alice", "c1") != 1 || hasGrantsMarker(t, ps) {
		t.Error("follower changed state")
	}
}

func TestMigrateCalendarGrantsPass_LeaderWithMarker(t *testing.T) {
	a, app, ps := newGrantsMigrationAgent(t)
	seedLegacyCalendar(t, ps, "alice", "c1", "bob")
	if err := ps.Set(grantsMarkerKey, []byte("done")); err != nil {
		t.Fatal(err)
	}
	if !a.migrateCalendarGrantsPass(always(true)) {
		t.Error("marker present but pass not done")
	}
	if n := app.calls.Load(); n != 0 {
		t.Errorf("applied %d commands despite marker", n)
	}
	if legacyShares(t, ps, "alice", "c1") != 1 {
		t.Error("calendar migrated despite marker")
	}
}

func TestMigrateCalendarGrantsPass_MigratesMarkerLast(t *testing.T) {
	a, app, ps := newGrantsMigrationAgent(t)
	seedLegacyCalendar(t, ps, "alice", "c1", "bob", "carl")
	seedLegacyCalendar(t, ps, "dave", "d1", "bob")
	if !a.migrateCalendarGrantsPass(always(true)) {
		t.Fatal("pass not done")
	}
	for _, c := range []struct{ owner, guid, grantee string }{{"alice", "c1", "bob"}, {"alice", "c1", "carl"}, {"dave", "d1", "bob"}} {
		g, err := a.ACL.GetCalendarGrant(c.owner, c.guid, c.grantee)
		if err != nil || g == nil {
			t.Fatalf("grant %v missing: %v", c, err)
		}
		if g.Level != store.GrantLevelFreeBusy || g.State != store.GrantStateAccepted || g.GrantedBy != c.owner {
			t.Errorf("grant %v = %+v", c, g)
		}
	}
	if legacyShares(t, ps, "alice", "c1") != 0 || legacyShares(t, ps, "dave", "d1") != 0 {
		t.Error("shared_with not cleared")
	}
	// Free/busy asks HasCalendarAccess: a migrated grantee passes, and a user
	// who was never in shared_with is still refused (migration widens nothing).
	for _, c := range []struct {
		owner, grantee string
		want           bool
	}{{"alice", "bob", true}, {"alice", "carl", true}, {"dave", "bob", true}, {"alice", "eve", false}, {"dave", "carl", false}} {
		ok, err := a.ACL.HasCalendarAccess(c.owner, c.grantee, store.GrantLevelFreeBusy)
		if err != nil {
			t.Fatal(err)
		}
		if ok != c.want {
			t.Errorf("HasCalendarAccess(%s, %s) = %v, want %v", c.owner, c.grantee, ok, c.want)
		}
	}
	if g, err := a.ACL.GetCalendarGrant("alice", "c1", "eve"); err != nil || g != nil {
		t.Errorf("a user outside shared_with got a grant: %+v, %v", g, err)
	}
	keys := app.recorded()
	if len(keys) == 0 || keys[len(keys)-1] != string(grantsMarkerKey) {
		t.Fatalf("marker not last: %v", keys)
	}
	for _, k := range keys[:len(keys)-1] {
		if k == string(grantsMarkerKey) {
			t.Errorf("marker written before the work: %v", keys)
		}
	}
}

func TestMigrateCalendarGrantsPass_SkipsUnusableEntriesAndKeepsExistingGrant(t *testing.T) {
	a, _, ps := newGrantsMigrationAgent(t)
	seedLegacyCalendar(t, ps, "alice", "c1", "", "alice", "x/y", "bob", "carl")
	// bob already holds a write grant; the migration must not downgrade it.
	if err := a.ACL.SetCalendarGrant("alice", "c1", "bob", store.GrantLevelWrite, store.GrantStateAccepted, "alice", time.Unix(5, 0)); err != nil {
		t.Fatal(err)
	}
	if !a.migrateCalendarGrantsPass(always(true)) {
		t.Fatal("pass not done")
	}
	if g, _ := a.ACL.GetCalendarGrant("alice", "c1", "bob"); g == nil || g.Level != store.GrantLevelWrite {
		t.Errorf("existing grant overwritten: %+v", g)
	}
	if g, _ := a.ACL.GetCalendarGrant("alice", "c1", "carl"); g == nil {
		t.Error("carl not granted")
	}
	if n, _ := a.ACL.CountCalendarGrants("alice", "c1"); n != 2 {
		t.Errorf("grants = %d, want 2 (empty, self and slash entries dropped)", n)
	}
	if legacyShares(t, ps, "alice", "c1") != 0 {
		t.Error("shared_with not cleared")
	}
}

func TestMigrateCalendarGrantsPass_FailureLeavesMarkerUnsetThenResumes(t *testing.T) {
	a, app, ps := newGrantsMigrationAgent(t)
	seedLegacyCalendar(t, ps, "alice", "c1", "bob")
	seedLegacyCalendar(t, ps, "alice", "c2", "bob")
	seedLegacyCalendar(t, ps, "alice", "c3", "bob")
	app.failOn = 2
	if a.migrateCalendarGrantsPass(always(true)) {
		t.Fatal("pass reported done after a failed command")
	}
	if hasGrantsMarker(t, ps) {
		t.Fatal("marker written despite failure")
	}
	app.failOn = 0
	if !a.migrateCalendarGrantsPass(always(true)) {
		t.Fatal("second pass not done")
	}
	for _, g := range []string{"c1", "c2", "c3"} {
		if gr, _ := a.ACL.GetCalendarGrant("alice", g, "bob"); gr == nil {
			t.Errorf("grant on %s missing after resume", g)
		}
		if legacyShares(t, ps, "alice", g) != 0 {
			t.Errorf("shared_with on %s not cleared after resume", g)
		}
	}
	keys := app.recorded()
	if keys[len(keys)-1] != string(grantsMarkerKey) {
		t.Errorf("marker not last: %v", keys)
	}
}

func TestMigrateCalendarGrantsPass_MarkerWriteFailureNotDone(t *testing.T) {
	a, app, ps := newGrantsMigrationAgent(t)
	seedLegacyCalendar(t, ps, "alice", "c1", "bob")
	app.failOn = 3 // grant, clear, then the marker
	if a.migrateCalendarGrantsPass(always(true)) {
		t.Error("done reported although the marker write failed")
	}
	if hasGrantsMarker(t, ps) {
		t.Error("marker present")
	}
}

func TestRunCalendarGrantsMigration_BecomesLeaderLater(t *testing.T) {
	a, app, ps := newGrantsMigrationAgent(t)
	seedLegacyCalendar(t, ps, "alice", "c1", "bob")

	var leader atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		a.runCalendarGrantsMigration(ctx, 5*time.Millisecond, leader.Load)
	}()

	// The wait only lets ticks elapse; it is not a synchronisation point.
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
	if !hasGrantsMarker(t, ps) {
		t.Error("migration did not run after gaining leadership")
	}
}

func TestRunCalendarGrantsMigration_StopsOnContext(t *testing.T) {
	a, _, _ := newGrantsMigrationAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		a.runCalendarGrantsMigration(ctx, 5*time.Millisecond, always(false))
	}()
	cancel()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("loop ignored context cancellation")
	}
}

// Run migrates before it reports ready, so free/busy never sees an unmigrated
// share, and starts the retry worker. Deleting the Run wiring turns this red.
func TestMigrateCalendarGrants_RunWiring(t *testing.T) {
	old := calendarGrantsInterval
	calendarGrantsInterval = time.Hour
	t.Cleanup(func() { calendarGrantsInterval = old })

	a := blobGCLeaderTestAgentSeeded(t, slog.New(slog.NewTextHandler(io.Discard, nil)), func(a *Agent) {
		seedLegacyCalendar(t, a.Store, "wire", "boot", "friend")
		for _, u := range []string{"wire", "friend"} {
			if err := a.Store.Set([]byte("/user/"+u+"/profile"), []byte("x")); err != nil {
				t.Fatal(err)
			}
		}
	})

	ok, err := a.ACL.HasCalendarAccess("wire", "friend", store.GrantLevelFreeBusy)
	if err != nil || !ok {
		t.Fatalf("migrated share does not authorise free/busy: %v %v", ok, err)
	}
	if !hasGrantsMarker(t, a.Store) {
		t.Fatal("marker not set by Run")
	}
	a.bgMu.Lock()
	live := a.bgLive["calendar-grants"]
	a.bgMu.Unlock()
	if live != 1 {
		t.Errorf("calendar-grants workers running = %d, want 1", live)
	}
}

func TestSweepOrphanedGrants(t *testing.T) {
	a := blobGCLeaderTestAgent(t)
	put := func(k string) {
		t.Helper()
		if err := a.Store.Set([]byte(k), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []string{"owner", "bob", "carl"} {
		put("/user/" + u + "/profile")
	}
	for _, c := range []string{"live", "doomed"} {
		if _, err := a.Calendars.Create("owner", &bongov1.Calendar{Guid: c, Name: c}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(1000, 0)
	set := func(owner, cal, grantee string) {
		t.Helper()
		if err := a.ACL.SetCalendarGrant(owner, cal, grantee, store.GrantLevelRead, store.GrantStateAccepted, owner, now); err != nil {
			t.Fatal(err)
		}
	}
	set("owner", "live", "bob")
	set("owner", "doomed", "bob")
	set("owner", "live", "carl")
	set("owner", "live", "ghost") // grantee has no profile
	set("nobody", "live", "bob")  // owner has no profile

	// Remove one calendar behind the grants' back, as a failed revoke would.
	if err := a.Store.Delete([]byte("/user/owner/calendar/doomed")); err != nil {
		t.Fatal(err)
	}

	has := func(owner, cal, grantee string) bool {
		g, err := a.ACL.GetCalendarGrant(owner, cal, grantee)
		if err != nil {
			t.Fatal(err)
		}
		return g != nil
	}
	prev := a.sweepOrphanedGrants(map[string]bool{})
	if len(prev) != 3 {
		t.Fatalf("first pass marked %d orphans, want 3", len(prev))
	}
	if !has("owner", "doomed", "bob") || !has("owner", "live", "ghost") || !has("nobody", "live", "bob") {
		t.Fatal("grant deleted on the first sighting")
	}

	a.sweepOrphanedGrants(prev)
	for _, g := range [][3]string{{"owner", "doomed", "bob"}, {"owner", "live", "ghost"}, {"nobody", "live", "bob"}} {
		if has(g[0], g[1], g[2]) {
			t.Errorf("orphan %v survived the second pass", g)
		}
	}
	if !has("owner", "live", "bob") || !has("owner", "live", "carl") {
		t.Error("a healthy grant was swept")
	}
	// The reverse markers went with them.
	refs, err := a.ACL.ListGrantRefs()
	if err != nil || len(refs) != 2 {
		t.Errorf("grants left = %v %v, want 2", refs, err)
	}
}

// A grant seen orphaned once and healthy on the next pass is not deleted.
func TestSweepOrphanedGrants_RecoveredGrantSurvives(t *testing.T) {
	a := blobGCLeaderTestAgent(t)
	for _, u := range []string{"owner", "bob"} {
		if err := a.Store.Set([]byte("/user/"+u+"/profile"), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Calendars.Create("owner", &bongov1.Calendar{Guid: "c", Name: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := a.ACL.SetCalendarGrant("owner", "c", "bob", store.GrantLevelRead, store.GrantStateAccepted, "owner", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Delete([]byte("/user/bob/profile")); err != nil {
		t.Fatal(err)
	}
	prev := a.sweepOrphanedGrants(map[string]bool{})
	if len(prev) != 1 {
		t.Fatalf("not marked: %v", prev)
	}
	if err := a.Store.Set([]byte("/user/bob/profile"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	prev = a.sweepOrphanedGrants(prev)
	if len(prev) != 0 {
		t.Errorf("recovered grant still marked: %v", prev)
	}
	if g, _ := a.ACL.GetCalendarGrant("owner", "c", "bob"); g == nil {
		t.Error("recovered grant deleted")
	}
}
