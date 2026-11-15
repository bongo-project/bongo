package store

import (
	"slices"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type groupACLEnv struct {
	ps  *PebbleStore
	gs  *GroupStore
	acl *ACLStore
}

func newGroupACLEnv(t *testing.T) *groupACLEnv {
	t.Helper()
	ps, app, gs := newGroupTestEnv(t)
	for _, id := range []string{"owner", "m", "n"} {
		seedGroupUser(t, ps, id, "")
	}
	return &groupACLEnv{ps: ps, gs: gs, acl: NewACLStore(ps, &serialApplier{inner: app})}
}

func (e *groupACLEnv) group(t *testing.T, addr string, members ...string) string {
	t.Helper()
	g, err := e.gs.CreateGroup(newGroup(addr, members, nil))
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	return g.Id
}

func (e *groupACLEnv) set(t *testing.T, kind, resource, grantee, level, rights, state string, at int64) {
	t.Helper()
	err := e.acl.apply(ACLRequest{Op: ACLOpSet, Kind: kind, Owner: "owner", Resource: resource, Grantee: grantee,
		Level: level, Rights: rights, State: state, GrantedBy: "owner", At: at})
	if err != nil {
		t.Fatalf("set grant: %v", err)
	}
}

func (e *groupACLEnv) calendar(t *testing.T, guid string) {
	t.Helper()
	if err := e.ps.Set(calendarKey("owner", guid), []byte("x")); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveGrantUnion(t *testing.T) {
	now := time.Now().Unix()
	old := now - PendingGrantTTL - 10
	g1, g2 := "grp1", "grp2"
	tests := []struct {
		name       string
		kind       string
		user       string
		members    map[string][]string // group -> members
		grants     []ACLRequest
		wantLevel  string
		wantRights string
		wantDirect bool
		wantGroups []string
	}{
		{name: "none", kind: ACLKindCalendar, user: "m"},
		{
			name: "user only", kind: ACLKindCalendar, user: "m",
			grants:    []ACLRequest{{Grantee: GranteeUser("m"), Level: GrantLevelRead, State: GrantStateAccepted}},
			wantLevel: GrantLevelRead, wantDirect: true,
		},
		{
			name: "group only", kind: ACLKindCalendar, user: "m",
			members:    map[string][]string{g1: {"m"}},
			grants:     []ACLRequest{{Grantee: GranteeGroup(g1), Level: GrantLevelWrite, State: GrantStateAccepted}},
			wantLevel:  GrantLevelWrite,
			wantGroups: []string{g1},
		},
		{
			name: "user read plus group write is write", kind: ACLKindCalendar, user: "m",
			members: map[string][]string{g1: {"m"}},
			grants: []ACLRequest{
				{Grantee: GranteeUser("m"), Level: GrantLevelRead, State: GrantStateAccepted},
				{Grantee: GranteeGroup(g1), Level: GrantLevelWrite, State: GrantStateAccepted},
			},
			wantLevel: GrantLevelWrite, wantDirect: true, wantGroups: []string{g1},
		},
		{
			name: "user write plus group read stays write", kind: ACLKindCalendar, user: "m",
			members: map[string][]string{g1: {"m"}},
			grants: []ACLRequest{
				{Grantee: GranteeUser("m"), Level: GrantLevelWrite, State: GrantStateAccepted},
				{Grantee: GranteeGroup(g1), Level: GrantLevelRead, State: GrantStateAccepted},
			},
			wantLevel: GrantLevelWrite, wantDirect: true, wantGroups: []string{g1},
		},
		{
			name: "address book highest level", kind: ACLKindAddressBook, user: "m",
			members: map[string][]string{g1: {"m"}},
			grants: []ACLRequest{
				{Grantee: GranteeUser("m"), Level: GrantLevelRead, State: GrantStateAccepted},
				{Grantee: GranteeGroup(g1), Level: GrantLevelWrite, State: GrantStateAccepted},
			},
			wantLevel: GrantLevelWrite, wantDirect: true, wantGroups: []string{g1},
		},
		{
			name: "mailbox letters union in canonical order", kind: ACLKindMailbox, user: "m",
			members: map[string][]string{g1: {"m"}},
			grants: []ACLRequest{
				{Grantee: GranteeUser("m"), Rights: "lr", State: GrantStateAccepted},
				{Grantee: GranteeGroup(g1), Rights: "wslr", State: GrantStateAccepted},
			},
			wantRights: "lrsw", wantDirect: true, wantGroups: []string{g1},
		},
		{
			name: "pending user grant ignored", kind: ACLKindCalendar, user: "m",
			grants: []ACLRequest{{Grantee: GranteeUser("m"), Level: GrantLevelWrite, State: GrantStatePending}},
		},
		{
			name: "pending group grant ignored", kind: ACLKindCalendar, user: "m",
			members: map[string][]string{g1: {"m"}},
			grants:  []ACLRequest{{Grantee: GranteeGroup(g1), Level: GrantLevelWrite, State: GrantStatePending}},
		},
		{
			name: "expired pending ignored", kind: ACLKindCalendar, user: "m",
			grants: []ACLRequest{{Grantee: GranteeUser("m"), Level: GrantLevelWrite, State: GrantStatePending, At: old}},
		},
		{
			name: "group the user is not in", kind: ACLKindCalendar, user: "m",
			members: map[string][]string{g1: {"n"}},
			grants:  []ACLRequest{{Grantee: GranteeGroup(g1), Level: GrantLevelWrite, State: GrantStateAccepted}},
		},
		{
			name: "two groups", kind: ACLKindMailbox, user: "m",
			members: map[string][]string{g1: {"m"}, g2: {"m"}},
			grants: []ACLRequest{
				{Grantee: GranteeGroup(g1), Rights: "lr", State: GrantStateAccepted},
				{Grantee: GranteeGroup(g2), Rights: "rw", State: GrantStateAccepted},
			},
			wantRights: "lrw", wantGroups: []string{g1, g2},
		},
		{
			name: "owner who is a member is not special", kind: ACLKindCalendar, user: "owner",
			members: map[string][]string{g1: {"owner"}},
			grants:  []ACLRequest{{Grantee: GranteeGroup(g1), Level: GrantLevelRead, State: GrantStateAccepted}},
			// The resolver does not know about owners; the listing skips them.
			wantLevel: GrantLevelRead, wantGroups: []string{g1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newGroupACLEnv(t)
			ids := map[string]string{}
			for _, name := range []string{g1, g2} {
				ids[name] = e.group(t, name+"@example.com", tt.members[name]...)
			}
			resource := "cal"
			if tt.kind != ACLKindCalendar {
				resource = kindsMailboxID
			}
			for _, g := range tt.grants {
				grantee := g.Grantee
				if typ, name, _ := splitGrantee(grantee); typ == "g" {
					grantee = GranteeGroup(ids[name])
				}
				at := g.At
				if at == 0 {
					at = now
				}
				e.set(t, tt.kind, resource, grantee, g.Level, g.Rights, g.State, at)
			}
			eff, err := e.acl.EffectiveGrant(tt.kind, "owner", resource, tt.user)
			if err != nil {
				t.Fatal(err)
			}
			var wantGroups []string
			for _, name := range tt.wantGroups {
				wantGroups = append(wantGroups, ids[name])
			}
			slices.Sort(wantGroups)
			slices.Sort(eff.Groups)
			if eff.Level != tt.wantLevel || eff.Rights != tt.wantRights || eff.Direct != tt.wantDirect ||
				!slices.Equal(eff.Groups, wantGroups) {
				t.Errorf("got %+v, want level %q rights %q direct %v groups %v",
					eff, tt.wantLevel, tt.wantRights, tt.wantDirect, wantGroups)
			}
		})
	}
}

// Membership is read on every call: removing the user from the group ends the
// access at once.
func TestEffectiveGrantFollowsMembership(t *testing.T) {
	e := newGroupACLEnv(t)
	e.calendar(t, "cal")
	gid := e.group(t, "team@example.com", "m")
	e.set(t, ACLKindCalendar, "cal", GranteeGroup(gid), GrantLevelRead, "", GrantStateAccepted, 1000)

	if lvl, err := e.acl.CalendarAccessLevel("owner", "cal", "m"); err != nil || lvl != GrantLevelRead {
		t.Fatalf("member level = %q, %v", lvl, err)
	}
	if _, err := e.gs.UpdateGroup(&bongov1.Group{Id: gid, MemberIds: []string{"n"}}); err != nil {
		t.Fatal(err)
	}
	if lvl, err := e.acl.CalendarAccessLevel("owner", "cal", "m"); err != nil || lvl != "" {
		t.Errorf("after leaving, level = %q, %v", lvl, err)
	}
	if ok, err := e.acl.HasCalendarAccess("owner", "m", GrantLevelFreeBusy); err != nil || ok {
		t.Errorf("after leaving, free/busy = %v, %v", ok, err)
	}
	if lvl, _ := e.acl.CalendarAccessLevel("owner", "cal", "n"); lvl != GrantLevelRead {
		t.Errorf("new member level = %q", lvl)
	}
	if ok, _ := e.acl.HasCalendarAccess("owner", "n", GrantLevelRead); !ok {
		t.Error("new member has no free/busy access")
	}
}

func TestCalendarAccessLevelGroupCalendarGone(t *testing.T) {
	e := newGroupACLEnv(t)
	gid := e.group(t, "team@example.com", "m")
	e.set(t, ACLKindCalendar, "cal", GranteeGroup(gid), GrantLevelRead, "", GrantStateAccepted, 1000)
	if lvl, err := e.acl.CalendarAccessLevel("owner", "cal", "m"); err != nil || lvl != "" {
		t.Errorf("level for a missing calendar = %q, %v", lvl, err)
	}
}

func TestListGrantsHeldViaGroups(t *testing.T) {
	e := newGroupACLEnv(t)
	for _, c := range []string{"direct", "viagroup", "both", "mine"} {
		e.calendar(t, c)
	}
	// "mine" belongs to m, who is also in the group it is shared with.
	if err := e.ps.Set(calendarKey("m", "mine"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	gid := e.group(t, "team@example.com", "m")
	now := time.Now().Unix()
	e.set(t, ACLKindCalendar, "direct", GranteeUser("m"), GrantLevelRead, "", GrantStateAccepted, now)
	e.set(t, ACLKindCalendar, "viagroup", GranteeGroup(gid), GrantLevelRead, "", GrantStateAccepted, now)
	e.set(t, ACLKindCalendar, "both", GranteeUser("m"), GrantLevelRead, "", GrantStateAccepted, now)
	e.set(t, ACLKindCalendar, "both", GranteeGroup(gid), GrantLevelWrite, "", GrantStateAccepted, now)
	err := e.acl.apply(ACLRequest{Op: ACLOpSet, Kind: ACLKindCalendar, Owner: "m", Resource: "mine",
		Grantee: GranteeGroup(gid), Level: GrantLevelRead, State: GrantStateAccepted, GrantedBy: "m", At: now})
	if err != nil {
		t.Fatal(err)
	}

	held, err := e.acl.ListGrantsHeld("m", GrantStateAccepted, time.Unix(now, 0))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]HeldShare{}
	for _, h := range held {
		got[h.Resource] = h
	}
	if len(held) != 3 {
		t.Fatalf("held = %+v, want direct, viagroup and both only", held)
	}
	if h := got["direct"]; !h.Direct || h.Level != GrantLevelRead || len(h.ViaGroups) != 0 {
		t.Errorf("direct = %+v", h)
	}
	if h := got["viagroup"]; h.Direct || h.Level != GrantLevelRead || !slices.Equal(h.ViaGroups, []string{gid}) {
		t.Errorf("viagroup = %+v", h)
	}
	if h := got["both"]; !h.Direct || h.Level != GrantLevelWrite {
		t.Errorf("both = %+v, want the strongest level", h)
	}

	cals, err := e.acl.ListCalendarGrantsHeld("m")
	if err != nil || len(cals) != 3 {
		t.Fatalf("ListCalendarGrantsHeld = %+v, %v", cals, err)
	}

	// Pending invitations stay personal: a pending group grant is not one.
	e.set(t, ACLKindCalendar, "direct", GranteeGroup(gid), GrantLevelWrite, "", GrantStatePending, now)
	if n, err := e.acl.CountPendingGrantsHeld("m", time.Unix(now, 0)); err != nil || n != 0 {
		t.Errorf("pending count = %d, %v", n, err)
	}
}

func TestListCalendarGrantsShowsGroups(t *testing.T) {
	e := newGroupACLEnv(t)
	e.calendar(t, "cal")
	gid := e.group(t, "team@example.com", "m")
	e.set(t, ACLKindCalendar, "cal", GranteeGroup(gid), GrantLevelRead, "", GrantStateAccepted, 1000)
	e.set(t, ACLKindCalendar, "cal", GranteeUser("n"), GrantLevelWrite, "", GrantStatePending, 1000)
	got, err := e.acl.ListCalendarGrants("owner", "cal")
	if err != nil {
		t.Fatal(err)
	}
	want := []CalendarGrantEntry{
		{Grantee: "n", Level: GrantLevelWrite, State: GrantStatePending},
		{GroupID: gid, Level: GrantLevelRead, State: GrantStateAccepted},
	}
	if !slices.Equal(got, want) {
		t.Errorf("grants = %+v, want %+v", got, want)
	}
}

func TestDeleteGroupRevokesItsGrants(t *testing.T) {
	e := newGroupACLEnv(t)
	e.calendar(t, "cal")
	gid := e.group(t, "team@example.com", "m")
	other := e.group(t, "other@example.com", "m")
	e.set(t, ACLKindCalendar, "cal", GranteeGroup(gid), GrantLevelRead, "", GrantStateAccepted, 1000)
	e.set(t, ACLKindMailbox, kindsMailboxID, GranteeGroup(gid), "", "lr", GrantStateAccepted, 1000)
	e.set(t, ACLKindCalendar, "cal", GranteeGroup(other), GrantLevelRead, "", GrantStateAccepted, 1000)
	e.set(t, ACLKindCalendar, "cal", GranteeUser("n"), GrantLevelRead, "", GrantStateAccepted, 1000)

	prefix := string(aclGranteePrefix(GranteeGroup(gid)))
	if n := len(groupKeys(t, e.ps, prefix)); n != 2 {
		t.Fatalf("reverse keys before delete = %d, want 2", n)
	}
	if n := len(groupKeys(t, e.ps, "/acl/")); n != 4 {
		t.Fatalf("forward keys before delete = %d, want 4", n)
	}
	if err := e.gs.DeleteGroup(gid); err != nil {
		t.Fatal(err)
	}
	for _, k := range groupKeys(t, e.ps, "/acl") {
		if ref, ok := parseACLKey(k); ok && ref.Principal == GranteeGroup(gid) {
			t.Errorf("grant key left: %s", k)
		}
	}
	if keys := groupKeys(t, e.ps, prefix); len(keys) != 0 {
		t.Errorf("reverse keys left: %v", keys)
	}
	// Another group's grants and the user grants survive the delete.
	if n := len(groupKeys(t, e.ps, "/acl/")); n != 2 {
		t.Errorf("forward keys after delete = %d, want the other group's and the user's", n)
	}
	if lvl, _ := e.acl.CalendarAccessLevel("owner", "cal", "m"); lvl != GrantLevelRead {
		t.Errorf("member keeps access through the other group, got %q", lvl)
	}
}

func TestGrantOrphanedGroup(t *testing.T) {
	e := newGroupACLEnv(t)
	e.calendar(t, "cal")
	gid := e.group(t, "team@example.com", "m")
	ref := GrantRef{Kind: ACLKindCalendar, Owner: "owner", Resource: "cal", Principal: GranteeGroup(gid)}
	if got, err := e.acl.GrantOrphaned(ref); err != nil || got {
		t.Errorf("live group: orphaned = %v, %v", got, err)
	}
	// Removed without the delete command, as a crash or an old binary would.
	if err := e.ps.Delete(GroupKey(gid)); err != nil {
		t.Fatal(err)
	}
	if got, err := e.acl.GrantOrphaned(ref); err != nil || !got {
		t.Errorf("vanished group: orphaned = %v, %v", got, err)
	}
}
