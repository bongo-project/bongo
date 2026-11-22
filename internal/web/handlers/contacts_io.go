package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/emersion/go-vcard"

	"github.com/bongo-project/bongo/internal/contactio"
	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	// contactsImportMaxRecords bounds the contacts of one import, so a file of
	// tiny rows cannot become hundreds of thousands of Raft writes.
	contactsImportMaxRecords = 5000
	// contactsImportWriteWindow is how long the write phase may take; the
	// server's own write timeout is a minute and 5000 sequential applies can
	// exceed it.
	contactsImportWriteWindow = 5 * time.Minute
	// maxImportGroupName bounds the name of a contact group read from a file.
	maxImportGroupName = 100
)

// importRef locates a record for an error message.
type importRef struct {
	format contactio.Format
	ref    contactio.Ref
}

// importItem is one contact built from the file, not yet written.
type importItem struct {
	contact *bongov1.Contact
	ref     importRef
}

// importFailure is an import refused whole, with the status and the message to
// show. Nothing has been written when one is returned.
type importFailure struct {
	status int
	msg    string
}

// ImportContacts handles POST /contacts/import. The file is read whole and
// validated before the first write: a malformed record, a file over the record
// cap, a group that is too large or a quota that would be exceeded refuses the
// entire import. The writes themselves are one Raft PUT per contact and are not
// atomic; a failure part way deletes what was written (see writeImport).
func (h *ContactsHandler) ImportContacts(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	// Thousands of sequential applies can outlast the server's write timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(contactsImportWriteWindow))

	if !parseUploadForm(w, r, contactsImportMaxBytes, "/contacts") {
		return
	}
	defer removeUploadTemp(r)
	book, err := h.formBook(userID, r.FormValue("addressbook"))
	if err != nil {
		answerFormBookError(w, r, err)
		return
	}
	file, _, err := r.FormFile("vcf")
	if err != nil {
		http.Redirect(w, r, withFlash("/contacts", flashNoFileUploaded), http.StatusSeeOther)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		http.Redirect(w, r, withFlash("/contacts", flashFileReadFailed), http.StatusSeeOther)
		return
	}

	locale := middleware.LocaleFromContext(r.Context())
	items, skipped, fail := buildImport(locale, raw, book)
	if fail != nil {
		refuseImport(w, r, email, book, fail)
		return
	}

	// The quota check counts what will be stored, not the upload: a CSV becomes
	// vCards several times its size.
	var size int64
	for _, it := range items {
		size += store.ContactRecordSize(it.contact)
	}
	if quotaExceeded(h.quota, nil, userID, size) {
		refuseQuotaFlash(w, r, contactsReturnURL(book))
		return
	}

	if fail := h.writeImport(locale, userID, items); fail != nil {
		refuseImport(w, r, email, book, fail)
		return
	}

	flash := flashContactsImported
	if skipped > 0 {
		flash = flashContactsImportedSomeSkipped
	}
	http.Redirect(w, r, withFlash(contactsReturnURL(book), flash), http.StatusSeeOther) // #nosec G710 -- fixed same-origin /contacts path, book id validated
}

// buildImport reads raw whole and returns the contacts to write: people first,
// then groups, which refer to their members by UID. It writes nothing.
func buildImport(locale string, raw []byte, book string) ([]importItem, int, *importFailure) {
	res, err := contactio.ParseLimit(raw, contactsImportMaxRecords)
	if err != nil {
		return nil, 0, parseFailure(locale, err)
	}
	if len(res.Records) > contactsImportMaxRecords {
		return nil, 0, &importFailure{http.StatusUnprocessableEntity,
			fmt.Sprintf(i18n.T(locale, "contacts.importErrTooMany"), contactsImportMaxRecords)}
	}

	skipped := res.Skipped
	items := make([]importItem, 0, len(res.Records))
	byRecord := make(map[int]*bongov1.Contact, len(res.Records))
	for i, rec := range res.Records {
		if rec.Group != nil {
			continue
		}
		ref := importRef{res.Format, rec.Ref}
		c, err := parseVCardBlock(rec.Raw)
		if err != nil {
			return nil, 0, refFailure(locale, ref, contactio.ReasonSyntax)
		}
		if c.DisplayName == "" && c.Email == "" {
			skipped++
			continue
		}
		c.AddressbookId = book
		giveNewContactUID(c)
		byRecord[i] = c
		items = append(items, importItem{c, ref})
	}
	for i, rec := range res.Records {
		if rec.Group == nil {
			continue
		}
		ref := importRef{res.Format, rec.Ref}
		g, ok := buildImportGroup(rec.Group, byRecord, book)
		if !ok {
			skipped++
			continue
		}
		byRecord[i] = g
		items = append(items, importItem{g, ref})
	}
	// A vCard group (KIND:group) is a person record to the splitter; check
	// every built contact so none is written that Create would refuse.
	for _, it := range items {
		if err := store.ValidateContact(it.contact); err != nil {
			return nil, 0, refFailure(locale, it.ref, groupTooLarge)
		}
	}
	return items, skipped, nil
}

