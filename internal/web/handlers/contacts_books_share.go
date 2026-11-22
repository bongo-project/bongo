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

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// bookShareRefused is the message id of the one answer for every share whose
// target does not resolve, and for a full book, so the reply does not say which
// addresses are accounts.
const bookShareRefused = "contacts.errShareRefused"

// BookShareHandler is the owner's side of address-book sharing: who may see a
// book, in which state, and the form to add or revoke a grantee. Only a book
// the user created can be shared. The implicit default book has no id a grant
// can name, so it is a 404 like any unknown book.
type BookShareHandler struct {
	acl        *store.ACLStore
	books      *store.AddressBookStore
	contacts   *ContactsHandler // sidebar view only
	shares     shareTargetResolver
	groups     groupLookup
	inviter    *ShareInviter
	limiter    shareRateLimiter
	authservID string
	now        func() time.Time
}

// NewBookShareHandler creates the handler. The limiter must be the calendar
// handler's (CalendarHandler.ShareLimiter), so the web calendar, folder and
// book forms and IMAP SETACL draw on one budget and the account oracle is not
// multiplied.
func NewBookShareHandler(acl *store.ACLStore, books *store.AddressBookStore) *BookShareHandler {
	return &BookShareHandler{acl: acl, books: books, now: time.Now}
}

func (h *BookShareHandler) SetShareResolver(r shareTargetResolver) { h.shares = r }
func (h *BookShareHandler) SetGroups(g groupLookup)                { h.groups = g }
func (h *BookShareHandler) SetShareInviter(s *ShareInviter)        { h.inviter = s }
func (h *BookShareHandler) SetShareLimiter(l shareRateLimiter)     { h.limiter = l }
func (h *BookShareHandler) SetAuthservID(host string)              { h.authservID = host }

// SetContacts gives the page the contacts sidebar of the user.
func (h *BookShareHandler) SetContacts(c *ContactsHandler) { h.contacts = c }

// ownBook resolves the {id} parameter to one of the user's own books. The
// default book, a malformed id, another user's book and a missing book are the
// same 404. It writes the response and reports false when there is no such book.
func (h *BookShareHandler) ownBook(w http.ResponseWriter, r *http.Request, userID string) (id, name string, ok bool) {
	id, valid := bookRef(r)
	if !valid {
		bookFail(w, r, http.StatusNotFound, "contacts.errBookNotFound")
		return "", "", false
	}
	book, err := h.books.Get(userID, id)
	switch {
	case errors.Is(err, store.ErrAddressBookNotFound):
		bookFail(w, r, http.StatusNotFound, "contacts.errBookNotFound")
		return "", "", false
	case err != nil:
		slog.Warn("web: book share: reading book failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errSharingUnavailable")
		return "", "", false
	}
	return id, book.Name, true
}

func bookSharingURL(id string) string { return "/contacts/books/" + id + "/sharing" }

// ShowSharing lists the grants on one of the user's books and the form to add
// one.
//
// GET /contacts/books/{id}/sharing
func (h *BookShareHandler) ShowSharing(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	if h.acl == nil || h.books == nil {
		bookFail(w, r, http.StatusServiceUnavailable, "contacts.errShareUnavailable")
		return
	}
	id, name, ok := h.ownBook(w, r, userID)
	if !ok {
		return
	}
	grants, err := h.acl.ListResourceGrants(store.ACLKindAddressBook, userID, id)
	if err != nil {
		slog.Warn("web: book share: listing grants failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errSharingUnavailable")
		return
	}
	now := h.now()
	rows := make([]templates.BookShareGrant, 0, len(grants))
	for _, g := range grants {
		d := grantDisplayRow(h.acl, h.groups, g, now)
		rows = append(rows, templates.BookShareGrant{Grantee: d.Grantee, Label: d.Label, State: d.State, Level: g.Grant.Level})
	}
	slices.SortFunc(rows, func(a, b templates.BookShareGrant) int { return strings.Compare(a.Label, b.Label) })

	view := templates.ContactBooksView{Active: id}
	if h.contacts != nil {
		if view, err = h.contacts.bookView(userID, id); err != nil {
			bookFail(w, r, http.StatusInternalServerError, "contacts.errSharingUnavailable")
			return
		}
	}
	data := templates.BookSharePageData{BookID: id, BookName: name, Grants: rows, Email: email, Flash: flashText(r), Books: view}
	templ.Handler(templates.BookSharePage(data)).ServeHTTP(w, r)
}

