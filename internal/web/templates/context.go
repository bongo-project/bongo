package templates

import (
	"context"

	"github.com/bongo-go/bongo-go/internal/i18n"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
)

// localeFromCtx returns the locale stored in the request context by the
// InjectLocale middleware. Falls back to "en" if not set.
func localeFromCtx(ctx context.Context) string {
	return middleware.LocaleFromContext(ctx)
}

// t translates a message ID using the locale from the request context.
// It is the primary helper used in .templ files: { t(ctx, "nav.inbox") }
func t(ctx context.Context, messageID string) string {
	return i18n.T(localeFromCtx(ctx), messageID)
}
