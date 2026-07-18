package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// newAddressAdminHandler builds an AdminHandler wired with real Pebble,
// MetadataManager, and AddressStore, suitable for address management tests.
func newAddressAdminHandler(t *testing.T) (*AdminHandler, *store.AddressStore, *store.MetadataManager, *store.PebbleStore) {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	mgr, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })

	applier := &directApplierTags{ps: ps}
	as := store.NewAddressStore(ps, applier, mgr.System)

	h := NewAdminHandler(ps, nil, mgr, applier, discardLogger())
	h.SetAddressStore(as)
	return h, as, mgr, ps
}

// insertTestUserWithProfile inserts a user into SQLite and writes a minimal
// UserProfile to Pebble (needed for ShowUser / primary-guard lookups).
func insertTestUserWithProfile(t *testing.T, ps *store.PebbleStore, mgr *store.MetadataManager, applier *directApplierTags, id, email string) {
	t.Helper()
	insertTestUser(t, mgr, id, email)

	// Write a minimal UserProfile so loadProfile works.
	profile := &bongov1.UserProfile{
		Email:       email,
		DisplayName: "Test User",
	}
	profileBytes, err := proto.Marshal(profile)
	if err != nil {
		t.Fatalf("insertTestUserWithProfile %q: marshal: %v", id, err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/profile", id)),
		Value: profileBytes,
	}
	if err := applier.ApplyCommand(cmd, time.Second); err != nil {
		t.Fatalf("insertTestUserWithProfile %q: apply: %v", id, err)
	}
}

// postAddAddress posts to /admin/users/{userID}/addresses with form data.
func postAddAddress(t *testing.T, h *AdminHandler, userID, address string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"address": {address}}
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/admin/users/%s/addresses", userID),
		strings.NewReader(form.Encode()),
	)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com"))
	r = withChiParam(r, "userID", userID)
	w := httptest.NewRecorder()
	h.AddAddress(w, r)
	return w
}

// postRemoveAddress posts to /admin/users/{userID}/addresses/delete with form data.
func postRemoveAddress(t *testing.T, h *AdminHandler, userID, address string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"address": {address}}
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/admin/users/%s/addresses/delete", userID),
		strings.NewReader(form.Encode()),
	)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com"))
	r = withChiParam(r, "userID", userID)
	w := httptest.NewRecorder()
	h.RemoveAddress(w, r)
	return w
}

// --- AddAddress handler tests ---

// TestAdminAddAddress_OK verifies that adding an address stores it in the index
// and makes it resolvable via Get (delivery would work).
func TestAdminAddAddress_OK(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	w := postAddAddress(t, h, "user1", "alice-extra@example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("AddAddress status = %d, want %d; body: %s", w.Code, http.StatusSeeOther, w.Body.String())
	}

	// Verify Pebble has the address record.
	rec, ok, err := as.Get("alice-extra@example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("added address not found in Pebble")
	}
	if rec.UserId != "user1" {
		t.Errorf("UserId = %q, want user1", rec.UserId)
	}
	if rec.Kind != "identity" {
		t.Errorf("Kind = %q, want identity", rec.Kind)
	}
	if rec.CanSend {
		t.Error("CanSend should be false for newly admin-added address")
	}
	if rec.Verified {
		t.Error("Verified should be false for newly admin-added address")
	}
	if rec.Domain != "example.com" {
		t.Errorf("Domain = %q, want example.com", rec.Domain)
	}
}