// ShareBook shares an own book with a user or a group.
//
// POST /contacts/books/{id}/share
//
// The order mirrors ShareFolder, and each step is where it is so the reply is
// no oracle for which addresses are accounts: the rate limit is charged first,
// the book must be the user's own, the level is validated and the cap read
// before the address resolves, and every negative resolution is one fixed
// refusal. The cap check is read-then-write, so concurrent shares can overshoot
// it; the grant and the invitation are separate commands and not atomic.
func (h *BookShareHandler) ShareBook(w http.ResponseWriter, r *http.Request) {
	userID, ownerEmail := middleware.UserFromContext(r.Context())
	if h.acl == nil || h.books == nil || h.shares == nil {
		bookFail(w, r, http.StatusServiceUnavailable, "contacts.errShareUnavailable")
		return
	}
	if h.limiter != nil && !h.limiter.Allow(userID) {
		secs := max(int(math.Ceil(h.limiter.RetryAfter(userID).Seconds())), 1)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		bookFail(w, r, http.StatusTooManyRequests, "contacts.errShareTooMany")
		return
	}
	id, name, ok := h.ownBook(w, r, userID)
	if !ok {
		return
	}
	shareWith := strings.TrimSpace(r.FormValue("share_with"))
	if shareWith == "" {
		bookFail(w, r, http.StatusBadRequest, "contacts.errShareWithRequired")
		return
	}
	// The level is validated before the address resolves.
	level := r.FormValue("level")
	if !store.ValidAddressBookLevel(level) {
		bookFail(w, r, http.StatusBadRequest, "contacts.errShareInvalidLevel")
		return
	}
	now := h.now()
	full, err := h.acl.GrantCapReached(store.ACLKindAddressBook, userID, id, now)
	if err != nil {
		slog.Warn("web: book share: reading grant cap failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errShareFailed")
		return
	}
	parsed, err := mail.ParseAddress(shareWith)
	if err != nil || parsed.Name != "" {
		bookFail(w, r, http.StatusBadRequest, bookShareRefused)
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
		bookFail(w, r, http.StatusBadRequest, bookShareRefused)
		return
	}
	offer := GrantOffer{
		Kind: store.ACLKindAddressBook, Owner: userID, OwnerEmail: ownerEmail,
		Resource: id, ResourceName: name, GranteeID: targetID, Recipient: parsed.Address,
		Level: level, Host: shareLinkHost(h.authservID, r), Full: full, Now: now,
	}
	if groupID != "" {
		offer.GroupID = groupID
		_, err = offerGroupGrant(h.acl, offer)
	} else {
		_, err = offerGrant(h.acl, h.inviter, offer)
	}
	switch {
	case errors.Is(err, errShareRefused):
		bookFail(w, r, http.StatusBadRequest, bookShareRefused)
		return
	case err != nil:
		slog.Error("web: book share failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errShareFailed")
		return
	}
	redirectAfterAction(w, r, withFlash(bookSharingURL(id), flashBookShared))
}

// UnshareBook revokes one grantee's grant on an own book. share_with is a user
// id or "g:" and a group id. A grant that is not there writes nothing and goes
// back to the page. It is not rate limited: removal reveals nothing about
// accounts.
//
// POST /contacts/books/{id}/unshare
func (h *BookShareHandler) UnshareBook(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if h.acl == nil || h.books == nil {
		bookFail(w, r, http.StatusServiceUnavailable, "contacts.errShareUnavailable")
		return
	}
	id, _, ok := h.ownBook(w, r, userID)
	if !ok {
		return
	}
	entry := r.FormValue("share_with")
	if entry == "" {
		bookFail(w, r, http.StatusBadRequest, "contacts.errShareWithRequired")
		return
	}
	typed := store.GranteeUser(entry)
	if gid, isGroup := strings.CutPrefix(entry, "g:"); isGroup {
		typed = store.GranteeGroup(gid)
	}
	existing, err := h.acl.GetGrant(store.ACLKindAddressBook, userID, id, typed)
	if err != nil {
		slog.Warn("web: book unshare: reading grant failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errUnshareFailed")
		return
	}
	if existing == nil {
		redirectAfterAction(w, r, bookSharingURL(id))
		return
	}
	if err := h.acl.RevokeGrant(store.ACLKindAddressBook, userID, id, typed); err != nil {
		slog.Error("web: book unshare failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errUnshareFailed")
		return
	}
	redirectAfterAction(w, r, withFlash(bookSharingURL(id), flashBookUnshared))
}
