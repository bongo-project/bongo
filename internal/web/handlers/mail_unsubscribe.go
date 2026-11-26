package handlers

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/unsubscribe"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// One-click unsubscribe (RFC 8058) and the other unsubscribe methods.
//
// Nothing is stored and no request carries a URI: every route names the
// message by mailbox and uid, resolves it in the session user's own keyspace
// and reads the headers from the stored blob. The server fetches an address
// only for a one-click offer whose DKIM signature covers both headers, and only
// through the safe client; any other https URI is a link the user opens
// themselves.

// UnsubscribesPerHour is the per-user budget of one-click POSTs. The limiter is
// in memory and per node, so behind a load balancer a user gets this many per
// node.
const UnsubscribesPerHour = 20

// defaultSpamTag is the tag threshold used when none is wired, as the deliver
// stage's default.
const defaultSpamTag = 0.5

// unsubscribePoster sends the one-click POST. netx.Client implements it.
type unsubscribePoster interface {
	PostOneClick(ctx context.Context, u *url.URL) error
}

// oneClickVerifier reports whether the stored message carries a valid DKIM
// signature covering List-Unsubscribe and List-Unsubscribe-Post.
type oneClickVerifier func(ctx context.Context, raw []byte) bool

// SetUnsubscribe wires the one-click POST: the poster, the DKIM check, and the
// per-user limiter. Without a poster no one-click button is offered.
func (h *MailHandler) SetUnsubscribe(p unsubscribePoster, verify oneClickVerifier, limiter shareRateLimiter) {
	h.unsubPoster = p
	h.unsubVerify = verify
	h.unsubLimiter = limiter
}

// SetSpamThreshold sets the source of the antispam tag threshold, used to
// refuse unsubscribing from a message the antispam stage tagged as spam.
func (h *MailHandler) SetSpamThreshold(fn func() float64) { h.spamTag = fn }

// unsubscribeBlocked reports whether a message must not be unsubscribed from:
// it lives in Junk or below it, or its stored spam score reached the tag
// threshold. The score is used rather than the stamped X-Spam-Status header
// because "not spam" resets the score and leaves the header.
func unsubscribeBlocked(mailbox string, meta *bongov1.MailMeta, tag func() float64) bool {
	if store.InOrBelowJunk(mailbox) {
		return true
	}
	threshold := defaultSpamTag
	if tag != nil {
		if v := tag(); v > 0 {
			threshold = v
		}
	}
	return float64(meta.SpamScore) >= threshold
}

// unsubscribeHeaders reads the two headers from a parsed message.
func unsubscribeHeaders(env *enmime.Envelope) unsubscribe.Options {
	if env == nil || env.Root == nil {
		return unsubscribe.Options{}
	}
	return unsubscribe.Parse(env.Root.Header.Values("List-Unsubscribe"), env.Root.Header.Values("List-Unsubscribe-Post"))
}

// unsubscribeViewOf is the view model for a message's options, or nil when the
// message offers nothing. A blocked message with an offer shows only the spam
// hint. oneClick says whether the server can send a POST at all.
func unsubscribeViewOf(opts unsubscribe.Options, blocked, oneClickAvailable bool) *templates.UnsubscribeView {
	if !opts.Any() {
		return nil
	}
	if blocked {
		return &templates.UnsubscribeView{SpamBlocked: true}
	}
	v := &templates.UnsubscribeView{Mailto: opts.Mailto != ""}
	if opts.HTTPS != nil {
		v.Host = opts.HTTPS.Hostname()
		v.LinkURL = opts.HTTPS.String()
		v.OneClick = opts.OneClick && oneClickAvailable
	}
	return v
}

// unsubscribeView builds the view of a message the session user owns.
func (h *MailHandler) unsubscribeView(env *enmime.Envelope, meta *bongov1.MailMeta, realMailbox string) *templates.UnsubscribeView {
	return unsubscribeViewOf(unsubscribeHeaders(env), unsubscribeBlocked(realMailbox, meta, h.spamTag), h.unsubPoster != nil)
}

