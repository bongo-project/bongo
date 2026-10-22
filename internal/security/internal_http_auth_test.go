package security

import (
	"bytes"
	"io"
	"net/http"
	"strings"
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

func TestVerifyInternalRequest_RejectsReplay(t *testing.T) {
	secret := []byte("test-internal-secret-32-bytes-xx")
	req, _ := http.NewRequest(http.MethodPost, "http://node/internal/kv/x", strings.NewReader("body"))
	SignInternalRequest(req, secret)

	// First verification of a freshly-signed request succeeds.
	if !VerifyInternalRequest(req, secret) {
		t.Fatal("first verification should succeed")
	}

	// Replaying the exact same signed header (same nonce) must be rejected.
	replay, _ := http.NewRequest(http.MethodPost, "http://node/internal/kv/x", strings.NewReader("body"))
	replay.Header.Set(InternalAuthHeader, req.Header.Get(InternalAuthHeader))
	if VerifyInternalRequest(replay, secret) {
		t.Error("replayed request with a reused nonce should be rejected")
	}
}

func TestBlobSyncAndInternalAreDistinct(t *testing.T) {
	signers := map[string]func(*http.Request, []byte){
		"internal": SignInternalRequest,
		"blob":     SignBlobSyncRequest,
	}
	verifiers := map[string]func(*http.Request, []byte) bool{
		"internal": VerifyInternalRequest,
		"blob":     VerifyBlobSyncRequest,
	}
	for sname, sign := range signers {
		for vname, verify := range verifiers {
			t.Run(sname+" signed, "+vname+" verifier", func(t *testing.T) {
				// Fresh request per case so the nonce cache cannot cause a rejection.
				req, _ := http.NewRequest(http.MethodGet, "http://x/internal/blob/abc", nil)
				sign(req, authSecret)
				if got, want := verify(req, authSecret), sname == vname; got != want {
					t.Errorf("accepted = %v, want %v", got, want)
				}
			})
		}
	}
}
