package middleware

import (
	"context"
	"net/http"

	"github.com/bongo-project/bongo/internal/config"
)

const sourceURLKey contextKey = "source_url"

// SourceURLFromContext returns the URL the "Source code" link points at
// (web.source_url). Falls back to the upstream repository if not set.
func SourceURLFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sourceURLKey).(string); ok && v != "" {
		return v
	}
	return config.DefaultSourceURL
}

// WithSourceURL returns ctx carrying the given source URL, as InjectSourceURL
// would set it.
func WithSourceURL(ctx context.Context, u string) context.Context {
	return context.WithValue(ctx, sourceURLKey, u)
}

// InjectSourceURL stores web.source_url in every request's context so the
// layouts and the login page can render the AGPL section 13 source link.
func InjectSourceURL(u string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithSourceURL(r.Context(), u)))
		})
	}
}
