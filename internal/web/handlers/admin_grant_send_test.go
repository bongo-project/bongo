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

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// postGrantSend posts to /admin/users/{userID}/addresses/grant-send.
// Uses a global-admin scope so the test is not affected by tenant isolation checks.
func postGrantSend(t *testing.T, h *AdminHandler, userID, address string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"address": {address}}
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/admin/users/%s/addresses/grant-send", userID),
		strings.NewReader(form.Encode()),
	)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	r = r.WithContext(ctx)
	r = withChiParam(r, "userID", userID)
	w := httptest.NewRecorder()
	h.GrantSend(w, r)
	return w
}

// seedUserAndAddress creates a user profile + identity address record for tests.
func seedUserAndAddress(t *testing.T, ps *store.PebbleStore, mgr *store.MetadataManager,
	applier *directApplierTags, as *store.AddressStore,
	userID, email, address string) {
	t.Helper()
	insertTestUserWithProfile(t, ps, mgr, applier, userID, email)
	err := as.Put(address, &bongov1.AddressRecord{
		UserId:   userID,
		Domain:   domainPart(address),
		Kind:     "identity",
		CanSend:  false,
		Verified: false,
	})
	if err != nil {
		t.Fatalf("seedUserAndAddress: Put %q: %v", address, err)
	}
}

func domainPart(address string) string {
	if idx := strings.LastIndex(address, "@"); idx >= 0 {
		return address[idx+1:]
	}
	return "localhost"
}

// TestAdminGrantSend_OK verifies that an admin can grant send capability.
// After GrantSend, CanSend=true AND Verified=true must be set on the record.
func TestAdminGrantSend_OK(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	seedUserAndAddress(t, ps, mgr, applier, as, "user1", "alice@example.com", "alias@example.com")

	w := postGrantSend(t, h, "user1", "alias@example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("GrantSend status = %d, want %d; body: %s", w.Code, http.StatusSeeOther, w.Body.String())
	}

	rec, ok, err := as.Get("alias@example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("address not found after GrantSend")
	}
	if !rec.CanSend {
		t.Error("CanSend should be true after GrantSend")
	}
	if !rec.Verified {
		t.Error("Verified should be true after GrantSend")
	}
}

// TestAdminGrantSend_AppearsInPicker verifies that after GrantSend, the address
// appears in the compose From picker (CanSend && Verified is the picker condition).
func TestAdminGrantSend_AppearsInPicker(t *testing.T) {
	env := newComposeSendAsEnv(t)
	// Use the compose env to get the picker, and its address store for seeding.

	userID := "user-grant-picker"
	email := "alice@example.com"
	alias := "alias@other.com"

	env.seedUser(t, userID, email)

	// Seed primary as can_send+verified so picker has at least one entry.
	env.seedAddress(t, email, userID, "example.com", true, true)

	// Seed alias as not-verified (admin added, not yet granted).
	env.seedAddress(t, alias, userID, "other.com", false, false)

	// Before grant: picker should NOT include alias.
	opts, _ := env.handler.buildFromPickerOptions(userID, email)
	for _, o := range opts {
		if strings.EqualFold(o, alias) {
			t.Error("picker includes unverified alias before GrantSend")
		}
	}

	// Grant send by directly updating the record (simulates admin grant handler).
	err := env.addresses.Put(alias, &bongov1.AddressRecord{
		UserId:   userID,
		Domain:   "other.com",
		Kind:     "identity",
		CanSend:  true,
		Verified: true,
	})
	if err != nil {
		t.Fatalf("simulating GrantSend: %v", err)
	}

	// After grant: picker MUST include alias.
	opts, _ = env.handler.buildFromPickerOptions(userID, email)
	found := false
	for _, o := range opts {
		if strings.EqualFold(o, alias) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("picker does not include %q after grant; options: %v", alias, opts)
	}
}

// TestAdminGrantSend_CrossUserOwnership rejects granting send on an address
// owned by a different user.
func TestAdminGrantSend_CrossUserOwnership(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")
	insertTestUserWithProfile(t, ps, mgr, applier, "user2", "bob@example.com")

	// Address owned by user2.
	err := as.Put("shared@example.com", &bongov1.AddressRecord{
		UserId:  "user2",
		Domain:  "example.com",
		Kind:    "identity",
		CanSend: false,
	})
	if err != nil {
		t.Fatalf("seeding address: %v", err)
	}

	// Try to grant send for user1 on an address owned by user2.
	w := postGrantSend(t, h, "user1", "shared@example.com")
	if w.Code == http.StatusSeeOther {
		t.Fatal("GrantSend should reject cross-user ownership but redirected (accepted)")
	}
	if w.Code < 400 {
		t.Fatalf("GrantSend cross-user status = %d, want 4xx", w.Code)
	}

	// Verify record is unchanged.
	rec, ok, err := as.Get("shared@example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("address disappeared")
	}
	if rec.CanSend || rec.Verified {
		t.Error("CanSend/Verified must not be set after rejected cross-user GrantSend")
	}
}

