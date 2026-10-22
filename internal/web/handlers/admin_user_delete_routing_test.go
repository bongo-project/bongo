package handlers

import (
	"errors"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

// Deleting a user must remove the legacy aliases that target the address and
// the catch-alls the user owns. A resolver liveness check alone cannot cover
// the aliases: once the address is re-registered the alias value resolves to
// the new, live account, which would then receive the old alias's mail.
func TestDeleteUserRemovesLegacyAliasesAndCatchAlls(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)

	bossID, err := auth.CreateUser(applier, md, "boss@keep.test", "Boss", "adminpass123", true)
	if err != nil {
		t.Fatal(err)
	}
	aID, err := auth.CreateUser(applier, md, "a@d.test", "A", "passpass123", false)
	if err != nil {
		t.Fatal(err)
	}
	cID, err := auth.CreateUser(applier, md, "c@d.test", "C", "passpass123", false)
	if err != nil {
		t.Fatal(err)
	}

	put := func(key, val string) {
		t.Helper()
		if err := h.store.Set([]byte(key), []byte(val)); err != nil {
			t.Fatal(err)
		}
	}
	put("/system/alias/x@d.test", "a@d.test")
	put("/system/alias/m@d.test", "A@d.test ") // mixed case and padded: pre-normalisation data
	put("/system/alias/y@d.test", "c@d.test")
	put("/system/alias/shared-suffix@d.test", "xa@d.test") // shares a suffix only
	put("/system/catchall/d.test", aID)
	put("/system/catchall/other.test", cID)
	if _, err := md.System.DB().Exec(
		"INSERT INTO aliases (address, target_user_id, enabled, created_at) VALUES ('x@d.test', ?, 1, 0)", aID); err != nil {
		t.Fatal(err)
	}
	if _, err := md.System.DB().Exec(
		"INSERT INTO aliases (address, target_user_id, enabled, created_at) VALUES ('y@d.test', ?, 1, 0)", cID); err != nil {
		t.Fatal(err)
	}

	resolver := store.NewRecipientResolver(h.addresses, h.store, md.System)
	if id, err := resolver.ResolveRecipient("x@d.test"); err != nil || id != aID {
		t.Fatalf("fixture: x@d.test = %q, %v; want %q", id, err, aID)
	}

	if w := deleteUserRequest(t, h, aID, bossID); w.Code >= 400 {
		t.Fatalf("DeleteUser = %d", w.Code)
	}

	for _, k := range []string{"/system/alias/x@d.test", "/system/alias/m@d.test", "/system/catchall/d.test"} {
		if _, err := h.store.Get([]byte(k)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s still present after delete (err %v)", k, err)
		}
	}
	var n int
	if err := md.System.DB().QueryRow("SELECT COUNT(*) FROM aliases WHERE target_user_id = ?", aID).Scan(&n); err != nil || n != 0 {
		t.Errorf("sqlite aliases rows for deleted user = %d (err %v), want 0", n, err)
	}

	// Positive controls: other users' routing survives.
	for _, k := range []string{"/system/alias/y@d.test", "/system/alias/shared-suffix@d.test", "/system/catchall/other.test"} {
		if _, err := h.store.Get([]byte(k)); err != nil {
			t.Errorf("%s was removed but belongs to a live user or merely shares a suffix: %v", k, err)
		}
	}
	if err := md.System.DB().QueryRow("SELECT COUNT(*) FROM aliases WHERE target_user_id = ?", cID).Scan(&n); err != nil || n != 1 {
		t.Errorf("live user's sqlite alias rows = %d (err %v), want 1", n, err)
	}

	// Re-register the address: the old alias must not follow it.
	if _, err := auth.CreateUser(applier, md, "a@d.test", "New A", "passpass123", false); err != nil {
		t.Fatal(err)
	}
	if id, err := resolver.ResolveRecipient("x@d.test"); err == nil {
		t.Errorf("x@d.test resolves to %q after the target was deleted and re-registered", id)
	}
	if id, err := resolver.ResolveRecipient("unmatched@d.test"); err == nil {
		t.Errorf("unmatched@d.test resolves to %q; the deleted owner's catch-all must be gone", id)
	}
	if id, err := resolver.ResolveRecipient("y@d.test"); err != nil || id != cID {
		t.Errorf("y@d.test = %q, %v; want %q", id, err, cID)
	}
	if id, err := resolver.ResolveRecipient("anything@other.test"); err != nil || id != cID {
		t.Errorf("anything@other.test = %q, %v; want %q", id, err, cID)
	}
}

// aliasFailApplier fails deletes of /system/alias keys and passes the rest on.
type aliasFailApplier struct{ *directApplierTags }

func (a aliasFailApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if cmd.Op == bongov1.Op_OP_DELETE && strings.HasPrefix(string(cmd.Key), "/system/alias/") {
		return errors.New("injected alias delete failure")
	}
	return a.directApplierTags.ApplyCommand(cmd, d)
}

func TestDeleteUserRoutingPurgeFailureFlagsPartialDelete(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)
	bossID, err := auth.CreateUser(applier, md, "boss@keep.test", "Boss", "adminpass123", true)
	if err != nil {
		t.Fatal(err)
	}
	aID, err := auth.CreateUser(applier, md, "a@d.test", "A", "passpass123", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.Set([]byte("/system/alias/x@d.test"), []byte("a@d.test")); err != nil {
		t.Fatal(err)
	}
	h.applier = aliasFailApplier{applier}

	w := deleteUserRequest(t, h, aID, bossID)
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "some+errors") {
		t.Fatalf("flash = %q, want a partial-failure flash", loc)
	}
}