// groupTooLarge is the reason for a group over the member cap. It is not a
// parse reason: the file is well formed, the store refuses the group.
const groupTooLarge contactio.Reason = "groupTooLarge"

// buildImportGroup makes the contact group of an LDIF groupOfNames entry. Its
// members are the people of the same file, already built.
func buildImportGroup(g *contactio.Group, built map[int]*bongov1.Contact, book string) (*bongov1.Contact, bool) {
	name := store.CleanAddressBookText(g.Name, maxImportGroupName)
	if name == "" {
		return nil, false
	}
	guid := store.NewContactGUID()
	uid, _ := store.UUIDFromGUID(guid)
	card, err := store.NewContactGroupCard(name, uid)
	if err != nil {
		return nil, false
	}
	for _, idx := range g.Members {
		m := built[idx]
		if m == nil {
			continue // an empty person that was skipped
		}
		value := store.ContactMemberValue(store.ContactIdentity(m))
		if value == "" {
			continue
		}
		if next, err := store.AddContactGroupMember(card, value); err == nil {
			card = next
		}
	}
	return &bongov1.Contact{Guid: guid, DisplayName: name, VcardRaw: card, AddressbookId: book}, true
}

// writeImport creates the contacts one by one. A failure deletes everything
// written so far, the failed one included (its apply may have landed before the
// error); if some deletes fail too the user is told how many contacts may remain.
// A crash between writes cannot be undone: the import is then partial.
func (h *ContactsHandler) writeImport(locale, userID string, items []importItem) *importFailure {
	created := make([]string, 0, len(items))
	for _, it := range items {
		created = append(created, it.contact.Guid)
		if _, err := h.contacts.Create(userID, it.contact); err != nil {
			stuck := 0
			for i := len(created) - 1; i >= 0; i-- {
				if derr := h.contacts.Delete(userID, created[i]); derr != nil {
					stuck++
					slog.Error("contact import: rollback delete failed", "user", userID, "contact", created[i], "error", derr)
				}
			}
			slog.Warn("contact import: write failed", "user", userID, "written", len(created)-1, "rolled_back_failed", stuck, "error", err)
			if stuck > 0 {
				return &importFailure{http.StatusInternalServerError,
					fmt.Sprintf(i18n.T(locale, "contacts.importErrWritePartial"), stuck)}
			}
			return &importFailure{http.StatusInternalServerError, i18n.T(locale, "contacts.importErrWrite")}
		}
	}
	return nil
}

// parseFailure turns a contactio error into the page the user sees.
func parseFailure(locale string, err error) *importFailure {
	var pe *contactio.ParseError
	switch {
	case errors.As(err, &pe):
		return refFailure(locale, importRef{pe.Format, contactio.Ref{N: pe.N, Line: pe.Line}}, pe.Reason)
	case errors.Is(err, contactio.ErrTooMany):
		return &importFailure{http.StatusUnprocessableEntity,
			fmt.Sprintf(i18n.T(locale, "contacts.importErrTooMany"), contactsImportMaxRecords)}
	case errors.Is(err, contactio.ErrEncoding):
		return &importFailure{http.StatusUnprocessableEntity, i18n.T(locale, "contacts.importErrEncoding")}
	default:
		return &importFailure{http.StatusUnprocessableEntity, i18n.T(locale, "contacts.importErrFormat")}
	}
}

