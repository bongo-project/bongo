package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// The shares page lists the invitations of every kind (calendar, folder,
// address book) the user holds, and what they have already joined. The
// handlers stay on CalendarHandler: the page borrows its sidebar and clock.

// sharesNotFound is the one answer for every request that names a grant the
// session user does not hold in the required state: unknown kind, malformed
// ids, no grant, someone else's grant, wrong state and expired. Nothing in the
// reply tells them apart.
const sharesNotFound = "invitation not found"

// SetMailboxes wires the mailbox store that names folders on the shares page.
func (h *CalendarHandler) SetMailboxes(ms *store.MailboxStore) { h.mailboxes = ms }

// SetAddressBooks wires the address book store that names books on the shares
// page.
func (h *CalendarHandler) SetAddressBooks(bs *store.AddressBookStore) { h.addressBooks = bs }

// SharesRedirect sends links in invitation mails sent before the page moved to
// the general shares page. The target is fixed, so it cannot be an open
// redirect; 302 keeps browsers from caching it.
//
// GET /calendar/shares
func SharesRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/shares", http.StatusFound)
}

// ShareInvitations renders the pending invitations of the user, of every kind,
// and the shares they have joined.
//
// GET /shares
func (h *CalendarHandler) ShareInvitations(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	var rows, joined []templates.PendingShare
	if h.acl != nil {
		now := h.clock()
		held, err := h.acl.ListGrantsHeld(userID, store.GrantStatePending, now)
		if err != nil {
			http.Error(w, "failed to load invitations", http.StatusInternalServerError)
			return
		}
		rows = h.shareRows(held)
		accepted, err := h.acl.ListGrantsHeld(userID, store.GrantStateAccepted, now)
		if err != nil {
			http.Error(w, "failed to load shares", http.StatusInternalServerError)
			return
		}
		joined = h.shareRows(accepted)
	}
	calendars, err := h.calendars.List(userID)
	if err != nil {
		http.Error(w, "failed to load calendars", http.StatusInternalServerError)
		return
	}
	r = clockRequest(r, h.nowIn(userID))
	r = h.sharedSidebar(r, userID)
	templ.Handler(templates.SharesPage(rows, joined, calendars, email)).ServeHTTP(w, r)
}

// clock returns the handler's time source reading, the wall clock by default.
func (h *CalendarHandler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// shareRows turns held grants into page rows, leaving out resources that can
// no longer be read.
func (h *CalendarHandler) shareRows(held []store.HeldShare) []templates.PendingShare {
	var rows []templates.PendingShare
	for _, g := range held {
		var name string
		switch g.Kind {
		case store.ACLKindCalendar:
			cal, err := h.calendars.Get(g.Owner, g.Resource)
			if err != nil {
				continue
			}
			name = cal.Name
		case store.ACLKindMailbox:
			if h.mailboxes == nil {
				continue
			}
			n, err := h.mailboxes.MailboxNameByID(g.Owner, g.Resource)
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					slog.Warn("calendar-share: resolving folder name failed", "owner", g.Owner, "error", err)
				}
				continue
			}
			name = n
		case store.ACLKindAddressBook:
			// Without the store the row stays, labelled by its id, so the page
			// and the pending count agree.
			if h.addressBooks != nil {
				b, err := h.addressBooks.Get(g.Owner, g.Resource)
				if err != nil {
					if !errors.Is(err, store.ErrAddressBookNotFound) {
						slog.Warn("calendar-share: resolving address book name failed", "owner", g.Owner, "error", err)
					}
					continue
				}
				name = b.Name
			}
		}
		label := h.acl.UserEmail(g.Owner)
		if label == "" {
			label = g.Owner
		}
		row := templates.PendingShare{
			Kind: g.Kind, Owner: g.Owner, OwnerLabel: label, Resource: g.Resource, ResourceName: name,
			Level: g.Level, Rights: g.Rights,
		}
		if !g.Direct && len(g.ViaGroups) > 0 {
			row.Via = h.groupLabels(g.ViaGroups)
		}
		rows = append(rows, row)
	}
	return rows
}

// groupAddress returns a group's address for display, or "" when it cannot be
// read. It is a label, never an authorisation input.
func (h *CalendarHandler) groupAddress(id string) string {
	if h.groups == nil {
		return ""
	}
	g, err := h.groups.GetGroup(id)
	if err != nil {
		return ""
	}
	return g.Address
}

// groupLabels joins the addresses of the groups, falling back to the id of one
// that cannot be read so the row is never blank.
func (h *CalendarHandler) groupLabels(ids []string) string {
	labels := make([]string, 0, len(ids))
	for _, id := range ids {
		l := h.groupAddress(id)
		if l == "" {
			l = id
		}
		labels = append(labels, l)
	}
	return strings.Join(labels, ", ")
}

// withPendingCount puts the number of pending invitations on the context for
// the sidebar link. A failed count is display only: it is logged and no number
// is shown, and the page still renders.
func (h *CalendarHandler) withPendingCount(r *http.Request, actor string) *http.Request {
	if h.acl == nil {
		return r
	}
	n, err := h.acl.CountPendingGrantsHeld(actor, h.clock())
	if err != nil {
		slog.Warn("calendar-share: counting invitations failed", "user", actor, "error", err)
		return r
	}
	if n == 0 {
		return r
	}
	return r.WithContext(templates.WithPendingShareCount(r.Context(), n))
}

