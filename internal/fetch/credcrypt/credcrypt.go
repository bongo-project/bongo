// Package credcrypt seals the passwords of users' remote POP3 and IMAP
// accounts with AES-256-GCM.
//
// The key is operator-held (fetch.credentials_key) and never written to
// Pebble, a snapshot or the data directory. Each sealed record is bound by its
// associated data to the user, the account guid and the connection identity
// (protocol, security, host, user name) and to the id of the key that sealed
// it. A record therefore cannot be moved to another user or account, and an
// account cannot be pointed at another host or user name without the password
// being entered again: the ciphertext simply stops opening.
//
// Only the server submits ciphertext to Open, and it comes from Pebble where
// Seal wrote it. No route accepts a nonce, ciphertext or key id from a
// browser, and every failure is a fixed error with no detail, so Open is not a
// decryption oracle. The plaintext password lives in a Go string for the
// length of one connection; Go strings cannot be zeroed.
package credcrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/bongo-project/bongo/internal/security"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	// KeyLen is the length of every credentials key.
	KeyLen = 32
	// MaxPasswordLen is the longest password Seal accepts.
	MaxPasswordLen = 1024

	version     = 1
	nonceLen    = 12
	encPurpose  = "bongo/fetch/credential-aes256gcm/v1"
	idPurpose   = "bongo/fetch/key-id/v1"
	loopPurpose = "bongo/fetch/loop/v1"
	aadDomain   = "bongo/fetch-credential/v1\x00"
)

var (
	// ErrKeyUnknown means the record names a key the keyring does not hold (or
	// there is no keyring). The worker maps it to needs_password.
	ErrKeyUnknown = errors.New("credential key unknown")
	// ErrCredentialInvalid means the record did not authenticate: it was
	// altered, moved, or is malformed. The worker maps it to needs_password.
	ErrCredentialInvalid = errors.New("credential invalid")
	// ErrNoKey is returned by Seal on a nil keyring.
	ErrNoKey = errors.New("no credentials key")
)

type key struct {
	id      string
	aead    cipher.AEAD
	loopKey []byte
}

// Keyring holds the current key and the decrypt-only previous keys. A nil
// *Keyring means the feature is off. It is immutable and safe for concurrent
// use.
type Keyring struct {
	current key
	byID    map[string]key
	// order lists the keys current first, then the previous keys in the order
	// given, for LoopTokens.
	order []key
}

// KeyIDHex returns the id of a raw key: the first 16 hex characters of an
// HMAC derived from it. The id is derived, not chosen, so two keys cannot
// share one, and it reveals nothing usable about the key.
func KeyIDHex(raw []byte) string {
	return hex.EncodeToString(security.DeriveKey(raw, idPurpose))[:16]
}

func newKey(raw []byte) (key, error) {
	if len(raw) != KeyLen {
		return key{}, fmt.Errorf("credentials key must be %d bytes", KeyLen)
	}
	block, err := aes.NewCipher(security.DeriveKey(raw, encPurpose))
	if err != nil {
		return key{}, fmt.Errorf("building cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return key{}, fmt.Errorf("building cipher: %w", err)
	}
	return key{id: KeyIDHex(raw), aead: aead, loopKey: security.DeriveKey(raw, loopPurpose)}, nil
}

// NewKeyring builds a keyring from the raw 32-byte current key and the raw
// previous keys. Errors never contain key material.
func NewKeyring(current []byte, previous [][]byte) (*Keyring, error) {
	cur, err := newKey(current)
	if err != nil {
		return nil, err
	}
	kr := &Keyring{current: cur, byID: map[string]key{cur.id: cur}, order: []key{cur}}
	for _, p := range previous {
		k, err := newKey(p)
		if err != nil {
			return nil, fmt.Errorf("previous key: %w", err)
		}
		if _, dup := kr.byID[k.id]; dup {
			return nil, errors.New("previous key equals another key")
		}
		kr.byID[k.id] = k
		kr.order = append(kr.order, k)
	}
	return kr, nil
}

// CurrentID is the id of the key new credentials are sealed under. It is not
// secret; the startup log shows it so operators can compare nodes.
func (k *Keyring) CurrentID() string {
	if k == nil {
		return ""
	}
	return k.current.id
}

// aad builds the associated data: a domain separator, then each field
// length-prefixed (uvarint), so no field can smuggle a separator into its
// neighbour.
func aad(userID string, acct *bongov1.FetchAccount, keyID string) []byte {
	out := []byte(aadDomain)
	for _, f := range []string{userID, acct.GetGuid(), acct.GetProtocol(), acct.GetSecurity(), acct.GetHost(), acct.GetUsername(), keyID} {
		out = binary.AppendUvarint(out, uint64(len(f)))
		out = append(out, f...)
	}
	return out
}

// Seal encrypts password for userID and the connection identity in acct under
// the current key, with a fresh random nonce. It does not modify acct; the
// caller stores the result in acct.Credential.
func (k *Keyring) Seal(userID string, acct *bongov1.FetchAccount, password string) (*bongov1.FetchCredential, error) {
	if k == nil {
		return nil, ErrNoKey
	}
	if password == "" || len(password) > MaxPasswordLen {
		return nil, errors.New("password length out of range")
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	ct := k.current.aead.Seal(nil, nonce, []byte(password), aad(userID, acct, k.current.id))
	return &bongov1.FetchCredential{Version: version, KeyId: k.current.id, Nonce: nonce, Ciphertext: ct}, nil
}

// Open decrypts acct.Credential for userID. The error is ErrKeyUnknown or
// ErrCredentialInvalid and carries no detail.
func (k *Keyring) Open(userID string, acct *bongov1.FetchAccount) (string, error) {
	if k == nil {
		return "", ErrKeyUnknown
	}
	c := acct.GetCredential()
	if c == nil {
		return "", ErrCredentialInvalid
	}
	key, ok := k.byID[c.GetKeyId()]
	if !ok {
		return "", ErrKeyUnknown
	}
	if c.GetVersion() != version || len(c.GetNonce()) != nonceLen || len(c.GetCiphertext()) == 0 {
		return "", ErrCredentialInvalid
	}
	pt, err := key.aead.Open(nil, c.GetNonce(), c.GetCiphertext(), aad(userID, acct, key.id))
	if err != nil {
		return "", ErrCredentialInvalid
	}
	return string(pt), nil
}

// NeedsRotation reports whether cred was sealed under a key other than the
// current one.
func (k *Keyring) NeedsRotation(cred *bongov1.FetchCredential) bool {
	return k != nil && cred != nil && cred.GetKeyId() != k.current.id
}

// LoopTokens returns the loop-detection tokens for an account, the current
// key's first and then each previous key's, so a key rotation does not turn
// loop detection off for mail the old key already marked. A token is a short
// hex string derived from a per-key loop key and the account guid; it is not
// the key id and reveals nothing of the key. A nil keyring returns nil.
func (k *Keyring) LoopTokens(guid string) []string {
	if k == nil {
		return nil
	}
	out := make([]string, 0, len(k.order))
	for _, key := range k.order {
		out = append(out, hex.EncodeToString(security.DeriveKey(key.loopKey, guid))[:16])
	}
	return out
}
