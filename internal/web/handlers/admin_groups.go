package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// AdminGroupsHandler is the admin UI for distribution groups. It sits behind
// the admin gate; every handler narrows to the caller's AdminScope itself.
type AdminGroupsHandler struct {
	groups   *store.GroupStore
	pebble   *store.PebbleStore
	metadata *store.MetadataManager
	logger   *slog.Logger
}

// NewAdminGroupsHandler creates an AdminGroupsHandler.
func NewAdminGroupsHandler(groups *store.GroupStore, pebble *store.PebbleStore, metadata *store.MetadataManager, logger *slog.Logger) *AdminGroupsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminGroupsHandler{groups: groups, pebble: pebble, metadata: metadata, logger: logger}
}

// List shows the groups of the domains the caller administers.
//
// GET /admin/groups
func (h *AdminGroupsHandler) List(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	scope := middleware.AdminScopeFromContext(r.Context())
	all, err := h.groups.ListGroups("")
	if err != nil {
		h.logger.Error("admin: listing groups failed", "error", err)
		h.fail(w, r, http.StatusInternalServerError, "admin.groupErrFailed")
		return
	}
	rows := make([]templates.GroupRow, 0, len(all))
	for _, g := range all {
		if !scope.HasDomain(g.Domain) {
			continue
		}
		rows = append(rows, templates.GroupRow{
			ID: g.Id, Address: g.Address, DisplayName: g.DisplayName,
			Members: len(g.MemberIds), Policy: g.PostingPolicy,
		})
	}
	templ.Handler(templates.AdminGroupList(rows, email, flashText(r), flashLevel(r))).ServeHTTP(w, r)
}

// NewForm shows the create form. Without a domain it first asks which of the
// caller's domains the group belongs to, because the member list is that
// domain's users.
//
// GET /admin/groups/new
func (h *AdminGroupsHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	scope := middleware.AdminScopeFromContext(r.Context())
	domain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain")))
	f := templates.GroupForm{Policy: store.GroupPolicyAnyone}
	if domain == "" {
		f.Domains = h.scopeDomains(scope)
		if len(f.Domains) == 1 {
			domain = f.Domains[0]
		}
	}
	if domain != "" {
		if !requireDomainAccess(w, scope, domain) {
			return
		}
		users, err := h.domainUsers(domain)
		if err != nil {
			h.logger.Error("admin: listing users for a group failed", "error", err)
			h.fail(w, r, http.StatusInternalServerError, "admin.groupErrFailed")
			return
		}
		f.Domain, f.Users = domain, users
	}
	templ.Handler(templates.AdminGroupForm(f, email)).ServeHTTP(w, r)
}

// Create makes a group. The address is the local part and the chosen domain;
// the domain must be inside the caller's scope.
//
// POST /admin/groups
func (h *AdminGroupsHandler) Create(w http.ResponseWriter, r *http.Request) {
	scope := middleware.AdminScopeFromContext(r.Context())
	domain := strings.ToLower(strings.TrimSpace(r.FormValue("domain")))
	if domain == "" || !requireDomainAccess(w, scope, domain) {
		if domain == "" {
			h.fail(w, r, http.StatusBadRequest, "admin.groupErrInvalid")
		}
		return
	}
	local := strings.TrimSpace(r.FormValue("local"))
	g := &bongov1.Group{
		Address:       store.NormaliseAddress(local + "@" + domain),
		DisplayName:   strings.TrimSpace(r.FormValue("display_name")),
		MemberIds:     formIDs(r, "member"),
		OwnerIds:      formIDs(r, "owner"),
		PostingPolicy: strings.TrimSpace(r.FormValue("posting_policy")),
	}
	form := templates.GroupForm{
		Domain: domain, LocalPart: local, DisplayName: g.DisplayName, Policy: g.PostingPolicy,
	}
	if status, msg := h.checkInput(g, domain); status != 0 {
		h.rerender(w, r, form, g, status, msg)
		return
	}
	if _, err := h.groups.CreateGroup(g); err != nil {
		status, msg := h.mapStoreError(err, "create", "domain", domain)
		h.rerender(w, r, form, g, status, msg)
		return
	}
	http.Redirect(w, r, withFlash("/admin/groups", flashGroupCreated), http.StatusSeeOther)
}

