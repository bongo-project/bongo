package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newGroupTestEnv(t *testing.T) (*PebbleStore, *directApplier, *GroupStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &directApplier{store: ps}
	gs := NewGroupStore(ps, app)
	if err := ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	return ps, app, gs
}

func seedGroupUser(t *testing.T, ps *PebbleStore, id, kind string) {
	t.Helper()
	data, err := proto.Marshal(&bongov1.UserProfile{Email: id + "@example.com", Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
		t.Fatal(err)
	}
}

func groupKeys(t *testing.T, ps *PebbleStore, prefix string) []string {
	t.Helper()
	keys, err := ps.scanKeys([]byte(prefix))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, k := range keys {
		out = append(out, string(k))
	}
	slices.Sort(out)
	return out
}

func planGroupFromPayload(t *testing.T, ps *PebbleStore, p groupPayload) error {
	t.Helper()
	// Hand-built, as a malicious or newer writer could produce.
	raw, mErr := json.Marshal(p)
	if mErr != nil {
		t.Fatal(mErr)
	}
	_, _, pErr := PlanGroup(ps.Get, ps.scanKeys, &bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte(groupPrefix + p.Op), Value: raw,
	})
	return pErr
}

func TestPlanGroupValidation(t *testing.T) {
	ps, _, _ := newGroupTestEnv(t)
	// An existing group whose id a member list tries to reuse.
	existing := &bongov1.Group{Id: "g-existing", Address: "x@example.com", Domain: "example.com", PostingPolicy: "anyone"}
	raw, _ := proto.Marshal(existing)
	if err := ps.Set(GroupKey("g-existing"), raw); err != nil {
		t.Fatal(err)
	}
	many := make([]string, MaxGroupMembers+1)
	for i := range many {
		many[i] = fmt.Sprintf("u%d", i)
	}
	base := func() groupPayload {
		return groupPayload{Op: GroupOpCreate, ID: "g1", Address: "team@example.com", Domain: "example.com",
			PostingPolicy: "anyone", MemberIDs: []string{"a"}, At: 5}
	}
	tests := []struct {
		name string
		mut  func(p *groupPayload)
	}{
		{"id with slash", func(p *groupPayload) { p.ID = "a/b" }},
		{"empty id", func(p *groupPayload) { p.ID = "" }},
		{"upper-case id", func(p *groupPayload) { p.ID = "G1" }},
		{"member with slash", func(p *groupPayload) { p.MemberIDs = []string{"a/b"} }},
		{"empty member", func(p *groupPayload) { p.MemberIDs = []string{""} }},
		{"duplicate member", func(p *groupPayload) { p.MemberIDs = []string{"a", "a"} }},
		{"owner with slash", func(p *groupPayload) { p.OwnerIDs = []string{"o/x"} }},
		{"1001 members", func(p *groupPayload) { p.MemberIDs = many }},
		{"nested group member", func(p *groupPayload) { p.MemberIDs = []string{"g-existing"} }},
		{"nested group owner", func(p *groupPayload) { p.OwnerIDs = []string{"g-existing"} }},
		{"bad policy", func(p *groupPayload) { p.PostingPolicy = "everyone" }},
		{"empty policy", func(p *groupPayload) { p.PostingPolicy = "" }},
		{"domain mismatch", func(p *groupPayload) { p.Domain = "other.com" }},
		{"address with slash", func(p *groupPayload) { p.Address = "a/b@example.com" }},
		{"address upper case", func(p *groupPayload) { p.Address = "Team@example.com" }},
		{"address without at", func(p *groupPayload) { p.Address = "team" }},
		{"unknown op", func(p *groupPayload) { p.Op = "explode" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base()
			tt.mut(&p)
			err := planGroupFromPayload(t, ps, p)
			if !errors.Is(err, ErrGroupInvalid) {
				t.Fatalf("err = %v, want ErrGroupInvalid", err)
			}
		})
	}
	if err := planGroupFromPayload(t, ps, base()); err != nil {
		t.Fatalf("valid create refused: %v", err)
	}

	t.Run("self-reference on create", func(t *testing.T) {
		for _, field := range []string{"member", "owner"} {
			p := base()
			if field == "member" {
				p.MemberIDs = append(p.MemberIDs, p.ID)
			} else {
				p.OwnerIDs = append(p.OwnerIDs, p.ID)
			}
			if err := planGroupFromPayload(t, ps, p); !errors.Is(err, ErrGroupInvalid) {
				t.Fatalf("%s = own id: err = %v, want ErrGroupInvalid", field, err)
			}
		}
	})
	t.Run("address change on update", func(t *testing.T) {
		p := base()
		p.ID, p.Address = "g-existing", "new@example.com"
		p.Op = GroupOpUpdate
		if err := planGroupFromPayload(t, ps, p); !errors.Is(err, ErrGroupInvalid) {
			t.Fatalf("err = %v, want ErrGroupInvalid", err)
		}
	})
	t.Run("update of an absent group", func(t *testing.T) {
		p := base()
		p.Op = GroupOpUpdate
		if err := planGroupFromPayload(t, ps, p); !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("err = %v, want ErrGroupNotFound", err)
		}
	})
	t.Run("remove_user with slash", func(t *testing.T) {
		if err := planGroupFromPayload(t, ps, groupPayload{Op: GroupOpRemoveUser, UserID: "a/b"}); !errors.Is(err, ErrGroupInvalid) {
			t.Fatalf("err = %v, want ErrGroupInvalid", err)
		}
	})
	t.Run("builder refuses malformed input", func(t *testing.T) {
		_, err := BuildGroupCommand(GroupRequest{Op: GroupOpCreate, Group: &bongov1.Group{Id: "a/b"}})
		if !errors.Is(err, ErrGroupInvalid) {
			t.Fatalf("err = %v, want ErrGroupInvalid", err)
		}
	})
}

