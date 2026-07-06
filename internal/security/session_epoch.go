package security

import (
	"crypto/sha256"
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
func SessionEpoch(passwordHash, totpSecret []byte, totpEnabled bool) string {
	h := sha256.New()
	h.Write(passwordHash)
	h.Write(totpSecret)
	if totpEnabled {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}
