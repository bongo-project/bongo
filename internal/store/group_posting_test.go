package store

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestGroupPostingPolicy(t *testing.T) {
	ps, _, gs := newGroupTestEnv(t)
	for _, id := range []string{"member", "owner", "colleague"} {
		seedGroupUser(t, ps, id, "")
	}
	other, _ := proto.Marshal(&bongov1.UserProfile{Email: "eve@other.example"})
	if err := ps.Set([]byte("/user/eve/profile"), other); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		policy string
		actor  string // user id
		want   bool
	}{
		{"anyone", "member", true}, {"anyone", "owner", true}, {"anyone", "colleague", true}, {"anyone", "eve", true}, {"anyone", "", true},
		{"", "", true},
		{"domain", "member", true}, {"domain", "owner", true}, {"domain", "colleague", true}, {"domain", "eve", false}, {"domain", "", false}, {"domain", "ghost", false},
		{"members", "member", true}, {"members", "owner", true}, {"members", "colleague", false}, {"members", "eve", false}, {"members", "", false},
		{"owners", "member", false}, {"owners", "owner", true}, {"owners", "colleague", false}, {"owners", "eve", false}, {"owners", "", false},
		{"bogus", "owner", false}, {"bogus", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.policy+"/"+tt.actor, func(t *testing.T) {
			g := &bongov1.Group{Domain: "example.com", PostingPolicy: tt.policy, MemberIds: []string{"member"}, OwnerIds: []string{"owner"}}
			err := gs.PostingAllowed(g, tt.actor)
			if tt.want && err != nil {
				t.Fatalf("got %v, want allowed", err)
			}
			if !tt.want && !errors.Is(err, ErrGroupPostingRestricted) {
				t.Fatalf("got %v, want ErrGroupPostingRestricted", err)
			}
		})
	}
}

func TestGroupPostingThroughStoredGroup(t *testing.T) {
	ps, _, gs := newGroupTestEnv(t)
	seedGroupUser(t, ps, "owner", "")
	g, err := gs.CreateGroup(&bongov1.Group{Address: "team@example.com", OwnerIds: []string{"owner"}, PostingPolicy: GroupPolicyOwners})
	if err != nil {
		t.Fatal(err)
	}
	got, err := gs.GetGroup(g.Id)
	if err != nil {
		t.Fatalf("GetGroup: %v (applier must dispatch group commands)", err)
	}
	if err := gs.PostingAllowed(got, "owner"); err != nil {
		t.Errorf("owner refused: %v", err)
	}
	if err := gs.PostingAllowed(got, ""); !errors.Is(err, ErrGroupPostingRestricted) {
		t.Errorf("outsider = %v, want refusal", err)
	}
}

func TestPostingAllowedDomainMatchExact(t *testing.T) {
	g := &bongov1.Group{Domain: "Example.COM", PostingPolicy: GroupPolicyDomain}
	if err := postingAllowed(g, "u", "Bob@EXAMPLE.com"); err != nil {
		t.Errorf("case-insensitive domain refused: %v", err)
	}
	if err := postingAllowed(g, "u", "bob@sub.example.com"); !errors.Is(err, ErrGroupPostingRestricted) {
		t.Errorf("sub domain = %v, want refusal", err)
	}
}
