package store

import (
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newOrphanTestEnv(t *testing.T) (*PebbleStore, *AddressStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return ps, NewAddressStore(ps, &directApplier{store: ps}, nil)
}

// TestFindOrphanedAddressesReproducesShadowedAlias is the regression test for
// the reported incident: an account is deleted, an alias is created for the
// same address, and mail still resolves to the deleted account because its
// address-index record outranks the alias.
func TestFindOrphanedAddresses(t *testing.T) {
	ps, as := newOrphanTestEnv(t)

	// A live account with an address record.
	seedProfile(t, ps, "live-user", &bongov1.UserProfile{Email: "live@example.com"})
	if err := as.Put("live@example.com", &bongov1.AddressRecord{
		UserId: "live-user", Domain: "example.com", Kind: "primary",
	}); err != nil {
		t.Fatalf("put live address: %v", err)
	}

	// A deleted account: address record retained, profile gone.
	if err := as.Put("ghost@example.com", &bongov1.AddressRecord{
		UserId: "ghost-user", Domain: "example.com", Kind: "primary",
	}); err != nil {
		t.Fatalf("put ghost address: %v", err)
	}

	orphans, err := FindOrphanedAddresses(ps)
	if err != nil {
		t.Fatalf("FindOrphanedAddresses: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("found %d orphans (%+v), want exactly 1 - a live address must never be reported", len(orphans), orphans)
	}
	if orphans[0].Address != "ghost@example.com" || orphans[0].UserID != "ghost-user" {
		t.Errorf("orphan = %+v, want ghost@example.com -> ghost-user", orphans[0])
	}
}

// The resolver consults the address index before aliases, so this proves the
// actual routing consequence, not just the scan.
func TestOrphanedAddressShadowsAliasUntilRepaired(t *testing.T) {
	ps, as := newOrphanTestEnv(t)
	applier := &directApplier{store: ps}

	seedProfile(t, ps, "target-user", &bongov1.UserProfile{Email: "target@example.com"})

	// The deleted account's record, and the alias created afterwards.
	if err := as.Put("shared@example.com", &bongov1.AddressRecord{
		UserId: "ghost-user", Domain: "example.com", Kind: "primary",
	}); err != nil {
		t.Fatalf("put ghost address: %v", err)
	}
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/system/alias/shared@example.com"),
		Value: []byte("target@example.com"),
	}, time.Second); err != nil {
		t.Fatalf("put alias: %v", err)
	}

	resolver := NewRecipientResolver(as, ps, nil)

	// Before repair: the dead record wins, so mail goes to a nonexistent user.
	got, err := resolver.ResolveRecipient("shared@example.com")
	if err != nil || got != "ghost-user" {
		t.Fatalf("fixture invalid: expected the orphaned record to shadow the alias, got %q err %v", got, err)
	}

	orphans, err := FindOrphanedAddresses(ps)
	if err != nil {
		t.Fatalf("FindOrphanedAddresses: %v", err)
	}
	deleted, err := DeleteOrphanedAddresses(ps, as, orphans)
	if err != nil {
		t.Fatalf("DeleteOrphanedAddresses: %v", err)
	}
	if len(deleted) != 1 {
		t.Fatalf("deleted %v, want exactly the one orphan", deleted)
	}

	// After repair the alias tier is reached. Without a SystemIndex the alias
	// cannot map the target username to an ID, so the resolver must at least
	// stop returning the ghost.
	got, err = resolver.ResolveRecipient("shared@example.com")
	if got == "ghost-user" {
		t.Errorf("after repair the orphaned record still resolves (got %q, err %v)", got, err)
	}
}

// A record whose owner exists again between scan and repair must be left alone:
// deleting a live routing entry is worse than leaving a dead one.
func TestDeleteOrphanedAddressesSkipsRevivedOwner(t *testing.T) {
	ps, as := newOrphanTestEnv(t)

	if err := as.Put("revived@example.com", &bongov1.AddressRecord{
		UserId: "revived-user", Domain: "example.com", Kind: "primary",
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	stale := []OrphanedAddress{{Address: "revived@example.com", UserID: "revived-user", Kind: "primary"}}

	// The owner comes back after the scan produced `stale`.
	seedProfile(t, ps, "revived-user", &bongov1.UserProfile{Email: "revived@example.com"})

	deleted, err := DeleteOrphanedAddresses(ps, as, stale)
	if err != nil {
		t.Fatalf("DeleteOrphanedAddresses: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("deleted %v, want none - the owner exists again", deleted)
	}
	if _, ok, _ := as.Get("revived@example.com"); !ok {
		t.Error("BUG: a live address record was deleted on a stale orphan list")
	}
}