// Unsubscribe sends the one-click unsubscribe POST for a message.
//
// POST /mail/{mailbox}/{uid}/unsubscribe
//
// The order is the defence: the per-user budget is charged first, before any
// blob read, DKIM lookup or network call; then the message is resolved in the
// session user's own keyspace; then the Junk and spam gate; then the blob is
// read again and the headers re-parsed (a form field, if any, is ignored); the
// DKIM signature is checked now, not when the page was drawn. A message that
// fails the signature check gets the link panel and nothing is fetched. Logs
// carry the user id, message guid and URI host, never the URI.
func (h *MailHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())

	if h.unsubLimiter != nil && !h.unsubLimiter.Allow(userID) {
		secs := max(int(math.Ceil(h.unsubLimiter.RetryAfter(userID).Seconds())), 1)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		http.Error(w, i18n.T(locale, "mail.unsubscribeRateLimited"), http.StatusTooManyRequests)
		return
	}
	notAvailable := func() {
		http.Error(w, i18n.T(locale, "mail.unsubscribeNotAvailable"), http.StatusNotFound)
	}

	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, err := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	if err != nil || h.unsubPoster == nil || h.unsubVerify == nil || h.blobs == nil {
		notAvailable()
		return
	}
	msg, realMailbox, err := h.resolveMessage(userID, mailbox, uint32(uid))
	if err != nil {
		notAvailable()
		return
	}
	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil || meta.RawMessage == nil || meta.RawMessage.Hash == "" {
		notAvailable()
		return
	}
	if unsubscribeBlocked(realMailbox, meta, h.spamTag) {
		http.Error(w, i18n.T(locale, "mail.unsubscribeSpamHint"), http.StatusForbidden)
		return
	}

	raw, err := store.RawMessageBytes(h.blobs, meta)
	if err != nil {
		h.logger.Warn("web: unsubscribe could not read message", "user", userID, "guid", meta.Guid, "error", err)
		notAvailable()
		return
	}
	env, _, err := mimesafe.ReadEnvelope(raw)
	if err != nil {
		notAvailable()
		return
	}
	opts := unsubscribeHeaders(env)
	if opts.HTTPS == nil || !opts.OneClick {
		notAvailable()
		return
	}
	u := opts.HTTPS
	host := u.Hostname()

	// Other methods the message offers, shown when this one cannot be used.
	mailtoHref := ""
	if opts.Mailto != "" {
		mailtoHref = templates.UnsubscribeMailtoURL(mailbox, uint32(uid))
	}

	if !h.unsubVerify(r.Context(), raw) {
		h.logger.Info("web: unsubscribe offer not signed, link shown instead", "user", userID, "guid", meta.Guid, "host", host)
		templ.Handler(templates.UnsubscribeResult(templates.UnsubscribeLink, host, u.String(), mailtoHref)).ServeHTTP(w, r)
		return
	}

	if err := h.unsubPoster.PostOneClick(r.Context(), u); err != nil {
		// The error names the host only; the URI and the remote reply stay out.
		h.logger.Warn("web: one-click unsubscribe failed", "user", userID, "guid", meta.Guid, "host", host, "error", err)
		templ.Handler(templates.UnsubscribeResult(templates.UnsubscribeFailure, host, u.String(), mailtoHref)).ServeHTTP(w, r)
		return
	}
	h.logger.Info("web: one-click unsubscribe sent", "user", userID, "guid", meta.Guid, "host", host)
	templ.Handler(templates.UnsubscribeResult(templates.UnsubscribeSuccess, host, "", "")).ServeHTTP(w, r)
}

// ListUnsubscribe shows the unsubscribe control of a mailing list, taken from
// the list's newest message in the folders the list views show (Junk, Trash,
// Sent and Drafts are left out). GET /mail/view/lists/{listID}/unsubscribe
func (h *MailHandler) ListUnsubscribe(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	view, ok := listViewFromRequest(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	msgs, names, err := h.headerViewMessages(userID, view, false)
	if err != nil {
		h.logger.Warn("web: listing mailing list for unsubscribe", "user", userID, "error", err)
		http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), "mail.errLoadMessages"), http.StatusInternalServerError)
		return
	}
	if len(msgs) == 0 {
		http.NotFound(w, r)
		return
	}
	list := templates.MailingList{ID: view.ListID}
	if lists, err := h.mailingLists(userID); err == nil {
		for _, l := range lists {
			if l.ID == view.ListID {
				list = l
			}
		}
	}

	newest, mailbox := msgs[0], names[0]
	var uv *templates.UnsubscribeView
	if meta := h.loadMailMeta(userID, newest.MailGuid); meta != nil && meta.RawMessage != nil && meta.RawMessage.Hash != "" && h.blobs != nil {
		if raw, err := store.RawMessageBytes(h.blobs, meta); err == nil {
			if env, _, err := mimesafe.ReadEnvelope(raw); err == nil {
				uv = h.unsubscribeView(env, meta, mailbox)
			}
		}
	}

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ListUnsubscribeContent(list, mailbox, newest.Uid, uv)).ServeHTTP(w, r)
		return
	}
	mailboxInfos := h.loadMailboxInfos(userID, listsOverviewPath)
	sidebarTags := h.loadSidebarTags(userID, listsOverviewPath)
	templ.Handler(templates.ListUnsubscribePage(mailboxInfos, sidebarTags, list, mailbox, newest.Uid, uv, email)).ServeHTTP(w, r)
}

// listsOverviewPath is the Mailing lists overview URL, the sidebar's selection.
var listsOverviewPath = listsOverview.URL()
