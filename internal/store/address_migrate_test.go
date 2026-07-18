package store

import (
	"log/slog"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// newMigrateTestEnv returns a PebbleStore, an AddressStore (backed by directApplier),
// and the real SystemIndex so tests can drive PopulateAddressIndex directly.
func newMigrateTestEnv(t *testing.T) (*PebbleStore, *AddressStore, *SystemIndex) {
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
	return ps, as, mgr.System
}

// seedProfile writes a UserProfile protobuf directly to Pebble (bypasses Raft,
// mirrors what the agent does via ApplyCommand when creating a user).
func seedProfile(t *testing.T, ps *PebbleStore, userID string, profile *bongov1.UserProfile) {
	t.Helper()
	data, err := proto.Marshal(profile)
	if err != nil {
		t.Fatalf("seedProfile %q: marshal: %v", userID, err)
	}
	if err := ps.Set([]byte("/user/"+userID+"/profile"), data); err != nil {
		t.Fatalf("seedProfile %q: set: %v", userID, err)
	}
}

// seedAlias writes a /system/alias/{addr} → targetUsername entry to Pebble.
func seedAlias(t *testing.T, ps *PebbleStore, aliasAddr, targetUsername string) {
	t.Helper()
	if err := ps.Set([]byte("/system/alias/"+aliasAddr), []byte(targetUsername)); err != nil {
		t.Fatalf("seedAlias %q → %q: %v", aliasAddr, targetUsername, err)
	}
}

// --- Primary record tests ---

func TestPopulateAddressIndex_PrimaryRecords(t *testing.T) {
	ps, as, sys := newMigrateTestEnv(t)

	seedProfile(t, ps, "user1", &bongov1.UserProfile{Email: "alice@example.com"})
	seedProfile(t, ps, "user2", &bongov1.UserProfile{Email: "bob@test.com"})

	if err := PopulateAddressIndex(ps, as, sys, slog.Default()); err != nil {
		t.Fatalf("PopulateAddressIndex: %v", err)
	}

	tests := []struct {
		addr   string
		userID string
		domain string
	}{
		{"alice@example.com", "user1", "example.com"},
		{"bob@test.com", "user2", "test.com"},
	}
	for _, tc := range tests {
		rec, ok, err := as.Get(tc.addr)
		if err != nil {
			t.Errorf("Get(%q): %v", tc.addr, err)
			continue
		}
		if !ok {
			t.Errorf("Get(%q): not found after migration", tc.addr)
			continue
		}
		if rec.Kind != "primary" {
			t.Errorf("Get(%q).Kind = %q, want primary", tc.addr, rec.Kind)
		}
		if !rec.CanSend {
			t.Errorf("Get(%q).CanSend = false, want true", tc.addr)
		}
		if !rec.Verified {
			t.Errorf("Get(%q).Verified = false, want true", tc.addr)
		}
		if rec.UserId != tc.userID {
			t.Errorf("Get(%q).UserId = %q, want %q", tc.addr, rec.UserId, tc.userID)
		}
		if rec.Domain != tc.domain {
			t.Errorf("Get(%q).Domain = %q, want %q", tc.addr, rec.Domain, tc.domain)
		}
	}
}

// --- Alias record tests ---

func TestPopulateAddressIndex_AliasRecord(t *testing.T) {
	ps, as, sys := newMigrateTestEnv(t)

	// alice@example.com is the primary; alias@example.com routes to alice (user1).
	seedProfile(t, ps, "user1", &bongov1.UserProfile{Email: "alice@example.com"})
	seedAlias(t, ps, "alias@example.com", "alice@example.com")

	if err := PopulateAddressIndex(ps, as, sys, slog.Default()); err != nil {
		t.Fatalf("PopulateAddressIndex: %v", err)
	}

	rec, ok, err := as.Get("alias@example.com")
	if err != nil {
		t.Fatalf("Get alias: %v", err)
	}
	if !ok {
		t.Fatal("alias@example.com not found after migration")
	}
	if rec.Kind != "alias" {
		t.Errorf("kind = %q, want alias", rec.Kind)
	}
	if rec.CanSend {
		t.Error("can_send = true, want false")
	}
	if !rec.Verified {
		t.Error("verified = false, want true")
	}
	// UserId must be the RESOLVED user_id (user1), NOT the raw username string.
	if rec.UserId != "user1" {
		t.Errorf("UserId = %q, want user1 (resolved)", rec.UserId)
	}
	if rec.Domain != "example.com" {
		t.Errorf("Domain = %q, want example.com", rec.Domain)
	}
}

// --- Idempotence test ---

func TestPopulateAddressIndex_Idempotent(t *testing.T) {
	ps, as, sys := newMigrateTestEnv(t)

	seedProfile(t, ps, "user1", &bongov1.UserProfile{Email: "alice@example.com"})
	seedAlias(t, ps, "alias@example.com", "alice@example.com")

	// Run the migration twice.
	for i := range 2 {
		if err := PopulateAddressIndex(ps, as, sys, slog.Default()); err != nil {
			t.Fatalf("run %d: PopulateAddressIndex: %v", i+1, err)
		}
	}

	// Verify records are correct after two runs.
	alice, ok, err := as.Get("alice@example.com")
	if err != nil || !ok {
		t.Fatalf("alice not found: ok=%v err=%v", ok, err)
	}
	if alice.Kind != "primary" || alice.UserId != "user1" {
		t.Errorf("alice record wrong after idempotent run: %+v", alice)
	}

	aliasRec, ok2, err2 := as.Get("alias@example.com")
	if err2 != nil || !ok2 {
		t.Fatalf("alias not found: ok=%v err=%v", ok2, err2)
	}
	if aliasRec.Kind != "alias" || aliasRec.UserId != "user1" {
		t.Errorf("alias record wrong after idempotent run: %+v", aliasRec)
	}

	// SQLite should have exactly 2 rows (no duplicates from second run).
	var count int
	if err := as.system.DB().QueryRow("SELECT COUNT(*) FROM addresses").Scan(&count); err != nil {
		t.Fatalf("count addresses: %v", err)
	}
	if count != 2 {
		t.Errorf("address count = %d after 2 runs, want 2", count)
	}
}

// --- Safety/skip tests ---

func TestPopulateAddressIndex_UnresolvableAlias_Skipped(t *testing.T) {
	ps, as, sys := newMigrateTestEnv(t)

	// Alias points to a username that has no Pebble profile and is not in SQLite.
	seedAlias(t, ps, "orphan@example.com", "nobody@example.com")

	// Must not return an error; the orphan alias is silently skipped.
	if err := PopulateAddressIndex(ps, as, sys, slog.Default()); err != nil {
		t.Fatalf("PopulateAddressIndex with orphan alias: %v", err)
	}

	_, ok, err := as.Get("orphan@example.com")
	if err != nil {
		t.Fatalf("Get orphan: %v", err)
	}
	if ok {
		t.Error("orphan alias was stored; want it skipped (unresolvable target)")
	}
}

func TestPopulateAddressIndex_MalformedEmail_Skipped(t *testing.T) {
	ps, as, sys := newMigrateTestEnv(t)

	// Profile with no '@' in the email — should be skipped, not fatal.
	seedProfile(t, ps, "user1", &bongov1.UserProfile{Email: "notanemail"})
	seedProfile(t, ps, "user2", &bongov1.UserProfile{Email: "good@example.com"})

	if err := PopulateAddressIndex(ps, as, sys, slog.Default()); err != nil {
		t.Fatalf("PopulateAddressIndex: %v", err)
	}

	// Malformed profile: no address record created.
	_, ok, err := as.Get("notanemail")
	if err != nil {
		t.Fatalf("Get notanemail: %v", err)
	}
	if ok {
		t.Error("malformed email was stored; want it skipped")
	}

	// Good profile must still be created.
	rec, ok2, err2 := as.Get("good@example.com")
	if err2 != nil || !ok2 {
		t.Fatalf("good profile not found: ok=%v err=%v", ok2, err2)
	}
	if rec.UserId != "user2" {
		t.Errorf("good profile UserId = %q, want user2", rec.UserId)
	}
}

func TestPopulateAddressIndex_EmptyEmail_Skipped(t *testing.T) {
	ps, as, sys := newMigrateTestEnv(t)

	// Profile with empty email — should be skipped.
	seedProfile(t, ps, "user1", &bongov1.UserProfile{Email: ""})

	if err := PopulateAddressIndex(ps, as, sys, slog.Default()); err != nil {
		t.Fatalf("PopulateAddressIndex: %v", err)
	}

	// No records should have been created.
	var count int
	if err := as.system.DB().QueryRow("SELECT COUNT(*) FROM addresses").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 records for empty email, got %d", count)
	}
}