// TestAdminAddAddress_UnknownUser rejects adding an address for a user that
// does not exist, so no record is created that would route mail to a dead
// user_id.
func TestAdminAddAddress_UnknownUser(t *testing.T) {
	h, as, _, _ := newAddressAdminHandler(t)

	// No user inserted for "ghost".
	w := postAddAddress(t, h, "ghost", "ghost-extra@example.com")
	if w.Code != http.StatusNotFound {
		t.Fatalf("AddAddress for unknown user status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}

	if _, ok, err := as.Get("ghost-extra@example.com"); err != nil {
		t.Fatalf("Get: %v", err)
	} else if ok {
		t.Error("no address record should be created for a non-existent user")
	}
}

// TestAdminAddAddress_NoAt rejects an address without an '@' sign.
func TestAdminAddAddress_NoAt(t *testing.T) {
	h, _, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	w := postAddAddress(t, h, "user1", "notanemail")
	if w.Code == http.StatusSeeOther {
		t.Fatal("AddAddress should reject address without @, but redirected (accepted)")
	}
	if w.Code < 400 {
		t.Fatalf("AddAddress status = %d for bad address, want 4xx", w.Code)
	}
}

// TestAdminAddAddress_Empty rejects an empty address.
func TestAdminAddAddress_Empty(t *testing.T) {
	h, _, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	w := postAddAddress(t, h, "user1", "")
	if w.Code == http.StatusSeeOther {
		t.Fatal("AddAddress should reject empty address, but redirected")
	}
	if w.Code < 400 {
		t.Fatalf("AddAddress status = %d for empty address, want 4xx", w.Code)
	}
}

// TestAdminAddAddress_OwnedByAnotherUser rejects adding an address already
// owned by a different user account.
func TestAdminAddAddress_OwnedByAnotherUser(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")
	insertTestUserWithProfile(t, ps, mgr, applier, "user2", "bob@example.com")

	// Pre-seed the address as owned by user2.
	if err := as.Put("shared@example.com", &bongov1.AddressRecord{
		UserId: "user2",
		Domain: "example.com",
		Kind:   "primary",
	}); err != nil {
		t.Fatalf("pre-seeding shared address: %v", err)
	}

	// Attempt to add it for user1 should be rejected.
	w := postAddAddress(t, h, "user1", "shared@example.com")
	if w.Code == http.StatusSeeOther {
		t.Fatal("AddAddress should reject address owned by another user, but redirected (accepted)")
	}
	if w.Code < 400 {
		t.Fatalf("AddAddress status = %d for cross-user address, want 4xx", w.Code)
	}
}

// TestAdminAddAddress_AlreadyOwnedBySameUser allows re-adding an address that
// already belongs to the same user (idempotent PUT).
func TestAdminAddAddress_AlreadyOwnedBySameUser(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	// Pre-seed the address as owned by user1.
	if err := as.Put("alice-extra@example.com", &bongov1.AddressRecord{
		UserId: "user1",
		Domain: "example.com",
		Kind:   "identity",
	}); err != nil {
		t.Fatalf("pre-seeding address: %v", err)
	}

	// Re-adding should succeed (redirects).
	w := postAddAddress(t, h, "user1", "alice-extra@example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("AddAddress (same user, re-add) status = %d, want %d; body: %s",
			w.Code, http.StatusSeeOther, w.Body.String())
	}
}

// TestAdminAddAddress_NilStore returns 500 when the address store is not configured.
func TestAdminAddAddress_NilStore(t *testing.T) {
	h, _, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}
	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	// Remove the store.
	h.addresses = nil

	w := postAddAddress(t, h, "user1", "alice-extra@example.com")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("AddAddress without store status = %d, want 500", w.Code)
	}
}

// --- RemoveAddress handler tests ---

// TestAdminRemoveAddress_OK verifies that removing a non-primary address
// deletes it from Pebble (making delivery fail for that address).
func TestAdminRemoveAddress_OK(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	// Seed a non-primary identity address.
	if err := as.Put("alice-extra@example.com", &bongov1.AddressRecord{
		UserId: "user1",
		Domain: "example.com",
		Kind:   "identity",
	}); err != nil {
		t.Fatalf("pre-seeding: %v", err)
	}

	w := postRemoveAddress(t, h, "user1", "alice-extra@example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("RemoveAddress status = %d, want %d; body: %s", w.Code, http.StatusSeeOther, w.Body.String())
	}

	// Verify it's gone from Pebble.
	_, ok, err := as.Get("alice-extra@example.com")
	if err != nil {
		t.Fatalf("Get after removal: %v", err)
	}
	if ok {
		t.Error("address still present after removal")
	}
}

