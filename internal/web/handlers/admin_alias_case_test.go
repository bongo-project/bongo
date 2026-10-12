package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
	"github.com/go-chi/chi/v5"
)

// aliasCaseFixture creates a target account and returns a resolver reading the
// same Pebble/SQLite pair the handler writes to, so these tests exercise the
// real write-then-resolve path rather than asserting on key names.
func aliasCaseFixture(t *testing.T) (*AdminHandler, *store.RecipientResolver, string) {
	t.Helper()
	h, applier, md := newAdminDeleteTestHandler(t)
	targetID, err := auth.CreateUser(applier, md, "admin@target.example", "Admin", "pass12345", true)
	if err != nil {
		t.Fatalf("create target user: %v", err)
	}
	return h, store.NewRecipientResolver(nil, h.store, md.System), targetID
}

// TestCreateAliasNormalisesCase is the regression test for the operator report:
// an alias entered with capitals was written to a key the resolver never asks
// for, because RecipientResolver lower-cases before every lookup. Mail to it
// was then relayed off-server rather than delivered, since an authenticated
// submission session treats an unresolvable recipient as external.
func TestCreateAliasNormalisesCase(t *testing.T) {
	h, resolver, targetID := aliasCaseFixture(t)

	// Control: an all-lower-case alias must resolve, or the test proves nothing.
	if w := createAliasRequest(t, h, "lower@alias.example", "admin@target.example", targetID); w.Code >= 400 {
		t.Fatalf("CreateAlias(lower) = %d: %s", w.Code, w.Body.String())
	}
	if got, err := resolver.ResolveRecipient("lower@alias.example"); err != nil || got != targetID {
		t.Fatalf("fixture invalid: lower-case alias must resolve, got %q err %v", got, err)
	}

	if w := createAliasRequest(t, h, "  Admin@Alias.Example  ", "admin@target.example", targetID); w.Code >= 400 {
		t.Fatalf("CreateAlias(mixed) = %d: %s", w.Code, w.Body.String())
	}

	// Resolvable however it is later presented -- the resolver lower-cases, so
	// both spellings must reach the same user.
	for _, probe := range []string{"admin@alias.example", "Admin@Alias.Example"} {
		got, err := resolver.ResolveRecipient(probe)
		if err != nil || got != targetID {
			t.Errorf("ResolveRecipient(%q) = %q, err %v; want %q", probe, got, err, targetID)
		}
	}
}

// TestCreateAliasNormalisesTarget: auth.CreateUser stores usernames lower-case,
// and resolver tier 2a matches users.username exactly, so a target typed with
// capitals must be normalised on write. The resolver here has no address index,
// so PopulateAddressIndex cannot rescue the alias; only tier 2a can resolve it.
func TestCreateAliasNormalisesTarget(t *testing.T) {
	h, resolver, targetID := aliasCaseFixture(t)

	// Control: a lower-case target resolves, or the test proves nothing.
	if w := createAliasRequest(t, h, "control@alias.example", "admin@target.example", targetID); w.Code >= 400 {
		t.Fatalf("CreateAlias(control) = %d: %s", w.Code, w.Body.String())
	}
	if got, err := resolver.ResolveRecipient("control@alias.example"); err != nil || got != targetID {
		t.Fatalf("fixture invalid: lower-case target must resolve, got %q err %v", got, err)
	}

	if w := createAliasRequest(t, h, "sales@alias.example", "  Admin@Target.EXAMPLE ", targetID); w.Code >= 400 {
		t.Fatalf("CreateAlias(mixed target) = %d: %s", w.Code, w.Body.String())
	}

	got, err := resolver.ResolveRecipient("sales@alias.example")
	if err != nil || got != targetID {
		t.Errorf("ResolveRecipient = %q, err %v; want %q", got, err, targetID)
	}

	v, err := h.store.Get([]byte("/system/alias/sales@alias.example"))
	if err != nil || string(v) != "admin@target.example" {
		t.Errorf("stored target = %q, err %v; want normalised admin@target.example", v, err)
	}

	var rowTarget string
	if err := h.metadata.System.DB().QueryRow(
		"SELECT target_user_id FROM aliases WHERE address = ?", "sales@alias.example").Scan(&rowTarget); err != nil || rowTarget != targetID {
		t.Errorf("sqlite alias row target = %q, err %v; want %q", rowTarget, err, targetID)
	}
}

// TestDeleteAliasRemovesMixedCaseKey covers the recovery path for aliases
// created BEFORE normalisation existed. The admin list renders the stored key
// verbatim, so the delete request arrives with capitals intact; deleting only
// the normalised form would leave exactly those broken aliases undeletable.
func TestDeleteAliasRemovesMixedCaseKey(t *testing.T) {
	h, _, targetID := aliasCaseFixture(t)

	// Write a legacy mixed-case key directly, as the old CreateAlias would have.
	const stored = "Legacy@Alias.Example"
	key := []byte(fmt.Sprintf("/system/alias/%s", stored))
	if err := h.applier.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: key, Value: []byte("admin@target.example"),
	}, 5*time.Second); err != nil {
		t.Fatalf("seed legacy alias: %v", err)
	}
	if v, err := h.store.Get(key); err != nil || len(v) == 0 {
		t.Fatalf("fixture invalid: legacy alias not seeded (err %v)", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/admin/aliases/"+stored+"/delete", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("address", stored)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = middleware.WithUserContext(ctx, targetID, "admin@target.example")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	h.DeleteAlias(w, r)
	if w.Code >= 400 {
		t.Fatalf("DeleteAlias returned %d: %s", w.Code, w.Body.String())
	}

	if v, err := h.store.Get(key); err == nil && len(v) > 0 {
		t.Error("BUG: mixed-case alias key survived deletion, leaving it unfixable from the UI")
	}
}
