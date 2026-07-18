package store

import (
	"testing"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// newAddressTestEnv creates an AddressStore backed by a fresh Pebble and SystemIndex.
func newAddressTestEnv(t *testing.T) *AddressStore {
	t.Helper()
	dir := t.TempDir()

	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	// Use MetadataManager so we get a real system.db with the addresses table.
	mgr, err := NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })

	return NewAddressStore(ps, &directApplier{store: ps}, mgr.System)
}

// testRec builds an AddressRecord for testing.
func testRec(userID, domain, kind string, canSend, verified bool) *bongov1.AddressRecord {
	return &bongov1.AddressRecord{
		UserId:   userID,
		Domain:   domain,
		Kind:     kind,
		CanSend:  canSend,
		Verified: verified,
	}
}

// --- Put / Get round-trip ---

func TestAddressStore_PutGet(t *testing.T) {
	as := newAddressTestEnv(t)

	rec := testRec("user1", "example.com", "primary", true, true)
	if err := as.Put("alice@example.com", rec); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok, err := as.Get("alice@example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get returned ok=false, want true")
	}
	if got.UserId != "user1" {
		t.Errorf("user_id = %q, want %q", got.UserId, "user1")
	}
	if got.Domain != "example.com" {
		t.Errorf("domain = %q, want %q", got.Domain, "example.com")
	}
	if got.Kind != "primary" {
		t.Errorf("kind = %q, want %q", got.Kind, "primary")
	}
	if !got.CanSend {
		t.Error("can_send = false, want true")
	}
	if !got.Verified {
		t.Error("verified = false, want true")
	}
}

func TestAddressStore_PutGet_CaseInsensitive(t *testing.T) {
	as := newAddressTestEnv(t)

	// Store using mixed-case address; domain is also mixed-case.
	rec := testRec("user1", "Example.COM", "primary", true, false)
	if err := as.Put("Alice@Example.COM", rec); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Retrieve using lowercase address.
	got, ok, err := as.Get("alice@example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get returned ok=false after mixed-case Put, want true")
	}
	// Domain must be normalised to lowercase in the stored record.
	if got.Domain != "example.com" {
		t.Errorf("domain = %q, want %q (lowercase)", got.Domain, "example.com")
	}
}

// --- Get unknown address ---

func TestAddressStore_Get_NotFound(t *testing.T) {
	as := newAddressTestEnv(t)

	got, ok, err := as.Get("nobody@example.com")
	if err != nil {
		t.Fatalf("Get on unknown address returned error: %v", err)
	}
	if ok {
		t.Errorf("Get returned ok=true for unknown address, got record: %+v", got)
	}
	if got != nil {
		t.Errorf("Get returned non-nil record for unknown address: %+v", got)
	}
}

// --- Delete ---

