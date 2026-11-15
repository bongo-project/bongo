package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func seedShareAccount(t *testing.T, ps *PebbleStore, id, email, kind string, disabled bool) {
	t.Helper()
	p, err := proto.Marshal(&bongov1.UserProfile{Email: email, Kind: kind, Disabled: disabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), p); err != nil {
		t.Fatal(err)
	}
	a, err := proto.Marshal(&bongov1.AddressRecord{UserId: id, Kind: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set(addressKey(email), a); err != nil {
		t.Fatal(err)
	}
}

func TestMailboxRights(t *testing.T) {
	_, acl := newKindsACL(t)
	now := time.Now()
	old := now.Add(-2 * time.Duration(PendingGrantTTL) * time.Second)

	set := func(grantee, rights, state string, at time.Time) {
		t.Helper()
		if err := acl.SetGrantKeepState(ACLKindMailbox, "o", kindsMailboxID, GranteeUser(grantee), "", rights, state, "o", at); err != nil {
			t.Fatal(err)
		}
	}
	set("acc", "lrs", GrantStateAccepted, now)
	set("pend", "lrs", GrantStatePending, now)
	set("exp", "lrs", GrantStatePending, old)

	tests := []struct {
		name    string
		owner   string
		id      string
		user    string
		want    string
		wantErr bool
	}{
		{"accepted", "o", kindsMailboxID, "acc", "lrs", false},
		{"pending", "o", kindsMailboxID, "pend", "", false},
		{"expired pending", "o", kindsMailboxID, "exp", "", false},
		{"no grant", "o", kindsMailboxID, "nobody", "", false},
		{"other owner", "x", kindsMailboxID, "acc", "", false},
		{"bad id", "o", "../x", "acc", "", false},
		{"empty user", "o", kindsMailboxID, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := acl.MailboxRights(tt.owner, tt.id, tt.user, now)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("MailboxRights = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestOwnerByPrimaryAddress(t *testing.T) {
	ps, acl := newKindsACL(t)
	seedShareAccount(t, ps, "u-ok", "Ann@Example.test", "", false)
	seedShareAccount(t, ps, "u-off", "off@example.test", "", true)
	seedShareAccount(t, ps, "u-room", "room@example.test", "resource", false)
	// An alias record must not resolve even though it names a live user.
	alias, _ := proto.Marshal(&bongov1.AddressRecord{UserId: "u-ok", Kind: "alias"})
	if err := ps.Set(addressKey("alias@example.test"), alias); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		addr   string
		wantID string
	}{
		{"ann@example.test", "u-ok"},
		{"ANN@EXAMPLE.TEST", "u-ok"},
		{"off@example.test", ""},
		{"room@example.test", ""},
		{"alias@example.test", ""},
		{"missing@example.test", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			id, _, err := acl.OwnerByPrimaryAddress(tt.addr)
			if tt.wantID == "" {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("got %q, %v; want ErrNotFound", id, err)
				}
				return
			}
			if err != nil || id != tt.wantID {
				t.Fatalf("got %q, %v; want %q", id, err, tt.wantID)
			}
		})
	}
	if got, err := acl.PrimaryAddressOf("u-ok"); err != nil || got != "Ann@Example.test" {
		t.Fatalf("PrimaryAddressOf = %q, %v", got, err)
	}
}

func TestSharedSubscription(t *testing.T) {
	_, acl := newKindsACL(t)
	on, err := acl.SharedSubscribed("u", "o", kindsMailboxID)
	if err != nil || on {
		t.Fatalf("default = %v, %v; want unsubscribed", on, err)
	}
	if err := acl.SetSharedSubscription("u", "o", kindsMailboxID, true); err != nil {
		t.Fatal(err)
	}
	if on, _ := acl.SharedSubscribed("u", "o", kindsMailboxID); !on {
		t.Fatal("not subscribed after subscribe")
	}
	if on, _ := acl.SharedSubscribed("other", "o", kindsMailboxID); on {
		t.Fatal("subscription leaked to another user")
	}
	if err := acl.SetSharedSubscription("u", "o", kindsMailboxID, false); err != nil {
		t.Fatal(err)
	}
	if on, _ := acl.SharedSubscribed("u", "o", kindsMailboxID); on {
		t.Fatal("still subscribed after unsubscribe")
	}
	if err := acl.SetSharedSubscription("u", "o", "bad", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad id = %v, want ErrNotFound", err)
	}
}

func TestSplitOtherUsers(t *testing.T) {
	ns := DefaultIMAPNamespaces()
	tests := []struct {
		name         string
		addr, folder string
		ok           bool
	}{
		{"Other Users/a@b.test/INBOX", "a@b.test", "INBOX", true},
		{"other users/a@b.test/Work/Sub", "a@b.test", "Work/Sub", true},
		{"Other Users", "", "", false},
		{"Other Users/", "", "", false},
		{"Other Users/a@b.test", "", "", false},
		{"Other Users/a@b.test/", "", "", false},
		{"Other Users//INBOX", "", "", false},
		{"Other UsersX/a@b.test/INBOX", "", "", false},
		{"Work/a@b.test/INBOX", "", "", false},
	}
	for _, tt := range tests {
		addr, folder, ok := ns.SplitOtherUsers(tt.name)
		if addr != tt.addr || folder != tt.folder || ok != tt.ok {
			t.Errorf("SplitOtherUsers(%q) = %q, %q, %v", tt.name, addr, folder, ok)
		}
	}
}

func TestMailboxRightsViaGroup(t *testing.T) {
	e := newGroupACLEnv(t)
	now := time.Now()
	gid := e.group(t, "team@example.com", "m")
	pend := e.group(t, "held@example.com", "m")
	rights := func(user string) string {
		t.Helper()
		got, err := e.acl.MailboxRights("owner", kindsMailboxID, user, now)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	e.set(t, ACLKindMailbox, kindsMailboxID, GranteeGroup(pend), "", "lrswi", GrantStatePending, now.Unix())
	if got := rights("m"); got != "" {
		t.Fatalf("pending group grant yields %q, want nothing", got)
	}

	e.set(t, ACLKindMailbox, kindsMailboxID, GranteeGroup(gid), "", "lr", GrantStateAccepted, now.Unix())
	if got := rights("m"); got != "lr" {
		t.Fatalf("member rights = %q, want lr", got)
	}
	if got := rights("n"); got != "" {
		t.Fatalf("non-member rights = %q, want nothing", got)
	}

	e.set(t, ACLKindMailbox, kindsMailboxID, GranteeUser("m"), "", "se", GrantStateAccepted, now.Unix())
	got := rights("m")
	for _, l := range "lrse" {
		if !strings.ContainsRune(got, l) {
			t.Fatalf("union rights = %q, missing %q", got, string(l))
		}
	}

	if _, err := e.gs.UpdateGroup(&bongov1.Group{Id: gid, MemberIds: []string{"n"}}); err != nil {
		t.Fatal(err)
	}
	if got := rights("m"); got != "se" {
		t.Fatalf("after leaving the group, rights = %q, want only the user grant se", got)
	}
	e.set(t, ACLKindMailbox, kindsMailboxID, GranteeUser("m"), "", "se", GrantStateAccepted, now.Unix())
	if err := e.acl.RevokeGrant(ACLKindMailbox, "owner", kindsMailboxID, GranteeUser("m")); err != nil {
		t.Fatal(err)
	}
	if got := rights("m"); got != "" {
		t.Fatalf("after leaving and revoke, rights = %q, want nothing", got)
	}
}
