package store

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func dirTestStore(t *testing.T) *PebbleStore {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	return ps
}

func dirPutProfile(t *testing.T, ps *PebbleStore, id string, p *bongov1.UserProfile) {
	t.Helper()
	b, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), b); err != nil {
		t.Fatal(err)
	}
}

func dirSet(t *testing.T, ps *PebbleStore, key, val string) {
	t.Helper()
	if err := ps.Set([]byte(key), []byte(val)); err != nil {
		t.Fatal(err)
	}
}

func TestListUserSummaries(t *testing.T) {
	ps := dirTestStore(t)
	dirPutProfile(t, ps, "u1", &bongov1.UserProfile{Email: "Zed@example.com", DisplayName: "Zed", IsAdmin: true})
	dirPutProfile(t, ps, "u2", &bongov1.UserProfile{Email: "amy@example.com", Disabled: true})
	dirPutProfile(t, ps, "u3", &bongov1.UserProfile{Email: "bob@sub.example.com"})
	dirPutProfile(t, ps, "u4", &bongov1.UserProfile{Email: "cat@other.org"})
	// Mail keys under a user must not be mistaken for profiles.
	dirSet(t, ps, "/user/u1/mail/profile", "garbage")
	// u2 is both owner and grantee of example.com (one entry), plus a grant on other.org.
	dirSet(t, ps, "/system/domain/example.com/owner", "u2")
	dirSet(t, ps, "/system/domain_admin/example.com/u2", "1")
	dirSet(t, ps, "/system/domain_admin/other.org/u2", "1")

	tests := []struct {
		name, domain string
		want         []string
	}{
		{"all, sorted by email", "", []string{"amy@example.com", "bob@sub.example.com", "cat@other.org", "Zed@example.com"}},
		{"suffix does not match subdomain", "example.com", []string{"amy@example.com", "Zed@example.com"}},
		{"subdomain only", "sub.example.com", []string{"bob@sub.example.com"}},
		{"no match", "nope.test", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, trunc, err := ListUserSummaries(ps, tc.domain)
			if err != nil || trunc {
				t.Fatalf("err=%v trunc=%v", err, trunc)
			}
			var emails []string
			for _, u := range got {
				emails = append(emails, u.Email)
			}
			if !slices.Equal(emails, tc.want) {
				t.Errorf("emails = %v, want %v", emails, tc.want)
			}
		})
	}

	got, _, _ := ListUserSummaries(ps, "example.com")
	if want := []string{"example.com", "other.org"}; !slices.Equal(got[0].AdminDomains, want) {
		t.Errorf("amy admin domains = %v, want %v (owner and grant deduplicated)", got[0].AdminDomains, want)
	}
	if !got[0].Disabled || got[0].Admin || !got[1].Admin || got[1].DisplayName != "Zed" {
		t.Errorf("flags wrong: %+v", got)
	}
}

func TestListUserSummariesFailsClosedOnBadProfile(t *testing.T) {
	ps := dirTestStore(t)
	dirPutProfile(t, ps, "u1", &bongov1.UserProfile{Email: "a@example.com"})
	dirSet(t, ps, "/user/u2/profile", "\xff\xff\xff not a protobuf")
	if _, _, err := ListUserSummaries(ps, ""); err == nil {
		t.Fatal("expected an error for an undecodable profile, got nil")
	}
}

