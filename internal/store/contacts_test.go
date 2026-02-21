package store

import (
	"testing"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func newContactTestEnv(t *testing.T) *ContactStore {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return NewContactStore(ps, &directApplier{store: ps})
}

func TestContactStore_CRUDRoundTrip(t *testing.T) {
	cs := newContactTestEnv(t)
	userID := "test-user-1"

	contact := &bongov1.Contact{
		DisplayName: "John Doe",
		Email:       "john@example.com",
		Phone:       "555-1234",
		Company:     "Acme Corp",
		Notes:       "Test contact",
	}

	guid, err := cs.Create(userID, contact)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if guid == "" {
		t.Fatal("Create returned empty GUID")
	}

	got, err := cs.Get(userID, guid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DisplayName != "John Doe" {
		t.Errorf("DisplayName: got %q, want %q", got.DisplayName, "John Doe")
	}
	if got.Email != "john@example.com" {
		t.Errorf("Email: got %q, want %q", got.Email, "john@example.com")
	}
	if got.Phone != "555-1234" {
		t.Errorf("Phone: got %q, want %q", got.Phone, "555-1234")
	}
	if got.Company != "Acme Corp" {
		t.Errorf("Company: got %q, want %q", got.Company, "Acme Corp")
	}
	if got.Notes != "Test contact" {
		t.Errorf("Notes: got %q, want %q", got.Notes, "Test contact")
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
	if got.UpdatedAt == 0 {
		t.Error("UpdatedAt should be set")
	}
}

func TestContactStore_ListPagination(t *testing.T) {
	cs := newContactTestEnv(t)
	userID := "test-user-2"

	for i := 0; i < 5; i++ {
		c := &bongov1.Contact{
			DisplayName: "Contact " + string(rune('A'+i)),
			Email:       "c" + string(rune('a'+i)) + "@example.com",
		}
		if _, err := cs.Create(userID, c); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	contacts, total, err := cs.List(userID, 2, 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 {
		t.Errorf("total: got %d, want 5", total)
	}
	if len(contacts) != 2 {
		t.Errorf("contacts: got %d, want 2", len(contacts))
	}
}

func TestContactStore_Delete(t *testing.T) {
	cs := newContactTestEnv(t)
	userID := "test-user-3"

	guid, err := cs.Create(userID, &bongov1.Contact{
		DisplayName: "To Delete",
		Email:       "delete@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := cs.Delete(userID, guid); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := cs.Get(userID, guid)
	if err == nil && got != nil {
		t.Error("Get after delete should fail or return nil")
	}
}

func TestContactStore_FindByEmail(t *testing.T) {
	cs := newContactTestEnv(t)
	userID := "test-user-4"

	cs.Create(userID, &bongov1.Contact{
		DisplayName: "Alice",
		Email:       "alice@example.com",
	})
	cs.Create(userID, &bongov1.Contact{
		DisplayName: "Bob",
		Email:       "bob@example.com",
	})

	found, err := cs.FindByEmail(userID, "Alice@Example.com")
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if found == nil {
		t.Fatal("FindByEmail should find Alice (case insensitive)")
	}
	if found.DisplayName != "Alice" {
		t.Errorf("DisplayName: got %q, want %q", found.DisplayName, "Alice")
	}

	notFound, err := cs.FindByEmail(userID, "nobody@example.com")
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if notFound != nil {
		t.Error("FindByEmail should return nil for non-existent email")
	}
}

func TestContactStore_Update(t *testing.T) {
	cs := newContactTestEnv(t)
	userID := "test-user-5"

	guid, err := cs.Create(userID, &bongov1.Contact{
		DisplayName: "Original",
		Email:       "orig@example.com",
		Phone:       "111",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := &bongov1.Contact{
		DisplayName: "Updated",
		Email:       "updated@example.com",
		Phone:       "222",
	}
	if err := cs.Update(userID, guid, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := cs.Get(userID, guid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DisplayName != "Updated" {
		t.Errorf("DisplayName: got %q, want %q", got.DisplayName, "Updated")
	}
	if got.Email != "updated@example.com" {
		t.Errorf("Email: got %q, want %q", got.Email, "updated@example.com")
	}
	if got.Phone != "222" {
		t.Errorf("Phone: got %q, want %q", got.Phone, "222")
	}
}
