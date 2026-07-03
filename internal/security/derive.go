package security

import (
	"crypto/hmac"
	"crypto/sha256"
)

// DeriveKey derives a distinct 32-byte subkey from a master secret for a given
// purpose using HMAC-SHA256(secret, purpose). Deriving per-purpose subkeys keeps
// unrelated usages (session-cookie HMAC, internal control-plane HMAC, blob-sync)
// cryptographically independent, so a weakness in one usage cannot be leveraged
// against another.
//
// Derivation is deterministic: the same secret and purpose always yield the same
// key. This is a hard requirement because the signer and verifier must derive an
// identical subkey. Purpose labels should be stable, versioned strings such as
// "bongo/internal-auth/v1".
func DeriveKey(secret []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(purpose))
	return mac.Sum(nil)
}
