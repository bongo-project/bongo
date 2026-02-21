package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/emersion/go-vcard"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const contactsPerPage = 25

// ContactsHandler handles contact management endpoints.
type ContactsHandler struct {
	contacts *store.ContactStore
	search   *store.SearchManager
}

// NewContactsHandler creates a ContactsHandler.
func NewContactsHandler(contacts *store.ContactStore, search *store.SearchManager) *ContactsHandler {
	return &ContactsHandler{contacts: contacts, search: search}
}

// ListContacts renders the contact list page.
func (h *ContactsHandler) ListContacts(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}

	offset := (page - 1) * contactsPerPage
	contacts, total, err := h.contacts.List(userID, offset, contactsPerPage)
	if err != nil {
		http.Error(w, "failed to list contacts", http.StatusInternalServerError)
		return
	}

	totalPages := (total + contactsPerPage - 1) / contactsPerPage
	if totalPages < 1 {
		totalPages = 1
	}

	flash := r.URL.Query().Get("flash")

	templ.Handler(templates.ContactsPage(contacts, page, totalPages, email, flash)).ServeHTTP(w, r)
}

// CreateContact handles POST to create a new contact.
func (h *ContactsHandler) CreateContact(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	contact := &bongov1.Contact{
		DisplayName: r.FormValue("display_name"),
		Email:       r.FormValue("email"),
		Phone:       r.FormValue("phone"),
		Company:     r.FormValue("company"),
		Notes:       r.FormValue("notes"),
	}

	if _, err := h.contacts.Create(userID, contact); err != nil {
		http.Error(w, "failed to create contact", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/contacts?flash=Contact+created", http.StatusSeeOther)
}

// ShowContact renders a single contact detail/edit view.
func (h *ContactsHandler) ShowContact(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	contact, err := h.contacts.Get(userID, guid)
	if err != nil {
		http.Error(w, "contact not found", http.StatusNotFound)
		return
	}

	templ.Handler(templates.ContactDetailPage(contact, email)).ServeHTTP(w, r)
}

// UpdateContact handles PUT to update a contact.
func (h *ContactsHandler) UpdateContact(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	existing, err := h.contacts.Get(userID, guid)
	if err != nil {
		http.Error(w, "contact not found", http.StatusNotFound)
		return
	}

	existing.DisplayName = r.FormValue("display_name")
	existing.Email = r.FormValue("email")
	existing.Phone = r.FormValue("phone")
	existing.Company = r.FormValue("company")
	existing.Notes = r.FormValue("notes")

	if err := h.contacts.Update(userID, guid, existing); err != nil {
		http.Error(w, "failed to update contact", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/contacts/"+guid+"?flash=Contact+updated", http.StatusSeeOther)
}

// DeleteContact handles DELETE to remove a contact.
func (h *ContactsHandler) DeleteContact(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	if err := h.contacts.Delete(userID, guid); err != nil {
		http.Error(w, "failed to delete contact", http.StatusInternalServerError)
		return
	}

	// HTMX request: return empty for swap.
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, "/contacts?flash=Contact+deleted", http.StatusSeeOther)
}

// QuickAddContact handles POST /contacts/quick-add from the message view.
func (h *ContactsHandler) QuickAddContact(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	fromEmail := r.FormValue("from_email")
	fromName := r.FormValue("from_name")
	returnTo := r.FormValue("return_to")

	if fromEmail == "" {
		http.Redirect(w, r, returnTo, http.StatusSeeOther)
		return
	}

	existing, _ := h.contacts.FindByEmail(userID, fromEmail)
	if existing != nil {
		if returnTo != "" {
			http.Redirect(w, r, returnTo+"?flash=Already+in+contacts", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/contacts?flash=Already+in+contacts", http.StatusSeeOther)
		return
	}

	contact := &bongov1.Contact{
		DisplayName: fromName,
		Email:       fromEmail,
	}
	h.contacts.Create(userID, contact)

	if returnTo != "" {
		http.Redirect(w, r, returnTo+"?flash=Contact+added", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/contacts?flash=Contact+added", http.StatusSeeOther)
}

// ExportContacts downloads all contacts as a .vcf file.
func (h *ContactsHandler) ExportContacts(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	contacts, _, err := h.contacts.List(userID, 0, 0)
	if err != nil {
		http.Error(w, "failed to list contacts", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	for _, c := range contacts {
		if len(c.VcardRaw) > 0 {
			buf.Write(c.VcardRaw)
			if !bytes.HasSuffix(c.VcardRaw, []byte("\n")) {
				buf.WriteByte('\n')
			}
			continue
		}

		card := make(vcard.Card)
		card.SetValue(vcard.FieldVersion, "3.0")
		card.SetValue(vcard.FieldFormattedName, c.DisplayName)
		if c.Email != "" {
			card.Set(vcard.FieldEmail, &vcard.Field{Value: c.Email})
		}
		if c.Phone != "" {
			card.Set(vcard.FieldTelephone, &vcard.Field{Value: c.Phone})
		}
		if c.Company != "" {
			card.Set(vcard.FieldOrganization, &vcard.Field{Value: c.Company})
		}
		if c.Notes != "" {
			card.SetValue(vcard.FieldNote, c.Notes)
		}

		enc := vcard.NewEncoder(&buf)
		enc.Encode(card)
	}

	w.Header().Set("Content-Type", "text/vcard")
	w.Header().Set("Content-Disposition", `attachment; filename="contacts.vcf"`)
	w.Write(buf.Bytes())
}

// ImportContacts uploads and parses a .vcf file to create contacts.
func (h *ContactsHandler) ImportContacts(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	file, _, err := r.FormFile("vcf")
	if err != nil {
		http.Redirect(w, r, "/contacts?flash=No+file+uploaded", http.StatusSeeOther)
		return
	}
	defer file.Close()

	dec := vcard.NewDecoder(file)
	imported := 0
	errCount := 0

	for {
		card, err := dec.Decode()
		if err != nil {
			break
		}

		displayName := card.PreferredValue(vcard.FieldFormattedName)
		email := card.PreferredValue(vcard.FieldEmail)
		phone := card.PreferredValue(vcard.FieldTelephone)
		company := card.PreferredValue(vcard.FieldOrganization)
		notes := card.PreferredValue(vcard.FieldNote)

		if displayName == "" && email == "" {
			errCount++
			continue
		}

		// Store raw vCard for round-trip fidelity.
		var rawBuf bytes.Buffer
		vcard.NewEncoder(&rawBuf).Encode(card)

		contact := &bongov1.Contact{
			DisplayName: displayName,
			Email:       email,
			Phone:       phone,
			Company:     company,
			Notes:       notes,
			VcardRaw:    rawBuf.Bytes(),
		}

		if _, err := h.contacts.Create(userID, contact); err != nil {
			errCount++
			continue
		}
		imported++
	}

	flash := fmt.Sprintf("Imported+%d+contacts", imported)
	if errCount > 0 {
		flash = fmt.Sprintf("Imported+%d+contacts,+%d+errors", imported, errCount)
	}

	http.Redirect(w, r, "/contacts?flash="+strings.ReplaceAll(flash, " ", "+"), http.StatusSeeOther)
}
