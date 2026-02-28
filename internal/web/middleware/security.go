package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// responseRecorder wraps http.ResponseWriter to capture the status code and
// allow inspecting headers after the handler writes them.
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.written = true
	r.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher if the underlying writer supports it.
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker if the underlying writer supports it.
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// SecurityHeaders returns middleware that adds standard security headers to all
// HTTP responses. For HTML responses it adds Cache-Control: no-store. When the
// request was made over TLS it also adds Strict-Transport-Security.
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Set headers that are independent of the response body.
			h := w.Header()
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("X-XSS-Protection", "0")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			h.Set("Content-Security-Policy",
				"default-src 'self'; "+
					"script-src 'self' 'unsafe-inline' 'unsafe-eval'; "+
					"style-src 'self' 'unsafe-inline'; "+
					"img-src 'self' data: cid:; "+
					"font-src 'self'; "+
					"connect-src 'self'; "+
					"frame-ancestors 'none'; "+
					"base-uri 'self'; "+
					"form-action 'self'",
			)

			// HSTS: only when the connection is over TLS.
			if r.TLS != nil {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			// Wrap the writer so we can inspect Content-Type after the handler runs.
			rr := newResponseRecorder(w)
			next.ServeHTTP(rr, r)

			// Add Cache-Control: no-store for HTML responses after the handler
			// has had a chance to set the Content-Type. Only set it when the
			// handler has not already written a Cache-Control header.
			ct := w.Header().Get("Content-Type")
			if strings.HasPrefix(ct, "text/html") {
				existing := w.Header().Get("Cache-Control")
				if existing == "" {
					w.Header().Set("Cache-Control", "no-store")
				}
			}
		})
	}
}
