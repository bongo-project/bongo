package handlers

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// The rights each preset stands for, in RFC 4314 canonical order. The form
// sends a preset name, never letters: the server decides what a preset means.
//
//	Read   lrs       look up, read, keep \Seen
//	Write  lrswite   Read plus flags, insert, delete messages and expunge
//	Full   lrswipkxte Write plus post and create or delete sub-folders
//
// "Can share" adds the administer right (a) to Full only.
const (
	folderRightsRead      = "lrs"
	folderRightsWrite     = "lrswite"
	folderRightsFull      = "lrswipkxte"
	folderRightsFullShare = "lrswipkxtea"
)

// folderPresetRights maps a preset name and the can-share box to the rights, or
// ok=false for an unknown preset or "can share" without Full.
func folderPresetRights(preset string, canShare bool) (string, bool) {
	switch preset {
	case templates.FolderPresetRead:
		return folderRightsRead, !canShare
	case templates.FolderPresetWrite:
		return folderRightsWrite, !canShare
	case templates.FolderPresetFull:
		if canShare {
			return folderRightsFullShare, true
		}
		return folderRightsFull, true
	}
	return "", false
}

// folderRightsPreset is the reverse, for display: a set IMAP SETACL chose that
// matches no preset is custom.
func folderRightsPreset(rights string) string {
	switch rights {
	case folderRightsRead:
		return templates.FolderPresetRead
	case folderRightsWrite:
		return templates.FolderPresetWrite
	case folderRightsFull:
		return templates.FolderPresetFull
	case folderRightsFullShare:
		return templates.FolderPresetFullShare
	}
	return templates.FolderPresetCustom
}

// folderShareRefused is the message id of the one answer for every share whose
// target does not resolve, so the reply does not say which addresses are
// accounts.
const folderShareRefused = "folders.errRefused"

// folderError answers a share or revoke form with translated text. The forms
// post through htmx into a data-form-status area, which shows the text of a
// 4xx or 5xx reply (bongo-pages.js, htmx:responseError).
func folderError(w http.ResponseWriter, r *http.Request, msgID string, status int) {
	http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), msgID), status)
}

// FolderShareHandler is the owner's side of folder sharing: who may see each of
// their folders, in which state, and the form to add or revoke a grantee.
type FolderShareHandler struct {
	acl        *store.ACLStore
	mailboxes  *store.MailboxStore
	shares     shareTargetResolver
	groups     groupLookup
	inviter    *ShareInviter
	limiter    shareRateLimiter
	namespaces store.IMAPNamespaces
	authservID string
	now        func() time.Time
}

// NewFolderShareHandler creates the handler. The limiter must be the calendar
// handler's (CalendarHandler.ShareLimiter), so the web calendar form, this form
// and IMAP SETACL draw on one budget and the account oracle is not multiplied.
func NewFolderShareHandler(acl *store.ACLStore, mailboxes *store.MailboxStore) *FolderShareHandler {
	return &FolderShareHandler{acl: acl, mailboxes: mailboxes, now: time.Now}
}

func (h *FolderShareHandler) SetShareResolver(r shareTargetResolver) { h.shares = r }
func (h *FolderShareHandler) SetGroups(g groupLookup)                { h.groups = g }
func (h *FolderShareHandler) SetShareInviter(s *ShareInviter)        { h.inviter = s }
func (h *FolderShareHandler) SetShareLimiter(l shareRateLimiter)     { h.limiter = l }
func (h *FolderShareHandler) SetAuthservID(host string)              { h.authservID = host }
func (h *FolderShareHandler) SetNamespacePrefixes(otherUsers, shared string) {
	h.namespaces = store.IMAPNamespaces{OtherUsers: otherUsers, Shared: shared}
}

