package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// noFlash is the flash returned with a refusal, which is answered directly.
var noFlash flashKey

// errSharedMemberRefused is the one refusal for every address that cannot be a
// member of a shared inbox, so the form says nothing about which accounts and
// groups exist.
var errSharedMemberRefused = errors.New("that address cannot be a member")

// errSharedUnavailable means the stores that member resolution needs are not
// wired on this node.
var errSharedUnavailable = errors.New("shared inboxes are not available on this node")

// SetSharedInbox wires the grant and group stores that shared inbox members
// need. Without them members cannot be assigned and the routes answer 503.
func (h *AdminHandler) SetSharedInbox(acl *store.ACLStore, groups *store.GroupStore) {
	h.acl = acl
	h.groups = groups
}

// isSharedForm reports whether the create form asks for a shared inbox.
func isSharedForm(r *http.Request) bool { return r.FormValue("kind") == auth.SharedKind }

// resolveSharedMember maps an address typed by an admin to the typed principal
// of a member of the shared inbox sharedEmail, or errSharedMemberRefused. The
// address must be on the shared inbox's own domain (as group membership is, so
// a domain admin cannot reach into another domain, and a global admin follows
// the same rule). It must be a live person's primary address or a group on that
// domain; the shared account itself, another shared account, a resource, a
// disabled account and any other address are refused alike.
func (h *AdminHandler) resolveSharedMember(sharedEmail, address string) (string, error) {
	addr := store.NormaliseAddress(address)
	shared := store.NormaliseAddress(sharedEmail)
	if h.addresses == nil {
		return "", errSharedUnavailable
	}
	if addr == "" || addr == shared ||
		!strings.EqualFold(domainFromEmail(addr), domainFromEmail(shared)) {
		return "", errSharedMemberRefused
	}
	rec, ok, err := h.addresses.Get(addr)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errSharedMemberRefused
	}
	if rec.Kind == store.AddressKindGroup {
		gid, ok := store.ShareableGroup(h.groups, rec.GroupId, shared)
		if !ok {
			return "", errSharedMemberRefused
		}
		return store.GranteeGroup(gid), nil
	}
	if rec.Kind != "primary" || rec.UserId == "" {
		return "", errSharedMemberRefused
	}
	p, err := h.loadProfile(rec.UserId)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", errSharedMemberRefused
		}
		return "", err
	}
	if p.Kind != "" || p.Disabled || store.NormaliseAddress(p.Email) != addr {
		return "", errSharedMemberRefused
	}
	return store.GranteeUser(rec.UserId), nil
}

// syncSharedMembers makes the folder grants of the shared inbox match its
// profile. The default folders are ensured first so a retry after a failed
// create still finds them.
func (h *AdminHandler) syncSharedMembers(userID string, profile *bongov1.UserProfile) error {
	if h.acl == nil || h.mailboxes == nil {
		return errors.New("shared inboxes are not available on this node")
	}
	if err := h.mailboxes.EnsureDefaultMailboxes(userID); err != nil {
		return fmt.Errorf("ensuring folders: %w", err)
	}
	return store.SyncSharedInboxGrants(h.acl, h.mailboxes, userID, profile.SharedMembers, time.Now())
}

// parseSharedMembers splits the create form's member field into distinct
// addresses. At most store.MaxSharedMembers are accepted.
func parseSharedMembers(raw string) ([]string, bool) {
	var out []string
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) }) {
		a := store.NormaliseAddress(f)
		if a != "" && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out, len(out) <= store.MaxSharedMembers
}

