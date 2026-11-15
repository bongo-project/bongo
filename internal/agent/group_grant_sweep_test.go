package agent

import (
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A g: grant whose group record is gone is swept on the second sighting; a
// grant to a live group is left alone.
func TestSweepOrphanedGrants_GroupGone(t *testing.T) {
	a := blobGCLeaderTestAgent(t)
	for _, u := range []string{"owner", "bob"} {
		if err := a.Store.Set([]byte("/user/"+u+"/profile"), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Store.Set([]byte("/system/domain/d.test"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Calendars.Create("owner", &bongov1.Calendar{Guid: "cal", Name: "cal"}); err != nil {
		t.Fatal(err)
	}
	live, err := a.Groups.CreateGroup(&bongov1.Group{Address: "live@d.test"})
	if err != nil {
		t.Fatal(err)
	}
	doomed, err := a.Groups.CreateGroup(&bongov1.Group{Address: "doomed@d.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{live.Id, doomed.Id} {
		if err := a.ACL.SetGrant(store.ACLKindCalendar, "owner", "cal", store.GranteeGroup(g),
			store.GrantLevelRead, "", store.GrantStateAccepted, "owner", time.Unix(1000, 0)); err != nil {
			t.Fatal(err)
		}
	}
	// Removed behind the grants' back, as an older binary's delete would.
	if err := a.Store.Delete(store.GroupKey(doomed.Id)); err != nil {
		t.Fatal(err)
	}
	has := func(id string) bool {
		g, err := a.ACL.GetGrant(store.ACLKindCalendar, "owner", "cal", store.GranteeGroup(id))
		if err != nil {
			t.Fatal(err)
		}
		return g != nil
	}
	prev := a.sweepOrphanedGrants(map[string]bool{})
	if len(prev) != 1 || !has(doomed.Id) {
		t.Fatalf("first pass: marked %d, doomed grant present %v", len(prev), has(doomed.Id))
	}
	a.sweepOrphanedGrants(prev)
	if has(doomed.Id) {
		t.Error("the grant to a vanished group survived the second pass")
	}
	if !has(live.Id) {
		t.Error("the grant to a live group was swept")
	}
}
