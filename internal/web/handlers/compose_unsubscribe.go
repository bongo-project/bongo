package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/unsubscribe"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// maxUnsubscribeSubjectBytes is the RFC 5322 line limit, for a subject taken
// from a mailto: URI.
const maxUnsubscribeSubjectBytes = 998

// SetSpamThreshold sets the source of the antispam tag threshold.
func (h *ComposeHandler) SetSpamThreshold(fn func() float64) { h.spamTag = fn }

// UnsubscribeMailto opens the composer prefilled from the message's
// List-Unsubscribe mailto: URI. GET /compose/unsubscribe/{mailbox}/{uid}
//
// Like ReplyList, the URI is read from the stored message on the server and
// never from the request. Only the single address, the subject and the body
// are used: cc, bcc and every other header field are dropped, so the link
// cannot add recipients. The user sends the message, so the normal send path,
// limits and Sent copy apply. A message in Junk or tagged as spam, or one with
// no usable mailto URI, redirects to a blank composer.
func (h *ComposeHandler) UnsubscribeMailto(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	msg, _ := h.mailboxes.GetMessage(userID, mailbox, uint32(uid))
	if msg == nil {
		http.Redirect(w, r, "/compose", http.StatusSeeOther)
		return
	}
	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil || unsubscribeBlocked(mailbox, meta, h.spamTag) {
		http.Redirect(w, r, "/compose", http.StatusSeeOther)
		return
	}
	opts := unsubscribeHeaders(h.loadEnvelope(meta))
	target, ok := unsubscribe.ParseMailto(opts.Mailto)
	to := composeToPrefill(target.To)
	if !ok || to == "" {
		http.Redirect(w, r, "/compose", http.StatusSeeOther)
		return
	}
	subject := mailtoSubject(target.Subject)
	body := mailtoBody(target.Body)

	fromOpts, fromSel := h.buildFromPickerOptions(userID, email)
	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ComposePage("new", to, "", "", subject, body, "", 0, fromOpts, fromSel, "", h.selfCardOption(userID, false))).ServeHTTP(w, r)
		return
	}
	mailboxInfos := h.loadMailboxInfos(userID)
	templ.Handler(templates.ComposePageFull("new", to, "", "", subject, body, "", 0, mailboxInfos, nil, email, fromOpts, fromSel, "", h.selfCardOption(userID, false))).ServeHTTP(w, r)
}

// mailtoSubject makes a decoded mailto: subject safe to put in a header: no
// control characters (CR and LF would start a new header) and at most 998
// bytes, cut on a rune boundary.
func mailtoSubject(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > maxUnsubscribeSubjectBytes {
		s = s[:maxUnsubscribeSubjectBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return strings.TrimSpace(s)
}

// mailtoBody makes a decoded mailto: body plain text of bounded size: line
// breaks are normalised, other control characters dropped, and the text is cut
// at the quick reply limit.
func mailtoBody(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return truncateRunes(s, maxQuickReplyRunes)
}
