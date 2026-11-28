package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/attpolicy"
	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// maxAttachmentFormBytes bounds the policy form body: at most 100 extensions
// and 100 MIME types of a few dozen bytes each.
const maxAttachmentFormBytes = 64 << 10

// AdminAttachmentsHandler serves the per-domain attachment policy page.
type AdminAttachmentsHandler struct {
	policies *store.AttachmentPolicyStore
	pebble   *store.PebbleStore
	logger   *slog.Logger
}

// NewAdminAttachmentsHandler creates the handler.
func NewAdminAttachmentsHandler(policies *store.AttachmentPolicyStore, pebble *store.PebbleStore, logger *slog.Logger) *AdminAttachmentsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminAttachmentsHandler{policies: policies, pebble: pebble, logger: logger}
}

// Show renders the policy of one domain.
//
// GET /admin/domains/{domain}/attachments
func (h *AdminAttachmentsHandler) Show(w http.ResponseWriter, r *http.Request) {
	domain, ok := h.scoped(w, r)
	if !ok {
		return
	}
	_, email := middleware.UserFromContext(r.Context())
	pol, err := h.policies.Get(domain)
	if err != nil {
		h.logger.Error("admin: reading attachment policy failed", "domain", domain, "error", err)
		h.fail(w, r, http.StatusInternalServerError, "admin.attachments.errFailed")
		return
	}
	f := h.formFor(domain, pol)
	if r.URL.Query().Get("defaults") == "1" {
		f.Extensions = strings.Join(attpolicy.DefaultExtensions, "\n")
		f.MimeTypes = strings.Join(attpolicy.DefaultMimeTypes, "\n")
	}
	f.Saved = r.URL.Query().Get("saved") == "1"
	templ.Handler(templates.AdminAttachmentPolicy(f, email)).ServeHTTP(w, r)
}

// Save validates and stores the policy.
//
// POST /admin/domains/{domain}/attachments
func (h *AdminAttachmentsHandler) Save(w http.ResponseWriter, r *http.Request) {
	domain, ok := h.scoped(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentFormBytes)
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, http.StatusBadRequest, "admin.attachments.errFailed")
		return
	}
	userID, email := middleware.UserFromContext(r.Context())
	p := &bongov1.AttachmentPolicy{
		Enabled:           r.FormValue("enabled") == "1",
		BlockedExtensions: splitList(r.FormValue("extensions")),
		BlockedMimeTypes:  splitList(r.FormValue("mime_types")),
		Action:            strings.TrimSpace(r.FormValue("action")),
		Language:          strings.TrimSpace(r.FormValue("language")),
	}
	if err := h.policies.Put(domain, p, userID); err != nil {
		var fe *store.AttachmentPolicyFieldError
		if errors.As(err, &fe) {
			f := h.formFor(domain, p)
			f.Extensions = r.FormValue("extensions")
			f.MimeTypes = r.FormValue("mime_types")
			f.Action = p.Action
			f.ErrField = fe.Field
			f.Error = i18n.T(middleware.LocaleFromContext(r.Context()), errMsgID(fe.Field))
			templ.Handler(templates.AdminAttachmentPolicy(f, email), templ.WithStatus(http.StatusUnprocessableEntity)).ServeHTTP(w, r)
			return
		}
		h.logger.Error("admin: storing attachment policy failed", "domain", domain, "admin", userID, "error", err)
		h.fail(w, r, http.StatusInternalServerError, "admin.attachments.errFailed")
		return
	}
	h.logger.Info("attachment-policy: policy saved", "admin", userID, "domain", domain, "action", p.Action,
		"enabled", p.Enabled, "extensions", len(p.BlockedExtensions), "mime_types", len(p.BlockedMimeTypes))
	http.Redirect(w, r, "/admin/domains/"+domain+"/attachments?saved=1", http.StatusSeeOther) // #nosec G710 -- domain is a hosted domain name that passed the scope check
}

// scoped resolves the domain from the path and applies the scope rules: a
// domain administrator gets one 403 for an out-of-scope and a missing domain;
// a global administrator gets 404 for a missing domain and 400 for an alias
// domain, whose target's policy applies.
func (h *AdminAttachmentsHandler) scoped(w http.ResponseWriter, r *http.Request) (string, bool) {
	domain := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "domain")))
	scope := middleware.AdminScopeFromContext(r.Context())
	forbidden := func() (string, bool) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return "", false
	}
	if domain == "" || strings.Contains(domain, "/") || !scope.HasDomain(domain) {
		return forbidden()
	}
	hosted, err := store.DomainHosted(h.pebble, domain)
	if err != nil {
		h.logger.Error("admin: reading domain failed", "domain", domain, "error", err)
		h.fail(w, r, http.StatusInternalServerError, "admin.attachments.errFailed")
		return "", false
	}
	if !hosted {
		if !scope.Global {
			return forbidden()
		}
		http.Error(w, "domain not found", http.StatusNotFound)
		return "", false
	}
	target, isAlias, err := store.DomainAliasOf(h.pebble, domain)
	if err != nil {
		h.logger.Error("admin: reading alias domain failed", "domain", domain, "error", err)
		h.fail(w, r, http.StatusInternalServerError, "admin.attachments.errFailed")
		return "", false
	}
	if isAlias {
		msg := i18n.T(middleware.LocaleFromContext(r.Context()), "admin.attachments.aliasDomain")
		http.Error(w, msg+" "+target, http.StatusBadRequest)
		return "", false
	}
	return domain, true
}

func (h *AdminAttachmentsHandler) formFor(domain string, pol *bongov1.AttachmentPolicy) templates.AttachmentPolicyForm {
	f := templates.AttachmentPolicyForm{Domain: domain, Action: store.AttachmentActionStrip}
	for _, l := range i18n.Locales() {
		f.Languages = append(f.Languages, templates.LocaleOption{Code: l.Code, Name: l.Name})
	}
	if pol == nil {
		f.NoPolicy = true
		f.Extensions = strings.Join(attpolicy.DefaultExtensions, "\n")
		f.MimeTypes = strings.Join(attpolicy.DefaultMimeTypes, "\n")
		return f
	}
	f.Enabled = pol.Enabled
	f.Extensions = strings.Join(pol.BlockedExtensions, "\n")
	f.MimeTypes = strings.Join(pol.BlockedMimeTypes, "\n")
	f.Action = pol.Action
	f.Language = pol.Language
	return f
}

func (h *AdminAttachmentsHandler) fail(w http.ResponseWriter, r *http.Request, status int, msgID string) {
	http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), msgID), status)
}

func errMsgID(field string) string {
	switch field {
	case "extensions":
		return "admin.attachments.errExtensions"
	case "mime_types":
		return "admin.attachments.errMimeTypes"
	case "language":
		return "admin.attachments.errLanguage"
	default:
		return "admin.attachments.errAction"
	}
}

// splitList splits a textarea or comma separated list into its entries.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t'
	})
}
