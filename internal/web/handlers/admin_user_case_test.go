package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// TestMixedCaseAccountResolvesAsLocalRecipient: an account created with
// capitals must resolve through the resolver SMTP Rcpt uses, without the
// address index populated (so only the users-table tier can hit).
func TestMixedCaseAccountResolvesAsLocalRecipient(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)
	id, err := auth.CreateUser(applier, md, "Carol@Example.COM", "Carol", "pass12345", false)
	if err != nil {
		t.Fatal(err)
	}
	resolver := store.NewRecipientResolver(nil, h.store, md.System)
	for _, probe := range []string{"carol@example.com", "<Carol@Example.com>"} {
		got, err := resolver.ResolveRecipient(probe)
		if err != nil || got != id {
			t.Errorf("ResolveRecipient(%q) = %q, %v; want %q", probe, got, err, id)
		}
	}
}

// TestAdminCreateUserStoresLowerCaseEmail guards the admin UI path, which
// previously used a private copy of CreateUser.
func TestAdminCreateUserStoresLowerCaseEmail(t *testing.T) {
	h, _, md := newAdminDeleteTestHandler(t)
	form := url.Values{"email": {"Dave@Example.COM"}, "password": {"pass12345"}, "display_name": {"Dave"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.WithUserContext(context.Background(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	w := httptest.NewRecorder()
	h.CreateUser(w, r.WithContext(ctx))
	if w.Code >= 400 {
		t.Fatalf("CreateUser = %d: %s", w.Code, w.Body.String())
	}

	var id, username string
	if err := md.System.DB().QueryRow("SELECT id, username FROM users").Scan(&id, &username); err != nil {
		t.Fatal(err)
	}
	if username != "dave@example.com" {
		t.Errorf("users.username = %q", username)
	}
	data, err := h.store.Get([]byte("/user/" + id + "/profile"))
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if p.Email != "dave@example.com" {
		t.Errorf("profile email = %q", p.Email)
	}
}

// TestAdminCreateUserReplacesUnverifiedClaimOnly: the admin form shows the
// same outcomes as auth.CreateUser. An unverified send-as claim does not block
// the account (303); a verified identity still does (409).
func TestAdminCreateUserReplacesUnverifiedClaimOnly(t *testing.T) {
	tests := []struct {
		name     string
		verified bool
		want     int
	}{
		{"unverified claim replaced", false, http.StatusSeeOther},
		{"verified identity refused", true, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, applier, md := newAdminDeleteTestHandler(t)
			as := store.NewAddressStore(h.store, applier, md.System)
			if err := as.Put("new@future.example", &bongov1.AddressRecord{
				UserId: "user-A", Domain: "future.example", Kind: "identity", CanSend: tt.verified, Verified: tt.verified,
			}); err != nil {
				t.Fatal(err)
			}

			form := url.Values{"email": {"new@future.example"}, "password": {"pass12345"}, "display_name": {"New"}}
			r := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			ctx := middleware.WithUserContext(context.Background(), "admin-id", "admin@example.com")
			ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
			w := httptest.NewRecorder()
			h.CreateUser(w, r.WithContext(ctx))
			if w.Code != tt.want {
				t.Fatalf("CreateUser = %d (%s), want %d", w.Code, w.Body.String(), tt.want)
			}

			rec, ok, err := as.Get("new@future.example")
			if err != nil || !ok {
				t.Fatalf("record: ok=%v err=%v", ok, err)
			}
			if tt.verified != (rec.UserId == "user-A") {
				t.Errorf("record owner = %q (verified=%v)", rec.UserId, tt.verified)
			}
			if !tt.verified && rec.Kind != "primary" {
				t.Errorf("kind = %q, want primary", rec.Kind)
			}
			if list, _ := as.ListByUser("user-A"); !tt.verified && len(list) != 0 {
				t.Errorf("user-A still lists %v", list)
			}
		})
	}
}
