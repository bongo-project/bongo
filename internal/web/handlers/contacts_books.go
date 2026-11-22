package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// errUnknownAddressBook is a form value that names no book of the user's.
var errUnknownAddressBook = errors.New("unknown address book")

// SetAddressBooks enables address books beyond the default one. Without it the
// contacts pages show one list, as before books existed.
func (h *ContactsHandler) SetAddressBooks(books *store.AddressBookStore) { h.books = books }

// bookFail answers a failed book request with a translated message.
func bookFail(w http.ResponseWriter, r *http.Request, status int, msgID string) {
	http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), msgID), status)
}

// bookRedirect sends the browser to a list URL, through HX-Redirect for an
// htmx request, which would otherwise swap the redirected page into place.
func bookRedirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther) // #nosec G710 -- fixed same-origin /contacts path, book id validated
}

// bookView loads the books for the contacts pages. active is "" for all
// contacts, "default", or the id of one of the user's books, which the caller has
// already checked.
func (h *ContactsHandler) bookView(userID, active string) (templates.ContactBooksView, error) {
	view := templates.ContactBooksView{Active: active}
	// The sidebar lists the contact groups of the active book, or of every book.
	// One more prefix scan of the user's contacts per page.
	groups, err := h.contacts.ListContactGroups(userID, active)
	if err != nil {
		return view, err
	}
	view.Groups = groups
	if h.books == nil {
		return view, nil
	}
	books, err := h.books.List(userID)
	if err != nil {
		return view, err
	}
	view.Enabled = true
	view.Books = books
	view.Shared = h.sharedBooksFor(userID)
	for _, b := range books {
		if b.Id == active {
			view.ActiveBook = b
		}
	}
	return view, nil
}

// formBook resolves the optional addressbook form value to the id a contact
// stores: "" for the default book (also what an absent value means), otherwise
// the id of one of the user's own books. Anything else is errUnknownAddressBook,
// so a request can never file a contact under another user's book or a made-up
// id.
func (h *ContactsHandler) formBook(userID, value string) (string, error) {
	if value == "" || value == store.DefaultAddressBookID {
		return "", nil
	}
	if h.books == nil || !store.ValidAddressBookID(value) {
		return "", errUnknownAddressBook
	}
	if _, err := h.books.Get(userID, value); err != nil {
		if errors.Is(err, store.ErrAddressBookNotFound) {
			return "", errUnknownAddressBook
		}
		return "", err
	}
	return value, nil
}

// answerFormBookError writes the response for a formBook failure.
func answerFormBookError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errUnknownAddressBook) {
		bookFail(w, r, http.StatusBadRequest, "contacts.errBookUnknown")
		return
	}
	bookFail(w, r, http.StatusInternalServerError, "contacts.errBookFailed")
}

// bookRef validates the {id} URL parameter of a book route. The default book is
// never a valid target for rename or delete.
func bookRef(r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	return id, store.ValidAddressBookID(id)
}

// CreateAddressBook handles POST /contacts/books.
func (h *ContactsHandler) CreateAddressBook(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if h.books == nil {
		bookFail(w, r, http.StatusNotFound, "contacts.errBookNotFound")
		return
	}
	name := store.CleanAddressBookText(r.FormValue("name"), store.MaxAddressBookNameLen)
	if name == "" {
		bookFail(w, r, http.StatusBadRequest, "contacts.errBookNameRequired")
		return
	}
	desc := store.CleanAddressBookText(r.FormValue("description"), store.MaxAddressBookDescLen)

	id := store.NewAddressBookID()
	if _, err := h.books.Create(userID, id, name, desc); err != nil {
		if errors.Is(err, store.ErrAddressBookLimit) {
			bookFail(w, r, http.StatusConflict, "contacts.errBookLimit")
			return
		}
		slog.Warn("create address book failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errBookFailed")
		return
	}
	bookRedirect(w, r, withFlash(contactsBookURL(id), flashAddressBookCreated))
}

// RenameAddressBook handles POST /contacts/books/{id}/rename. Only the web
// renames a book; CardDAV has no rename.
func (h *ContactsHandler) RenameAddressBook(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	id, ok := bookRef(r)
	if !ok || h.books == nil {
		bookFail(w, r, http.StatusNotFound, "contacts.errBookNotFound")
		return
	}
	name := store.CleanAddressBookText(r.FormValue("name"), store.MaxAddressBookNameLen)
	if name == "" {
		bookFail(w, r, http.StatusBadRequest, "contacts.errBookNameRequired")
		return
	}
	if err := h.books.Rename(userID, id, name); err != nil {
		if errors.Is(err, store.ErrAddressBookNotFound) {
			bookFail(w, r, http.StatusNotFound, "contacts.errBookNotFound")
			return
		}
		slog.Warn("rename address book failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errBookFailed")
		return
	}
	bookRedirect(w, r, withFlash(contactsBookURL(id), flashAddressBookRenamed))
}

// DeleteAddressBook handles POST /contacts/books/{id}/delete: the book, then its
// contacts and grants, with the resumable semantics of store.PurgeAddressBook.
// A failure part way answers 500 and leaves the book in place, so the user can
// repeat the request to finish.
func (h *ContactsHandler) DeleteAddressBook(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if chi.URLParam(r, "id") == store.DefaultAddressBookID {
		bookFail(w, r, http.StatusForbidden, "contacts.errBookProtected")
		return
	}
	id, ok := bookRef(r)
	if !ok || h.books == nil {
		bookFail(w, r, http.StatusNotFound, "contacts.errBookNotFound")
		return
	}
	err := store.PurgeAddressBook(h.books, h.contacts, userID, id)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrAddressBookNotFound):
		bookFail(w, r, http.StatusNotFound, "contacts.errBookNotFound")
		return
	case errors.Is(err, store.ErrAddressBookProtected):
		bookFail(w, r, http.StatusForbidden, "contacts.errBookProtected")
		return
	case errors.Is(err, store.ErrAddressBookGrantsNotRevoked):
		// The book is gone; the leader's orphan sweep removes what is left.
		slog.Warn("address book deleted but grants not revoked", "user", userID, "error", err)
	default:
		slog.Warn("delete address book failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errBookDeleteFailed")
		return
	}
	bookRedirect(w, r, withFlash("/contacts", flashAddressBookDeleted))
}

func contactsBookURL(id string) string {
	// Only a well-formed book id reaches the URL, so the redirect target is
	// always the same-origin contacts list.
	if !store.ValidAddressBookID(id) {
		return "/contacts"
	}
	return "/contacts?book=" + url.QueryEscape(id)
}