func TestListDomainSummaries(t *testing.T) {
	ps := dirTestStore(t)
	dirPutProfile(t, ps, "u1", &bongov1.UserProfile{Email: "catch@example.com"})
	dirSet(t, ps, "/system/domain/example.com", "enabled")
	dirSet(t, ps, "/system/domain/off.test", "disabled")
	dirSet(t, ps, "/system/domain/alias.test", "enabled")
	dirSet(t, ps, "/system/domain/alias.test/alias_of", "example.com")
	dirSet(t, ps, "/system/domain/ghost.test", "enabled")
	dirSet(t, ps, "/system/domain/Legacy.Test", "enabled")
	dirSet(t, ps, "/system/domain/Legacy.Test/dkim", "private-key-bytes")
	dirSet(t, ps, "/system/domain/example.com/dkim", "private-key-bytes")
	dirSet(t, ps, "/system/domain/example.com/owner", "u1")
	dirSet(t, ps, string(CatchAllKey("example.com")), "u1")
	dirSet(t, ps, string(CatchAllKey("ghost.test")), "deleted-user")

	q, _ := proto.Marshal(&bongov1.DomainQuota{MaxUsers: 5, MaxStorageBytes: 1000})
	if err := ps.Set(DomainQuotaKey("example.com"), q); err != nil {
		t.Fatal(err)
	}
	usage := make([]byte, 8)
	binary.LittleEndian.PutUint64(usage, 42)
	if err := ps.Set(DomainUsageKey("example.com"), usage); err != nil {
		t.Fatal(err)
	}

	got, trunc, err := ListDomainSummaries(ps)
	if err != nil || trunc {
		t.Fatalf("err=%v trunc=%v", err, trunc)
	}
	by := map[string]DomainSummary{}
	var names []string
	for _, d := range got {
		by[d.Name] = d
		names = append(names, d.Name)
	}
	if want := []string{"alias.test", "example.com", "ghost.test", "legacy.test", "off.test"}; !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v (sub-keys excluded, sorted)", names, want)
	}
	ex := by["example.com"]
	if !ex.Enabled || ex.CatchAll != "catch@example.com" || ex.MaxUsers != 5 || ex.MaxStorageBytes != 1000 || ex.UsageBytes != 42 || !ex.DKIM {
		t.Errorf("example.com = %+v", ex)
	}
	if by["off.test"].Enabled {
		t.Error("off.test should be disabled")
	}
	if by["alias.test"].AliasOf != "example.com" || by["example.com"].AliasOf != "" {
		t.Errorf("alias_of wrong: %+v / %+v", by["alias.test"], ex)
	}
	if by["alias.test"].DKIM {
		t.Error("alias.test has no dkim key")
	}
	if !by["legacy.test"].DKIM {
		t.Error("mixed-case dkim key not detected")
	}
	if c := by["ghost.test"].CatchAll; !strings.Contains(c, "deleted-user") || !strings.Contains(c, "missing account") {
		t.Errorf("ghost catch-all = %q", c)
	}
}

func TestListAddressAliases(t *testing.T) {
	ps := dirTestStore(t)
	dirSet(t, ps, "/system/alias/sales@example.com", "amy@example.com")
	dirSet(t, ps, "/system/alias/Info@Example.COM", "bob@example.com")
	dirSet(t, ps, "/system/alias/x@sub.example.com", "bob@example.com")

	got, _, err := ListAddressAliases(ps, "")
	if err != nil || len(got) != 3 || got[0].Address != "Info@Example.COM" {
		t.Fatalf("all: %+v err=%v", got, err)
	}
	got, _, _ = ListAddressAliases(ps, "example.com")
	if len(got) != 2 || got[1].Address != "sales@example.com" || got[1].Target != "amy@example.com" {
		t.Errorf("filtered: %+v", got)
	}
}

func TestNormaliseDirectoryDomain(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"  Example.COM ", "example.com", true},
		{"a/b", "", false},
		{"a@b", "", false},
		{"a\nb", "", false},
		{"a b", "", false},
		{strings.Repeat("a", 254), "", false},
		{strings.Repeat("a", 253), strings.Repeat("a", 253), true},
	}
	for _, tc := range tests {
		got, ok := NormaliseDirectoryDomain(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("NormaliseDirectoryDomain(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCapRows(t *testing.T) {
	rows := make([]int, DirectoryListLimit+1)
	got, trunc := capRows(rows)
	if !trunc || len(got) != DirectoryListLimit {
		t.Errorf("over limit: len=%d trunc=%v", len(got), trunc)
	}
	got, trunc = capRows(rows[:DirectoryListLimit])
	if trunc || len(got) != DirectoryListLimit {
		t.Errorf("at limit: len=%d trunc=%v", len(got), trunc)
	}
}