// createShared creates a shared inbox from the admin form. The caller has
// composed and scope-checked newEmail and applied the domain user limit. Every
// member is resolved before anything is written, so a refused member creates
// nothing. Kind is in the first profile write; there is no password, no welcome
// mail and no admin flag.
func (h *AdminHandler) createShared(w http.ResponseWriter, r *http.Request, newEmail string) {
	if h.acl == nil || h.mailboxes == nil {
		h.sharedFail(w, r, http.StatusServiceUnavailable, "admin.sharedErrUnavailable")
		return
	}
	name := strings.TrimSpace(r.FormValue("display_name"))
	if name == "" || len([]rune(name)) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		h.sharedFail(w, r, http.StatusBadRequest, "admin.sharedErrName")
		return
	}
	addrs, ok := parseSharedMembers(r.FormValue("members"))
	if !ok {
		h.sharedFail(w, r, http.StatusUnprocessableEntity, "admin.sharedErrTooMany", store.MaxSharedMembers)
		return
	}
	var members []string
	for _, a := range addrs {
		p, err := h.resolveSharedMember(newEmail, a)
		if err != nil {
			h.memberError(w, r, err)
			return
		}
		if !slices.Contains(members, p) {
			members = append(members, p)
		}
	}

	userID, err := auth.CreateShared(h.applier, h.metadata, newEmail, name)
	if err != nil {
		if errors.Is(err, auth.ErrAccountExists) {
			h.sharedFail(w, r, http.StatusConflict, "admin.sharedErrExists")
			return
		}
		h.logger.Error("admin: creating a shared inbox failed", "email", newEmail, "error", err)
		h.sharedFail(w, r, http.StatusInternalServerError, "admin.sharedErrCreate")
		return
	}

	unlock := lockUser(userID)
	defer unlock()
	profile, err := h.loadProfile(userID)
	if err == nil {
		profile.SharedMembers = members
		err = h.saveProfile(userID, profile)
	}
	if err == nil {
		err = h.syncSharedMembers(userID, profile)
	}
	if err != nil {
		h.logger.Error("admin: shared inbox created but its members were not saved", "email", newEmail, "error", err)
		h.sharedFail(w, r, http.StatusInternalServerError, "admin.sharedErrMembersNotSaved")
		return
	}
	h.logger.Info("admin: shared inbox created", "email", newEmail, "members", len(members))
	http.Redirect(w, r, withFlash("/admin/users/"+url.PathEscape(userID), flashSharedCreated), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// memberError answers a failed member resolution: the one refusal for every
// address that cannot be a member, 500 for a store failure.
func (h *AdminHandler) memberError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errSharedMemberRefused):
		h.sharedFail(w, r, http.StatusUnprocessableEntity, "admin.sharedErrMember")
	case errors.Is(err, errSharedUnavailable):
		h.sharedFail(w, r, http.StatusServiceUnavailable, "admin.sharedErrUnavailable")
	default:
		h.logger.Error("admin: resolving a shared inbox member failed", "error", err)
		h.sharedFail(w, r, http.StatusInternalServerError, "admin.sharedErrLookup")
	}
}

// sharedFail answers with the translated message for the request locale.
func (h *AdminHandler) sharedFail(w http.ResponseWriter, r *http.Request, status int, msgID string, args ...any) {
	msg := i18n.T(middleware.LocaleFromContext(r.Context()), msgID)
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	http.Error(w, msg, status)
}

// NewSharedInboxForm shows the create form for a shared inbox, with the same
// domain scoping as the user form.
func (h *AdminHandler) NewSharedInboxForm(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	scope := middleware.AdminScopeFromContext(r.Context())
	domains := h.userFormDomains()
	if !scope.Global {
		filtered := domains[:0]
		for _, d := range domains {
			if scope.HasDomain(d.Name) {
				filtered = append(filtered, d)
			}
		}
		domains = filtered
	}
	templ.Handler(templates.AdminSharedForm(domains, email)).ServeHTTP(w, r)
}

// requireShared loads the target of a members request: the caller must be
// allowed to manage the account, and it must be a shared inbox.
func (h *AdminHandler) requireShared(w http.ResponseWriter, r *http.Request) (string, *bongov1.UserProfile, bool) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())
	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
		return "", nil, false
	}
	if profile.Kind != auth.SharedKind {
		h.sharedFail(w, r, http.StatusNotFound, "admin.sharedErrNotShared")
		return "", nil, false
	}
	if h.acl == nil || h.mailboxes == nil {
		h.sharedFail(w, r, http.StatusServiceUnavailable, "admin.sharedErrUnavailable")
		return "", nil, false
	}
	return userID, profile, true
}

