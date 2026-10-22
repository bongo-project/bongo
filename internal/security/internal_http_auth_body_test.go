package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// endlessReader yields 'a' bytes up to limit and counts what was consumed.
type endlessReader struct {
	limit int64
	read  int64
}

func (e *endlessReader) Read(p []byte) (int, error) {
	if e.read >= e.limit {
		return 0, io.EOF
	}
	n := int64(len(p))
	if rem := e.limit - e.read; n > rem {
		n = rem
	}
	for i := int64(0); i < n; i++ {
		p[i] = 'a'
	}
	e.read += n
	return int(n), nil
}

func (e *endlessReader) Close() error { return nil }

func forgedAuthHeader() string {
	sig := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, sha256.Size))
	return fmt.Sprintf("ts=%d,nonce=%s,sig=%s", time.Now().Unix(), newNonce(), sig)
}

func TestVerifyBodyCap(t *testing.T) {
	const unbounded = 8<<20 + 7

	tests := []struct {
		name      string
		build     func(src *endlessReader) *http.Request
		wantMaxRd int64
	}{
		{
			name: "forged header chunked endless body",
			build: func(src *endlessReader) *http.Request {
				r, _ := http.NewRequest(http.MethodPost, "http://node/internal/x", src)
				r.ContentLength = -1
				r.Header.Set(InternalAuthHeader, forgedAuthHeader())
				return r
			},
			wantMaxRd: maxInternalBodyBytes + 1,
		},
		{
			name: "forged header oversize content length",
			build: func(src *endlessReader) *http.Request {
				r, _ := http.NewRequest(http.MethodPost, "http://node/internal/x", src)
				r.ContentLength = 64 << 20
				r.Header.Set(InternalAuthHeader, forgedAuthHeader())
				return r
			},
			wantMaxRd: 0,
		},
		{
			name: "missing header",
			build: func(src *endlessReader) *http.Request {
				r, _ := http.NewRequest(http.MethodPost, "http://node/internal/x", src)
				r.ContentLength = -1
				return r
			},
			wantMaxRd: 0,
		},
		{
			name: "short signature",
			build: func(src *endlessReader) *http.Request {
				r, _ := http.NewRequest(http.MethodPost, "http://node/internal/x", src)
				r.ContentLength = -1
				r.Header.Set(InternalAuthHeader, fmt.Sprintf("ts=%d,nonce=n,sig=AAAA", time.Now().Unix()))
				return r
			},
			wantMaxRd: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := &endlessReader{limit: unbounded}
			if VerifyInternalRequest(tc.build(src), authSecret) {
				t.Fatal("forged request must not verify")
			}
			if src.read > tc.wantMaxRd {
				t.Fatalf("read %d bytes, want at most %d", src.read, tc.wantMaxRd)
			}
		})
	}
}

func TestVerifyBodyCapBoundary(t *testing.T) {
	tests := []struct {
		name string
		size int
		want bool
	}{
		{"cap minus three passes", maxInternalBodyBytes - 3, true},
		{"exactly cap passes", maxInternalBodyBytes, true},
		{"cap plus one rejected", maxInternalBodyBytes + 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte{'b'}, tc.size)
			req, _ := http.NewRequest(http.MethodPut, "http://node/internal/kv/k", bytes.NewReader(payload))
			SignInternalRequest(req, authSecret)
			// Force the chunked path so the limit reader, not the length
			// pre-check, enforces the cap.
			req.ContentLength = -1
			if got := VerifyInternalRequest(req, authSecret); got != tc.want {
				t.Fatalf("verify = %v, want %v", got, tc.want)
			}
			if tc.want {
				body, _ := io.ReadAll(req.Body)
				if !bytes.Equal(body, payload) {
					t.Fatal("body not preserved after verify")
				}
			}
		})
	}
}

func TestVerifyBlobSyncEmptyBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://node/internal/blob/abc", nil)
	SignBlobSyncRequest(req, authSecret)
	if !VerifyBlobSyncRequest(req, authSecret) {
		t.Fatal("signed blob sync GET must verify")
	}
}
