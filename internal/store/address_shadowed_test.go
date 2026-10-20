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
