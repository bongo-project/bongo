package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// settingsList renders the self-serve address list for userID, built over the
// same stores the admin handler writes to.
func settingsList(t *testing.T, ps *store.PebbleStore, ap store.RaftApplier, as *store.AddressStore, userID string) string {
	t.Helper()
	vh := NewAddressVerifyHandler(discardLogger())
	vh.SetAddressStore(as)
	vh.SetVerifyStore(store.NewAddressVerifyStore(ps, ap))
	r := httptest.NewRequest(http.MethodGet, "/settings/addresses", nil)
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))
	w := httptest.NewRecorder()
	vh.ShowAddresses(w, r)
	return w.Body.String()
}

func putClaim(t *testing.T, avs *store.AddressVerifyStore, userID, address string) {
	t.Helper()
	if err := avs.Put(userID, address, &bongov1.AddressVerification{}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveAddress_DeletesUsersClaim(t *testing.T) {
	const addr = "Ext@Example.net"
	h, as, mgr, ps := newAddressAdminHandler(t)
	ap := &directApplierTags{ps: ps}
	insertTestUserWithProfile(t, ps, mgr, ap, "user1", "alice@example.com")
	avs := store.NewAddressVerifyStore(ps, ap)

	if err := as.Put("ext@example.net", &bongov1.AddressRecord{UserId: "user1", Domain: "example.net", Kind: "identity"}); err != nil {
		t.Fatal(err)
	}
	putClaim(t, avs, "user1", addr)
	putClaim(t, avs, "user2", addr)

	if !strings.Contains(settingsList(t, ps, ap, as, "user1"), "ext@example.net") {
		t.Fatal("precondition: address not listed")
	}
	if w := postRemoveAddress(t, h, "user1", addr); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if body := settingsList(t, ps, ap, as, "user1"); strings.Contains(body, "ext@example.net") {
		t.Error("removed address still listed")
	}
	if _, ok, _ := avs.Get("user1", "ext@example.net"); ok {
		t.Error("user1 claim survived removal")
	}
	// Another user's own unverified statement is not the admin's to remove.
	if _, ok, _ := avs.Get("user2", "ext@example.net"); !ok {
		t.Error("user2 pending claim was deleted")
	}
}

func TestRemoveAddress_DeletesRecordOwnersClaim(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	ap := &directApplierTags{ps: ps}
	insertTestUserWithProfile(t, ps, mgr, ap, "user1", "alice@example.com")
	avs := store.NewAddressVerifyStore(ps, ap)
	if err := as.Put("ext@example.net", &bongov1.AddressRecord{UserId: "user2", Domain: "example.net", Kind: "identity"}); err != nil {
		t.Fatal(err)
	}
	putClaim(t, avs, "user2", "ext@example.net")

	if w := postRemoveAddress(t, h, "user1", "ext@example.net"); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if _, ok, _ := avs.Get("user2", "ext@example.net"); ok {
		t.Error("record owner's claim survived removal")
	}
}

func TestRemoveAddress_ClaimDeleteFailureFailsRequest(t *testing.T) {
	h, as, mgr, ps := newAddressAdminHandler(t)
	ap := &directApplierTags{ps: ps}
	insertTestUserWithProfile(t, ps, mgr, ap, "user1", "alice@example.com")
	avs := store.NewAddressVerifyStore(ps, ap)
	if err := as.Put("ext@example.net", &bongov1.AddressRecord{UserId: "user1", Domain: "example.net", Kind: "identity"}); err != nil {
		t.Fatal(err)
	}
	putClaim(t, avs, "user1", "ext@example.net")
	h.applier = &failDeleteApplier{directApplierTags: ap, failPrefix: "/user/user1/addrverify/"}

	if w := postRemoveAddress(t, h, "user1", "ext@example.net"); w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if _, ok, _ := as.Get("ext@example.net"); !ok {
		t.Error("record deleted although the claim delete failed")
	}
}

func TestDeleteDomain_DeletesClaims(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	d := cascadeDomain
	cascadeCreateDomain(t, env, d)
	avs := store.NewAddressVerifyStore(env.ps, env.applier)
	putClaim(t, avs, "userA", "X@"+d) // mixed case at claim time
	putClaim(t, avs, "userB", "x@"+d)
	putClaim(t, avs, "userA", "y@other.test")
	putClaim(t, avs, "userA", "z@sub."+d)

	if !strings.Contains(settingsList(t, env.ps, env.applier, env.as, "userA"), "x@"+d) {
		t.Fatal("precondition: claim not listed")
	}
	if w := cascadeDeleteDomain(t, env, d); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := settingsList(t, env.ps, env.applier, env.as, "userA")
	if strings.Contains(body, "x@"+d) {
		t.Error("claim on deleted domain still listed")
	}
	for _, a := range []string{"y@other.test", "z@sub." + d} {
		if !strings.Contains(body, a) {
			t.Errorf("claim %s was deleted", a)
		}
	}
	if _, ok, _ := avs.Get("userB", "x@"+d); ok {
		t.Error("userB claim survived domain deletion")
	}
}

func TestDeleteDomain_ClaimDeleteFailureKeepsDomain(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	d := cascadeDomain
	cascadeCreateDomain(t, env, d)
	putClaim(t, store.NewAddressVerifyStore(env.ps, env.applier), "userA", "x@"+d)
	env.h.applier = &failDeleteApplier{directApplierTags: env.applier, failPrefix: "/user/userA/addrverify/"}

	if w := cascadeDeleteDomain(t, env, d); w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !cascadeHas(env, "/system/domain/"+d) {
		t.Error("domain key deleted despite claim purge failure")
	}
}
