package templates

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// jsMessageIDs are the translation keys that bongo.js needs at runtime. They
// are serialized into the #i18n-data element by layout.templ and read by the
// i18n() helper in bongo.js. CSP forbids inline scripts, so a JSON data island
// (in a data attribute) is used instead of an inline <script>.
var jsMessageIDs = []string{
	"shortcuts.title", "tag.createFailed",
	"js.newMail", "js.upcomingEvent", "js.eventReminderPrefix", "js.eventReminderSuffix",
	"js.scanFailedPrefix", "js.scanErrorPrefix", "js.scannedInfix", "js.movedToJunkSuffix", "js.scanCancelled",
	"js.tagsLoadFailed", "js.tagNameEmpty", "js.tagSaveFailed", "js.tagDeleteFailed",
	"js.shortcutNextMessage", "js.shortcutPrevMessage", "js.shortcutOpenMessage", "js.shortcutReply",
	"js.shortcutDelete", "js.shortcutToggleStar", "js.shortcutFocusSearch", "js.shortcutCompose",
	"js.shortcutCloseModal", "js.shortcutShowHelp", "js.close", "js.contentLoaded", "js.logConnecting",
	"js.logConnected", "js.logConnectionLost",
}

// i18nJSON returns a JSON object mapping each bongo.js message key to its
// translation in the request locale. json.Marshal escapes <, >, & to \u00XX,
// so the result is safe to embed in an HTML attribute value.
func i18nJSON(ctx context.Context) string {
	m := make(map[string]string, len(jsMessageIDs))
	for _, id := range jsMessageIDs {
		m[id] = t(ctx, id)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// localeFromCtx returns the locale stored in the request context by the
// InjectLocale middleware. Falls back to "en" if not set.
func localeFromCtx(ctx context.Context) string {
	return middleware.LocaleFromContext(ctx)
}

// sourceURL returns the configured link target for the "Source code" link.
func sourceURL(ctx context.Context) templ.SafeURL {
	return templ.URL(middleware.SourceURLFromContext(ctx))
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

// csrfHeaders returns a JSON object suitable for an hx-headers attribute so
// every HTMX mutation carries the CSRF token. json.Marshal escapes <, >, & so
// the result is safe to embed in an HTML attribute (as with i18nJSON).
func csrfHeaders(ctx context.Context) string {
	token := middleware.CSRFTokenFromContext(ctx)
	if token == "" {
		return "{}"
	}
	b, err := json.Marshal(map[string]string{"X-CSRF-Token": token})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// csrfToken returns the request's CSRF token, or "" outside enforcing roles.
// Multipart forms render it as their first hidden field so RequireCSRF can find
// it without reading past the start of an upload.
func csrfToken(ctx context.Context) string {
	return middleware.CSRFTokenFromContext(ctx)
}

// newSendID returns a fresh compose send ID: 128 random bits as 32 lowercase
// hex characters. It is generated each time the compose form renders, so one
// rendering is one send; the server uses it to make a retried submission
// idempotent. The format must match the check in handlers.Send.
func newSendID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on supported platforms. An empty ID makes
		// Send refuse the form rather than accept a predictable one.
		return ""
	}
	return hex.EncodeToString(b)
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
