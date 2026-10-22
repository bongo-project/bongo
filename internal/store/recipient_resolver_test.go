package store

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// insertUser is a test helper that inserts a user directly into the SQLite users
// table, simulating what the FSM does on user creation (username = profile.Email).
func insertUser(t *testing.T, sys *SystemIndex, id, email string) {
	t.Helper()
	_, err := sys.DB().Exec(
		`INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
		 VALUES (?, ?, ?, 1, unixepoch(), unixepoch())
		 ON CONFLICT(id) DO NOTHING`,
		id, email, "Test User",
	)
	if err != nil {
		t.Fatalf("insertUser %q: %v", id, err)
	}
}

// newResolverTestEnv creates isolated test state for RecipientResolver tests.
func newResolverTestEnv(t *testing.T) (*PebbleStore, *AddressStore, *SystemIndex, *RecipientResolver) {
	t.Helper()
	dir := t.TempDir()

	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	mgr, err := NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })

	as := NewAddressStore(ps, &directApplier{store: ps}, mgr.System)
	resolver := NewRecipientResolver(as, ps, mgr.System)
	return ps, as, mgr.System, resolver
}

// putAddress writes an AddressRecord into the address index (tier 1).
func putAddress(t *testing.T, as *AddressStore, addr, userID, kind string) {
	t.Helper()
	if err := as.Put(addr, &bongov1.AddressRecord{
		UserId: userID,
		Domain: domainOf(addr),
		Kind:   kind,
	}); err != nil {
		t.Fatalf("putAddress %q: %v", addr, err)
	}
}

func domainOf(email string) string {
	for i := len(email) - 1; i >= 0; i-- {
		if email[i] == '@' {
			return email[i+1:]
		}
	}
	return ""
}

// --- Individual unit tests ---

func TestRecipientResolver_UserHit(t *testing.T) {
	ps, as, sys, resolver := newResolverTestEnv(t)
	insertUser(t, sys, "user1", "alice@example.com")
	putUserProfile(t, ps, "user1", "alice@example.com", false)
	putAddress(t, as, "alice@example.com", "user1", "primary")

	userID, err := resolver.ResolveRecipient("alice@example.com")
	if err != nil {
		t.Fatalf("ResolveRecipient: %v", err)
	}
	if userID != "user1" {
		t.Errorf("got %q, want user1", userID)
	}
}

func TestRecipientResolver_AliasHitViaIndex(t *testing.T) {
	ps, as, sys, resolver := newResolverTestEnv(t)
	insertUser(t, sys, "user1", "alice@example.com")
	putUserProfile(t, ps, "user1", "alice@example.com", false)
	putAddress(t, as, "alice@example.com", "user1", "primary")
	putAddress(t, as, "alias@example.com", "user1", "alias")

	userID, err := resolver.ResolveRecipient("alias@example.com")
	if err != nil {
		t.Fatalf("ResolveRecipient alias: %v", err)
	}
	if userID != "user1" {
		t.Errorf("got %q, want user1", userID)
	}
}

// TestRecipientResolver_LegacyFallbackHit verifies tier 2b: a user in the SQLite
// users table but NOT in the address index is still resolved (post-boot account).
func TestRecipientResolver_LegacyFallbackHit(t *testing.T) {
	ps, _, sys, resolver := newResolverTestEnv(t)
	// User in SQLite only — no address index entry.
	insertUser(t, sys, "user2", "bob@example.com")
	putUserProfile(t, ps, "user2", "bob@example.com", false)

	userID, err := resolver.ResolveRecipient("bob@example.com")
	if err != nil {
		t.Fatalf("ResolveRecipient legacy: %v", err)
	}
	if userID != "user2" {
		t.Errorf("got %q, want user2", userID)
	}
}

// TestRecipientResolver_LegacyAliasFallback verifies tier 2a: a legacy Pebble
// /system/alias key resolves even when no address index entry exists.
func TestRecipientResolver_LegacyAliasFallback(t *testing.T) {
	ps, _, sys, resolver := newResolverTestEnv(t)
	insertUser(t, sys, "user3", "carol@example.com")
	putUserProfile(t, ps, "user3", "carol@example.com", false)
	// Legacy alias Pebble key (not in address index).
	seedAlias(t, ps, "legacyalias@example.com", "carol@example.com")

	userID, err := resolver.ResolveRecipient("legacyalias@example.com")
	if err != nil {
		t.Fatalf("ResolveRecipient legacy alias: %v", err)
	}
	if userID != "user3" {
		t.Errorf("got %q, want user3", userID)
	}
}

