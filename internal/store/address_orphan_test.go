package store

import (
	"errors"
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

	// Before repair: the dead record still wins the tier, but the resolver now
	// refuses it rather than delivering to a nonexistent user.
	got, err := resolver.ResolveRecipient("shared@example.com")
	if !errors.Is(err, ErrRecipientUnavailable) {
		t.Fatalf("fixture invalid: expected the orphaned record to be refused, got %q err %v", got, err)
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

func TestFindOrphanedAddresses_LegacyAliasAndCatchAll(t *testing.T) {
	ps, as := newOrphanTestEnv(t)

	// Live account with a primary record.
	seedProfile(t, ps, "u-primary", &bongov1.UserProfile{Email: "primary@example.com"})
	if err := as.Put("primary@example.com", &bongov1.AddressRecord{UserId: "u-primary", Domain: "example.com", Kind: "primary"}); err != nil {
		t.Fatal(err)
	}
	// Live account found only through the profile email (no address record).
	seedProfile(t, ps, "u-email", &bongov1.UserProfile{Email: "Emailonly@Example.com"})
	// Disabled account counts as live.
	seedProfile(t, ps, "u-off", &bongov1.UserProfile{Email: "off@example.com", Disabled: true})
	if err := as.Put("off@example.com", &bongov1.AddressRecord{UserId: "u-off", Domain: "example.com", Kind: "primary"}); err != nil {
		t.Fatal(err)
	}
	// Existing address-index orphan.
	if err := as.Put("ghost@example.com", &bongov1.AddressRecord{UserId: "ghost", Domain: "example.com", Kind: "primary"}); err != nil {
		t.Fatal(err)
	}

	// Stale record: primary owned by a user with no profile, and no profile
	// carries the email. The alias is dead; the record is an index orphan.
	if err := as.Put("ghost2@example.com", &bongov1.AddressRecord{UserId: "nouser", Domain: "example.com", Kind: "primary"}); err != nil {
		t.Fatal(err)
	}
	seedAlias(t, ps, "stale@example.com", "ghost2@example.com")
	// Target holds only an identity record (live owner, different email):
	// the resolver ignores identity records for legacy aliases, so it is dead.
	seedProfile(t, ps, "u-id", &bongov1.UserProfile{Email: "idowner@example.com"})
	if err := as.Put("ident@example.com", &bongov1.AddressRecord{UserId: "u-id", Domain: "example.com", Kind: "identity"}); err != nil {
		t.Fatal(err)
	}
	seedAlias(t, ps, "identalias@example.com", "ident@example.com")

	seedAlias(t, ps, "dead@example.com", "gone@example.com")
	seedAlias(t, ps, "live1@example.com", "primary@example.com")
	// Mixed-case target reachable only through the profile-email fallback.
	seedAlias(t, ps, "live2@example.com", "  EmailOnly@Example.COM ")
	seedAlias(t, ps, "live3@example.com", "off@example.com")
	// Alias record owned by a disabled account whose profile email differs,
	// so only the address-record path can find it live.
	if err := as.Put("offalias@example.com", &bongov1.AddressRecord{UserId: "u-off", Domain: "example.com", Kind: "alias"}); err != nil {
		t.Fatal(err)
	}
	seedAlias(t, ps, "live5@example.com", "offalias@example.com")
	seedAlias(t, ps, "live4@example.com", "  PRIMARY@Example.com ")
	seedAlias(t, ps, "empty@example.com", "   ")

	seedCatchAll(t, ps, "dead.example", "ghost-cc")
	seedCatchAll(t, ps, "live.example", "u-primary")

	orphans, err := FindOrphanedAddresses(ps)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]OrphanedAddress{}
	for _, o := range orphans {
		got[o.Kind+"|"+o.Address] = o
	}
	want := map[string]OrphanedAddress{
		"primary|ghost@example.com":           {Address: "ghost@example.com", UserID: "ghost", Kind: "primary"},
		"legacy-alias|dead@example.com":       {Address: "dead@example.com", Target: "gone@example.com", Kind: KindLegacyAlias},
		"primary|ghost2@example.com":          {Address: "ghost2@example.com", UserID: "nouser", Kind: "primary"},
		"legacy-alias|stale@example.com":      {Address: "stale@example.com", Target: "ghost2@example.com", Kind: KindLegacyAlias},
		"legacy-alias|identalias@example.com": {Address: "identalias@example.com", Target: "ident@example.com", Kind: KindLegacyAlias},
		"catchall|dead.example":               {Address: "dead.example", UserID: "ghost-cc", Kind: KindCatchAll},
	}
	if len(got) != len(want) || len(orphans) != len(want) {
		t.Fatalf("orphans = %+v, want %+v", orphans, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %+v, want %+v", k, got[k], w)
		}
	}
}

func TestDeleteOrphanedAddresses_SkipsReportOnlyKinds(t *testing.T) {
	ps, as := newOrphanTestEnv(t)

	// X is a live address record that happens to share a name with a dead legacy alias.
	if err := as.Put("x@example.com", &bongov1.AddressRecord{UserId: "live", Domain: "example.com", Kind: "alias"}); err != nil {
		t.Fatal(err)
	}
	seedAlias(t, ps, "x@example.com", "gone@example.com")
	seedCatchAll(t, ps, "dead.example", "ghost-cc")

	deleted, err := DeleteOrphanedAddresses(ps, as, []OrphanedAddress{
		{Address: "x@example.com", Target: "gone@example.com", Kind: KindLegacyAlias},
		{Address: "dead.example", UserID: "ghost-cc", Kind: KindCatchAll},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted = %v, want none", deleted)
	}
	for _, k := range []string{"/system/address/x@example.com", "/system/alias/x@example.com", "/system/catchall/dead.example"} {
		if _, err := ps.Get([]byte(k)); err != nil {
			t.Errorf("key %s missing after repair: %v", k, err)
		}
	}
}

// A corrupt target record aborts the report rather than yielding a false
// orphan (the address-index pass, by contrast, skips corrupt records).
func TestFindOrphanedAddresses_CorruptAliasTargetRecordSkipped(t *testing.T) {
	ps, as := newOrphanTestEnv(t)
	if err := ps.Set([]byte("/system/address/bad@example.com"), []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	seedAlias(t, ps, "a@example.com", "bad@example.com")
	// A real index orphan must still be reported alongside the corrupt record.
	if err := as.Put("ghost@example.com", &bongov1.AddressRecord{UserId: "ghost", Domain: "example.com", Kind: "primary"}); err != nil {
		t.Fatal(err)
	}

	orphans, err := FindOrphanedAddresses(ps)
	if err != nil {
		t.Fatalf("FindOrphanedAddresses: %v", err)
	}
	if len(orphans) != 1 || orphans[0].Address != "ghost@example.com" || orphans[0].Kind != "primary" {
		t.Fatalf("orphans = %+v, want only the ghost@example.com index orphan", orphans)
	}
}
