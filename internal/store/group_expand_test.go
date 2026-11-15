package store

import (
	"errors"
	"net/textproto"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func seedExpandUser(t *testing.T, ps *PebbleStore, as *AddressStore, id string, p *bongov1.UserProfile) {
	t.Helper()
	data, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
		t.Fatal(err)
	}
	if err := as.Put(p.Email, &bongov1.AddressRecord{UserId: id, Domain: "example.com", Kind: "primary"}); err != nil {
		t.Fatal(err)
	}
}

func TestExpandMembers(t *testing.T) {
	ps, app, gs := newGroupTestEnv(t)
	as := NewAddressStore(ps, app, nil)
	seedExpandUser(t, ps, as, "u1", &bongov1.UserProfile{Email: "One@example.com"})
	seedExpandUser(t, ps, as, "u2", &bongov1.UserProfile{Email: "two@example.com", Disabled: true})
	seedExpandUser(t, ps, as, "u3", &bongov1.UserProfile{Email: "three@example.com"})
	seedExpandUser(t, ps, as, "u4", &bongov1.UserProfile{Email: "room@example.com", Kind: "resource"})
	seedExpandUser(t, ps, as, "u5", &bongov1.UserProfile{Email: "five@example.com"})
	seedExpandUser(t, ps, as, "gone", &bongov1.UserProfile{Email: "gone@example.com"})

	g, err := gs.CreateGroup(&bongov1.Group{
		Address: "team@example.com", DisplayName: "Team",
		MemberIds: []string{"u5", "u1", "u3"},
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	// A member can become dead after it joined: disabled, a resource, or its
	// profile deleted. Write that state straight into the stored group.
	g.MemberIds = []string{"u5", "u1", "u2", "u3", "u4", "gone"}
	raw, _ := proto.Marshal(g)
	if err := ps.Set(GroupKey(g.Id), raw); err != nil {
		t.Fatal(err)
	}
	if err := ps.Delete([]byte("/user/gone/profile")); err != nil {
		t.Fatal(err)
	}

	got, members, err := gs.ExpandMembers(g.Id)
	if err != nil {
		t.Fatalf("ExpandMembers: %v", err)
	}
	if got.Address != "team@example.com" {
		t.Errorf("group address = %q", got.Address)
	}
	want := []GroupMember{
		{"u5", "five@example.com"},
		{"u1", "one@example.com"},
		{"u3", "three@example.com"},
	}
	if !slices.Equal(members, want) {
		t.Fatalf("members = %+v, want %+v (order kept; disabled, resource and missing skipped)", members, want)
	}

	resolver := NewRecipientResolver(as, ps, nil)
	for _, m := range members {
		id, err := resolver.ResolveRecipient(m.Address)
		if err != nil || id != m.UserID {
			t.Errorf("ResolveRecipient(%q) = %q, %v; want %q", m.Address, id, err, m.UserID)
		}
	}
	var gre *GroupRecipientError
	if _, err := resolver.ResolveRecipient("team@example.com"); !errors.As(err, &gre) || gre.GroupID != g.Id {
		t.Errorf("group address resolved to %v, want GroupRecipientError for %s", err, g.Id)
	}
}

func TestExpandMembersUnknownGroup(t *testing.T) {
	_, _, gs := newGroupTestEnv(t)
	for _, id := range []string{"0123456789abcdef0123456789abcdef", "bad/id", ""} {
		if _, _, err := gs.ExpandMembers(id); !errors.Is(err, ErrGroupNotFound) {
			t.Errorf("ExpandMembers(%q) err = %v, want ErrGroupNotFound", id, err)
		}
	}
}

func TestHeaderHasGroup(t *testing.T) {
	tests := []struct {
		name string
		h    textproto.MIMEHeader
		id   string
		want bool
	}{
		{"match", textproto.MIMEHeader{"X-Bongo-Group": {"abc123"}}, "abc123", true},
		{"trimmed and case folded", textproto.MIMEHeader{"X-Bongo-Group": {"  ABC123 "}}, "abc123", true},
		{"second instance", textproto.MIMEHeader{"X-Bongo-Group": {"other", "abc123"}}, "abc123", true},
		{"other group", textproto.MIMEHeader{"X-Bongo-Group": {"other"}}, "abc123", false},
		{"absent", textproto.MIMEHeader{}, "abc123", false},
		{"empty id", textproto.MIMEHeader{"X-Bongo-Group": {""}}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HeaderHasGroup(tt.h, tt.id); got != tt.want {
				t.Errorf("HeaderHasGroup = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGroupPostingAllowedHook(t *testing.T) {
	if err := GroupPostingAllowed(&bongov1.Group{}, "u1", "a@example.com"); err != nil {
		t.Fatalf("hook = %v, want nil until the posting policy is enforced", err)
	}
}
