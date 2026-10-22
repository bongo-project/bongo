package security

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	InternalAuthHeader    = "X-Bongo-Internal-Auth"
	internalTimestampName = "ts"
	internalSkew          = 90 * time.Second

	// internalAuthPurpose labels the subkey derived from the raw session secret
	// for internal control-plane HMAC. Blob sync has its own purpose below, so a
	// signature minted for one surface is rejected by the other.
	internalAuthPurpose = "bongo/internal-auth/v1"

	// blobSyncPurpose labels the subkey for node-to-node blob pulls.
	blobSyncPurpose = "bongo/blob-sync/v1"

	// maxInternalBodyBytes caps the request body buffered for signature
	// verification. The buffering happens before the caller is authenticated,
	// so it must be bounded. Legitimate internal bodies are small JSON
	// documents (cluster join, user create); blob sync is GET/HEAD with an
	// empty request body. Raise it only if an internal caller needs to send
	// a larger body.
	maxInternalBodyBytes = 1 << 20
)

// signedPayload builds the string covered by the HMAC. It binds the timestamp,
// a per-request nonce, method, URI, AND a hash of the request body so a captured
// signature cannot be replayed (the nonce is single-use within the skew window)
// nor reused with a different body.
func signedPayload(ts int64, nonce, method, uri string, bodyHash string) string {
	return fmt.Sprintf("%d|%s|%s|%s|%s", ts, nonce, method, uri, bodyHash)
}

// SignInternalRequest adds an HMAC-based auth header for internal control-plane
// HTTP calls.
func SignInternalRequest(r *http.Request, secret []byte) {
	signWithPurpose(r, secret, internalAuthPurpose)
}

// SignBlobSyncRequest signs a node-to-node blob pull. It is not accepted by
// VerifyInternalRequest.
func SignBlobSyncRequest(r *http.Request, secret []byte) {
	signWithPurpose(r, secret, blobSyncPurpose)
}

func signWithPurpose(r *http.Request, secret []byte, purpose string) {
	if len(secret) == 0 || r == nil {
		return
	}
	bodyHash := hashBodyForSigning(r)
	ts := time.Now().Unix()
	nonce := newNonce()
	payload := signedPayload(ts, nonce, r.Method, r.URL.RequestURI(), bodyHash)
	mac := hmac.New(sha256.New, DeriveKey(secret, purpose))
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	r.Header.Set(InternalAuthHeader, fmt.Sprintf("%s=%d,nonce=%s,sig=%s", internalTimestampName, ts, nonce, sig))
}

// newNonce returns a random 128-bit base64 nonce.
func newNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// internalNonces tracks recently-seen nonces so a signed request cannot be
// replayed within the timestamp skew window.
var internalNonces = &nonceCache{seen: make(map[string]time.Time)}

type nonceCache struct {
	mu        sync.Mutex
	seen      map[string]time.Time // nonce -> expiry
	lastSweep time.Time
}

// checkAndRecord returns false if the nonce was already seen (a replay);
// otherwise it records the nonce with an expiry and returns true.
func (c *nonceCache) checkAndRecord(nonce string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.lastSweep) > internalSkew {
		for k, exp := range c.seen {
			if now.After(exp) {
				delete(c.seen, k)
			}
		}
		c.lastSweep = now
	}
	if exp, ok := c.seen[nonce]; ok && now.Before(exp) {
		return false
	}
	c.seen[nonce] = now.Add(2 * internalSkew)
	return true
}

// hashBodyForSigning returns the base64 SHA-256 of the request body without
// consuming it for later transmission (uses GetBody when available).
func hashBodyForSigning(r *http.Request) string {
	var body []byte
	switch {
	case r.GetBody != nil:
		if rc, err := r.GetBody(); err == nil {
			body, _ = io.ReadAll(rc)
			_ = rc.Close()
		}
	case r.Body != nil:
		body, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		buf := body
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf)), nil }
	}
	sum := sha256.Sum256(body)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// VerifyInternalRequest verifies the internal control-plane auth header.
func VerifyInternalRequest(r *http.Request, secret []byte) bool {
	return verifyWithPurpose(r, secret, internalAuthPurpose)
}

// VerifyBlobSyncRequest verifies a request signed by SignBlobSyncRequest.
func VerifyBlobSyncRequest(r *http.Request, secret []byte) bool {
	return verifyWithPurpose(r, secret, blobSyncPurpose)
}

func verifyWithPurpose(r *http.Request, secret []byte, purpose string) bool {
	if len(secret) == 0 || r == nil {
		return false
	}
	raw := r.Header.Get(InternalAuthHeader)
	if raw == "" {
		return false
	}

	var tsStr, nonce, sigB64 string
	for _, field := range splitComma(raw) {
		k, v, ok := cutKV(field)
		if !ok {
			continue
		}
		switch k {
		case internalTimestampName:
			tsStr = v
		case "nonce":
			nonce = v
		case "sig":
			sigB64 = v
		}
	}
	if tsStr == "" || nonce == "" || sigB64 == "" {
		return false
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return false
	}
	now := time.Now()
	reqTime := time.Unix(ts, 0)
	if reqTime.Before(now.Add(-internalSkew)) || reqTime.After(now.Add(internalSkew)) {
		return false
	}

	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}

	// Reject junk signatures and oversize bodies before reading anything:
	// nothing above authenticates the caller.
	if len(sig) != sha256.Size || r.ContentLength > maxInternalBodyBytes {
		return false
	}

	// Hash the body and restore it so the downstream handler can still read it.
	// Chunked bodies (ContentLength -1) are bounded by the limit reader.
	var body []byte
	if r.Body != nil {
		var readErr error
		body, readErr = io.ReadAll(io.LimitReader(r.Body, maxInternalBodyBytes+1))
		if readErr != nil || len(body) > maxInternalBodyBytes {
			return false
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	sum := sha256.Sum256(body)
	bodyHash := base64.RawURLEncoding.EncodeToString(sum[:])

	payload := signedPayload(ts, nonce, r.Method, r.URL.RequestURI(), bodyHash)
	mac := hmac.New(sha256.New, DeriveKey(secret, purpose))
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return false
	}

	// Signature is valid; reject if this nonce was already used (replay).
	return internalNonces.checkAndRecord(nonce, now)
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, trimSpace(s[start:]))
	return parts
}

func cutKV(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return trimSpace(s[:i]), trimSpace(s[i+1:]), true
		}
	}
	return "", "", false
}

func trimSpace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
