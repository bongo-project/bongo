package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// groupShareEnv is a shares env with a real GroupStore. team@d.test has bob as
// a member; crew@ca.test sits on another domain with dave in it.
type groupShareEnv struct {
	*sharesEnv
	groups *store.GroupStore
	teamID string
	crewID string
}

func newGroupShareEnv(t *testing.T) *groupShareEnv {
	t.Helper()
	e := &groupShareEnv{sharesEnv: newSharesEnv(t)}
	for _, d := range []string{"d.test", "ca.test"} {
		if err := e.ps.Set([]byte("/system/domain/"+d), []byte("enabled")); err != nil {
			t.Fatal(err)
		}
	}
	e.groups = store.NewGroupStore(e.ps, &directApplierTags{ps: e.ps})
	team, err := e.groups.CreateGroup(&bongov1.Group{Address: "team@d.test", DisplayName: "Team", MemberIds: []string{"bob"}})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	crew, err := e.groups.CreateGroup(&bongov1.Group{Address: "crew@ca.test", MemberIds: []string{"dave"}})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	e.teamID, e.crewID = team.Id, crew.Id
	e.h.SetGroups(e.groups)
	return e
}

func (e *groupShareEnv) level(who string) string {
	lvl, err := e.acl.CalendarAccessLevel("alice", e.calID, who)
	if err != nil {
		panic(err)
	}
	return lvl
}

func (e *groupShareEnv) unshare(caller, entry string) *httptest.ResponseRecorder {
	req := withRoute(sFormReq(http.MethodPost, "/calendar/"+e.calID+"/unshare", url.Values{"share_with": {entry}}),
		caller, map[string]string{"calendarID": e.calID})
	rr := httptest.NewRecorder()
	e.h.UnshareCalendar(rr, req)
	return rr
}

