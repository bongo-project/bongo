package templates

import (
	"context"
	"strings"

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

// encMailbox encodes a mailbox name for use in URL paths by replacing
// '/' with %2F so chi routes it as a single path segment.
func encMailbox(name string) string {
	return strings.ReplaceAll(name, "/", "%2F")
}

// mailboxURL returns a URL path for a mailbox with the name properly encoded.
func mailboxURL(name string) string {
	return "/mail/" + encMailbox(name)
}

// require2FAFromCtx returns true when the system requires 2FA and the
// current user has not yet enabled it.
func require2FAFromCtx(ctx context.Context) bool {
	return middleware.Require2FAFromContext(ctx)
}

// pageWindow returns the page numbers to display in a truncated pagination
// control. It always includes page 1 and totalPages, the current page, and
// up to 2 pages on each side. Gaps are represented by 0 (rendered as "...").
func pageWindow(page, totalPages int) []int {
	if totalPages <= 9 {
		pages := make([]int, totalPages)
		for i := range pages {
			pages[i] = i + 1
		}
		return pages
	}

	// Collect the set of pages to show.
	show := map[int]bool{1: true, totalPages: true}
	for i := page - 2; i <= page+2; i++ {
		if i >= 1 && i <= totalPages {
			show[i] = true
		}
	}

	// Build sorted list with 0 for gaps.
	var pages []int
	prev := 0
	for i := 1; i <= totalPages; i++ {
		if show[i] {
			if prev != 0 && i-prev > 1 {
				pages = append(pages, 0)
			}
			pages = append(pages, i)
			prev = i
		}
	}
	return pages
}
