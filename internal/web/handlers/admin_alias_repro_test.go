package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func userListed(h *AdminHandler, email string) bool {
	for _, u := range h.loadUsers("") {
		if u.Email == email {
			return true
		}
	}
	return false
}

func createAliasRequest(t *testing.T, h *AdminHandler, address, target, actingUserID string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"address": {address}, "target_user": {target}}
	r := httptest.NewRequest(http.MethodPost, "/admin/aliases", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(context.Background(), actingUserID, "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	h.CreateAlias(w, r)
	return w
}

// Reproduces the user's report: create a "postmaster" account, delete it, then
// create a "postmaster" alias — does the alias resurrect it as a user?
func TestAliasAfterUserDeleteDoesNotResurrectUser(t *testing.T) {
	h, applier, _ := newAdminDeleteTestHandler(t)

	bossID, err := auth.CreateUser(applier, h.metadata, "boss@example.com", "Boss", "adminpass123", true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	pmID, err := auth.CreateUser(applier, h.metadata, "postmaster@example.com", "PM", "pmpass123", false)
	if err != nil {
		t.Fatalf("create postmaster: %v", err)
	}

	// Sanity: postmaster lists and its profile loads.
	if !userListed(h, "postmaster@example.com") {
		t.Fatal("postmaster should be listed right after creation")
	}
	if _, err := h.loadProfile(pmID); err != nil {
		t.Fatalf("postmaster profile should load after creation: %v", err)
	}

	// Delete the postmaster user.
	w := deleteUserRequest(t, h, pmID, bossID)
	if w.Code >= 400 {
		t.Fatalf("DeleteUser returned %d", w.Code)
	}

	// After delete: gone from the list AND the profile no longer loads.
	if userListed(h, "postmaster@example.com") {
		t.Error("BUG: postmaster still in user list after delete")
	}
	if _, err := h.loadProfile(pmID); err == nil {
		t.Error("BUG: postmaster profile still loads after delete")
	}

	// Now create a postmaster alias pointing at boss.
	aw := createAliasRequest(t, h, "postmaster@example.com", "boss@example.com", bossID)
	if aw.Code >= 400 {
		t.Fatalf("CreateAlias returned %d: %s", aw.Code, aw.Body.String())
	}

	// THE CHECK: the alias must not make postmaster reappear as a user.
	if userListed(h, "postmaster@example.com") {
		t.Error("BUG REPRODUCED: creating an alias resurrected the deleted user in the admin user list")
	}
}

// Whatever the trigger, a users row whose Pebble profile is missing must not
// appear in the admin list (it would 404 on click). This asserts the defensive
// filter in loadUsers.
func TestLoadUsersSkipsRowsWithoutProfile(t *testing.T) {
	h, applier, _ := newAdminDeleteTestHandler(t)

	uid, err := auth.CreateUser(applier, h.metadata, "ghost@example.com", "Ghost", "pass1234", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if !userListed(h, "ghost@example.com") {
		t.Fatal("user should list while its profile exists")
	}

	// Simulate an orphaned row: remove only the Pebble profile, leaving the
	// SQLite users row behind.
	profileKey := []byte(fmt.Sprintf("/user/%s/profile", uid))
	if err := applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: profileKey}, time.Second); err != nil {
		t.Fatalf("delete profile: %v", err)
	}

	if userListed(h, "ghost@example.com") {
		t.Error("orphaned row (no profile) should be hidden from the admin user list")
	}
}
