package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// shareGAL is the shared-book compose env with a real GAL index, stored
// profiles and distribution groups, for the share=1 mode of /api/gal.
type shareGAL struct {
	*sharedGAL
	groups *store.GroupStore
}

func newShareGAL(t *testing.T) *shareGAL {
	t.Helper()
	s := newSharedGAL(t)
	ps := s.env.ps
	for _, d := range []string{"example.com", "other.example"} {
		if err := ps.Set([]byte("/system/domain/"+d), []byte("enabled")); err != nil {
			t.Fatal(err)
		}
	}
	gs := store.NewGroupStore(ps, &directApplierTags{ps: ps})
	s.env.handler.SetGroups(gs)
	return &shareGAL{sharedGAL: s, groups: gs}
}

// user indexes a GAL entry and stores its profile.
func (s *shareGAL) user(t *testing.T, id, email, name, kind string, disabled bool) {
	t.Helper()
	if err := s.sm.IndexProfile(id, email, name); err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(&bongov1.UserProfile{Email: email, DisplayName: name, Kind: kind, Disabled: disabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.env.ps.Set([]byte("/user/"+id+"/profile"), raw); err != nil {
		t.Fatal(err)
	}
}

func (s *shareGAL) grp(t *testing.T, addr, name string) {
	t.Helper()
	if _, err := s.groups.CreateGroup(&bongov1.Group{Address: addr, DisplayName: name}); err != nil {
		t.Fatal(err)
	}
}

func addrs(got []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, e := range got {
		out[e["email"].(string)] = e
	}
	return out
}

func TestGALShareMode(t *testing.T) {
	s := newShareGAL(t)
	s.user(t, "u-alice", "u-alice@example.com", "Zor Caller", "", false)
	s.user(t, "u-zed", "zored@example.com", "Zor Ed Person", "", false)
	s.user(t, "u-zoe", "zoroe@example.com", "Zor Oe Person", "", false)
	s.user(t, "u-room", "zorroom@example.com", "Zor Room", "resource", false)
	s.user(t, "u-shared", "zorshared@example.com", "Zor Shared", store.SharedInboxKind, false)
	s.user(t, "u-off", "zoroff@example.com", "Zor Disabled", "", true)
	s.user(t, "u-far", "zorfar@other.example", "Zor Far", "", false)
	s.grp(t, "zorteam@example.com", "Zor Team")
	s.grp(t, "zorfargroup@other.example", "Zor Far Group")

	// Personal contact on a same-domain address, a plain contact, a contact
	// group and a shared-book contact.
	s.person(t, "u-alice", g32("1"), "Zor Ed As Contact", "zored@example.com")
	s.person(t, "u-alice", g32("2"), "Zor Ack Contact", "zorack@elsewhere.test")
	s.group(t, "u-alice", g32("3"), "Zor Ulu Contacts", "urn:uuid:"+g32("1"))
	book := s.book(t, "u-owner", "Clients")
	s.contact(t, "u-owner", book, g32("4"), "Zor Elda Shared", "zorelda@client.test")
	s.share(t, "u-owner", book, "u-alice", store.GrantStateAccepted)

	got := s.gal(t, "u-alice", "q=zor&share=1&contact_groups=1")
	by := addrs(got)
	for _, want := range []string{"zored@example.com", "zoroe@example.com", "zorteam@example.com"} {
		if by[want] == nil {
			t.Errorf("missing %s in %v", want, by)
		}
	}
	for _, bad := range []string{
		"zorack@elsewhere.test", "zorelda@client.test", "zorroom@example.com", "zorshared@example.com",
		"zoroff@example.com", "zorfar@other.example", "zorfargroup@other.example", "u-alice@example.com",
	} {
		if by[bad] != nil {
			t.Errorf("%s must not be offered: %v", bad, by[bad])
		}
	}
	for _, e := range got {
		if e["kind"] == "contact-group" || e["source"] != "gal" {
			t.Errorf("unexpected entry %v", e)
		}
		if _, ok := e["guid"]; ok {
			t.Errorf("share mode leaks a guid: %v", e)
		}
		for k := range e {
			switch k {
			case "key", "email", "display_name", "source", "kind", "label":
			default:
				t.Errorf("share mode entry carries unexpected field %q: %v", k, e)
			}
		}
		for _, k := range []string{"user_id", "id", "guid"} {
			if _, ok := e[k]; ok {
				t.Errorf("share mode leaks %q: %v", k, e)
			}
		}
	}
	if e := by["zorteam@example.com"]; e != nil && e["kind"] != "group" {
		t.Errorf("group kind = %v", e["kind"])
	}
	if len(got) != 3 {
		t.Errorf("got %d entries, want 3: %v", len(got), by)
	}
}

func TestGALShareModeCapAndEmpty(t *testing.T) {
	s := newShareGAL(t)
	for i := 0; i < 15; i++ {
		s.user(t, fmt.Sprintf("u-p%02d", i), fmt.Sprintf("pat%02d@example.com", i), fmt.Sprintf("Pat %02d", i), "", false)
	}
	for i := 0; i < 5; i++ {
		s.grp(t, fmt.Sprintf("pgroup%d@example.com", i), fmt.Sprintf("Pat Group %d", i))
	}
	if got := s.gal(t, "u-alice", "q=pat&share=1"); len(got) != 10 {
		t.Errorf("got %d entries, want exactly 10", len(got))
	}
	req := httptest.NewRequest(http.MethodGet, "/api/gal?q=&share=1", nil)
	req = withUserContext(req, "u-alice", "alice@example.com")
	rr := httptest.NewRecorder()
	s.env.handler.GALSearch(rr, req)
	if strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Errorf("empty q body = %q", rr.Body.String())
	}
}

func TestGALShareModeFailsClosedWithoutProfiles(t *testing.T) {
	s := newShareGAL(t)
	s.user(t, "u-zed", "zored@example.com", "Zor Ed Person", "", false)
	s.env.handler.pebble = nil
	if got := s.gal(t, "u-alice", "q=zor&share=1"); len(got) != 0 {
		t.Errorf("people returned without a readable profile: %v", got)
	}
}

func TestGALWithoutShareModeIsUnchanged(t *testing.T) {
	s := newShareGAL(t)
	s.user(t, "u-room", "zorroom@example.com", "Zor Room", "resource", false)
	s.person(t, "u-alice", g32("2"), "Zor Ack Contact", "zorack@elsewhere.test")
	s.group(t, "u-alice", g32("3"), "Zor Ulu Contacts", "urn:uuid:"+g32("1"))
	got := s.gal(t, "u-alice", "q=zor&contact_groups=1")
	by := addrs(got)
	if by["zorack@elsewhere.test"] == nil || by["zorroom@example.com"] == nil {
		t.Errorf("compose results lost contacts or resources: %v", by)
	}
	hasGroup := false
	for _, e := range got {
		if e["kind"] == "contact-group" {
			hasGroup = true
		}
	}
	if !hasGroup {
		t.Error("compose lost contact groups")
	}
}

// Every group share mode offers is one the share handlers accept.
func TestGALShareModeGroupsAreShareable(t *testing.T) {
	s := newShareGAL(t)
	s.grp(t, "gone@example.com", "Gone Team")
	s.grp(t, "gtwo@example.com", "Gone Two")
	s.grp(t, "gfar@other.example", "Gone Far")
	offered := s.gal(t, "u-alice", "q=gone&share=1")
	if len(offered) != 2 {
		t.Fatalf("offered %d groups, want 2: %v", len(offered), offered)
	}
	matched := s.env.handler.matchGroups("gone", "alice@example.com", 10)
	if len(matched) == 0 {
		t.Fatal("matchGroups offered nothing")
	}
	for _, g := range matched {
		if _, ok := shareableGroupOf(s.groups, g.Id, "alice@example.com"); !ok {
			t.Errorf("group %s offered but not shareable", g.Address)
		}
	}
	for _, e := range offered {
		if strings.HasSuffix(e["email"].(string), "other.example") {
			t.Errorf("other domain group offered: %v", e)
		}
	}
}

// listOnlyGroups lists groups but cannot answer the share rule.
type listOnlyGroups struct{ groups []*bongov1.Group }

func (l listOnlyGroups) ListGroups(string) ([]*bongov1.Group, error) { return l.groups, nil }
func (listOnlyGroups) ExpandMembers(string) (*bongov1.Group, []store.GroupMember, error) {
	return nil, nil, store.ErrGroupNotFound
}
func (listOnlyGroups) PostingAllowed(*bongov1.Group, string) error { return nil }

// staleGroups lists a group on the caller's domain whose stored record is gone.
type staleGroups struct{ listOnlyGroups }

func (staleGroups) GetGroup(string) (*bongov1.Group, error) { return nil, store.ErrGroupNotFound }

// TestGALShareModeGroupRuleFailsClosed pins that every offered group passes
// the share handlers' own rule: a listed group the rule refuses, or a group
// store that cannot answer the rule, offers nothing.
func TestGALShareModeGroupRuleFailsClosed(t *testing.T) {
	listed := []*bongov1.Group{{Id: g32("9"), Address: "zorteam@example.com", DisplayName: "Zor Team", Domain: "example.com"}}
	for name, groups := range map[string]groupExpander{
		"rule refuses": staleGroups{listOnlyGroups{listed}},
		"no rule":      listOnlyGroups{listed},
	} {
		t.Run(name, func(t *testing.T) {
			s := newShareGAL(t)
			s.env.handler.SetGroups(groups)
			if by := addrs(s.gal(t, "u-alice", "q=zor&share=1")); by["zorteam@example.com"] != nil {
				t.Errorf("group offered although the share rule cannot accept it: %v", by)
			}
		})
	}
}
