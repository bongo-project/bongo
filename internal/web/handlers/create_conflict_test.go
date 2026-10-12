package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

func seedLegacyUser(t *testing.T, md *store.MetadataManager) {
	t.Helper()
	if _, err := md.System.DB().Exec(`INSERT INTO users (id, username, enabled, created_at) VALUES ('legacy', 'Eve@Example.com', 1, 0)`); err != nil {
		t.Fatal(err)
	}
}

func TestAdminCreateUserDuplicateIsConflict(t *testing.T) {
	h, _, md := newAdminDeleteTestHandler(t)
	seedLegacyUser(t, md)
	form := url.Values{"email": {"eve@example.com"}, "password": {"pass12345"}, "display_name": {"Eve"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(context.Background(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	w := httptest.NewRecorder()
	h.CreateUser(w, r.WithContext(ctx))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
}

func TestUserCreateDuplicateIsConflict(t *testing.T) {
	md, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { md.Close() })
	seedLegacyUser(t, md)

	h := NewUserHandler(nopApplier{}, md)
	body, _ := json.Marshal(map[string]any{"email": "eve@example.com", "password": "secret123"})
	req := httptest.NewRequest(http.MethodPost, "/internal/user/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}
