package handlers

import (
	"net/http"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// An admin-added extra address has Kind=alias but no /system/alias/ key.
// Deleting it through the alias form must not remove it.
func TestDeleteAlias_ExtraAddressIsNotDeletable(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	applier := &directApplierTags{ps: ps}
	insertTestUserWithProfile(t, ps, mgr, applier, "userU", "u@hosted.example")

	if w := postAddAddress(t, h, "userU", "x@hosted.example"); w.Code != http.StatusSeeOther {
		t.Fatalf("AddAddress = %d: %s", w.Code, w.Body.String())
	}
	if _, err := ps.Get([]byte("/system/alias/x@hosted.example")); err == nil {
		t.Fatal("precondition: extra address must not have an alias key")
	}

	w := deleteAliasRequest(t, h, "x@hosted.example", "admin")
	if w.Code != http.StatusNotFound {
		t.Fatalf("DeleteAlias = %d, want 404: %s", w.Code, w.Body.String())
	}
	rec, ok, err := as.Get("x@hosted.example")
	if err != nil || !ok || rec.Kind != "alias" || rec.UserId != "userU" {
		t.Fatalf("record = %+v ok=%v err=%v, want it unchanged", rec, ok, err)
	}
	resolver := store.NewRecipientResolver(as, ps, mgr.System)
	if got, err := resolver.ResolveRecipient("x@hosted.example"); err != nil || got != "userU" {
		t.Errorf("ResolveRecipient = %q, %v, want userU", got, err)
	}
}

func TestDeleteAlias_NilStoreFailsClosed(t *testing.T) {
	h, _, _ := newAdminDeleteTestHandler(t)
	h.store = nil
	if w := deleteAliasRequest(t, h, "a@b.example", "admin"); w.Code != http.StatusInternalServerError {
		t.Fatalf("DeleteAlias = %d, want 500", w.Code)
	}
}