// --- Table-driven comprehensive test ---

func TestPopulateAddressIndex_TableDriven(t *testing.T) {
	type wantRec struct {
		kind    string
		canSend bool
		userID  string
	}

	tests := []struct {
		name          string
		users         map[string]string // userID -> email
		aliases       map[string]string // aliasAddr -> targetUsername (email)
		wantAddresses map[string]wantRec
		wantMissing   []string
	}{
		{
			name:  "two users no aliases",
			users: map[string]string{"u1": "alice@a.com", "u2": "bob@b.com"},
			wantAddresses: map[string]wantRec{
				"alice@a.com": {"primary", true, "u1"},
				"bob@b.com":   {"primary", true, "u2"},
			},
		},
		{
			name:    "alias resolves to existing user by email",
			users:   map[string]string{"u1": "user@x.com"},
			aliases: map[string]string{"alias@x.com": "user@x.com"},
			wantAddresses: map[string]wantRec{
				"user@x.com":  {"primary", true, "u1"},
				"alias@x.com": {"alias", false, "u1"},
			},
		},
		{
			name:        "orphan alias skipped without error",
			aliases:     map[string]string{"dead@d.com": "nonexistent@d.com"},
			wantMissing: []string{"dead@d.com"},
		},
		{
			name: "case normalisation: uppercase input stored lowercase",
			users: map[string]string{
				"u1": "UPPER@EXAMPLE.COM",
			},
			wantAddresses: map[string]wantRec{
				"upper@example.com": {"primary", true, "u1"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ps, as, sys := newMigrateTestEnv(t)

			for userID, email := range tc.users {
				seedProfile(t, ps, userID, &bongov1.UserProfile{Email: email})
			}
			for addr, target := range tc.aliases {
				seedAlias(t, ps, addr, target)
			}

			// Run twice to validate idempotence as part of every sub-test.
			for i := range 2 {
				if err := PopulateAddressIndex(ps, as, sys, slog.Default()); err != nil {
					t.Fatalf("run %d: PopulateAddressIndex: %v", i+1, err)
				}
			}

			for addr, want := range tc.wantAddresses {
				rec, ok, err := as.Get(addr)
				if err != nil {
					t.Errorf("Get(%q): %v", addr, err)
					continue
				}
				if !ok {
					t.Errorf("Get(%q): not found", addr)
					continue
				}
				if rec.Kind != want.kind {
					t.Errorf("Get(%q).Kind = %q, want %q", addr, rec.Kind, want.kind)
				}
				if rec.CanSend != want.canSend {
					t.Errorf("Get(%q).CanSend = %v, want %v", addr, rec.CanSend, want.canSend)
				}
				if rec.UserId != want.userID {
					t.Errorf("Get(%q).UserId = %q, want %q", addr, rec.UserId, want.userID)
				}
			}

			for _, addr := range tc.wantMissing {
				_, ok, err := as.Get(addr)
				if err != nil {
					t.Errorf("Get(%q): %v", addr, err)
					continue
				}
				if ok {
					t.Errorf("Get(%q): found but should be missing/skipped", addr)
				}
			}
		})
	}
}
