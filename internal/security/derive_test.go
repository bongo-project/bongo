package security

import (
	"bytes"
	"testing"
)

func TestDeriveKey(t *testing.T) {
	secret := []byte("super-secret-master-key")

	tests := []struct {
		name    string
		secret  []byte
		purpose string
	}{
		{name: "internal auth", secret: secret, purpose: "bongo/internal-auth/v1"},
		{name: "session cookie", secret: secret, purpose: "bongo/session-cookie/v1"},
		{name: "empty secret", secret: nil, purpose: "bongo/internal-auth/v1"},
		{name: "empty purpose", secret: secret, purpose: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveKey(tt.secret, tt.purpose)

			// Fixed 32-byte (SHA-256) output.
			if len(got) != 32 {
				t.Fatalf("DeriveKey length = %d, want 32", len(got))
			}

			// Deterministic: same inputs yield the same key.
			again := DeriveKey(tt.secret, tt.purpose)
			if !bytes.Equal(got, again) {
				t.Errorf("DeriveKey not deterministic for %q", tt.purpose)
			}
		})
	}
}

func TestDeriveKey_DistinctPurposes(t *testing.T) {
	secret := []byte("super-secret-master-key")

	internal := DeriveKey(secret, "bongo/internal-auth/v1")
	session := DeriveKey(secret, "bongo/session-cookie/v1")
	blob := DeriveKey(secret, "bongo/blob-sync/v1")

	if bytes.Equal(internal, session) {
		t.Error("internal-auth and session-cookie keys must differ")
	}
	if bytes.Equal(internal, blob) {
		t.Error("internal-auth and blob-sync keys must differ")
	}
	if bytes.Equal(session, blob) {
		t.Error("session-cookie and blob-sync keys must differ")
	}
}

func TestDeriveKey_DistinctSecrets(t *testing.T) {
	a := DeriveKey([]byte("secret-a"), "bongo/session-cookie/v1")
	b := DeriveKey([]byte("secret-b"), "bongo/session-cookie/v1")
	if bytes.Equal(a, b) {
		t.Error("different secrets must yield different keys")
	}
}