// AddSharedMember adds a user or group, named by address, to a shared inbox.
//
// POST /admin/users/{userID}/members
func (h *AdminHandler) AddSharedMember(w http.ResponseWriter, r *http.Request) {
	h.changeSharedMembers(w, r, func(profile *bongov1.UserProfile) (flashKey, bool) {
		principal, err := h.resolveSharedMember(profile.Email, r.FormValue("address"))
		if err != nil {
			h.memberError(w, r, err)
			return noFlash, false
		}
		if slices.Contains(profile.SharedMembers, principal) {
			h.memberError(w, r, errSharedMemberRefused)
			return noFlash, false
		}
		if len(profile.SharedMembers) >= store.MaxSharedMembers {
			h.sharedFail(w, r, http.StatusUnprocessableEntity, "admin.sharedErrTooMany", store.MaxSharedMembers)
			return noFlash, false
		}
		profile.SharedMembers = append(profile.SharedMembers, principal)
		return flashSharedMemberAdded, true
	})
}

// RemoveSharedMember removes a member, named by its typed principal, from a
// shared inbox. The profile is written first, so sending as the address ends on
// the next request; the folder grants are revoked after, and a failure there is
// a 500 the admin retries (the sync is idempotent).
//
// POST /admin/users/{userID}/members/remove
func (h *AdminHandler) RemoveSharedMember(w http.ResponseWriter, r *http.Request) {
	h.changeSharedMembers(w, r, func(profile *bongov1.UserProfile) (flashKey, bool) {
		principal := r.FormValue("principal")
		i := slices.Index(profile.SharedMembers, principal)
		if i < 0 {
			h.sharedFail(w, r, http.StatusNotFound, "admin.sharedErrNotMember")
			return noFlash, false
		}
		profile.SharedMembers = slices.Delete(profile.SharedMembers, i, i+1)
		return flashSharedMemberRemoved, true
	})
}

// changeSharedMembers is the shared shell of the two member handlers: scope
// check, per-user lock around the read-modify-write, change, write, sync. The
// lock is held through the sync so two admins cannot interleave a stale list.
func (h *AdminHandler) changeSharedMembers(w http.ResponseWriter, r *http.Request, change func(*bongov1.UserProfile) (flashKey, bool)) {
	userID := chi.URLParam(r, "userID")
	defer lockUser(userID)()
	userID, profile, ok := h.requireShared(w, r)
	if !ok {
		return
	}
	flash, ok := change(profile)
	if !ok {
		return
	}
	if err := h.saveProfile(userID, profile); err != nil {
		h.logger.Error("admin: saving shared inbox members failed", "user_id", userID, "error", err)
		h.sharedFail(w, r, http.StatusInternalServerError, "admin.sharedErrSave")
		return
	}
	if err := h.syncSharedMembers(userID, profile); err != nil {
		h.logger.Error("admin: syncing shared inbox grants failed", "user_id", userID, "error", err)
		h.sharedFail(w, r, http.StatusInternalServerError, "admin.sharedErrSync")
		return
	}
	http.Redirect(w, r, withFlash("/admin/users/"+url.PathEscape(userID), flash), http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// sharedMemberInfos labels the members of a shared inbox for the detail page.
// A user or group that no longer exists is shown as removed, so the admin can
// clear the stale entry.
func (h *AdminHandler) sharedMemberInfos(profile *bongov1.UserProfile) []templates.AdminMemberInfo {
	out := make([]templates.AdminMemberInfo, 0, len(profile.SharedMembers))
	for _, m := range profile.SharedMembers {
		info := templates.AdminMemberInfo{Principal: m}
		if typ, id, ok := strings.Cut(m, ":"); ok {
			switch {
			case typ == "u":
				if p, err := h.loadProfile(id); err == nil {
					info.Label, info.Live = p.Email, true
				}
			case typ == "g" && h.groups != nil:
				if g, err := h.groups.GetGroup(id); err == nil {
					info.Label, info.Group, info.Live = g.Address, true, true
				}
			}
		}
		out = append(out, info)
	}
	return out
}