// EditForm shows one group. The stored group decides the scope check, never a
// request field.
//
// GET /admin/groups/{id}
func (h *AdminGroupsHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	g, ok := h.loadScoped(w, r)
	if !ok {
		return
	}
	users, err := h.domainUsers(g.Domain)
	if err != nil {
		h.logger.Error("admin: listing users for a group failed", "group", g.Id, "error", err)
		h.fail(w, r, http.StatusInternalServerError, "admin.groupErrFailed")
		return
	}
	markSelected(users, g.MemberIds, g.OwnerIds)
	f := templates.GroupForm{
		ID: g.Id, Domain: g.Domain, Address: g.Address, DisplayName: g.DisplayName,
		Policy: g.PostingPolicy, Users: users,
	}
	templ.Handler(templates.AdminGroupForm(f, email)).ServeHTTP(w, r)
}

// Update replaces the display name, members, owners and posting policy.
//
// POST /admin/groups/{id}
func (h *AdminGroupsHandler) Update(w http.ResponseWriter, r *http.Request) {
	stored, ok := h.loadScoped(w, r)
	if !ok {
		return
	}
	g := &bongov1.Group{
		Id:            stored.Id,
		DisplayName:   strings.TrimSpace(r.FormValue("display_name")),
		MemberIds:     formIDs(r, "member"),
		OwnerIds:      formIDs(r, "owner"),
		PostingPolicy: strings.TrimSpace(r.FormValue("posting_policy")),
	}
	form := templates.GroupForm{
		ID: stored.Id, Domain: stored.Domain, Address: stored.Address,
		DisplayName: g.DisplayName, Policy: g.PostingPolicy,
	}
	if status, msg := h.checkInput(g, stored.Domain); status != 0 {
		h.rerender(w, r, form, g, status, msg)
		return
	}
	if _, err := h.groups.UpdateGroup(g); err != nil {
		status, msg := h.mapStoreError(err, "update", "group", stored.Id)
		h.rerender(w, r, form, g, status, msg)
		return
	}
	http.Redirect(w, r, withFlash("/admin/groups", flashGroupUpdated), http.StatusSeeOther)
}

// Delete removes the group, its member index and its grants.
//
// POST /admin/groups/{id}/delete
func (h *AdminGroupsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	g, ok := h.loadScoped(w, r)
	if !ok {
		return
	}
	if err := h.groups.DeleteGroup(g.Id); err != nil {
		h.logger.Error("admin: deleting group failed", "group", g.Id, "error", err)
		h.fail(w, r, http.StatusInternalServerError, "admin.groupErrFailed")
		return
	}
	http.Redirect(w, r, withFlash("/admin/groups", flashGroupDeleted), http.StatusSeeOther)
}

// loadScoped loads the group named in the URL and checks the caller may
// administer its stored domain. Like requireUserAccess, a domain admin gets the
// same 403 for a group that is absent and one that is out of scope, so the
// answer is not an existence oracle; a global admin gets a 404.
func (h *AdminGroupsHandler) loadScoped(w http.ResponseWriter, r *http.Request) (*bongov1.Group, bool) {
	scope := middleware.AdminScopeFromContext(r.Context())
	g, err := h.groups.GetGroup(chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrGroupNotFound) {
			if scope.Global {
				http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), "admin.errGroupNotFound"), http.StatusNotFound)
			} else {
				http.Error(w, "Forbidden", http.StatusForbidden)
			}
			return nil, false
		}
		h.logger.Error("admin: reading group failed", "error", err)
		h.fail(w, r, http.StatusInternalServerError, "admin.groupErrFailed")
		return nil, false
	}
	if !requireDomainAccess(w, scope, g.Domain) {
		return nil, false
	}
	return g, true
}