// refFailure names the record and the reason, in the file's own terms.
func refFailure(locale string, ref importRef, reason contactio.Reason) *importFailure {
	why := i18n.T(locale, "contacts.importReason."+string(reason))
	var msg string
	switch ref.format {
	case contactio.FormatCSV:
		msg = fmt.Sprintf(i18n.T(locale, "contacts.importErrRow"), ref.ref.N, why)
	case contactio.FormatLDIF:
		msg = fmt.Sprintf(i18n.T(locale, "contacts.importErrEntry"), ref.ref.N, ref.ref.Line, why)
	default:
		msg = fmt.Sprintf(i18n.T(locale, "contacts.importErrCard"), ref.ref.N, why)
	}
	return &importFailure{http.StatusUnprocessableEntity, msg}
}

// refuseImport renders the refusal page. The message never contains contact
// data, only record numbers.
func refuseImport(w http.ResponseWriter, r *http.Request, email, book string, f *importFailure) {
	templ.Handler(templates.ContactsImportRefusedPage(f.msg, email, contactsReturnURL(book)), templ.WithStatus(f.status)).ServeHTTP(w, r)
}

// quickCard is the vCard of a contact that has no stored one.
func quickCard(c *bongov1.Contact) vcard.Card {
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
	return card
}

// contactCard is the contact's stored vCard, or quickCard when it has none.
func contactCard(c *bongov1.Contact) vcard.Card {
	if len(c.VcardRaw) > 0 {
		if card, err := vcard.NewDecoder(bytes.NewReader(c.VcardRaw)).Decode(); err == nil && len(card) > 0 {
			return card
		}
	}
	return quickCard(c)
}

// ExportContacts downloads contacts as vCard (the default), CSV (Google header
// set) or LDIF (Thunderbird). The book parameter limits the export to one book;
// without it every book is exported. Download names are fixed so no user text
// reaches a header.
func (h *ContactsHandler) ExportContacts(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	format := r.URL.Query().Get("format")
	switch format {
	case "", "vcard", "csv", "ldif":
	default:
		bookFail(w, r, http.StatusBadRequest, "contacts.errExportFormat")
		return
	}
	book := r.URL.Query().Get("book")
	if !h.checkListBook(w, r, userID, book) {
		return
	}

	var (
		contacts []*bongov1.Contact
		err      error
	)
	if book == "" {
		contacts, _, err = h.contacts.List(userID, 0, 0)
	} else {
		contacts, _, err = h.contacts.ListByBook(userID, book, 0, 0)
	}
	if err != nil {
		slog.Warn("contact export: listing failed", "user", userID, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errBookFailed")
		return
	}

	var (
		buf         bytes.Buffer
		contentType string
		name        string
	)
	switch format {
	case "csv":
		contentType, name = "text/csv; charset=utf-8", "contacts.csv"
		var cards []vcard.Card
		for _, c := range contacts {
			if !store.IsContactGroup(c) {
				cards = append(cards, contactCard(c))
			}
		}
		err = contactio.WriteCSV(&buf, cards)
	case "ldif":
		contentType, name = "text/x-ldif; charset=utf-8", "contacts.ldif"
		var (
			people []contactio.Person
			groups []contactio.GroupOut
		)
		for _, c := range contacts {
			if ok, members := store.ContactGroupInfo(c); ok {
				groups = append(groups, contactio.GroupOut{Name: c.DisplayName, Refs: members})
				continue
			}
			people = append(people, contactio.Person{Card: contactCard(c), Ref: store.ContactIdentity(c)})
		}
		err = contactio.WriteLDIF(&buf, people, groups)
	default:
		contentType, name = "text/vcard", "contacts.vcf"
		for _, c := range contacts {
			if len(c.VcardRaw) > 0 {
				buf.Write(c.VcardRaw)
				if !bytes.HasSuffix(c.VcardRaw, []byte("\n")) {
					buf.WriteByte('\n')
				}
				continue
			}
			_ = vcard.NewEncoder(&buf).Encode(quickCard(c))
		}
	}
	if err != nil {
		slog.Warn("contact export: encoding failed", "user", userID, "format", format, "error", err)
		bookFail(w, r, http.StatusInternalServerError, "contacts.errBookFailed")
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(buf.Bytes())
}
