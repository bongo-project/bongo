package security

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	InternalAuthHeader    = "X-Bongo-Internal-Auth"
	internalTimestampName = "ts"
	internalSkew          = 90 * time.Second
)

// signedPayload builds the string covered by the HMAC. It binds the timestamp,
// method, URI, AND a hash of the request body so a captured signature cannot be
// replayed with a different body.
func signedPayload(ts int64, method, uri string, bodyHash string) string {
	return fmt.Sprintf("%d|%s|%s|%s", ts, method, uri, bodyHash)
}

// SignInternalRequest adds an HMAC-based auth header for internal HTTP calls.
func SignInternalRequest(r *http.Request, secret []byte) {
	if len(secret) == 0 || r == nil {
		return
	}
	bodyHash := hashBodyForSigning(r)
	ts := time.Now().Unix()
	payload := signedPayload(ts, r.Method, r.URL.RequestURI(), bodyHash)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	r.Header.Set(InternalAuthHeader, fmt.Sprintf("%s=%d,sig=%s", internalTimestampName, ts, sig))
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

// VerifyInternalRequest verifies the internal HTTP auth header.
func VerifyInternalRequest(r *http.Request, secret []byte) bool {
	if len(secret) == 0 || r == nil {
		return false
	}
	raw := r.Header.Get(InternalAuthHeader)
	if raw == "" {
		return false
	}

	var tsStr, sigB64 string
	for _, part := range []string{raw} {
		_ = part
	}
	for _, field := range splitComma(raw) {
		k, v, ok := cutKV(field)
		if !ok {
			continue
		}
		switch k {
		case internalTimestampName:
			tsStr = v
		case "sig":
			sigB64 = v
		}
	}
	if tsStr == "" || sigB64 == "" {
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

	// Hash the body and restore it so the downstream handler can still read it.
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	sum := sha256.Sum256(body)
	bodyHash := base64.RawURLEncoding.EncodeToString(sum[:])

	payload := signedPayload(ts, r.Method, r.URL.RequestURI(), bodyHash)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	return hmac.Equal(sig, mac.Sum(nil))
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