// AcceptShare turns a pending grant held by the session user into an accepted
// one. The grantee is always the session user: the form names only the grant.
// The accept is decided inside the FSM, so a grant the owner revoked meanwhile
// stays revoked; the re-read reports that as 404.
//
// POST /shares/accept (and the older /calendar/shares/accept)
func (h *CalendarHandler) AcceptShare(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	// One reading serves the pre-check and the command, so the handler and the
	// FSM judge expiry by the same instant.
	now := h.clock()
	kind, owner, res, ok := h.heldGrant(w, r, userID, store.GrantStatePending, now)
	if !ok {
		return
	}
	if err := h.acl.AcceptGrant(kind, owner, res, store.GranteeUser(userID), now); err != nil {
		slog.Error("calendar-share: accept failed", "user", userID, "owner", owner, "kind", kind, "resource", res, "error", err)
		http.Error(w, "failed to accept invitation", http.StatusInternalServerError)
		return
	}
	g, err := h.acl.GetGrant(kind, owner, res, store.GranteeUser(userID))
	if err != nil {
		http.Error(w, "failed to accept invitation", http.StatusInternalServerError)
		return
	}
	if g == nil || g.State != store.GrantStateAccepted {
		http.Error(w, sharesNotFound, http.StatusNotFound)
		return
	}
	shareAnswered(w, r)
}

// DeclineShare deletes a pending grant held by the session user.
//
// POST /shares/decline (and the older /calendar/shares/decline)
func (h *CalendarHandler) DeclineShare(w http.ResponseWriter, r *http.Request) {
	h.revokeHeld(w, r, store.GrantStatePending, "decline", "failed to decline invitation")
}

// LeaveShare removes an accepted grant held by the session user. Like accept
// and decline it names only the grant: the grantee is always the session user,
// so no form field can point it at someone else's grant. It reuses the revoke
// command. Absent, pending and malformed all answer the same 404.
//
// POST /shares/leave (and the older /calendar/shares/leave)
func (h *CalendarHandler) LeaveShare(w http.ResponseWriter, r *http.Request) {
	h.revokeHeld(w, r, store.GrantStateAccepted, "leave", "failed to leave share")
}

func (h *CalendarHandler) revokeHeld(w http.ResponseWriter, r *http.Request, state, action, failure string) {
	userID, _ := middleware.UserFromContext(r.Context())
	kind, owner, res, ok := h.heldGrant(w, r, userID, state, h.clock())
	if !ok {
		return
	}
	if err := h.acl.RevokeGrant(kind, owner, res, store.GranteeUser(userID)); err != nil {
		slog.Error("calendar-share: "+action+" failed", "user", userID, "owner", owner, "kind", kind, "resource", res, "error", err)
		http.Error(w, failure, http.StatusInternalServerError)
		return
	}
	shareAnswered(w, r)
}

// heldGrant reads kind, owner and resource from the form and checks that the
// session user holds a grant on them in the given state. A missing kind means
// a calendar, and a calendar's resource may arrive as "calendar", as the forms
// of the calendar-only page posted it. A pending grant expired at now counts as
// absent. It writes the response and reports false when not. Every refusal
// answers the same 404.
func (h *CalendarHandler) heldGrant(w http.ResponseWriter, r *http.Request, userID, state string, now time.Time) (kind, owner, resource string, ok bool) {
	if h.acl == nil {
		http.Error(w, "sharing is unavailable", http.StatusServiceUnavailable)
		return "", "", "", false
	}
	kind, owner, resource = r.FormValue("kind"), r.FormValue("owner"), r.FormValue("resource")
	if kind == "" {
		kind = store.ACLKindCalendar
	}
	if resource == "" && kind == store.ACLKindCalendar {
		resource = r.FormValue("calendar")
	}
	if !validGrantTarget(kind, owner, resource) {
		http.Error(w, sharesNotFound, http.StatusNotFound)
		return "", "", "", false
	}
	// The grantee is the session user, never a form field.
	g, err := h.acl.GetGrant(kind, owner, resource, store.GranteeUser(userID))
	if err != nil {
		http.Error(w, "failed to read invitation", http.StatusInternalServerError)
		return "", "", "", false
	}
	if g == nil || g.State != state || store.PendingGrantExpired(g, now.Unix()) {
		http.Error(w, sharesNotFound, http.StatusNotFound)
		return "", "", "", false
	}
	return kind, owner, resource, true
}

// validGrantTarget checks the shape of a posted grant reference before any
// read. Folder and address-book ids are 32 lowercase hex characters.
func validGrantTarget(kind, owner, resource string) bool {
	if !store.ValidACLKind(kind) || !validOwnerID(owner) || !validOwnerID(resource) {
		return false
	}
	return kind == store.ACLKindCalendar || store.ValidMailboxID(resource)
}

func shareAnswered(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("HX-Trigger", "calendarRefresh")
	refreshSettings(w, r)
	w.WriteHeader(http.StatusOK)
}
