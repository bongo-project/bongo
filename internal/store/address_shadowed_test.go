package store

import (
	"bytes"
	"testing"
)

func TestFindShadowedIdentities(t *testing.T) {
	ps, as, sys, _ := newResolverTestEnv(t)
	insertUser(t, sys, "userB", "b@example.com")
	seedCatchAll(t, ps, "catch.example", "catcher")

	putIdentity(t, as, "b@example.com", "userA", true)       // verified, collides with account
	putIdentity(t, as, "x@external.example", "userA", false) // unverified, no conflict
	putIdentity(t, as, "ok@external.example", "userA", true) // verified, clean: not reported
	putIdentity(t, as, "any@catch.example", "userA", false)  // unverified, catch-all
	putAddress(t, as, "p@example.com", "userP", "primary")   // not an identity

	snapshot := func() map[string][]byte {
		m := map[string][]byte{}
		iter, err := ps.PrefixScan([]byte("/system/"))
		if err != nil {
			t.Fatal(err)
		}
		defer iter.Close()
		for iter.Next() {
			v, _ := iter.Value()
			m[string(iter.Key())] = bytes.Clone(v)
		}
		return m
	}
	before := snapshot()

	got, err := FindShadowedIdentities(ps, sys)
	if err != nil {
		t.Fatal(err)
	}
	byAddr := map[string]ShadowedIdentity{}
	for _, g := range got {
		byAddr[g.Address] = g
	}
	if len(got) != 3 {
		t.Fatalf("got %d results %+v, want 3", len(got), got)
	}
	if g := byAddr["b@example.com"]; g.Conflict != "account" || g.ConflictOwner != "userB" || !g.Verified {
		t.Errorf("b@example.com = %+v", g)
	}
	if g := byAddr["x@external.example"]; g.Conflict != "" || g.Verified {
		t.Errorf("x@external.example = %+v", g)
	}
	if g := byAddr["any@catch.example"]; g.Conflict != "catch-all" || g.ConflictOwner != "catcher" {
		t.Errorf("any@catch.example = %+v", g)
	}

	after := snapshot()
	if len(before) != len(after) {
		t.Fatalf("key count changed %d -> %d", len(before), len(after))
	}
	for k, v := range before {
		if !bytes.Equal(after[k], v) {
			t.Errorf("record %s was modified", k)
		}
	}
}

func TestFindShadowedIdentities_Revoke(t *testing.T) {
	ps, as, sys, _ := newResolverTestEnv(t)
	// B: a live account (profile plus users row).
	putUserProfile(t, ps, "userB", "b@example.com", false)
	insertUser(t, sys, "userB", "b@example.com")
	// A: the identity owner, also live.
	putUserProfile(t, ps, "userA", "a@example.com", false)
	insertUser(t, sys, "userA", "a@example.com")
	// Orphan users row: no Pebble profile.
	insertUser(t, sys, "ghost", "ghost@example.com")
	// Alias-table conflict backed by Pebble and SQLite.
	seedAlias(t, ps, "sales@example.com", "b@example.com")
	if _, err := sys.DB().Exec(`INSERT INTO aliases (address, target_user_id, enabled, created_at) VALUES ('sales@example.com', 'userB', 1, 0)`); err != nil {
		t.Fatal(err)
	}
	// Stale alias-table row with no Pebble key.
	if _, err := sys.DB().Exec(`INSERT INTO aliases (address, target_user_id, enabled, created_at) VALUES ('stale@example.com', 'userB', 1, 0)`); err != nil {
		t.Fatal(err)
	}
	// Legacy alias key pointing at the identity's own owner.
	seedAlias(t, ps, "mine@example.com", "a@example.com")
	// Legacy alias key pointing at B.
	seedAlias(t, ps, "legacy@example.com", "b@example.com")
	// Legacy alias key whose target has a users row but no profile.
	seedAlias(t, ps, "ghostalias@example.com", "ghost@example.com")
	// Legacy alias key whose target has no users row at all: unresolvable.
	seedAlias(t, ps, "unknown@example.com", "nobody@example.com")
	seedCatchAll(t, ps, "catch.example", "catcher")
	if err := ps.Set([]byte("/system/domain/hosted.example"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		addr     string
		verified bool
		conflict string
		revoke   bool
	}{
		{"b@example.com", true, "account", true},
		{"sales@example.com", true, "alias-table", true},
		{"legacy@example.com", true, "alias", true},
		{"b@example.com", false, "account", true},
		{"ghost@example.com", true, "account", false},
		{"stale@example.com", true, "alias-table", false},
		{"ghostalias@example.com", true, "alias", false},
		{"unknown@example.com", true, "alias", false},
		{"mine@example.com", true, "", false},
		{"any@catch.example", true, "catch-all", false},
		{"any@hosted.example", true, "hosted-domain", false},
		{"free@external.example", false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			putIdentity(t, as, tt.addr, "userA", tt.verified)
			got, err := FindShadowedIdentities(ps, sys)
			if err != nil {
				t.Fatal(err)
			}
			var found *ShadowedIdentity
			for i := range got {
				if got[i].Address == tt.addr {
					found = &got[i]
				}
			}
			if found == nil {
				if tt.verified && tt.conflict == "" {
					return // clean verified identity: not reported, so never revoked
				}
				t.Fatalf("%s not reported", tt.addr)
			}
			if found.Conflict != tt.conflict || found.Revoke != tt.revoke {
				t.Errorf("got conflict=%q revoke=%v, want %q %v", found.Conflict, found.Revoke, tt.conflict, tt.revoke)
			}
		})
	}

	t.Run("clean verified identity is not reported", func(t *testing.T) {
		putIdentity(t, as, "ok@external.example", "userA", true)
		got, _ := FindShadowedIdentities(ps, sys)
		for _, g := range got {
			if g.Address == "ok@external.example" {
				t.Errorf("reported: %+v", g)
			}
		}
	})
}