func newGroup(address string, members, owners []string) *bongov1.Group {
	return &bongov1.Group{Address: address, DisplayName: "Team", MemberIds: members, OwnerIds: owners}
}

func TestGroupCreateUpdateDeleteKeys(t *testing.T) {
	ps, _, gs := newGroupTestEnv(t)
	for _, u := range []string{"alice", "bob", "carol"} {
		seedGroupUser(t, ps, u, "")
	}

	g, err := gs.CreateGroup(newGroup("Team@Example.com", []string{"alice", "bob"}, []string{"carol"}))
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if g.Address != "team@example.com" || g.Domain != "example.com" || g.PostingPolicy != "anyone" || g.Id == "" {
		t.Fatalf("created group = %+v", g)
	}

	raw, err := ps.Get([]byte("/system/address/team@example.com"))
	if err != nil {
		t.Fatalf("address record: %v", err)
	}
	var rec bongov1.AddressRecord
	if err := proto.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Kind != "group" || rec.GroupId != g.Id || rec.UserId != "" || rec.Domain != "example.com" {
		t.Fatalf("address record = %+v", &rec)
	}
	wantMembers := []string{
		"/system/groupmember/alice/" + g.Id,
		"/system/groupmember/bob/" + g.Id,
	}
	if got := groupKeys(t, ps, "/system/groupmember/"); !slices.Equal(got, wantMembers) {
		t.Fatalf("member keys = %v, want %v", got, wantMembers)
	}

	// Update: bob leaves, carol joins; the diff comes from the stored list.
	upd := proto.Clone(g).(*bongov1.Group)
	upd.MemberIds = []string{"alice", "carol"}
	upd.DisplayName = "Renamed"
	upd.PostingPolicy = GroupPolicyMembers
	upd.Address = "ignored@example.com"
	got, err := gs.UpdateGroup(upd)
	if err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}
	if got.Address != "team@example.com" || got.CreatedAt != g.CreatedAt {
		t.Fatalf("updated group = %+v", got)
	}
	wantMembers = []string{
		"/system/groupmember/alice/" + g.Id,
		"/system/groupmember/carol/" + g.Id,
	}
	if keys := groupKeys(t, ps, "/system/groupmember/"); !slices.Equal(keys, wantMembers) {
		t.Fatalf("member keys after update = %v, want %v", keys, wantMembers)
	}
	stored, err := gs.GetGroup(g.Id)
	if err != nil || stored.DisplayName != "Renamed" || stored.PostingPolicy != GroupPolicyMembers {
		t.Fatalf("GetGroup = %+v, %v", stored, err)
	}

	mine, err := gs.GroupsForUser("carol")
	if err != nil || len(mine) != 1 || mine[0].Id != g.Id {
		t.Fatalf("GroupsForUser(carol) = %v, %v", mine, err)
	}
	if mine, _ := gs.GroupsForUser("bob"); len(mine) != 0 {
		t.Fatalf("bob still in %d groups", len(mine))
	}
	if list, err := gs.ListGroups("example.com"); err != nil || len(list) != 1 {
		t.Fatalf("ListGroups = %v, %v", list, err)
	}
	if list, _ := gs.ListGroups("other.com"); len(list) != 0 {
		t.Fatalf("ListGroups(other.com) = %v", list)
	}

	if err := gs.DeleteGroup(g.Id); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	for _, prefix := range []string{"/system/group/", "/system/groupmember/", "/system/address/"} {
		if keys := groupKeys(t, ps, prefix); len(keys) != 0 {
			t.Errorf("keys left under %s: %v", prefix, keys)
		}
	}
	if _, err := gs.GetGroup(g.Id); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("GetGroup after delete = %v", err)
	}
	if err := gs.DeleteGroup(g.Id); err != nil {
		t.Fatalf("second delete = %v, want nil", err)
	}
}

