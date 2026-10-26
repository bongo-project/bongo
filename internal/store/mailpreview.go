package store

import (
	"mime"
	"strings"

	"github.com/jhillyerd/enmime"
	"golang.org/x/net/html"
)

// MailPreviewRunes is the maximum length of a stored preview, in runes.
const MailPreviewRunes = 160

// MailPreview derives the message-list preview and attachment flag from a
// parsed envelope. It is pure: no I/O, so it is safe wherever the envelope is
// already in hand.
//
// The preview is plain text: the first text/plain part when there is one,
// otherwise the HTML body stripped of markup (script, style and head content
// dropped, entities decoded). enmime's own env.Text is not used for HTML-only
// mail because it is html2text output with link annotations and table art.
// Lines starting with '>' (quoted replies) are skipped, whitespace is
// collapsed, and the result is cut to MailPreviewRunes runes. It is never
// HTML and must be rendered escaped.
//
// hasAttachments is true for any attachment part, or a non-text, non-inline
// part. Inline (cid) images do not count, so a signature logo does not earn a
// paperclip; this matches mainstream mail clients. A part counts as inline
// when it says Content-Disposition: inline or carries a Content-ID, so a cid
// image with no disposition header is not an attachment either. The converse
// also follows: a PDF sent as "Content-Disposition: inline; filename=a.pdf"
// (Apple Mail style) does not earn a paperclip.
func MailPreview(env *enmime.Envelope) (preview string, hasAttachments bool) {
	if env == nil {
		return "", false
	}

	text := ""
	if hasPlainTextPart(env) {
		text = env.Text
	} else if env.HTML != "" {
		text = htmlToText(env.HTML)
	} else {
		text = env.Text
	}

	return truncateRunes(collapsePreview(text), MailPreviewRunes), envelopeHasAttachment(env)
}

func hasPlainTextPart(env *enmime.Envelope) bool {
	if env.Root == nil {
		return false
	}
	return env.Root.BreadthMatchFirst(func(p *enmime.Part) bool {
		return strings.EqualFold(p.ContentType, "text/plain") &&
			!strings.EqualFold(p.Disposition, "attachment")
	}) != nil
}

func envelopeHasAttachment(env *enmime.Envelope) bool {
	if len(env.Attachments) > 0 {
		return true
	}
	for _, p := range env.OtherParts {
		// enmime already routes Content-Disposition: inline parts to Inlines,
		// so only a Content-ID without a disposition reaches OtherParts.
		if p.ContentID != "" {
			continue
		}
		mt, _, err := mime.ParseMediaType(p.ContentType)
		if err != nil {
			mt = strings.ToLower(p.ContentType)
		}
		if strings.HasPrefix(mt, "text/") {
			continue
		}
		return true
	}
	return false
}

// collapsePreview drops quoted lines and collapses every whitespace run.
func collapsePreview(s string) string {
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t\r"), ">") {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(strings.Fields(strings.Join(kept, " ")), " ")
}

func truncateRunes(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// htmlToText extracts visible text from an HTML document. Block-level tags
// become whitespace so adjacent paragraphs do not run together.
func htmlToText(src string) string {
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(src))
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return b.String()
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			tt := z.Token()
			name := tt.Data
			switch name {
			case "script", "style", "head", "title", "template":
				switch tt.Type {
				case html.StartTagToken:
					skip++
				case html.EndTagToken:
					if skip > 0 {
						skip--
					}
				}
			case "a", "b", "i", "u", "em", "strong", "span", "font", "small", "big", "sub", "sup", "code", "abbr", "s":
				// Inline: no separator, so "<b>He</b>llo" stays one word.
			default:
				b.WriteByte(' ')
			}
		}
	}
}
