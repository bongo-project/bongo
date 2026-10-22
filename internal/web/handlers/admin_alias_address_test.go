package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func primaryRecord(userID string) *bongov1.AddressRecord {
	return &bongov1.AddressRecord{UserId: userID, Domain: "example.com", Kind: "primary", CanSend: true, Verified: true}
}

func deleteAliasRequest(t *testing.T, h *AdminHandler, address, actingUserID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/admin/aliases/"+address+"/delete", nil)
	ctx := middleware.WithUserContext(r.Context(), actingUserID, "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	r = withChiParam(r.WithContext(ctx), "address", address)
	w := httptest.NewRecorder()
	h.DeleteAlias(w, r)
	return w
}

func TestCreateAlias_WritesAddressRecordAndDeleteRemovesIt(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)
	targetID, err := auth.CreateUser(applier, md, "boss@target.example", "Boss", "pass12345", true)
	if err != nil {
		t.Fatal(err)
	}
	resolver := store.NewRecipientResolver(h.addresses, h.store, md.System)

	if w := createAliasRequest(t, h, "Sales@Alias.Example", "boss@target.example", targetID); w.Code >= 400 {
		t.Fatalf("CreateAlias = %d: %s", w.Code, w.Body.String())
	}
	rec, ok, err := h.addresses.Get("sales@alias.example")
	if err != nil || !ok || rec.Kind != "alias" || rec.UserId != targetID || !rec.Verified || rec.CanSend {
		t.Fatalf("alias record = %+v ok=%v err=%v", rec, ok, err)
	}

	// Tier 1 alone (no legacy tiers) resolves it.
	tier1 := store.NewRecipientResolver(h.addresses, nil, nil)
	if got, err := tier1.ResolveRecipient("sales@alias.example"); err != nil || got != targetID {
		t.Errorf("tier1 resolve = %q err %v", got, err)
	}

	if w := deleteAliasRequest(t, h, "sales@alias.example", targetID); w.Code >= 400 {
		t.Fatalf("DeleteAlias = %d: %s", w.Code, w.Body.String())
	}
	if _, ok, _ := h.addresses.Get("sales@alias.example"); ok {
		t.Error("alias address record survived DeleteAlias")
	}
	if got, err := resolver.ResolveRecipient("sales@alias.example"); err == nil {
		t.Errorf("deleted alias still resolves to %q", got)
	}
}

func TestCreateAlias_Refusals(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)
	targetID, err := auth.CreateUser(applier, md, "boss@target.example", "Boss", "pass12345", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateUser(applier, md, "taken@target.example", "T", "pass12345", false); err != nil {
		t.Fatal(err)
	}

	if w := createAliasRequest(t, h, "x@alias.example", "nobody@target.example", targetID); w.Code != http.StatusBadRequest {
		t.Errorf("unknown target: status = %d, want 400", w.Code)
	}
	if _, ok, _ := h.addresses.Get("x@alias.example"); ok {
		t.Error("record written for unknown target")
	}
	if w := createAliasRequest(t, h, "taken@target.example", "boss@target.example", targetID); w.Code != http.StatusConflict {
		t.Errorf("alias over account: status = %d, want 409", w.Code)
	}
}

func TestAdminAddAddress_Refusals(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}
	insertTestUserWithProfile(t, ps, mgr, applier, "user1", "alice@example.com")
	insertTestUserWithProfile(t, ps, mgr, applier, "user2", "bob@example.com")

	// Another account's address with no address record (post-boot account).
	if w := postAddAddress(t, h, "user1", "bob@example.com"); w.Code != http.StatusConflict {
		t.Errorf("add other account's address: status = %d, want 409", w.Code)
	}
	if _, ok, _ := as.Get("bob@example.com"); ok {
		t.Error("record written over another account")
	}
	// Re-adding the user's own primary never downgrades it.
	if err := as.Put("alice@example.com", primaryRecord("user1")); err != nil {
		t.Fatal(err)
	}
	if w := postAddAddress(t, h, "user1", "alice@example.com"); w.Code != http.StatusSeeOther {
		t.Fatalf("re-add own primary: status = %d", w.Code)
	}
	if rec, _, _ := as.Get("alice@example.com"); rec.Kind != "primary" || !rec.CanSend {
		t.Errorf("primary downgraded: %+v", rec)
	}
}
