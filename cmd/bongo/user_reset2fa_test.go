package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserReset2FA(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr string
		wantOut string
	}{
		{"reset", http.StatusOK, "", "two-factor authentication turned off: a@x.test"},
		{"unknown", http.StatusNotFound, "no account has the primary address a@x.test", ""},
		{"already off", http.StatusConflict, "two-factor authentication is not on for a@x.test", ""},
		{"unsigned", http.StatusUnauthorized, "401 Unauthorized", ""},
		{"server error", http.StatusInternalServerError, "server returned 500: boom", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotEmail string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				var body struct{ Email string }
				_ = json.NewDecoder(r.Body).Decode(&body)
				gotEmail = body.Email
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("boom\n"))
			}))
			defer srv.Close()

			oldAddr, oldEmail := userHTTPAddr, userEmail
			t.Cleanup(func() { userHTTPAddr, userEmail = oldAddr, oldEmail })
			userHTTPAddr, userEmail = srv.URL, "a@x.test"

			var out bytes.Buffer
			userReset2FACmd.SetOut(&out)
			err := runUserReset2FA(userReset2FACmd, nil)

			if gotPath != "/internal/user/reset-2fa" || gotEmail != "a@x.test" {
				t.Errorf("request path %q email %q", gotPath, gotEmail)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !strings.Contains(out.String(), tt.wantOut) {
					t.Errorf("output %q, want %q", out.String(), tt.wantOut)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %v, want %q", err, tt.wantErr)
			}
		})
	}
}
