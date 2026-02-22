package templates

import (
	"context"

	"github.com/bongo-go/bongo-go/internal/web/middleware"
)

// localeFromCtx returns the locale stored in the request context by the
// InjectLocale middleware. Falls back to "en" if not set.
func localeFromCtx(ctx context.Context) string {
	return middleware.LocaleFromContext(ctx)
}
