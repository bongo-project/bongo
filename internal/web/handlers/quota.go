package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// QuotaChecker is the domain-quota dependency shared by the web write paths.
// store.DomainQuotaStore satisfies it, the same computation IMAP APPEND, SMTP
// RCPT and CalDAV/CardDAV PUT use.
//
// RecipientWouldExceedQuota(userID, 0) means "is the user's domain already at
// or over quota"; a positive addBytes also refuses a write that would not fit.
// Every caller passes the quantity the usage recompute counts for what it
// stores (mail: MailMeta.RawMessage.Size).
//
// The check is best effort, not a hard limit. Usage is recomputed by a
// leader-only job roughly every ten minutes, so a burst of writes between
// recomputes, or two concurrent requests, can overshoot. A nil checker means
// unlimited, and a checker error fails open (logged), matching the other
// protocols: a Pebble hiccup must never block a user's own writes.
//
// Only the domain quota is enforced. UserProfile.QuotaBytes is not.
type QuotaChecker interface {
	RecipientWouldExceedQuota(userID string, addBytes int64) (bool, error)
}

// quotaExceeded reports whether a write of addBytes for userID must be refused.
func quotaExceeded(qc QuotaChecker, logger *slog.Logger, userID string, addBytes int64) bool {
	if qc == nil {
		return false
	}
	over, err := qc.RecipientWouldExceedQuota(userID, addBytes)
	if err != nil {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("web: quota check failed, allowing write", "user", userID, "error", err)
		return false
	}
	return over
}

// quotaText returns the translated "quota full" message for the request locale.
func quotaText(r *http.Request) string {
	return i18n.T(middleware.LocaleFromContext(r.Context()), "quota.exceeded")
}

// quotaRecipientText returns the translated "recipient mailbox full" message.
func quotaRecipientText(r *http.Request, address string) string {
	return fmt.Sprintf(i18n.T(middleware.LocaleFromContext(r.Context()), "quota.recipientExceeded"), address)
}

// refuseQuota answers a quota refusal with 507 Insufficient Storage.
func refuseQuota(w http.ResponseWriter, r *http.Request) {
	refuseQuotaText(w, r, quotaText(r))
}

// refuseQuotaText answers a quota refusal with msg. An htmx request gets the
// short text its swap target shows; a full-page post gets the message inside
// the app layout.
func refuseQuotaText(w http.ResponseWriter, r *http.Request, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		http.Error(w, msg, http.StatusInsufficientStorage)
		return
	}
	email, _ := middleware.UserFromContext(r.Context())
	templ.Handler(templates.QuotaRefusedPage(msg, email), templ.WithStatus(http.StatusInsufficientStorage)).ServeHTTP(w, r)
}

// refuseQuotaFlash answers a quota refusal on a flash-redirect route. to is a
// fixed path or one already constrained by safeContactReturnPath.
func refuseQuotaFlash(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to+"?flash="+url.QueryEscape(quotaText(r)), http.StatusSeeOther) // #nosec G710 -- to is a fixed same-origin path
}
