package caldav

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const shGwen = "gwen" // a member of the group the calendar is shared with

// groupEnv shares calA with the group team@d.test, whose only member is gwen.
func newShGroupEnv(t *testing.T, level, state string) (*shEnv, *store.GroupStore, string) {
	t.Helper()
	e := newShEnv(t)
	if err := e.ps.Set([]byte("/system/domain/d.test"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{shGwen, shNone} {
		data, _ := proto.Marshal(&bongov1.UserProfile{Email: id + "@d.test"})
		if err := e.ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
			t.Fatal(err)
		}
	}
	gs := store.NewGroupStore(e.ps, e.app)
	g, err := gs.CreateGroup(&bongov1.Group{Address: "team@d.test", MemberIds: []string{shGwen}})
	if err != nil {
		t.Fatal(err)
	}
	err = e.acl.SetGrant(store.ACLKindCalendar, shOwner, shCalA, store.GranteeGroup(g.Id), level, "", state, shOwner, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	return e, gs, g.Id
}

func TestSharedCalDAVViaGroup(t *testing.T) {
	evPath := func(u, guid string) string { return sharedPath(u, shCalA) + guid + ".ics" }
	e, gs, gid := newShGroupEnv(t, store.GrantLevelWrite, store.GrantStateAccepted)

	if c, _ := e.do(shGwen, "GET", evPath(shGwen, "ev-a"), "", nil); c != 200 {
		t.Errorf("member GET = %d, want 200", c)
	}
	if c, _ := e.do(shNone, "GET", evPath(shNone, "ev-a"), "", nil); c != 404 {
		t.Errorf("non-member GET = %d, want 404", c)
	}
	if c, body := e.propfind(shGwen, "/caldav/"+shGwen+"/calendars/", "1"); c != 207 || !strings.Contains(body, sharedSegment(shOwner, shCalA)) {
		t.Errorf("member home = %d, shared calendar listed: %v", c, strings.Contains(body, sharedSegment(shOwner, shCalA)))
	}
	if c, _ := e.do(shGwen, "PUT", evPath(shGwen, "viagroup"), shICS("viagroup", "From the group"), nil); c != 201 {
		t.Errorf("member PUT = %d, want 201", c)
	}
	if e.title(shOwner, "viagroup") == "" {
		t.Error("the group member's write did not land in the owner's calendar")
	}
	if c, _ := e.do(shGwen, "GET", sharedPath(shGwen, shCalB)+"ev-b.ics", "", nil); c != 404 {
		t.Errorf("member reached another calendar of the owner: %d", c)
	}

	// Leaving the group ends access on the next request: nothing is cached.
	if _, err := gs.UpdateGroup(&bongov1.Group{Id: gid}); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.do(shGwen, "GET", evPath(shGwen, "ev-a"), "", nil); c != 404 {
		t.Errorf("GET after leaving the group = %d, want 404", c)
	}
	if c, _ := e.do(shGwen, "PUT", evPath(shGwen, "afterleave"), shICS("afterleave", "x"), nil); c != 404 {
		t.Errorf("PUT after leaving the group = %d, want 404", c)
	}
	if _, body := e.propfind(shGwen, "/caldav/"+shGwen+"/calendars/", "1"); strings.Contains(body, sharedSegment(shOwner, shCalA)) {
		t.Error("a former member still sees the shared calendar")
	}
}

func TestSharedCalDAVReadGroupIsReadOnly(t *testing.T) {
	e, _, _ := newShGroupEnv(t, store.GrantLevelRead, store.GrantStateAccepted)
	evPath := sharedPath(shGwen, shCalA) + "ev-a.ics"
	if c, _ := e.do(shGwen, "GET", evPath, "", nil); c != 200 {
		t.Errorf("GET = %d", c)
	}
	if c, _ := e.do(shGwen, "PUT", evPath, shICS("ev-a", "Changed"), nil); c != 403 {
		t.Errorf("PUT = %d, want 403", c)
	}
}

func TestSharedCalDAVPendingGroupGrantAuthorisesNothing(t *testing.T) {
	e, _, _ := newShGroupEnv(t, store.GrantLevelWrite, store.GrantStatePending)
	evPath := sharedPath(shGwen, shCalA) + "ev-a.ics"
	if c, _ := e.do(shGwen, "GET", evPath, "", nil); c != 404 {
		t.Errorf("GET = %d, want 404", c)
	}
	if c, body := e.propfind(shGwen, "/caldav/"+shGwen+"/calendars/", "1"); c != 207 || strings.Contains(body, sharedSegment(shOwner, shCalA)) {
		t.Errorf("a pending group grant lists the calendar (status %d)", c)
	}
}
