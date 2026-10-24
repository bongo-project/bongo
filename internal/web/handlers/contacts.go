package handlers

import (
	"bytes"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/emersion/go-vcard"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// qpHexPattern matches sequences like =4A=6F=68=6E (3+ consecutive QP-encoded bytes).
var qpHexPattern = regexp.MustCompile(`(?:=[0-9A-Fa-f]{2}){3,}`)

const contactsPerPage = 25

// ContactsHandler handles contact management endpoints.
type ContactsHandler struct {
	contacts *store.ContactStore
	search   *store.SearchManager
	quota    QuotaChecker // nil = unlimited
}

// NewContactsHandler creates a ContactsHandler.
func NewContactsHandler(contacts *store.ContactStore, search *store.SearchManager) *ContactsHandler {
	return &ContactsHandler{contacts: contacts, search: search}
}

// SetQuotaChecker enables the domain storage-quota check on contact writes.
func (h *ContactsHandler) SetQuotaChecker(qc QuotaChecker) { h.quota = qc }

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

	contact := &bongov1.Contact{}
	// Build the full vCard from the edit form (also sets the derived quick fields).
	buildVCardFromForm(contact, r)

	// Best-effort domain quota (usage is a periodic recompute). The estimate is
	// the stored record size, the same quantity the recompute counts.
	if quotaExceeded(h.quota, nil, userID, store.ContactRecordSize(contact)) {
		refuseQuota(w, r)
		return
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

// ShowContactDetail renders the contact detail partial for the HTMX reading pane.
func (h *ContactsHandler) ShowContactDetail(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	contact, err := h.contacts.Get(userID, guid)
	if err != nil {
		http.Error(w, "contact not found", http.StatusNotFound)
		return
	}

	templ.Handler(templates.ContactDetailPartial(contact)).ServeHTTP(w, r)
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

	// Rebuild the stored vCard from the full edit form (all fields), preserving
	// any properties the form does not cover, and refresh the derived quick fields.
	oldSize := proto.Size(existing)
	buildVCardFromForm(existing, r)

	// At or over quota an update is allowed only if the stored record does not grow.
	// This is the same rule CardDAV PUT applies: a full user can still edit or
	// shrink what they have.
	if growth := int64(proto.Size(existing) - oldSize); growth > 0 && quotaExceeded(h.quota, nil, userID, growth) {
		refuseQuota(w, r)
		return
	}

	if err := h.contacts.Update(userID, guid, existing); err != nil {
		http.Error(w, "failed to update contact", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/contacts/"+url.PathEscape(guid)+"?flash=Contact+updated", http.StatusSeeOther)
}

// DeleteContact handles DELETE to remove a contact.
func (h *ContactsHandler) DeleteContact(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	if err := h.contacts.Delete(userID, guid); err != nil {
		http.Error(w, "failed to delete contact", http.StatusInternalServerError)
		return
	}

	// HTMX request: redirect to reload the contacts list.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/contacts?flash=Contact+deleted")
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
	returnTo := safeContactReturnPath(r.FormValue("return_to"))

	if fromEmail == "" {
		http.Redirect(w, r, returnTo, http.StatusSeeOther) // #nosec G710 -- returnTo is constrained by safeContactReturnPath
		return
	}

	existing, _ := h.contacts.FindByEmail(userID, fromEmail)
	if existing != nil {
		if returnTo != "" {
			http.Redirect(w, r, returnTo+"?flash=Already+in+contacts", http.StatusSeeOther) // #nosec G710 -- returnTo is constrained by safeContactReturnPath
			return
		}
		http.Redirect(w, r, "/contacts?flash=Already+in+contacts", http.StatusSeeOther)
		return
	}

	contact := &bongov1.Contact{
		DisplayName: fromName,
		Email:       fromEmail,
	}
	syncQuickFieldsToVCard(contact)
	if quotaExceeded(h.quota, nil, userID, store.ContactRecordSize(contact)) {
		refuseQuotaFlash(w, r, returnTo)
		return
	}
	if _, err := h.contacts.Create(userID, contact); err != nil {
		http.Error(w, "failed to add contact", http.StatusInternalServerError)
		return
	}

	if returnTo != "" {
		http.Redirect(w, r, returnTo+"?flash=Contact+added", http.StatusSeeOther) // #nosec G710 -- returnTo is constrained by safeContactReturnPath
		return
	}
	http.Redirect(w, r, "/contacts?flash=Contact+added", http.StatusSeeOther)
}

func safeContactReturnPath(raw string) string {
	if raw == "" {
		return "/contacts"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return "/contacts"
	}
	return u.RequestURI()
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
		_ = enc.Encode(card)
	}

	w.Header().Set("Content-Type", "text/vcard")
	w.Header().Set("Content-Disposition", `attachment; filename="contacts.vcf"`)
	if _, err := w.Write(buf.Bytes()); err != nil {
		return
	}
}

// ImportContacts uploads and parses a .vcf file to create contacts.
func (h *ContactsHandler) ImportContacts(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	if !parseUploadForm(w, r, contactsImportMaxBytes, "/contacts") {
		return
	}
	defer removeUploadTemp(r)
	file, _, err := r.FormFile("vcf")
	if err != nil {
		http.Redirect(w, r, "/contacts?flash=No+file+uploaded", http.StatusSeeOther)
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		http.Redirect(w, r, "/contacts?flash=Failed+to+read+file", http.StatusSeeOther)
		return
	}

	// All or nothing, best effort: refuse the whole upload up front when it
	// would not fit, rather than importing part of it. The upload size is an
	// approximation: the stored size is roughly the raw bytes plus the copied
	// quick fields.
	if quotaExceeded(h.quota, nil, userID, int64(len(raw))) {
		refuseQuotaFlash(w, r, "/contacts")
		return
	}

	// Split into individual vCard blocks for resilient parsing.
	// Each block that fails to parse is skipped instead of aborting the whole import.
	blocks := splitVCardBlocks(raw)

	imported := 0
	withEmail := 0
	withPhone := 0
	skipped := 0

	for _, block := range blocks {
		contact, err := parseVCardBlock(block)
		if err != nil {
			skipped++
			continue
		}

		if contact.DisplayName == "" && contact.Email == "" {
			skipped++
			continue
		}

		if _, err := h.contacts.Create(userID, contact); err != nil {
			skipped++
			continue
		}
		imported++
		if contact.Email != "" {
			withEmail++
		}
		if contact.Phone != "" {
			withPhone++
		}
	}

	flash := fmt.Sprintf("Imported %d contacts (%d with email, %d with phone)", imported, withEmail, withPhone)
	if skipped > 0 {
		flash = fmt.Sprintf("%s, %d skipped", flash, skipped)
	}

	http.Redirect(w, r, "/contacts?flash="+strings.ReplaceAll(flash, " ", "+"), http.StatusSeeOther)
}

// splitVCardBlocks splits raw VCF data into individual BEGIN:VCARD...END:VCARD blocks.
func splitVCardBlocks(data []byte) [][]byte {
	var blocks [][]byte
	text := string(data)

	for {
		beginIdx := strings.Index(strings.ToUpper(text), "BEGIN:VCARD")
		if beginIdx < 0 {
			break
		}
		endIdx := strings.Index(strings.ToUpper(text[beginIdx:]), "END:VCARD")
		if endIdx < 0 {
			break
		}
		// endIdx is relative to beginIdx; add length of "END:VCARD"
		blockEnd := beginIdx + endIdx + len("END:VCARD")
		blocks = append(blocks, []byte(text[beginIdx:blockEnd]))
		text = text[blockEnd:]
	}
	return blocks
}

// parseVCardBlock parses a single vCard block into a Contact protobuf.
func parseVCardBlock(block []byte) (*bongov1.Contact, error) {
	dec := vcard.NewDecoder(bytes.NewReader(block))
	card, err := dec.Decode()
	if err != nil {
		return nil, fmt.Errorf("decode vcard: %w", err)
	}

	displayName := decodeVCardField(card, vcard.FieldFormattedName)
	if displayName == "" {
		displayName = buildNameFromN(card)
	}

	email := extractFirstEmail(card)
	phone := extractFirstPhone(card)
	company := decodeVCardField(card, vcard.FieldOrganization)
	notes := decodeVCardField(card, vcard.FieldNote)

	// Store raw vCard for round-trip fidelity.
	var rawBuf bytes.Buffer
	_ = vcard.NewEncoder(&rawBuf).Encode(card)

	return &bongov1.Contact{
		DisplayName: displayName,
		Email:       email,
		Phone:       phone,
		Company:     company,
		Notes:       notes,
		VcardRaw:    rawBuf.Bytes(),
	}, nil
}

// syncQuickFieldsToVCard re-encodes the contact's stored vCard so the quick
// fields editable in the web UI (formatted name, primary email/phone, company,
// note) are reflected in the vCard served to CardDAV clients, while preserving
// every other property the vCard already carried (additional emails/phones,
// postal address, birthday, title, etc.). Without this, web-UI edits silently
// diverge from what CardDAV clients see, since CardDAV serves the raw vCard.
func syncQuickFieldsToVCard(c *bongov1.Contact) {
	var card vcard.Card
	if len(c.VcardRaw) > 0 {
		if decoded, err := vcard.NewDecoder(bytes.NewReader(c.VcardRaw)).Decode(); err == nil {
			card = decoded
		}
	}
	if card == nil {
		card = make(vcard.Card)
		card.SetValue(vcard.FieldVersion, "3.0")
	}

	card.SetValue(vcard.FieldFormattedName, c.DisplayName)
	setPreferredVCardValue(card, vcard.FieldEmail, c.Email)
	setPreferredVCardValue(card, vcard.FieldTelephone, c.Phone)
	setPreferredVCardValue(card, vcard.FieldOrganization, c.Company)
	if c.Notes != "" {
		card.SetValue(vcard.FieldNote, c.Notes)
	} else {
		delete(card, vcard.FieldNote)
	}

	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err == nil {
		c.VcardRaw = buf.Bytes()
	}
}

// buildVCardFromForm rebuilds a contact's stored vCard from the full edit form.
// It merges into the existing vCard so properties the form does not model (photo,
// IMPP, custom X- fields, etc.) are preserved, replaces every field the form does
// cover, and refreshes the derived quick fields used by the list and search index.
// Form fields: display_name; email[]/email_type[]; phone[]/phone_type[]; company;
// title; adr_street/adr_city/adr_region/adr_postal/adr_country; birthday; website;
// nickname; timezone; notes.
func buildVCardFromForm(c *bongov1.Contact, r *http.Request) {
	_ = r.ParseForm()

	var card vcard.Card
	if len(c.VcardRaw) > 0 {
		if decoded, err := vcard.NewDecoder(bytes.NewReader(c.VcardRaw)).Decode(); err == nil {
			card = decoded
		}
	}
	if card == nil {
		card = make(vcard.Card)
		card.SetValue(vcard.FieldVersion, "3.0")
	}

	fn := strings.TrimSpace(r.FormValue("display_name"))
	card.SetValue(vcard.FieldFormattedName, fn)

	// Multi-valued fields: replace the whole set from the form's parallel arrays.
	replaceTypedVCardFields(card, vcard.FieldEmail, r.Form["email"], r.Form["email_type"])
	replaceTypedVCardFields(card, vcard.FieldTelephone, r.Form["phone"], r.Form["phone_type"])

	setOrDeleteVCardValue(card, vcard.FieldOrganization, r.FormValue("company"))
	setOrDeleteVCardValue(card, vcard.FieldTitle, r.FormValue("title"))
	setOrDeleteVCardValue(card, vcard.FieldBirthday, r.FormValue("birthday"))
	setOrDeleteVCardValue(card, vcard.FieldURL, r.FormValue("website"))
	setOrDeleteVCardValue(card, vcard.FieldNickname, r.FormValue("nickname"))
	setOrDeleteVCardValue(card, vcard.FieldTimezone, r.FormValue("timezone"))
	setOrDeleteVCardValue(card, vcard.FieldNote, r.FormValue("notes"))

	// Postal addresses (multi-valued; each keeps its optional type).
	delete(card, vcard.FieldAddress)
	streets := r.Form["adr_street"]
	cities := r.Form["adr_city"]
	regions := r.Form["adr_region"]
	postals := r.Form["adr_postal"]
	countries := r.Form["adr_country"]
	adrTypes := r.Form["adr_type"]
	at := func(a []string, i int) string {
		if i < len(a) {
			return strings.TrimSpace(a[i])
		}
		return ""
	}
	for i := range streets {
		adr := &vcard.Address{
			StreetAddress: at(streets, i),
			Locality:      at(cities, i),
			Region:        at(regions, i),
			PostalCode:    at(postals, i),
			Country:       at(countries, i),
		}
		if adr.StreetAddress == "" && adr.Locality == "" && adr.Region == "" && adr.PostalCode == "" && adr.Country == "" {
			continue
		}
		// Pre-seed the embedded Field so AddAddress preserves the TYPE param.
		if typ := at(adrTypes, i); typ != "" {
			adr.Field = &vcard.Field{Params: vcard.Params{vcard.ParamType: []string{typ}}}
		}
		card.AddAddress(adr)
	}

	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err == nil {
		c.VcardRaw = buf.Bytes()
	}

	// Derived quick fields (list, search, SQLite index).
	c.DisplayName = fn
	c.Email = card.PreferredValue(vcard.FieldEmail)
	c.Phone = card.PreferredValue(vcard.FieldTelephone)
	c.Company = card.PreferredValue(vcard.FieldOrganization)
	c.Notes = card.Value(vcard.FieldNote)
}

// replaceTypedVCardFields replaces all values of a multi-valued field from
// parallel value/type arrays, skipping blank values.
func replaceTypedVCardFields(card vcard.Card, field string, values, types []string) {
	delete(card, field)
	for i, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		f := &vcard.Field{Value: v}
		if i < len(types) {
			if typ := strings.TrimSpace(types[i]); typ != "" {
				f.Params = vcard.Params{vcard.ParamType: []string{typ}}
			}
		}
		card.Add(field, f)
	}
}

// setOrDeleteVCardValue sets a single-valued field, or removes it when blank.
func setOrDeleteVCardValue(card vcard.Card, field, value string) {
	if value = strings.TrimSpace(value); value == "" {
		delete(card, field)
		return
	}
	card.SetValue(field, value)
}

// setPreferredVCardValue updates the preferred value of a (possibly multi-valued)
// vCard field in place so other values are preserved, adds the field if absent,
// or drops it when the new value is empty and it was the only value.
func setPreferredVCardValue(card vcard.Card, field, value string) {
	if pref := card.Preferred(field); pref != nil {
		if value == "" && len(card[field]) <= 1 {
			delete(card, field)
			return
		}
		pref.Value = value
		return
	}
	if value != "" {
		card.Add(field, &vcard.Field{Value: value})
	}
}

// buildNameFromN constructs a display name from the structured N field.
// N format: Family;Given;Additional;Prefix;Suffix
func buildNameFromN(card vcard.Card) string {
	n := decodeVCardField(card, vcard.FieldName)
	if n == "" {
		return ""
	}
	parts := strings.Split(n, ";")
	var nameParts []string
	// Given name first, then family name.
	if len(parts) > 1 && parts[1] != "" {
		nameParts = append(nameParts, strings.TrimSpace(parts[1]))
	}
	if len(parts) > 0 && parts[0] != "" {
		nameParts = append(nameParts, strings.TrimSpace(parts[0]))
	}
	return strings.Join(nameParts, " ")
}

// decodeVCardField extracts a field value, decoding QP if needed.
// Handles exact key match, case-insensitive match, and vCard 2.1
// bare-parameter keys like "FN;ENCODING=QUOTED-PRINTABLE".
func decodeVCardField(card vcard.Card, field string) string {
	// Try standard library lookup first (exact key match).
	if fields, ok := card[field]; ok && len(fields) > 0 {
		val := fields[0].Value
		if val != "" {
			return decodeQPIfNeeded(val, fields)
		}
	}
	// Scan all keys: case-insensitive exact match or prefix match for
	// vCard 2.1 keys with bare params (e.g., "FN;ENCODING=QUOTED-PRINTABLE").
	upperField := strings.ToUpper(field)
	for k, fields := range card {
		upper := strings.ToUpper(k)
		if (upper == upperField || strings.HasPrefix(upper, upperField+";")) && len(fields) > 0 {
			val := fields[0].Value
			if val != "" {
				return decodeQPIfNeeded(val, fields)
			}
		}
	}
	return ""
}

// extractFirstEmail extracts the first email from a vCard.
// Handles standard EMAIL, EMAIL;TYPE=WORK, and vCard 2.1 EMAIL;INTERNET keys.
func extractFirstEmail(card vcard.Card) string {
	// Try standard field name first (exact match).
	if fields, ok := card[vcard.FieldEmail]; ok && len(fields) > 0 {
		for _, f := range fields {
			val := decodeQPIfNeeded(f.Value, []*vcard.Field{f})
			val = strings.TrimSpace(val)
			if val != "" {
				return val
			}
		}
	}
	// Scan all keys: case-insensitive exact match or prefix match for
	// vCard 2.1 bare-parameter keys like "EMAIL;INTERNET", "EMAIL;INTERNET;WORK".
	for k, fields := range card {
		upper := strings.ToUpper(k)
		if (upper == "EMAIL" || strings.HasPrefix(upper, "EMAIL;")) && len(fields) > 0 {
			for _, f := range fields {
				val := decodeQPIfNeeded(f.Value, []*vcard.Field{f})
				val = strings.TrimSpace(val)
				if val != "" {
					return val
				}
			}
		}
	}
	return ""
}

// extractFirstPhone extracts the first phone from a vCard.
// Handles standard TEL, TEL;TYPE=CELL, and vCard 2.1 TEL;HOME, TEL;CELL keys.
func extractFirstPhone(card vcard.Card) string {
	// Try standard field name first (exact match).
	if fields, ok := card[vcard.FieldTelephone]; ok && len(fields) > 0 {
		for _, f := range fields {
			val := decodeQPIfNeeded(f.Value, []*vcard.Field{f})
			val = strings.TrimSpace(val)
			if val != "" {
				return val
			}
		}
	}
	// Scan all keys: case-insensitive exact match or prefix match for
	// vCard 2.1 bare-parameter keys like "TEL;HOME", "TEL;CELL;VOICE".
	for k, fields := range card {
		upper := strings.ToUpper(k)
		if (upper == "TEL" || strings.HasPrefix(upper, "TEL;")) && len(fields) > 0 {
			for _, f := range fields {
				val := decodeQPIfNeeded(f.Value, []*vcard.Field{f})
				val = strings.TrimSpace(val)
				if val != "" {
					return val
				}
			}
		}
	}
	return ""
}

// decodeQPIfNeeded decodes quoted-printable encoding if the field has
// ENCODING=QUOTED-PRINTABLE parameter (common in vCard 2.1).
// vCard 2.1 uses bare params like "N;ENCODING=QUOTED-PRINTABLE:" or even
// "N;QUOTED-PRINTABLE:" while 3.0/4.0 should not use QP at all.
func decodeQPIfNeeded(val string, fields []*vcard.Field) string {
	if val == "" {
		return val
	}

	isQP := fieldHasQPEncoding(fields)

	// Heuristic fallback: detect QP by looking for 3+ consecutive =XX hex sequences.
	// This catches cases where the go-vcard library strips the ENCODING param
	// but leaves the value un-decoded.
	if !isQP {
		isQP = qpHexPattern.MatchString(val)
	}

	if !isQP {
		return val
	}

	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(val)))
	if err != nil {
		// Partial decode is better than raw hex. Try to return what we got.
		return val
	}
	return string(decoded)
}

// fieldHasQPEncoding checks whether any of the given fields have a
// QUOTED-PRINTABLE encoding parameter. Handles both vCard 2.1 style
// (bare "QUOTED-PRINTABLE" param or "ENCODING=QUOTED-PRINTABLE") and
// vCard 3.0 style ("ENCODING" key with "QUOTED-PRINTABLE" value).
func fieldHasQPEncoding(fields []*vcard.Field) bool {
	for _, f := range fields {
		if f == nil {
			continue
		}
		// Check ENCODING param explicitly (vCard 3.0+).
		if enc := f.Params.Get("ENCODING"); strings.EqualFold(enc, "QUOTED-PRINTABLE") {
			return true
		}
		// Scan all param values for bare "QUOTED-PRINTABLE" (vCard 2.1).
		for paramKey, paramValues := range f.Params {
			// vCard 2.1 bare params: the library may store "QUOTED-PRINTABLE"
			// as a param key with empty values, or as a value under "ENCODING".
			if strings.EqualFold(paramKey, "QUOTED-PRINTABLE") {
				return true
			}
			for _, v := range paramValues {
				if strings.EqualFold(v, "QUOTED-PRINTABLE") {
					return true
				}
			}
		}
	}
	return false
}
