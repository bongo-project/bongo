package security

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// SessionEpoch derives a short, stable fingerprint of a user's credential state
// from their password hash and TOTP configuration. It is embedded in the
// session cookie at login and re-checked on every request: because the value
// changes whenever the password hash, TOTP secret, or TOTP-enabled flag
// changes, any existing session issued before a password change or a 2FA
// enable/disable is invalidated automatically — without needing an explicit
// "logged-out session" list. It carries no secret (it is a hash) but is
// non-reversible, so it is safe to place in the cookie payload.
//
// generation is the profile's web-session generation, bumped on logout so the
// server can revoke a session cookie it can no longer recall. It is mixed in
// only when non-zero, so profiles that have never logged out keep exactly the
// epoch their existing cookies carry and an upgrade does not sign anyone out.
// Callers that must not be affected by web logouts (protocol sessions) pass 0.
func SessionEpoch(passwordHash, totpSecret []byte, totpEnabled bool, generation uint64) string {
	h := sha256.New()
	h.Write(passwordHash)
	h.Write(totpSecret)
	if totpEnabled {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	if generation != 0 {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], generation)
		h.Write([]byte("gen"))
		h.Write(buf[:])
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}
