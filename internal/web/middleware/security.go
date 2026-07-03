package middleware

import "net/http"

// SecurityHeaders returns middleware that adds standard security headers to all
// HTTP responses. It sets Cache-Control: no-store by default unless the handler
// overrides it before the response is written. When the request was made over
// TLS it also adds Strict-Transport-Security.
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
					"script-src 'self'; "+
					"style-src 'self'; "+
					"img-src 'self' data: cid:; "+
					"font-src 'self'; "+
					"connect-src 'self'; "+
					"frame-ancestors 'none'; "+
					"base-uri 'self'; "+
					"form-action 'self'; "+
					"object-src 'none'",
			)

			// HSTS: only when the connection is over TLS.
			if r.TLS != nil {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			if h.Get("Cache-Control") == "" {
				h.Set("Cache-Control", "no-store")
			}

			next.ServeHTTP(w, r)
		})
	}
}