func TestGroupCreateChecks(t *testing.T) {
	ps, _, gs := newGroupTestEnv(t)
	seedGroupUser(t, ps, "alice", "")
	seedGroupUser(t, ps, "room1", "resource")

	tests := []struct {
		name string
		g    *bongov1.Group
		want error
	}{
		{"unknown member", newGroup("a@example.com", []string{"ghost"}, nil), ErrGroupInvalid},
		{"resource member", newGroup("b@example.com", []string{"room1"}, nil), ErrGroupInvalid},
		{"unknown owner", newGroup("c@example.com", nil, []string{"ghost"}), ErrGroupInvalid},
		{"unhosted domain", newGroup("d@elsewhere.net", nil, nil), ErrGroupInvalid},
		{"bad policy", &bongov1.Group{Address: "e@example.com", PostingPolicy: "nope"}, ErrGroupInvalid},
		{"ok", newGroup("f@example.com", []string{"alice"}, []string{"alice"}), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gs.CreateGroup(tt.g)
			if !errors.Is(err, tt.want) { // errors.Is(nil, nil) is true
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}

	t.Run("pre-check refusal", func(t *testing.T) {
		gs.SetAddressCheck(func(string) error { return errors.New("taken by a user row") })
		defer gs.SetAddressCheck(nil)
		if _, err := gs.CreateGroup(newGroup("g@example.com", nil, nil)); !errors.Is(err, ErrGroupAddressTaken) {
			t.Fatalf("err = %v, want ErrGroupAddressTaken", err)
		}
	})
}

func TestGroupCreateRefusedByExistingAddress(t *testing.T) {
	ps, app, gs := newGroupTestEnv(t)
	rec, _ := proto.Marshal(&bongov1.AddressRecord{UserId: "u1", Domain: "example.com", Kind: "primary"})
	if err := ps.Set([]byte("/system/address/taken@example.com"), rec); err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/system/alias/aliased@example.com"), []byte("u1@example.com")); err != nil {
		t.Fatal(err)
	}
	_ = app
	for _, addr := range []string{"taken@example.com", "aliased@example.com"} {
		if _, err := gs.CreateGroup(newGroup(addr, nil, nil)); !errors.Is(err, ErrGroupAddressTaken) {
			t.Errorf("%s: err = %v, want ErrGroupAddressTaken", addr, err)
		}
	}
	if keys := groupKeys(t, ps, "/system/group/"); len(keys) != 0 {
		t.Errorf("refused create left keys %v", keys)
	}
}

func TestGroupCreateRace(t *testing.T) {
	ps, app, _ := newGroupTestEnv(t)
	const n = 16
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := range n {
		wg.Go(func() {
			g := &bongov1.Group{
				Id: fmt.Sprintf("g%02d", i), Address: "race@example.com", Domain: "example.com",
				PostingPolicy: "anyone", MemberIds: []string{fmt.Sprintf("m%02d", i)},
			}
			cmd, err := BuildGroupCommand(GroupRequest{Op: GroupOpCreate, Group: g, At: 1})
			if err != nil {
				results[i] = err
				return
			}
			results[i] = app.ApplyCommand(cmd, 0)
		})
	}
	wg.Wait()

	wins, winner := 0, -1
	for i, err := range results {
		switch {
		case err == nil:
			wins++
			winner = i
		case errors.Is(err, ErrGroupAddressTaken):
		default:
			t.Fatalf("goroutine %d: unexpected error %v", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d creates won, want exactly 1", wins)
	}
	wantID := fmt.Sprintf("g%02d", winner)
	if keys := groupKeys(t, ps, "/system/group/"); !slices.Equal(keys, []string{"/system/group/" + wantID}) {
		t.Errorf("group keys = %v, want only %s", keys, wantID)
	}
	if keys := groupKeys(t, ps, "/system/groupmember/"); len(keys) != 1 || !strings.HasSuffix(keys[0], "/"+wantID) {
		t.Errorf("member keys = %v, want one for %s", keys, wantID)
	}
}

func TestGroupDeleteLeavesForeignAddressRecord(t *testing.T) {
	ps, app, gs := newGroupTestEnv(t)
	g, err := gs.CreateGroup(newGroup("team@example.com", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	// A plain-Put writer won the window and replaced the group's record.
	rec, _ := proto.Marshal(&bongov1.AddressRecord{UserId: "u9", Domain: "example.com", Kind: "alias"})
	if err := ps.Set([]byte("/system/address/team@example.com"), rec); err != nil {
		t.Fatal(err)
	}
	_ = app
	if err := gs.DeleteGroup(g.Id); err != nil {
		t.Fatal(err)
	}
	got, err := ps.Get([]byte("/system/address/team@example.com"))
	if err != nil || !bytes.Equal(got, rec) {
		t.Fatalf("foreign address record was removed or changed: %v", err)
	}
	if keys := groupKeys(t, ps, "/system/group/"); len(keys) != 0 {
		t.Errorf("group key left: %v", keys)
	}
}

func TestGroupRemoveUser(t *testing.T) {
	ps, _, gs := newGroupTestEnv(t)
	for _, u := range []string{"ab", "abc", "boss"} {
		seedGroupUser(t, ps, u, "")
	}
	g1, err := gs.CreateGroup(newGroup("one@example.com", []string{"ab", "abc"}, []string{"boss"}))
	if err != nil {
		t.Fatal(err)
	}
	g2, err := gs.CreateGroup(newGroup("two@example.com", []string{"abc"}, []string{"ab", "boss"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := gs.RemoveUser("ab"); err != nil {
		t.Fatal(err)
	}
	a, _ := gs.GetGroup(g1.Id)
	b, _ := gs.GetGroup(g2.Id)
	if !slices.Equal(a.MemberIds, []string{"abc"}) || !slices.Equal(a.OwnerIds, []string{"boss"}) {
		t.Errorf("group one = %v / %v", a.MemberIds, a.OwnerIds)
	}
	if !slices.Equal(b.MemberIds, []string{"abc"}) || !slices.Equal(b.OwnerIds, []string{"boss"}) {
		t.Errorf("group two = %v / %v", b.MemberIds, b.OwnerIds)
	}
	want := []string{"/system/groupmember/abc/" + g1.Id, "/system/groupmember/abc/" + g2.Id}
	slices.Sort(want)
	if keys := groupKeys(t, ps, "/system/groupmember/"); !slices.Equal(keys, want) {
		t.Errorf("member keys = %v, want %v (ab removed, abc kept)", keys, want)
	}
	// Repeating is harmless.
	if err := gs.RemoveUser("ab"); err != nil {
		t.Fatal(err)
	}
}

func TestIsGroupSyntheticCommand(t *testing.T) {
	cmd, err := BuildGroupCommand(GroupRequest{Op: GroupOpDelete, ID: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if !IsGroupSyntheticCommand(cmd.Key) || IsGroupSyntheticCommand([]byte("/system/group/g1")) {
		t.Fatal("IsGroupSyntheticCommand mismatch")
	}
}

// A group address must never route, send or count as free.
func TestGroupAddressReaders(t *testing.T) {
	ps, as, sys, resolver := newResolverTestEnv(t)
	app := &directApplier{store: ps}
	gs := NewGroupStore(ps, app)
	if err := ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/system/domain/alias.example"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/system/domain/alias.example/alias_of"), []byte("example.com")); err != nil {
		t.Fatal(err)
	}
	insertUser(t, sys, "boss", "boss@example.com")
	putUserProfile(t, ps, "boss", "boss@example.com", false)
	putAddress(t, as, "boss@example.com", "boss", "primary")
	// The catch-all owner must never receive group mail.
	if err := ps.Set(CatchAllKey("example.com"), []byte("boss")); err != nil {
		t.Fatal(err)
	}
	g, err := gs.CreateGroup(newGroup("team@example.com", nil, nil))
	if err != nil {
		t.Fatal(err)
	}

	for _, addr := range []string{"team@example.com", "TEAM@example.com", "team@alias.example"} {
		for _, strict := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s strict=%v", addr, strict), func(t *testing.T) {
				var id string
				var err error
				if strict {
					id, err = resolver.ResolveAccount(addr)
				} else {
					id, err = resolver.ResolveRecipient(addr)
				}
				if id != "" {
					t.Fatalf("resolved to user %q", id)
				}
				var ge *GroupRecipientError
				if !errors.Is(err, ErrRecipientUnavailable) || !errors.Is(err, ErrRecipientGroup) || !errors.As(err, &ge) {
					t.Fatalf("err = %v, want a GroupRecipientError", err)
				}
				if addr == "team@example.com" && ge.GroupID != g.Id {
					t.Errorf("GroupID = %q, want %q", ge.GroupID, g.Id)
				}
			})
		}
	}

	// Unknown addresses on the domain still reach the catch-all.
	if id, err := resolver.ResolveRecipient("nobody@example.com"); err != nil || id != "boss" {
		t.Fatalf("catch-all = %q, %v", id, err)
	}

	t.Run("send-as", func(t *testing.T) {
		for _, userID := range []string{"", "boss", g.Id} {
			if ok, err := CanSendAs(as, userID, "", "team@example.com"); ok || err != nil {
				t.Errorf("CanSendAs(%q) = %v, %v, want false", userID, ok, err)
			}
		}
		// Even a record marked verified and sendable stays refused.
		rec, _ := proto.Marshal(&bongov1.AddressRecord{Kind: "group", GroupId: g.Id, CanSend: true, Verified: true})
		if err := ps.Set([]byte("/system/address/team@example.com"), rec); err != nil {
			t.Fatal(err)
		}
		if ok, _ := CanSendAs(as, "", "", "team@example.com"); ok {
			t.Error("CanSendAs allowed a group record with can_send set")
		}
	})

	t.Run("domain dependents", func(t *testing.T) {
		deps, err := DomainDependentCounts(ps, "example.com")
		if err != nil {
			t.Fatal(err)
		}
		if deps.Groups != 1 || !deps.Any() {
			t.Fatalf("dependents = %+v, want one group", deps)
		}
		// Purge must leave a live group's address record alone.
		if err := PurgeDomainConfig(ps, app, "example.com"); err != nil {
			t.Fatal(err)
		}
		if _, err := ps.Get([]byte("/system/address/team@example.com")); err != nil {
			t.Fatalf("purge removed a live group's address record: %v", err)
		}
		// Once the group is gone, a stale record is purgeable and uncounted.
		if err := ps.Delete(GroupKey(g.Id)); err != nil {
			t.Fatal(err)
		}
		deps, _ = DomainDependentCounts(ps, "example.com")
		if deps.Groups != 0 {
			t.Fatalf("dependents = %+v, want no groups", deps)
		}
	})

	t.Run("orphan repair skips groups", func(t *testing.T) {
		orphans, err := FindOrphanedAddresses(ps)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range orphans {
			if o.Address == "team@example.com" {
				t.Fatalf("group record reported as orphan: %+v", o)
			}
		}
	})
}