// TestRecipientResolver_Miss verifies that an unknown address returns an error
// (open-relay guard: unknown addresses must not be silently accepted).
func TestRecipientResolver_Miss(t *testing.T) {
	_, _, _, resolver := newResolverTestEnv(t)

	_, err := resolver.ResolveRecipient("nobody@example.com")
	if err == nil {
		t.Error("expected error for unknown address, got nil (open-relay risk)")
	}
}

// --- Table-driven comprehensive test ---

func TestRecipientResolver_TableDriven(t *testing.T) {
	type setup struct {
		// index entries: addr -> userID
		indexPrimary map[string]string
		indexAlias   map[string]string
		// SQLite users: userID -> email
		sqliteUsers map[string]string
		// legacy Pebble alias keys: aliasAddr -> targetEmail
		legacyAliases map[string]string
	}

	tests := []struct {
		name      string
		setup     setup
		email     string
		wantID    string
		wantError bool
	}{
		{
			name: "primary user hit via address index",
			setup: setup{
				indexPrimary: map[string]string{"primary@test.com": "u1"},
				sqliteUsers:  map[string]string{"u1": "primary@test.com"},
			},
			email:  "primary@test.com",
			wantID: "u1",
		},
		{
			name: "alias hit via address index",
			setup: setup{
				indexPrimary: map[string]string{"owner@test.com": "u1"},
				indexAlias:   map[string]string{"alias@test.com": "u1"},
				sqliteUsers:  map[string]string{"u1": "owner@test.com"},
			},
			email:  "alias@test.com",
			wantID: "u1",
		},
		{
			name: "legacy fallback - primary user not in address index",
			setup: setup{
				sqliteUsers: map[string]string{"u2": "newuser@test.com"},
			},
			email:  "newuser@test.com",
			wantID: "u2",
		},
		{
			name: "legacy fallback - alias key in Pebble not in index",
			setup: setup{
				sqliteUsers:   map[string]string{"u3": "target@test.com"},
				legacyAliases: map[string]string{"oldalias@test.com": "target@test.com"},
			},
			email:  "oldalias@test.com",
			wantID: "u3",
		},
		{
			name:      "miss - unknown address returns error",
			setup:     setup{},
			email:     "unknown@test.com",
			wantError: true,
		},
		{
			name: "case normalization - uppercase input resolved lowercase",
			setup: setup{
				indexPrimary: map[string]string{"lower@test.com": "u4"},
				sqliteUsers:  map[string]string{"u4": "lower@test.com"},
			},
			email:  "LOWER@TEST.COM",
			wantID: "u4",
		},
		{
			name: "angle bracket stripping",
			setup: setup{
				indexPrimary: map[string]string{"angle@test.com": "u5"},
				sqliteUsers:  map[string]string{"u5": "angle@test.com"},
			},
			email:  "<angle@test.com>",
			wantID: "u5",
		},
		{
			name: "index takes priority over legacy for same address",
			setup: setup{
				// Index says u1 owns the address; legacy alias also exists for u2.
				indexPrimary:  map[string]string{"shared@test.com": "u1"},
				sqliteUsers:   map[string]string{"u1": "shared@test.com", "u2": "other@test.com"},
				legacyAliases: map[string]string{"shared@test.com": "other@test.com"},
			},
			email:  "shared@test.com",
			wantID: "u1",
		},
		{
			name: "disabled user not resolved",
			// Users with enabled=0 must not resolve (access-revocation).
			setup: setup{
				sqliteUsers: map[string]string{}, // we add disabled manually below
			},
			email:     "disabled@test.com",
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ps, as, sys, resolver := newResolverTestEnv(t)

			// Seed SQLite users.
			for id, email := range tc.setup.sqliteUsers {
				insertUser(t, sys, id, email)
				putUserProfile(t, ps, id, email, false)
			}

			// Disabled-user special case: add with enabled=0 via raw SQL.
			if tc.name == "disabled user not resolved" {
				_, err := sys.DB().Exec(
					`INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
					 VALUES ('disabled1', 'disabled@test.com', 'Disabled', 0, unixepoch(), unixepoch())`,
				)
				if err != nil {
					t.Fatalf("insert disabled user: %v", err)
				}
				putUserProfile(t, ps, "disabled1", "disabled@test.com", true)
			}

			// Seed address index.
			for addr, uid := range tc.setup.indexPrimary {
				putAddress(t, as, addr, uid, "primary")
			}
			for addr, uid := range tc.setup.indexAlias {
				putAddress(t, as, addr, uid, "alias")
			}

			// Seed legacy Pebble alias keys.
			for aliasAddr, target := range tc.setup.legacyAliases {
				seedAlias(t, ps, aliasAddr, target)
			}

			userID, err := resolver.ResolveRecipient(tc.email)
			if tc.wantError {
				if err == nil {
					t.Errorf("want error (reject), got userID=%q", userID)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if userID != tc.wantID {
				t.Errorf("got %q, want %q", userID, tc.wantID)
			}
		})
	}
}

// --- Catch-all (tier 3) tests ---

// seedCatchAll writes a catch-all entry for a domain directly to Pebble.
func seedCatchAll(t *testing.T, ps *PebbleStore, domain, ownerID string) {
	t.Helper()
	if err := ps.Set(CatchAllKey(domain), []byte(ownerID)); err != nil {
		t.Fatalf("seedCatchAll %q → %q: %v", domain, ownerID, err)
	}
}

// TestRecipientResolver_CatchAll_Hit verifies that an unmatched address on a domain
// with a catch-all configured resolves to the catch-all owner.
func TestRecipientResolver_CatchAll_Hit(t *testing.T) {
	ps, _, _, resolver := newResolverTestEnv(t)

	const ownerID = "catchall-owner"
	putUserProfile(t, ps, ownerID, "owner@catchall.example.com", false)
	seedCatchAll(t, ps, "catchall.example.com", ownerID)

	userID, err := resolver.ResolveRecipient("random@catchall.example.com")
	if err != nil {
		t.Fatalf("expected catch-all resolution, got error: %v", err)
	}
	if userID != ownerID {
		t.Errorf("got %q, want %q", userID, ownerID)
	}
}

// TestRecipientResolver_CatchAll_Miss verifies that without a catch-all,
// unmatched mail is still rejected — no open relay.
func TestRecipientResolver_CatchAll_Miss(t *testing.T) {
	_, _, _, resolver := newResolverTestEnv(t)

	_, err := resolver.ResolveRecipient("nobody@nodomain.example.com")
	if err == nil {
		t.Error("expected rejection for domain without catch-all (open-relay risk), got nil")
	}
}

// TestRecipientResolver_ExplicitBeats_CatchAll verifies that an explicit user
// or alias always wins over the catch-all.
func TestRecipientResolver_ExplicitBeats_CatchAll(t *testing.T) {
	ps, as, sys, resolver := newResolverTestEnv(t)

	// Set up a catch-all on the domain.
	seedCatchAll(t, ps, "example.com", "catchall-owner")
	putUserProfile(t, ps, "catchall-owner", "owner@example.com", false)

	// Also set up an explicit user for bob@example.com.
	insertUser(t, sys, "bob-id", "bob@example.com")
	putUserProfile(t, ps, "bob-id", "bob@example.com", false)
	putAddress(t, as, "bob@example.com", "bob-id", "primary")

	// bob@example.com must resolve to bob, not the catch-all owner.
	userID, err := resolver.ResolveRecipient("bob@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userID != "bob-id" {
		t.Errorf("got %q, want %q (explicit must beat catch-all)", userID, "bob-id")
	}

	// A non-existent address on same domain falls back to catch-all.
	userID, err = resolver.ResolveRecipient("nobody@example.com")
	if err != nil {
		t.Fatalf("catch-all should accept non-existent address: %v", err)
	}
	if userID != "catchall-owner" {
		t.Errorf("got %q, want %q", userID, "catchall-owner")
	}
}

// TestRecipientResolver_CatchAll_CaseNormalisation verifies that a catch-all
// lookup is case-insensitive (domain part lowercased before lookup).
func TestRecipientResolver_CatchAll_CaseNormalisation(t *testing.T) {
	ps, _, _, resolver := newResolverTestEnv(t)
	seedCatchAll(t, ps, "mixedcase.example.com", "owner1")
	putUserProfile(t, ps, "owner1", "owner1@mixedcase.example.com", false)

	userID, err := resolver.ResolveRecipient("Anything@MIXEDCASE.EXAMPLE.COM")
	if err != nil {
		t.Fatalf("catch-all case normalisation: %v", err)
	}
	if userID != "owner1" {
		t.Errorf("got %q, want owner1", userID)
	}
}

// TestRecipientResolver_NilAddressStoreGraceful verifies that a nil AddressStore
// (edge/degraded node) falls through to the legacy tier without panicking.
func TestRecipientResolver_NilAddressStoreGraceful(t *testing.T) {
	dir := t.TempDir()
	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	mgr, err := NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })

	// nil AddressStore — tier 1 must be skipped, tier 2 should still work.
	resolver := NewRecipientResolver(nil, ps, mgr.System)
	insertUser(t, mgr.System, "u1", "edge@example.com")
	putUserProfile(t, ps, "u1", "edge@example.com", false)

	userID, err := resolver.ResolveRecipient("edge@example.com")
	if err != nil {
		t.Fatalf("expected tier-2 resolution to succeed, got: %v", err)
	}
	if userID != "u1" {
		t.Errorf("got %q, want u1", userID)
	}
}
