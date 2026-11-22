package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// sharedBookNotFound is the constant body of every refusal on the shared book
// pages: no grant, a pending or expired grant, a revoked one, the viewer's own
// book, a malformed id, a deleted book, and a contact that is missing or filed
// in another book. Nothing in the reply tells them apart.
const sharedBookNotFound = "address book not found"

// maxSharedSidebarBooks bounds the "Shared with me" list the contacts sidebar
// reads on every page.
const maxSharedSidebarBooks = 50

// SetACL wires the grant store the shared book pages and the sidebar read.
// Without it nothing shared is reachable.
func (h *ContactsHandler) SetACL(acl *store.ACLStore) { h.acl = acl }

func (h *ContactsHandler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// sharedBook is a book another user shared with the session user, as
// resolveSharedBook found it on this request.
type sharedBook struct {
	owner string
	id    string
	level string
	book  *bongov1.AddressBook
}

func sharedNotFound(w http.ResponseWriter) {
	http.Error(w, sharedBookNotFound, http.StatusNotFound)
}

// resolveSharedBook is the one place the shared book pages decide access, and
// it runs on every request: nothing is cached in the handler or a session, so
// a revoke or leaving a group ends access on the next one. The checks run in
// this order, and every failure is the same constant 404:
//
//  1. the stores are wired, the owner id is well formed, the book id has the
//     32-hex form of a created book (so "default" and path tricks fail here);
//  2. the owner is not the session user;
//  3. the viewer's effective level is at least read (the one resolver
//     ACLStore.AddressBookLevel: an accepted grant of their own or of a group
//     they are in; a pending, expired or revoked one adds nothing);
//  4. the book still exists under the owner.
//
// The owner's account state is not consulted: a disabled owner's book stays
// reachable, as a shared calendar does.
func (h *ContactsHandler) resolveSharedBook(w http.ResponseWriter, r *http.Request) (*sharedBook, bool) {
	userID, _ := middleware.UserFromContext(r.Context())
	owner, id := chi.URLParam(r, "owner"), chi.URLParam(r, "book")
	if h.acl == nil || h.books == nil || h.contacts == nil || !validOwnerID(owner) || !store.ValidAddressBookID(id) || owner == userID {
		sharedNotFound(w)
		return nil, false
	}
	level, err := h.acl.AddressBookLevel(owner, id, userID)
	if err != nil {
		slog.Warn("web: shared book: resolving access failed", "user", userID, "error", err)
		http.Error(w, "failed to load address book", http.StatusInternalServerError)
		return nil, false
	}
	if level == "" {
		sharedNotFound(w)
		return nil, false
	}
	book, err := h.books.Get(owner, id)
	switch {
	case errors.Is(err, store.ErrAddressBookNotFound):
		sharedNotFound(w)
		return nil, false
	case err != nil:
		slog.Warn("web: shared book: reading book failed", "user", userID, "error", err)
		http.Error(w, "failed to load address book", http.StatusInternalServerError)
		return nil, false
	}
	return &sharedBook{owner: owner, id: id, level: level, book: book}, true
}

// sharedView is the sidebar view of a shared page: the viewer's own books and
// shares, with this share marked active and no own book or group active.
func (h *ContactsHandler) sharedView(userID string, sb *sharedBook) (templates.ContactBooksView, error) {
	view, err := h.bookView(userID, "")
	if err != nil {
		return view, err
	}
	view.SharedActive = templates.SharedBookKey(sb.owner, sb.id)
	return view, nil
}

// sharedBooksFor lists the address books shared with userID for the sidebar.
// A failure is display only: it is logged and the sidebar shows none.
func (h *ContactsHandler) sharedBooksFor(userID string) []templates.SharedBookLink {
	if h.acl == nil || h.books == nil {
		return nil
	}
	held, err := h.acl.AddressBooksSharedWith(userID, h.clock())
	if err != nil {
		slog.Warn("web: contacts sidebar: listing shared books failed", "user", userID, "error", err)
		return nil
	}
	var out []templates.SharedBookLink
	for _, s := range held {
		if len(out) >= maxSharedSidebarBooks {
			break
		}
		b, err := h.books.Get(s.Owner, s.Resource)
		if err != nil {
			if !errors.Is(err, store.ErrAddressBookNotFound) {
				slog.Warn("web: contacts sidebar: reading shared book failed", "owner", s.Owner, "error", err)
			}
			continue
		}
		out = append(out, templates.SharedBookLink{Owner: s.Owner, Book: s.Resource, Name: b.Name, OwnerLabel: h.ownerLabel(s.Owner)})
	}
	return out
}

// ownerLabel is the address shown for an owner, or the id when it is unknown.
func (h *ContactsHandler) ownerLabel(owner string) string {
	if h.acl != nil {
		if e := h.acl.UserEmail(owner); e != "" {
			return e
		}
	}
	return owner
}

// SharedList shows the contacts of a book shared with the session user, read
// only.
//
// GET /shared/contacts/{owner}/{book}
func (h *ContactsHandler) SharedList(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	sb, ok := h.resolveSharedBook(w, r)
	if !ok {
		return
	}
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	contacts, total, err := h.contacts.ListByBook(sb.owner, sb.id, (page-1)*contactsPerPage, contactsPerPage)
	if err != nil {
		http.Error(w, "failed to list contacts", http.StatusInternalServerError)
		return
	}
	view, err := h.sharedView(userID, sb)
	if err != nil {
		http.Error(w, "failed to load address books", http.StatusInternalServerError)
		return
	}
	totalPages := max((total+contactsPerPage-1)/contactsPerPage, 1)
	templ.Handler(templates.SharedContactsPage(templates.SharedBookPageData{
		Email: email, Owner: sb.owner, OwnerLabel: h.ownerLabel(sb.owner), BookID: sb.id, BookName: sb.book.Name,
		Contacts: contacts, Page: page, TotalPages: totalPages, Books: view,
	})).ServeHTTP(w, r)
}

// SharedShow shows one contact of a shared book, read only. A contact group
// lists its members by name, resolved in the owner's book. A contact that is
// missing or filed in another book of the owner's is the same 404 as a book
// the viewer cannot see.
//
// GET /shared/contacts/{owner}/{book}/{guid}
func (h *ContactsHandler) SharedShow(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	sb, ok := h.resolveSharedBook(w, r)
	if !ok {
		return
	}
	// ContactStore.Get does not tell a missing record from a read error, and the
	// owner's own page treats both as 404; so does this one.
	guid := chi.URLParam(r, "guid")
	if !validOwnerID(guid) {
		sharedNotFound(w)
		return
	}
	contact, err := h.contacts.Get(sb.owner, guid)
	if err != nil || store.ContactBook(contact) != sb.id {
		sharedNotFound(w)
		return
	}
	view, err := h.sharedView(userID, sb)
	if err != nil {
		http.Error(w, "failed to load address books", http.StatusInternalServerError)
		return
	}
	data := templates.SharedContactPageData{
		Email: email, Owner: sb.owner, OwnerLabel: h.ownerLabel(sb.owner), BookID: sb.id, BookName: sb.book.Name,
		Contact: contact, Books: view,
	}
	if store.IsContactGroup(contact) {
		if data.Group, err = h.groupView(sb.owner, contact); err != nil {
			http.Error(w, "failed to load contact group", http.StatusInternalServerError)
			return
		}
	}
	templ.Handler(templates.SharedContactPage(data)).ServeHTTP(w, r)
}