// TestAdminRemoveAddress_PrimaryKindGuard refuses to remove an address whose
// Kind == "primary".
func TestAdminRemoveAddress_PrimaryKindGuard(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	// Seed a primary address.
	if err := as.Put("alice@example.com", &bongov1.AddressRecord{
		UserId: "user1",
		Domain: "example.com",
		Kind:   "primary",
	}); err != nil {
		t.Fatalf("pre-seeding primary: %v", err)
	}

	w := postRemoveAddress(t, h, "user1", "alice@example.com")
	// A redirect with an error flash is acceptable; a bare 4xx is also acceptable.
	// What is NOT acceptable: a bare 303 with no error message, or actual deletion.
	if w.Code == http.StatusSeeOther {
		loc := w.Header().Get("Location")
		if !strings.Contains(strings.ToLower(loc), "flash") &&
			!strings.Contains(strings.ToLower(loc), "cannot") &&
			!strings.Contains(strings.ToLower(loc), "primary") &&
			!strings.Contains(strings.ToLower(loc), "error") {
			t.Fatalf("RemoveAddress redirected without error flash for primary address; Location: %s", loc)
		}
	}

	// Address must still exist regardless.
	_, ok, err := as.Get("alice@example.com")
	if err != nil {
		t.Fatalf("Get after blocked removal: %v", err)
	}
	if !ok {
		t.Error("primary address was deleted despite guard")
	}
}

// TestAdminRemoveAddress_ProfileEmailGuard refuses to remove an address that
// equals the user's profile email (even if Kind is not "primary").
func TestAdminRemoveAddress_ProfileEmailGuard(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	// Seed the same address as kind=identity (misconfigured, but guard must still fire).
	if err := as.Put("alice@example.com", &bongov1.AddressRecord{
		UserId: "user1",
		Domain: "example.com",
		Kind:   "identity",
	}); err != nil {
		t.Fatalf("pre-seeding: %v", err)
	}

	w := postRemoveAddress(t, h, "user1", "alice@example.com")
	if w.Code == http.StatusSeeOther {
		loc := w.Header().Get("Location")
		if !strings.Contains(strings.ToLower(loc), "flash") &&
			!strings.Contains(strings.ToLower(loc), "cannot") &&
			!strings.Contains(strings.ToLower(loc), "primary") &&
			!strings.Contains(strings.ToLower(loc), "error") {
			t.Fatalf("RemoveAddress redirected without error flash for profile-email address; Location: %s", loc)
		}
	}

	// Address must still exist.
	_, ok, err := as.Get("alice@example.com")
	if err != nil {
		t.Fatalf("Get after blocked removal: %v", err)
	}
	if !ok {
		t.Error("profile-email address was deleted despite guard")
	}
}

// TestAdminRemoveAddress_NilStore returns 500 when the address store is not configured.
func TestAdminRemoveAddress_NilStore(t *testing.T) {
	h, _, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}
	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	// Remove the store.
	h.addresses = nil

	w := postRemoveAddress(t, h, "user1", "alice@example.com")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("RemoveAddress without store status = %d, want 500", w.Code)
	}
}

// --- ResolveRecipient integration test ---

// TestAdminAddressGet_ResolveRecipient verifies that after admin-adding an address,
// AddressStore.Get returns a valid record owned by the target account. This is
// sufficient to prove inbound delivery would work (the resolver calls Get — no
// need to re-test full SMTP here).
func TestAdminAddressGet_ResolveRecipient(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	// Add a secondary address via the admin handler.
	w := postAddAddress(t, h, "user1", "b@d2.example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("AddAddress status = %d; body: %s", w.Code, w.Body.String())
	}

	// Get must return a record owned by user1.
	rec, ok, err := as.Get("b@d2.example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get returned not-found for just-added address")
	}
	if rec.UserId != "user1" {
		t.Errorf("Get().UserId = %q, want user1", rec.UserId)
	}

	// Remove the address and verify Get returns not-found (resolver would reject it).
	w2 := postRemoveAddress(t, h, "user1", "b@d2.example.com")
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("RemoveAddress status = %d; body: %s", w2.Code, w2.Body.String())
	}

	_, ok2, err2 := as.Get("b@d2.example.com")
	if err2 != nil {
		t.Fatalf("Get after remove: %v", err2)
	}
	if ok2 {
		t.Error("address still present in Pebble after removal (resolver would still accept it)")
	}
}
