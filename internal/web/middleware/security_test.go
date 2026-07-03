package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	tests := []struct {
		name          string
		contentType   string
		cacheControl  string
		tlsEnabled    bool
		wantCacheCtrl string
		wantHSTS      bool
	}{
		{
			name:          "html response sets no-store",
			contentType:   "text/html; charset=utf-8",
			tlsEnabled:    false,
			wantCacheCtrl: "no-store",
			wantHSTS:      false,
		},
		{
			name:          "non-html response still defaults to no-store",
			contentType:   "application/json",
			tlsEnabled:    false,
			wantCacheCtrl: "no-store",
			wantHSTS:      false,
		},
		{
			name:          "css response still defaults to no-store unless overridden",
			contentType:   "text/css",
			tlsEnabled:    false,
			wantCacheCtrl: "no-store",
			wantHSTS:      false,
		},
		{
			name:          "tls request sets hsts",
			contentType:   "text/html; charset=utf-8",
			tlsEnabled:    true,
			wantCacheCtrl: "no-store",
			wantHSTS:      true,
		},
		{
			name:          "non-tls request omits hsts",
			contentType:   "text/html; charset=utf-8",
			tlsEnabled:    false,
			wantCacheCtrl: "no-store",
			wantHSTS:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				if tc.cacheControl != "" {
					w.Header().Set("Cache-Control", tc.cacheControl)
				}
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.tlsEnabled {
				req.TLS = &tls.ConnectionState{}
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Check fixed security headers.
			checkHeader(t, rr, "X-Frame-Options", "DENY")
			checkHeader(t, rr, "X-Content-Type-Options", "nosniff")
			checkHeader(t, rr, "Referrer-Policy", "strict-origin-when-cross-origin")
			checkHeader(t, rr, "X-XSS-Protection", "0")
			checkHeader(t, rr, "Permissions-Policy", "camera=(), microphone=(), geolocation=()")

			csp := rr.Header().Get("Content-Security-Policy")
			if csp == "" {
				t.Error("Content-Security-Policy header missing")
			}

			// Check Cache-Control.
			got := rr.Header().Get("Cache-Control")
			if got != tc.wantCacheCtrl {
				t.Errorf("Cache-Control = %q, want %q", got, tc.wantCacheCtrl)
			}

			// Check HSTS.
			hsts := rr.Header().Get("Strict-Transport-Security")
			if tc.wantHSTS && hsts == "" {
				t.Error("Strict-Transport-Security header missing for TLS request")
			}
			if !tc.wantHSTS && hsts != "" {
				t.Errorf("Strict-Transport-Security header unexpectedly set: %q", hsts)
			}
		})
	}
}

func TestSecurityHeadersDefaultCacheControlSurvivesCommittedHeaders(t *testing.T) {
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html></html>"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestSecurityHeadersCSP(t *testing.T) {
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	wantDirectives := []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data: cid:",
		"font-src 'self'",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"object-src 'none'",
	}
	for _, directive := range wantDirectives {
		found := false
		for _, part := range splitCSP(csp) {
			if part == directive {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("CSP missing directive %q, got: %s", directive, csp)
		}
	}
}

func TestSecurityHeadersExistingCacheControl(t *testing.T) {
	// When the handler already sets Cache-Control (e.g., static asset handler),
	// the security middleware must not override it.
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	got := rr.Header().Get("Cache-Control")
	want := "public, max-age=31536000, immutable"
	if got != want {
		t.Errorf("Cache-Control = %q, want %q (handler value should be preserved)", got, want)
	}
}

func TestSecurityHeadersHSTSValue(t *testing.T) {
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = &tls.ConnectionState{}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	got := rr.Header().Get("Strict-Transport-Security")
	want := "max-age=31536000; includeSubDomains"
	if got != want {
		t.Errorf("Strict-Transport-Security = %q, want %q", got, want)
	}
}

// checkHeader asserts that a specific header has the expected value.
func checkHeader(t *testing.T, rr *httptest.ResponseRecorder, name, want string) {
	t.Helper()
	got := rr.Header().Get(name)
	if got != want {
		t.Errorf("header %s = %q, want %q", name, got, want)
	}
}

// splitCSP splits a CSP string on "; " boundaries, trimming whitespace.
func splitCSP(csp string) []string {
	var parts []string
	for _, p := range splitOnSemicolon(csp) {
		p = trimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitOnSemicolon(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
