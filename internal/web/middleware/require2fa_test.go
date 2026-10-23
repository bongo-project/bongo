package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// fakeKV serves keys from a map. A key mapped to a nil entry is absent
// (store.ErrNotFound); errKeys return an I/O error.
type fakeKV struct {
	data    map[string][]byte
	errKeys map[string]error
}

func (f *fakeKV) GetKey(key []byte) ([]byte, error) {
	if err, ok := f.errKeys[string(key)]; ok {
		return nil, err
	}
	v, ok := f.data[string(key)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return v, nil
}

func TestRequire2FAFailsClosed(t *testing.T) {
	const policyKey = "/system/settings/require_2fa"
	const profileKey = "/user/u1/profile"
	ioErr := errors.New("io")
	withTOTP, _ := proto.Marshal(&bongov1.UserProfile{TotpEnabled: true})
	withoutTOTP, _ := proto.Marshal(&bongov1.UserProfile{})

	tests := []struct {
		name         string
		kv           *fakeKV
		path         string
		wantStatus   int
		wantNext     bool
		wantLocation string
	}{
		{"policy read error denies", &fakeKV{errKeys: map[string]error{policyKey: ioErr}}, "/mail/INBOX", http.StatusServiceUnavailable, false, ""},
		{"profile read error denies", &fakeKV{data: map[string][]byte{policyKey: []byte("1")}, errKeys: map[string]error{profileKey: ioErr}}, "/mail/INBOX", http.StatusServiceUnavailable, false, ""},
		{"profile missing denies", &fakeKV{data: map[string][]byte{policyKey: []byte("1")}}, "/mail/INBOX", http.StatusServiceUnavailable, false, ""},
		{"corrupt profile denies", &fakeKV{data: map[string][]byte{policyKey: []byte("1"), profileKey: {0xff, 0xff, 0xff}}}, "/mail/INBOX", http.StatusServiceUnavailable, false, ""},
		{"profile read error denies exempt path too", &fakeKV{data: map[string][]byte{policyKey: []byte("1")}, errKeys: map[string]error{profileKey: ioErr}}, "/settings/security", http.StatusServiceUnavailable, false, ""},
		// Positive controls.
		{"no policy key passes", &fakeKV{}, "/mail/INBOX", http.StatusOK, true, ""},
		{"policy off passes", &fakeKV{data: map[string][]byte{policyKey: []byte("0")}}, "/mail/INBOX", http.StatusOK, true, ""},
		{"policy on with TOTP passes", &fakeKV{data: map[string][]byte{policyKey: []byte("1"), profileKey: withTOTP}}, "/mail/INBOX", http.StatusOK, true, ""},
		{"policy on without TOTP redirects", &fakeKV{data: map[string][]byte{policyKey: []byte("1"), profileKey: withoutTOTP}}, "/mail/INBOX", http.StatusSeeOther, false, "/settings/security"},
		{"exempt path passes without TOTP", &fakeKV{data: map[string][]byte{policyKey: []byte("1"), profileKey: withoutTOTP}}, "/settings/security", http.StatusOK, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			h := Require2FA(tt.kv)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req = req.WithContext(WithUserContext(req.Context(), "u1", "u1@example.com"))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if called != tt.wantNext {
				t.Errorf("next called = %v, want %v", called, tt.wantNext)
			}
			if tt.wantNext {
				return
			}
			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if loc := rr.Header().Get("Location"); loc != tt.wantLocation {
				t.Errorf("Location = %q, want %q", loc, tt.wantLocation)
			}
		})
	}
}
