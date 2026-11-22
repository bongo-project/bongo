package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Without an address store member resolution cannot work: that is a 503 with a
// translated message, not the 422 refusal of a bad member.
func TestSharedMemberWithoutAddressStoreIs503(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	env.h.addresses = nil
	_, err := env.h.resolveSharedMember("support@"+testDomain1, "user1@"+testDomain1)
	if !errors.Is(err, errSharedUnavailable) {
		t.Fatalf("err = %v, want errSharedUnavailable", err)
	}
	w := httptest.NewRecorder()
	env.h.memberError(w, httptest.NewRequest(http.MethodPost, "/", nil), err)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "admin.sharedErr") {
		t.Fatalf("status %d body %q, want 503 and a translated message", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	env.h.memberError(w, httptest.NewRequest(http.MethodPost, "/", nil), errSharedMemberRefused)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("refusal status = %d, want 422", w.Code)
	}
}
