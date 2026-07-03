package security

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

var authSecret = []byte("test-internal-secret")

func TestInternalAuthRoundTrip(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "http://node/internal/user/create", bytes.NewReader([]byte(`{"email":"a@b"}`)))
	SignInternalRequest(req, authSecret)

	if !VerifyInternalRequest(req, authSecret) {
		t.Fatal("valid signed request should verify")
	}
	// Body must still be readable by the handler after verification.
	body, _ := io.ReadAll(req.Body)
	if string(body) != `{"email":"a@b"}` {
		t.Fatalf("body not preserved after verify: %q", body)
	}
}

func TestInternalAuthRejectsTamperedBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "http://node/internal/user/create", bytes.NewReader([]byte(`{"email":"a@b"}`)))
	SignInternalRequest(req, authSecret)

	// Attacker swaps the body but replays the captured signature.
	req.Body = io.NopCloser(bytes.NewReader([]byte(`{"email":"attacker@evil","admin":true}`)))

	if VerifyInternalRequest(req, authSecret) {
		t.Fatal("request with swapped body must NOT verify")
	}
}

func TestInternalAuthRejectsWrongSecret(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://node/internal/cluster/members", nil)
	SignInternalRequest(req, authSecret)

	if VerifyInternalRequest(req, []byte("other-secret")) {
		t.Fatal("request signed with a different secret must NOT verify")
	}
}
