package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func newAdminDeleteTestHandler(t *testing.T) (*AdminHandler, *directApplierTags, *store.MetadataManager) {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	md, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { md.Close() })

	applier := &directApplierTags{ps: ps}
	h := NewAdminHandler(ps, nil, md, applier, discardLogger())
	return h, applier, md
}

// deleteUserRequest performs POST /admin/users/{userID}/delete as actingUserID.
// Uses a global-admin scope so the test is not affected by tenant isolation checks.
func deleteUserRequest(t *testing.T, h *AdminHandler, targetUserID, actingUserID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/users/%s/delete", targetUserID), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", targetUserID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	ctx := middleware.WithUserContext(r.Context(), actingUserID, "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	h.DeleteUser(w, r)
	return w
}

func TestDeleteUserRemovesProfileAndData(t *testing.T) {
	h, applier, _ := newAdminDeleteTestHandler(t)

	// An admin doing the deleting, plus the victim to be removed.
	if _, err := auth.CreateUser(applier, h.metadata, "boss@example.com", "Boss", "adminpass123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	victimID, err := auth.CreateUser(applier, h.metadata, "victim@example.com", "Victim", "victimpass123", false)
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}

	// Seed a mail key under the victim's prefix to confirm prefix deletion.
	mailKey := []byte(fmt.Sprintf("/user/%s/mail/guid-1", victimID))
	if err := applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: mailKey, Value: []byte("x")}, time.Second); err != nil {
		t.Fatalf("seed mail: %v", err)
	}

	rec := deleteUserRequest(t, h, victimID, "some-other-admin-id")

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("HX-Redirect"); loc != "/admin/users?flash=User+deleted" {
		t.Errorf("HX-Redirect = %q, want the user-deleted redirect", loc)
	}

	// Profile removed from Pebble.
	if _, err := h.store.Get([]byte(fmt.Sprintf("/user/%s/profile", victimID))); err == nil {
		t.Error("victim profile still present in Pebble after delete")
	}
	// Mail key removed.
	if _, err := h.store.Get(mailKey); err == nil {
		t.Error("victim mail key still present after delete")
	}
	// Removed from the user list (system.db).
	for _, u := range h.loadUsers("") {
		if u.UserID == victimID {
			t.Error("victim still present in user list after delete")
		}
	}
}

func TestDeleteUserRefusesSelf(t *testing.T) {
	h, applier, _ := newAdminDeleteTestHandler(t)
	adminID, err := auth.CreateUser(applier, h.metadata, "boss@example.com", "Boss", "adminpass123", true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	rec := deleteUserRequest(t, h, adminID, adminID)

	if loc := rec.Header().Get("HX-Redirect"); loc == "/admin/users?flash=User+deleted" {
		t.Fatal("self-delete was allowed")
	}
	if _, err := h.store.Get([]byte(fmt.Sprintf("/user/%s/profile", adminID))); err != nil {
		t.Error("acting admin profile was removed on self-delete attempt")
	}
}

func TestDeleteUserRefusesLastAdmin(t *testing.T) {
	h, applier, _ := newAdminDeleteTestHandler(t)
	adminID, err := auth.CreateUser(applier, h.metadata, "only-admin@example.com", "Only", "adminpass123", true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	rec := deleteUserRequest(t, h, adminID, "another-admin-id")

	if loc := rec.Header().Get("HX-Redirect"); loc == "/admin/users?flash=User+deleted" {
		t.Fatal("deleting the last admin was allowed")
	}
	if _, err := h.store.Get([]byte(fmt.Sprintf("/user/%s/profile", adminID))); err != nil {
		t.Error("last admin profile was removed")
	}
}
