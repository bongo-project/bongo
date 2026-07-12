// Package sanitize provides reusable HTML sanitization for user-generated
// compose and signature content. It is stricter than the inbound mail display
// policy in internal/web/handlers/mail.go: only colour CSS properties are
// allowed, and img src is restricted to data: and cid: schemes so that no
// remote images can be embedded in outgoing content.
package sanitize

import (
	"regexp"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

// composePolicy is the shared, concurrency-safe policy instance for compose /
// signature HTML. It is constructed once on first use via sync.Once.
var (
	composeOnce   sync.Once
	composePolicy *bluemonday.Policy
)

// imgSrcRe matches img src attribute values that begin with data: or cid:
// (case-insensitive). Using a per-attribute Matching regex rather than
// AllowURLSchemes avoids the bluemonday limitation that URL-scheme allowlists
// are global: AllowURLSchemes("http") would permit http on img src as well as
// on a href. With Matching, the attribute is kept only when the value satisfies
// this pattern, regardless of the global scheme allowlist.
var imgSrcRe = regexp.MustCompile(`(?i)^(data:|cid:)`)

// blockElements lists the structural/layout elements we allow in compose HTML.
var blockElements = []string{
	"p", "br", "div", "span",
	"ul", "ol", "li",
	"h1", "h2", "h3", "h4", "h5", "h6",
	"blockquote",
	"table", "thead", "tbody", "tr", "td", "th",
}

// inlineElements lists inline formatting elements we allow.
var inlineElements = []string{
	"b", "i", "u", "strong", "em",
}

// allTextElements is the union of block and inline elements for style rules.
var allTextElements = append(append([]string{}, blockElements...), inlineElements...)

func buildComposePolicy() *bluemonday.Policy {
	// Start from a blank policy so that we have full control over what is
	// permitted. Starting from UGCPolicy() and trying to narrow it down does
	// not work: AllowAttrs() appends rules, so existing UGCPolicy rules
	// (e.g. img src with http/https) cannot be removed after the fact.
	p := bluemonday.NewPolicy()

	// Allow inline formatting elements with no attributes.
	p.AllowElements(inlineElements...)

	// Allow structural elements with no attributes.
	p.AllowElements(blockElements...)

	// Allow links with href restricted to safe URL schemes only.
	p.AllowAttrs("href").OnElements("a")
	// cid is included so that inline-embedded images (RFC 2392) in img[src]
	// pass both the per-attribute Matching regex (imgSrcRe) AND bluemonday's
	// global scheme validation, which is applied on top of attribute matching.
	// cid: is harmless on a[href] — browsers treat it as a dead link.
	p.AllowURLSchemes("http", "https", "mailto", "cid")

	// Allow the target attribute on links.
	p.AllowAttrs("target").Matching(regexp.MustCompile(`^(_blank|_self|_parent|_top)$`)).OnElements("a")

	// Allow rel attribute on links (needed for noopener etc.).
	p.AllowAttrs("rel").OnElements("a")

	// Allow img with src restricted to data: and cid: only. The Matching
	// regex gates the src to these two schemes; AllowURLSchemes above ensures
	// both pass bluemonday's global scheme validation (data: via
	// AllowDataURIImages, cid: via the explicit scheme allowlist).
	// http/https are NOT in imgSrcRe so remote images are blocked even though
	// http/https are in AllowURLSchemes for a[href].
	p.AllowDataURIImages()
	p.AllowAttrs("src").Matching(imgSrcRe).OnElements("img")
	p.AllowAttrs("alt", "width", "height").OnElements("img")

	// Allow inline style restricted to colour-only properties. The broader
	// display policy in mail.go also allows font, margin, padding, etc. —
	// we deliberately keep compose/signature output minimal.
	p.AllowStyles("color", "background-color").OnElements(allTextElements...)

	return p
}

func policy() *bluemonday.Policy {
	composeOnce.Do(func() {
		composePolicy = buildComposePolicy()
	})
	return composePolicy
}

// Compose sanitizes HTML that originates from a compose window or signature
// editor. It allows common formatting tags, restricts links to http/https/mailto,
// and limits img src to data: and cid: only so that no remote images can be
// embedded in outgoing messages.
func Compose(html string) string {
	return policy().Sanitize(html)
}