// ShowFolders lists the user's own folders with the grants on each.
//
// GET /settings/folders
func (h *FolderShareHandler) ShowFolders(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	if h.acl == nil || h.mailboxes == nil {
		http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), "shares.errUnavailable"), http.StatusServiceUnavailable)
		return
	}
	mbs, err := h.mailboxes.ListMailboxes(userID)
	if err != nil {
		http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), "shares.errLoadFolders"), http.StatusInternalServerError)
		return
	}
	ns := h.namespaces.OrDefaults()
	now := h.now()
	var folders []templates.FolderShareFolder
	for _, mb := range mbs {
		if ns.Reserves(mb.Name) {
			continue
		}
		f := templates.FolderShareFolder{Name: mb.Name}
		// A folder with no id has never been shared, so it has no grants.
		if mb.Id != "" {
			grants, err := h.acl.ListResourceGrants(store.ACLKindMailbox, userID, mb.Id)
			if err != nil {
				http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), "shares.errLoadGrants"), http.StatusInternalServerError)
				return
			}
			for _, g := range grants {
				f.Grants = append(f.Grants, h.grantRow(g, now))
			}
			slices.SortFunc(f.Grants, func(a, b templates.FolderShareGrant) int { return strings.Compare(a.Label, b.Label) })
		}
		folders = append(folders, f)
	}
	slices.SortFunc(folders, func(a, b templates.FolderShareFolder) int { return strings.Compare(a.Name, b.Name) })
	data := templates.FolderSharePageData{Folders: folders, Email: email, Flash: flashText(r)}
	templ.Handler(templates.FolderSharePage(data)).ServeHTTP(w, r)
}

// grantRow turns one stored grant into a display row.
func (h *FolderShareHandler) grantRow(g store.ResourceGrant, now time.Time) templates.FolderShareGrant {
	d := grantDisplayRow(h.acl, h.groups, g, now)
	return templates.FolderShareGrant{
		Grantee: d.Grantee, Label: d.Label, State: d.State,
		Rights: g.Grant.Rights, Preset: folderRightsPreset(g.Grant.Rights),
	}
}

// grantDisplay is what the owner's sharing pages show of one grant, whatever
// the kind of resource: who it names, in which state.
type grantDisplay struct {
	// Grantee is what the revoke form sends: a user id, or "g:" and a group id.
	Grantee string
	// Label is the grantee's address, or the group's address.
	Label string
	// State is "pending", "accepted" or "expired".
	State string
}

// grantDisplayRow turns one stored grant into a display row. A grantee whose
// account or group is gone still shows, by id, so the owner can revoke it. The
// folder and address-book sharing pages both use it, so labels and states
// cannot drift apart.
func grantDisplayRow(acl *store.ACLStore, groups groupLookup, g store.ResourceGrant, now time.Time) grantDisplay {
	row := grantDisplay{Grantee: g.Principal}
	if gid, isGroup := strings.CutPrefix(g.Principal, "g:"); isGroup {
		row.Label = gid
		if groups != nil {
			if grp, err := groups.GetGroup(gid); err == nil && grp != nil && grp.Address != "" {
				row.Label = grp.Address
			}
		}
	} else {
		id := strings.TrimPrefix(g.Principal, "u:")
		row.Grantee = id
		row.Label = acl.UserEmail(id)
		if row.Label == "" {
			row.Label = id
		}
	}
	switch {
	case g.Grant.State == store.GrantStateAccepted:
		row.State = "accepted"
	case store.PendingGrantExpired(g.Grant, now.Unix()):
		row.State = "expired"
	default:
		row.State = "pending"
	}
	return row
}