func TestAddressStore_Delete(t *testing.T) {
	as := newAddressTestEnv(t)

	rec := testRec("user1", "example.com", "primary", true, true)
	if err := as.Put("bob@example.com", rec); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Verify it exists first.
	if _, ok, err := as.Get("bob@example.com"); err != nil || !ok {
		t.Fatalf("pre-delete Get: ok=%v err=%v", ok, err)
	}

	if err := as.Delete("bob@example.com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Pebble tier: should be gone.
	_, ok, err := as.Get("bob@example.com")
	if err != nil {
		t.Fatalf("post-delete Get error: %v", err)
	}
	if ok {
		t.Error("post-delete Get returned ok=true, want false")
	}

	// SQLite tier: verify row is gone.
	var count int
	err = as.system.DB().QueryRow(
		`SELECT COUNT(*) FROM addresses WHERE address = ?`, "bob@example.com",
	).Scan(&count)
	if err != nil {
		t.Fatalf("SQLite count after delete: %v", err)
	}
	if count != 0 {
		t.Errorf("SQLite still has %d row(s) after delete, want 0", count)
	}
}

func TestAddressStore_Delete_Unknown_IsIdempotent(t *testing.T) {
	as := newAddressTestEnv(t)

	// Deleting a non-existent address must not return an error.
	if err := as.Delete("ghost@example.com"); err != nil {
		t.Errorf("Delete of unknown address returned error: %v", err)
	}
}

// --- ListByUser ---

func TestAddressStore_ListByUser(t *testing.T) {
	as := newAddressTestEnv(t)

	rows := []struct {
		addr   string
		userID string
	}{
		{"alice@example.com", "user1"},
		{"alice-alias@example.com", "user1"},
		{"bob@example.com", "user2"},
	}

	for _, a := range rows {
		rec := testRec(a.userID, "example.com", "primary", true, true)
		if err := as.Put(a.addr, rec); err != nil {
			t.Fatalf("Put %q: %v", a.addr, err)
		}
	}

	user1Addrs, err := as.ListByUser("user1")
	if err != nil {
		t.Fatalf("ListByUser(user1): %v", err)
	}
	if len(user1Addrs) != 2 {
		t.Errorf("user1 address count = %d, want 2", len(user1Addrs))
	}

	user2Addrs, err := as.ListByUser("user2")
	if err != nil {
		t.Fatalf("ListByUser(user2): %v", err)
	}
	if len(user2Addrs) != 1 {
		t.Errorf("user2 address count = %d, want 1", len(user2Addrs))
	}
	if user2Addrs[0] != "bob@example.com" {
		t.Errorf("user2 address = %q, want %q", user2Addrs[0], "bob@example.com")
	}
}

func TestAddressStore_ListByUser_Empty(t *testing.T) {
	as := newAddressTestEnv(t)

	addrs, err := as.ListByUser("nobody")
	if err != nil {
		t.Fatalf("ListByUser on unknown user: %v", err)
	}
	if len(addrs) != 0 {
		t.Errorf("expected 0 addresses, got %d", len(addrs))
	}
}

// --- SQLite mirror upsert ---

func TestAddressStore_SQLiteMirror(t *testing.T) {
	as := newAddressTestEnv(t)

	rec := testRec("user1", "example.com", "alias", false, true)
	if err := as.Put("carol@example.com", rec); err != nil {
		t.Fatalf("Put: %v", err)
	}

	var userID, domain, kind string
	var canSend, verified int
	err := as.system.DB().QueryRow(
		`SELECT user_id, domain, kind, can_send, verified FROM addresses WHERE address = ?`,
		"carol@example.com",
	).Scan(&userID, &domain, &kind, &canSend, &verified)
	if err != nil {
		t.Fatalf("SQLite row not found: %v", err)
	}
	if userID != "user1" {
		t.Errorf("SQLite user_id = %q, want %q", userID, "user1")
	}
	if domain != "example.com" {
		t.Errorf("SQLite domain = %q, want %q", domain, "example.com")
	}
	if kind != "alias" {
		t.Errorf("SQLite kind = %q, want %q", kind, "alias")
	}
	if canSend != 0 {
		t.Errorf("SQLite can_send = %d, want 0", canSend)
	}
	if verified != 1 {
		t.Errorf("SQLite verified = %d, want 1", verified)
	}
}

// --- Table-driven canonical cases ---

func TestAddressStore_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		putAddr  string
		getAddr  string
		userID   string
		kind     string
		canSend  bool
		verified bool
		wantOK   bool
	}{
		{
			name:    "exact match",
			putAddr: "dave@test.com",
			getAddr: "dave@test.com",
			userID:  "u1",
			kind:    "primary",
			canSend: true,
			wantOK:  true,
		},
		{
			name:     "put uppercase get lowercase",
			putAddr:  "EVE@TEST.COM",
			getAddr:  "eve@test.com",
			userID:   "u2",
			kind:     "identity",
			verified: true,
			wantOK:   true,
		},
		{
			name:     "put lowercase get uppercase",
			putAddr:  "frank@test.com",
			getAddr:  "FRANK@TEST.COM",
			userID:   "u3",
			kind:     "alias",
			canSend:  true,
			verified: true,
			wantOK:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			as := newAddressTestEnv(t)
			rec := testRec(tc.userID, "test.com", tc.kind, tc.canSend, tc.verified)
			if err := as.Put(tc.putAddr, rec); err != nil {
				t.Fatalf("Put: %v", err)
			}
			got, ok, err := as.Get(tc.getAddr)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && got == nil {
				t.Fatal("expected record, got nil")
			}
		})
	}
}
