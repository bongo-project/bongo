package store

import (
	"testing"
)

// newCatchAllTestEnv creates isolated Pebble + CatchAllStore for tests.
func newCatchAllTestEnv(t *testing.T) (*PebbleStore, *CatchAllStore) {
	t.Helper()
	dir := t.TempDir()
	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	cs := NewCatchAllStore(ps, &directApplier{store: ps})
	return ps, cs
}

// TestCatchAllStore_SetGetClear verifies the Set→Get→Clear round-trip.
func TestCatchAllStore_SetGetClear(t *testing.T) {
	_, cs := newCatchAllTestEnv(t)

	const domain = "example.com"
	const ownerID = "user-abc-123"

	// Get on empty returns "", false, nil.
	got, ok, err := cs.Get(domain)
	if err != nil {
		t.Fatalf("Get before Set: %v", err)
	}
	if ok || got != "" {
		t.Errorf("Get before Set: want (empty, false), got (%q, %v)", got, ok)
	}

	// Set stores the owner.
	if err := cs.Set(domain, ownerID); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get returns the owner.
	got, ok, err = cs.Get(domain)
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if !ok || got != ownerID {
		t.Errorf("Get after Set: want (%q, true), got (%q, %v)", ownerID, got, ok)
	}

	// Clear removes it.
	if err := cs.Clear(domain); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	got, ok, err = cs.Get(domain)
	if err != nil {
		t.Fatalf("Get after Clear: %v", err)
	}
	if ok || got != "" {
		t.Errorf("Get after Clear: want (empty, false), got (%q, %v)", got, ok)
	}
}

// TestCatchAllStore_DomainLowercasing verifies that Set normalises the domain.
func TestCatchAllStore_DomainLowercasing(t *testing.T) {
	_, cs := newCatchAllTestEnv(t)

	if err := cs.Set("EXAMPLE.COM", "user1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get with lowercase domain must find it.
	got, ok, err := cs.Get("example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || got != "user1" {
		t.Errorf("case normalisation: want (user1, true), got (%q, %v)", got, ok)
	}

	// Get with uppercase must also find it (normalised on lookup too).
	got, ok, err = cs.Get("EXAMPLE.COM")
	if err != nil {
		t.Fatalf("Get uppercase: %v", err)
	}
	if !ok || got != "user1" {
		t.Errorf("uppercase lookup: want (user1, true), got (%q, %v)", got, ok)
	}
}

// TestCatchAllKey_Consistent verifies that CatchAllKey uses a consistent prefix
// so the resolver and the store agree on key format (defined once, used twice).
func TestCatchAllKey_Consistent(t *testing.T) {
	key := CatchAllKey("example.com")
	want := []byte("/system/catchall/example.com")
	if string(key) != string(want) {
		t.Errorf("CatchAllKey(%q) = %q, want %q", "example.com", key, want)
	}
}