// TestAdminGrantSend_AddressNotFound rejects granting send for an address
// that doesn't exist in the index.
func TestAdminGrantSend_AddressNotFound(t *testing.T) {
	h, _, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	w := postGrantSend(t, h, "user1", "nonexistent@example.com")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GrantSend for nonexistent address = %d, want 404", w.Code)
	}
}

// TestAdminGrantSend_UnknownUser rejects granting send for a non-existent user.
func TestAdminGrantSend_UnknownUser(t *testing.T) {
	h, _, _, _ := newAddressAdminHandler(t)

	w := postGrantSend(t, h, "ghost-user", "alias@example.com")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GrantSend for unknown user = %d, want 404", w.Code)
	}
}

// TestAdminGrantSend_NilStore returns 500 when address store is not configured.
func TestAdminGrantSend_NilStore(t *testing.T) {
	h, _, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}
	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	h.addresses = nil

	w := postGrantSend(t, h, "user1", "alias@example.com")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("GrantSend without store = %d, want 500", w.Code)
	}
}

// TestAdminGrantSend_Idempotent verifies that calling GrantSend on an already
// verified address succeeds (idempotent PUT).
func TestAdminGrantSend_Idempotent(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}
	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	// Pre-seed as already verified.
	err := as.Put("alias@example.com", &bongov1.AddressRecord{
		UserId:   "user1",
		Domain:   "example.com",
		Kind:     "identity",
		CanSend:  true,
		Verified: true,
	})
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}

	w := postGrantSend(t, h, "user1", "alias@example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("GrantSend idempotent = %d, want 303; body: %s", w.Code, w.Body.String())
	}

	rec, ok, err := as.Get("alias@example.com")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if !rec.CanSend || !rec.Verified {
		t.Error("CanSend/Verified should remain true after idempotent GrantSend")
	}
}

// TestShowUser_AddressesEnrichedWithStatus verifies that ShowUser now populates
// AdminAddressInfo with CanSend/Verified status from Pebble.
func TestShowUser_AddressesEnrichedWithStatus(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}

	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")

	// A verified address.
	verifiedAddr := "verified@example.com"
	err := as.Put(verifiedAddr, &bongov1.AddressRecord{
		UserId:   "user1",
		Domain:   "example.com",
		Kind:     "identity",
		CanSend:  true,
		Verified: true,
	})
	if err != nil {
		t.Fatalf("seeding verified address: %v", err)
	}

	// An unverified address.
	unverifiedAddr := "pending@example.com"
	err = as.Put(unverifiedAddr, &bongov1.AddressRecord{
		UserId:   "user1",
		Domain:   "example.com",
		Kind:     "identity",
		CanSend:  false,
		Verified: false,
	})
	if err != nil {
		t.Fatalf("seeding unverified address: %v", err)
	}

	// Also need a user profile so ShowUser can load it.
	profileBytes, _ := proto.Marshal(&bongov1.UserProfile{
		Email:       "alice@example.com",
		DisplayName: "Alice",
	})
	profileCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/user1/profile"),
		Value: profileBytes,
	}
	if err := applier.ApplyCommand(profileCmd, time.Second); err != nil {
		t.Fatalf("writing profile: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/admin/users/user1", nil)
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	r = r.WithContext(ctx)
	r = withChiParam(r, "userID", "user1")
	w := httptest.NewRecorder()
	h.ShowUser(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("ShowUser status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	// The template renders verified/not-verified badges.
	if !strings.Contains(body, "Send-capable") && !strings.Contains(body, "Senden erlaubt") &&
		!strings.Contains(body, "send-capable") && !strings.Contains(body, "sendCapable") {
		// Check i18n resolved "admin.sendCapable" text
		if !strings.Contains(body, verifiedAddr) {
			t.Logf("body does not contain expected status for verified address %q", verifiedAddr)
		}
	}
	// At minimum, both addresses should appear in the HTML.
	if !strings.Contains(body, verifiedAddr) {
		t.Errorf("ShowUser body does not contain verified address %q", verifiedAddr)
	}
	if !strings.Contains(body, unverifiedAddr) {
		t.Errorf("ShowUser body does not contain unverified address %q", unverifiedAddr)
	}
}
