package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newFetchEnv(t *testing.T) (*FetchStore, *PebbleStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return NewFetchStore(ps, &directApplier{store: ps}), ps
}

func fetchGUID(n int) string { return fmt.Sprintf("%032x", n) }

func TestValidFetchGUID(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{fetchGUID(1), true},
		{strings.Repeat("a", 32), true},
		{strings.Repeat("a", 31), false},
		{strings.Repeat("a", 33), false},
		{strings.Repeat("A", 32), false},
		{strings.Repeat("g", 32), false},
		{"", false},
		{strings.Repeat("a", 31) + "/", false},
		{strings.Repeat("a", 30) + "..", false},
		{strings.Repeat("a", 31) + "\x00", false},
	}
	for _, tt := range tests {
		if got := ValidFetchGUID(tt.in); got != tt.want {
			t.Errorf("ValidFetchGUID(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestFetchAccountCRUD(t *testing.T) {
	s, ps := newFetchEnv(t)
	for _, n := range []int{3, 1, 2} {
		if err := s.Put("u1", &bongov1.FetchAccount{Guid: fetchGUID(n), Protocol: "imap", Host: "h.example", Username: "u"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put("u2", &bongov1.FetchAccount{Guid: fetchGUID(9)}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.Get("u1", fetchGUID(2))
	if err != nil || !ok || got.Host != "h.example" {
		t.Fatalf("Get = %v %v %v", got, ok, err)
	}
	if _, ok, err := s.Get("u1", fetchGUID(9)); err != nil || ok {
		t.Fatalf("Get of another user's guid = %v %v", ok, err)
	}
	list, err := s.List("u1")
	if err != nil || len(list) != 3 {
		t.Fatalf("List = %d %v", len(list), err)
	}
	if list[0].Guid != fetchGUID(1) || list[2].Guid != fetchGUID(3) {
		t.Fatalf("List not ordered by guid: %s %s", list[0].Guid, list[2].Guid)
	}

	// The record sits under the documented key.
	if _, err := ps.Get([]byte("/user/u1/fetch/" + fetchGUID(1))); err != nil {
		t.Fatalf("account not at its documented key: %v", err)
	}

	if err := s.Delete("u1", fetchGUID(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("u1", fetchGUID(1)); err != nil {
		t.Fatalf("delete is idempotent: %v", err)
	}
	if list, _ := s.List("u1"); len(list) != 2 {
		t.Fatalf("after delete: %d", len(list))
	}
}

func TestFetchStoreRefusesBadGUID(t *testing.T) {
	s, _ := newFetchEnv(t)
	bad := "../../x"
	if err := s.Put("u", &bongov1.FetchAccount{Guid: bad}); err == nil {
		t.Error("Put accepted a bad guid")
	}
	if _, _, err := s.Get("u", bad); err == nil {
		t.Error("Get accepted a bad guid")
	}
	if err := s.Delete("u", bad); err == nil {
		t.Error("Delete accepted a bad guid")
	}
	if _, _, err := s.GetState("u", bad); err == nil {
		t.Error("GetState accepted a bad guid")
	}
	if err := s.PutState("u", bad, &bongov1.FetchState{}); err == nil {
		t.Error("PutState accepted a bad guid")
	}
	if err := s.DeleteState("u", bad); err == nil {
		t.Error("DeleteState accepted a bad guid")
	}
	if _, err := s.SeenSet("u", bad); err == nil {
		t.Error("SeenSet accepted a bad guid")
	}
	if err := s.MarkSeen("u", bad, strings.Repeat("a", 32), &bongov1.FetchSeen{}); err == nil {
		t.Error("MarkSeen accepted a bad guid")
	}
	g := fetchGUID(1)
	for _, h := range []string{"", "xyz", strings.Repeat("a", 31), strings.Repeat("A", 32), strings.Repeat("a", 31) + "/"} {
		if err := s.MarkSeen("u", g, h, &bongov1.FetchSeen{}); err == nil {
			t.Errorf("MarkSeen accepted hash %q", h)
		}
		if err := s.DeleteSeen("u", g, h); err == nil {
			t.Errorf("DeleteSeen accepted hash %q", h)
		}
	}
}

func TestFetchStateRoundTrip(t *testing.T) {
	s, _ := newFetchEnv(t)
	g := fetchGUID(1)
	if _, ok, err := s.GetState("u", g); err != nil || ok {
		t.Fatalf("absent state = %v %v", ok, err)
	}
	want := &bongov1.FetchState{Generation: 4, NextRunAt: 99, Paused: true, PausedReason: "auth_failed", AuthFailures: 5, UidValidity: 7}
	if err := s.PutState("u", g, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetState("u", g)
	if err != nil || !ok || got.Generation != 4 || !got.Paused || got.PausedReason != "auth_failed" || got.UidValidity != 7 {
		t.Fatalf("GetState = %+v %v %v", got, ok, err)
	}
	if err := s.DeleteState("u", g); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetState("u", g); ok {
		t.Fatal("state survived delete")
	}
}

// The three key kinds share the text "fetch"; a trailing slash on every prefix
// keeps one kind's scan out of another's, and user "ab" out of "abc".
func TestFetchPrefixIsolation(t *testing.T) {
	s, _ := newFetchEnv(t)
	g := fetchGUID(1)
	h := strings.Repeat("a", 32)
	for _, u := range []string{"ab", "abc"} {
		if err := s.Put(u, &bongov1.FetchAccount{Guid: g, Host: u}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutState(u, g, &bongov1.FetchState{Generation: 1}); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkSeen(u, g, h, &bongov1.FetchSeen{At: 1, Status: 1}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List("ab")
	if err != nil || len(list) != 1 || list[0].Host != "ab" {
		t.Fatalf("List(ab) = %v %v", list, err)
	}
	seen, err := s.SeenSet("ab", g)
	if err != nil || len(seen) != 1 {
		t.Fatalf("SeenSet(ab) = %v %v", seen, err)
	}
	// The fetch/ listing never decodes a state or seen record as an account:
	// a hash-named seen key would not unmarshal into a different shape
	// silently, so check the count of keys under the account prefix directly.
	n := 0
	it, err := s.pebble.PrefixScan([]byte(fetchAccountPrefix("ab")))
	if err != nil {
		t.Fatal(err)
	}
	for it.Next() {
		n++
		if !strings.HasPrefix(string(it.Key()), "/user/ab/fetch/") || strings.Contains(string(it.Key()), "fetchstate") || strings.Contains(string(it.Key()), "fetchseen") {
			t.Fatalf("account prefix matched %s", it.Key())
		}
	}
	_ = it.Close()
	if n != 1 {
		t.Fatalf("account prefix matched %d keys", n)
	}
	for _, p := range []string{fetchAccountPrefix("ab"), fetchStatePrefix("ab"), fetchSeenPrefix("ab", g)} {
		if !strings.HasSuffix(p, "/") {
			t.Errorf("prefix %q lacks the trailing slash", p)
		}
	}
}

func TestFetchSeenSetScopedToOneGUID(t *testing.T) {
	s, _ := newFetchEnv(t)
	// A guid and a longer string starting with it must not share records; the
	// guid length is fixed, so use two guids that differ only in the last digit.
	a, b := fetchGUID(0x10), fetchGUID(0x11)
	ha, hb := strings.Repeat("a", 32), strings.Repeat("b", 32)
	if err := s.MarkSeen("u", a, ha, &bongov1.FetchSeen{At: 1, Status: 1, UidValidity: 5}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSeen("u", b, hb, &bongov1.FetchSeen{At: 2, Status: 2}); err != nil {
		t.Fatal(err)
	}
	set, err := s.SeenSet("u", a)
	if err != nil || len(set) != 1 || set[ha] == nil || set[ha].UidValidity != 5 || set[hb] != nil {
		t.Fatalf("SeenSet(a) = %v %v", set, err)
	}
	if err := s.DeleteSeen("u", a, ha); err != nil {
		t.Fatal(err)
	}
	if set, _ := s.SeenSet("u", a); len(set) != 0 {
		t.Fatal("seen record survived delete")
	}
	if set, _ := s.SeenSet("u", b); len(set) != 1 {
		t.Fatal("delete leaked into another guid")
	}
}

func TestFetchSeenHashes(t *testing.T) {
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:])[:32] }
	if got, want := IMAPSeenHash("INBOX", 42, 7), sum("imap\x00INBOX\x0042\x007"); got != want {
		t.Errorf("IMAPSeenHash = %s, want %s", got, want)
	}
	if got, want := POP3SeenHash("uidl/with/slashes"), sum("pop3\x00uidl/with/slashes"); got != want {
		t.Errorf("POP3SeenHash = %s, want %s", got, want)
	}
	// Every field matters.
	base := IMAPSeenHash("INBOX", 42, 7)
	for name, h := range map[string]string{
		"folder": IMAPSeenHash("Inbox", 42, 7), "validity": IMAPSeenHash("INBOX", 43, 7), "uid": IMAPSeenHash("INBOX", 42, 8),
		"pop3": POP3SeenHash("INBOX"),
	} {
		if h == base {
			t.Errorf("%s does not change the hash", name)
		}
	}
	// A server-chosen string never reaches a key unhashed, so a hostile UIDL
	// still yields a valid key segment.
	if !validSeenHash(POP3SeenHash("../../x\x00\n")) || !validSeenHash(base) {
		t.Error("hash is not a valid key segment")
	}
}

func TestFetchLeftovers(t *testing.T) {
	s, _ := newFetchEnv(t)
	live, orphanState, orphanSeen, orphanBoth := fetchGUID(1), fetchGUID(2), fetchGUID(3), fetchGUID(4)
	if err := s.Put("u", &bongov1.FetchAccount{Guid: live}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutState("u", live, &bongov1.FetchState{}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := s.MarkSeen("u", live, fmt.Sprintf("%032x", i), &bongov1.FetchSeen{At: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutState("u", orphanState, &bongov1.FetchState{}); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		if err := s.MarkSeen("u", orphanSeen, fmt.Sprintf("%032x", i), &bongov1.FetchSeen{At: 1}); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkSeen("u", orphanBoth, fmt.Sprintf("%032x", i), &bongov1.FetchSeen{At: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutState("u", orphanBoth, &bongov1.FetchState{}); err != nil {
		t.Fatal(err)
	}
	// Another user's leftovers are not reported, nor is a similar user id.
	if err := s.PutState("uu", fetchGUID(7), &bongov1.FetchState{}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListAccountGUIDsWithLeftovers("u")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{orphanState, orphanSeen, orphanBoth}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("leftovers = %v, want %v", got, want)
	}
	if got, _ := s.ListAccountGUIDsWithLeftovers("nobody"); len(got) != 0 {
		t.Fatalf("leftovers for a user with none = %v", got)
	}
}

// Deleting by user prefix (as AdminHandler.DeleteUser does) takes every fetch
// key kind, because all three live under /user/{id}/.
func TestFetchKeysLiveUnderUserPrefix(t *testing.T) {
	s, _ := newFetchEnv(t)
	g := fetchGUID(1)
	_ = s.Put("u", &bongov1.FetchAccount{Guid: g})
	_ = s.PutState("u", g, &bongov1.FetchState{})
	_ = s.MarkSeen("u", g, strings.Repeat("c", 32), &bongov1.FetchSeen{})
	it, err := s.pebble.PrefixScan([]byte("/user/u/"))
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	var kinds []string
	for it.Next() {
		kinds = append(kinds, strings.Split(string(it.Key()), "/")[3])
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{"fetch", "fetchseen", "fetchstate"}) {
		t.Fatalf("kinds under the user prefix = %v", kinds)
	}
}