// ShareFolder shares an own folder with a user or a group.
//
// POST /settings/folders/share
//
// The order mirrors ShareCalendar, and each step is where it is so the reply
// is no oracle for which addresses are accounts: the rate limit is charged
// first, the cap is read and the preset validated before the address resolves,
// every negative resolution is one fixed refusal, and the folder gets its id
// only once the target is known good. The id, the grant and the invitation are
// separate commands and not atomic, as for IMAP SETACL; the cap check is
// read-then-write, so concurrent shares can overshoot it.
func (h *FolderShareHandler) ShareFolder(w http.ResponseWriter, r *http.Request) {
	userID, ownerEmail := middleware.UserFromContext(r.Context())
	if h.acl == nil || h.mailboxes == nil || h.shares == nil {
		folderError(w, r, "folders.errUnavailable", http.StatusServiceUnavailable)
		return
	}
	if h.limiter != nil && !h.limiter.Allow(userID) {
		secs := max(int(math.Ceil(h.limiter.RetryAfter(userID).Seconds())), 1)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		folderError(w, r, "folders.errTooMany", http.StatusTooManyRequests)
		return
	}
	name := r.FormValue("folder")
	if name == "" || h.namespaces.OrDefaults().Reserves(name) {
		folderError(w, r, "folders.errNotFound", http.StatusNotFound)
		return
	}
	meta, err := h.mailboxes.GetMailbox(userID, name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		folderError(w, r, "folders.errNotFound", http.StatusNotFound)
		return
	case err != nil:
		folderError(w, r, "folders.errShareFailed", http.StatusInternalServerError)
		return
	}
	shareWith := strings.TrimSpace(r.FormValue("share_with"))
	if shareWith == "" {
		folderError(w, r, "folders.errShareWithRequired", http.StatusBadRequest)
		return
	}
	// The preset is validated before the address resolves.
	rights, ok := folderPresetRights(strings.TrimSpace(r.FormValue("preset")), r.FormValue("can_share") != "")
	if !ok {
		folderError(w, r, "folders.errInvalidPreset", http.StatusBadRequest)
		return
	}
	now := h.now()
	full := false
	if meta.Id != "" {
		if full, err = h.acl.GrantCapReached(store.ACLKindMailbox, userID, meta.Id, now); err != nil {
			folderError(w, r, "folders.errShareFailed", http.StatusInternalServerError)
			return
		}
	}
	parsed, err := mail.ParseAddress(shareWith)
	if err != nil || parsed.Name != "" {
		folderError(w, r, folderShareRefused, http.StatusBadRequest)
		return
	}
	targetID, err := h.shares.ResolveAccount(parsed.Address)
	groupID := ""
	var groupErr *store.GroupRecipientError
	if errors.As(err, &groupErr) {
		if gid, ok := shareableGroupOf(h.groups, groupErr.GroupID, ownerEmail); ok {
			groupID, err = gid, nil
		}
	}
	if err != nil || (groupID == "" && targetID == userID) {
		folderError(w, r, folderShareRefused, http.StatusBadRequest)
		return
	}
	id, err := h.mailboxes.EnsureMailboxID(userID, name)
	if err != nil {
		slog.Error("web: folder share: assigning folder id failed", "user", userID, "error", err)
		folderError(w, r, "folders.errShareFailed", http.StatusInternalServerError)
		return
	}
	offer := GrantOffer{
		Kind: store.ACLKindMailbox, Owner: userID, OwnerEmail: ownerEmail,
		Resource: id, ResourceName: name, GranteeID: targetID, Recipient: parsed.Address,
		Rights: rights, Host: shareLinkHost(h.authservID, r), Full: full, Now: now,
	}
	if groupID != "" {
		offer.GroupID = groupID
		_, err = offerGroupGrant(h.acl, offer)
	} else {
		_, err = offerGrant(h.acl, h.inviter, offer)
	}
	switch {
	case errors.Is(err, errShareRefused):
		folderError(w, r, folderShareRefused, http.StatusBadRequest)
		return
	case err != nil:
		slog.Error("web: folder share failed", "user", userID, "error", err)
		folderError(w, r, "folders.errShareFailed", http.StatusInternalServerError)
		return
	}
	redirectAfterAction(w, r, withFlash("/settings/folders", flashFolderShared))
}

// UnshareFolder revokes one grantee's grant on an own folder. share_with is a
// user id or "g:" and a group id. A grant that is not there writes nothing and
// goes back to the page. It is not rate limited: removal reveals nothing about accounts.
//
// POST /settings/folders/unshare
func (h *FolderShareHandler) UnshareFolder(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if h.acl == nil || h.mailboxes == nil {
		folderError(w, r, "folders.errUnavailable", http.StatusServiceUnavailable)
		return
	}
	name := r.FormValue("folder")
	meta, err := h.mailboxes.GetMailbox(userID, name)
	switch {
	case name == "" || errors.Is(err, store.ErrNotFound):
		folderError(w, r, "folders.errNotFound", http.StatusNotFound)
		return
	case err != nil:
		folderError(w, r, "folders.errUpdateFailed", http.StatusInternalServerError)
		return
	}
	entry := r.FormValue("share_with")
	if entry == "" {
		folderError(w, r, "folders.errShareWithRequired", http.StatusBadRequest)
		return
	}
	if meta.Id == "" {
		redirectAfterAction(w, r, "/settings/folders")
		return
	}
	typed := store.GranteeUser(entry)
	if gid, isGroup := strings.CutPrefix(entry, "g:"); isGroup {
		typed = store.GranteeGroup(gid)
	}
	existing, err := h.acl.GetGrant(store.ACLKindMailbox, userID, meta.Id, typed)
	if err != nil {
		folderError(w, r, "folders.errUpdateFailed", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		redirectAfterAction(w, r, "/settings/folders")
		return
	}
	if err := h.acl.RevokeGrant(store.ACLKindMailbox, userID, meta.Id, typed); err != nil {
		slog.Error("web: folder unshare failed", "user", userID, "error", err)
		folderError(w, r, "folders.errUpdateFailed", http.StatusInternalServerError)
		return
	}
	redirectAfterAction(w, r, withFlash("/settings/folders", flashFolderUnshared))
}