// checkInput validates what the store does not: the posting policy and that
// every member and owner is a local, non-resource user of the group's own
// domain. A domain admin must not be able to pull another tenant's users into
// a group, so a user of any other domain is refused, as is an unknown one, with
// one message. It returns a status of 0 when the input is fine.
func (h *AdminGroupsHandler) checkInput(g *bongov1.Group, domain string) (int, string) {
	if g.PostingPolicy == "" {
		g.PostingPolicy = store.GroupPolicyAnyone
	}
	if !store.ValidGroupPolicy(g.PostingPolicy) {
		return http.StatusUnprocessableEntity, "admin.groupErrInvalid"
	}
	for _, id := range slices.Concat(g.MemberIds, g.OwnerIds) {
		p, err := h.profile(id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return http.StatusUnprocessableEntity, "admin.groupErrMember"
			}
			h.logger.Error("admin: reading a group member failed", "error", err)
			return http.StatusInternalServerError, "admin.groupErrFailed"
		}
		if p.Kind != "" || strings.ToLower(domainFromEmail(p.Email)) != domain {
			return http.StatusUnprocessableEntity, "admin.groupErrMember"
		}
	}
	return 0, ""
}

func (h *AdminGroupsHandler) profile(userID string) (*bongov1.UserProfile, error) {
	if userID == "" || strings.Contains(userID, "/") {
		return nil, store.ErrNotFound
	}
	data, err := h.pebble.Get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		return nil, err
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// mapStoreError turns a store refusal into a status and a message ID, and logs
// anything unexpected with the identifier only, never an address or a member.
func (h *AdminGroupsHandler) mapStoreError(err error, op, what, ident string) (int, string) {
	switch {
	case errors.Is(err, store.ErrGroupAddressTaken):
		return http.StatusConflict, "admin.groupErrTaken"
	case errors.Is(err, store.ErrGroupInvalid):
		return http.StatusUnprocessableEntity, "admin.groupErrInvalid"
	case errors.Is(err, store.ErrGroupNotFound):
		return http.StatusNotFound, "admin.groupErrInvalid"
	}
	h.logger.Error("admin: group "+op+" failed", what, ident, "error", err)
	return http.StatusInternalServerError, "admin.groupErrFailed"
}

// rerender shows the form again with the submitted values and a translated
// error, at the status the failure calls for.
func (h *AdminGroupsHandler) rerender(w http.ResponseWriter, r *http.Request, f templates.GroupForm, g *bongov1.Group, status int, msgID string) {
	_, email := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())
	f.Error = i18n.T(locale, msgID)
	if users, err := h.domainUsers(f.Domain); err == nil {
		markSelected(users, g.MemberIds, g.OwnerIds)
		f.Users = users
	}
	templ.Handler(templates.AdminGroupForm(f, email), templ.WithStatus(status)).ServeHTTP(w, r)
}

// fail writes a plain translated error.
func (h *AdminGroupsHandler) fail(w http.ResponseWriter, r *http.Request, status int, msgID string) {
	http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), msgID), status)
}

// scopeDomains lists the domains the caller may create groups in.
func (h *AdminGroupsHandler) scopeDomains(scope middleware.AdminScope) []string {
	var out []string
	if scope.Global {
		if h.metadata == nil || h.metadata.System == nil {
			return nil
		}
		rows, err := h.metadata.System.DB().Query("SELECT name FROM domains ORDER BY name")
		if err != nil {
			return nil
		}
		defer rows.Close()
		for rows.Next() {
			var d string
			if rows.Scan(&d) == nil {
				out = append(out, strings.ToLower(d))
			}
		}
		return out
	}
	for d := range scope.Domains {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}

// domainUsers lists the local, non-resource users whose address is on domain.
func (h *AdminGroupsHandler) domainUsers(domain string) ([]templates.GroupUserOption, error) {
	if h.metadata == nil || h.metadata.System == nil {
		return nil, errors.New("user directory unavailable")
	}
	rows, err := h.metadata.System.DB().Query("SELECT id, username FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []templates.GroupUserOption
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if strings.ToLower(domainFromEmail(name)) != domain {
			continue
		}
		if p, err := h.profile(id); err != nil || p.Kind != "" {
			continue
		}
		out = append(out, templates.GroupUserOption{ID: id, Email: name})
	}
	return out, rows.Err()
}

func markSelected(users []templates.GroupUserOption, members, owners []string) {
	for i := range users {
		users[i].Member = slices.Contains(members, users[i].ID)
		users[i].Owner = slices.Contains(owners, users[i].ID)
	}
}

// formIDs returns the distinct non-empty values of a repeated form field.
func formIDs(r *http.Request, name string) []string {
	if r.Form == nil {
		_ = r.ParseForm()
	}
	var out []string
	for _, v := range r.Form[name] {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