func TestShareCalendarWithGroupEndToEnd(t *testing.T) {
	e := newGroupShareEnv(t)

	if rr := e.shareLevel("alice", e.calID, "team@d.test", store.GrantLevelWrite); rr.Code != http.StatusOK {
		t.Fatalf("share = %d %s", rr.Code, rr.Body.String())
	}
	g, err := e.acl.GetGrant(store.ACLKindCalendar, "alice", e.calID, store.GranteeGroup(e.teamID))
	if err != nil || g == nil {
		t.Fatalf("group grant = %v, %v", g, err)
	}
	if g.State != store.GrantStateAccepted || g.Level != store.GrantLevelWrite || g.GrantedBy != "alice" {
		t.Errorf("grant = %+v, want accepted write by alice", g)
	}
	e.drain()
	if n := e.sender.count(); n != 0 {
		t.Errorf("a group share sent %d invitation mails", n)
	}

	// The member reaches the calendar through the page's own check; others do not.
	if lvl, st := e.h.calendarAccess("bob", "alice", e.calID, needWrite); st != 0 || lvl != store.GrantLevelWrite {
		t.Errorf("member access = %q, %d", lvl, st)
	}
	for _, who := range []string{"carol", "dave"} {
		if _, st := e.h.calendarAccess(who, "alice", e.calID, needRead); st == 0 {
			t.Errorf("%s reached a calendar shared with a group they are not in", who)
		}
	}

	// A re-share with the same level changes nothing and sends nothing.
	if rr := e.shareLevel("alice", e.calID, "team@d.test", store.GrantLevelWrite); rr.Code != http.StatusOK {
		t.Fatalf("re-share = %d", rr.Code)
	}

	// Leaving the group ends access on the very next request.
	if _, err := e.groups.UpdateGroup(&bongov1.Group{Id: e.teamID, MemberIds: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, st := e.h.calendarAccess("bob", "alice", e.calID, needRead); st == 0 {
		t.Error("a member who left the group still has access")
	}
}

func TestShareCalendarGroupRefusals(t *testing.T) {
	e := newGroupShareEnv(t)
	var unknown string
	for i, target := range []string{"nobody@d.test", "crew@ca.test"} {
		rr := e.share("alice", e.calID, target)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", target, rr.Code)
		}
		if i == 0 {
			unknown = rr.Body.String()
		} else if rr.Body.String() != unknown {
			t.Errorf("another domain's group answered %q, an unknown address %q", rr.Body.String(), unknown)
		}
	}
	if keys, _ := e.acl.ListGrantRefs(); len(keys) != 50 {
		t.Errorf("grants after refusals = %d, want only the 50 fillers", len(keys))
	}

	// A full calendar refuses a new group like any other new grantee.
	if rr := e.share("alice", e.fullID, "team@d.test"); rr.Code != http.StatusBadRequest || rr.Body.String() != unknown {
		t.Errorf("full calendar: %d %q", rr.Code, rr.Body.String())
	}
	// Without a group lookup a group address is refused, not mis-resolved.
	e.h.SetGroups(nil)
	if rr := e.share("alice", e.calID, "team@d.test"); rr.Code != http.StatusBadRequest {
		t.Errorf("no lookup: %d", rr.Code)
	}
}

func TestPendingGroupGrantDoesNotAuthorise(t *testing.T) {
	e := newGroupShareEnv(t)
	// Written directly: no share path creates a pending group grant.
	if err := e.acl.SetGrantKeepState(store.ACLKindCalendar, "alice", e.calID, store.GranteeGroup(e.teamID),
		store.GrantLevelWrite, "", store.GrantStatePending, "alice", e.now); err != nil {
		t.Fatal(err)
	}
	if _, st := e.h.calendarAccess("bob", "alice", e.calID, needRead); st == 0 {
		t.Error("a pending group grant authorised a member")
	}
	if ok, _ := e.acl.HasCalendarAccess("alice", "bob", store.GrantLevelFreeBusy); ok {
		t.Error("a pending group grant authorised free/busy")
	}
}

func TestUnshareCalendarGroupGrant(t *testing.T) {
	e := newGroupShareEnv(t)
	e.shareLevel("alice", e.calID, "team@d.test", store.GrantLevelRead)
	e.share("alice", e.calID, "crew@ca.test") // refused; must not disturb the list

	grants, err := e.acl.ListCalendarGrants("alice", e.calID)
	if err != nil || len(grants) != 1 || grants[0].GroupID != e.teamID {
		t.Fatalf("owner list = %+v, %v", grants, err)
	}

	// The settings card shows the group by address and posts it back typed.
	rr := httptest.NewRecorder()
	e.h.CalendarSettings(rr, withRoute(httptest.NewRequest("GET", "/calendar/settings", nil), "alice", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "team@d.test") || !strings.Contains(body, `value="g:`+e.teamID+`"`) {
		t.Errorf("settings card lacks the group row:\n%s", body)
	}

	// A bare group id is a user id as far as the handler goes: nothing to remove.
	if rr := e.unshare("alice", e.teamID); rr.Code != http.StatusOK {
		t.Fatalf("unshare bare id = %d", rr.Code)
	}
	if e.level("bob") == "" {
		t.Fatal("a bare id removed the group grant")
	}
	// Someone else's calendar is not theirs to unshare.
	if rr := e.unshare("bob", "g:"+e.teamID); rr.Code != http.StatusNotFound || e.level("bob") == "" {
		t.Errorf("unshare by a non-owner = %d", rr.Code)
	}
	if rr := e.unshare("alice", "g:"+e.teamID); rr.Code != http.StatusOK {
		t.Fatalf("unshare = %d %s", rr.Code, rr.Body.String())
	}
	if e.level("bob") != "" {
		t.Error("the member kept access after the owner removed the group")
	}
	if grants, _ := e.acl.ListCalendarGrants("alice", e.calID); len(grants) != 0 {
		t.Errorf("grants left: %+v", grants)
	}
}

func TestSharesPageGroupReachedShare(t *testing.T) {
	e := newGroupShareEnv(t)
	e.shareLevel("alice", e.calID, "team@d.test", store.GrantLevelRead)

	page := e.page("bob")
	if !strings.Contains(page, "Work") || !strings.Contains(page, "team@d.test") {
		t.Errorf("the group-reached share is missing or unlabelled:\n%s", page)
	}
	if strings.Contains(page, "/shares/leave") {
		t.Error("a share reached only through a group offers Leave, which would do nothing")
	}

	// With a direct accepted grant as well, the user can leave their own.
	e.grant(t, store.ACLKindCalendar, e.calID, "bob", store.GrantLevelWrite, "", store.GrantStateAccepted, e.now)
	page = e.page("bob")
	if strings.Count(page, "Work") == 0 || !strings.Contains(page, "/shares/leave") {
		t.Errorf("direct share must offer Leave:\n%s", page)
	}
	if lvl := e.level("bob"); lvl != store.GrantLevelWrite {
		t.Errorf("effective level = %q, want the strongest", lvl)
	}

	// A group the owner belongs to does not list the owner's own calendar.
	if _, err := e.groups.UpdateGroup(&bongov1.Group{Id: e.teamID, MemberIds: []string{"alice", "bob"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.page("alice"), "team@d.test") {
		t.Error("the owner sees their own calendar as shared with them")
	}
}
