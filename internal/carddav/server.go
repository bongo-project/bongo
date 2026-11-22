package carddav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-vcard"
	webdav "github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/carddav"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Path hierarchy (after stripping /carddav prefix):
// /                               → root (depth 0)
// /{userID}/                      → user principal (depth 1)
// /{userID}/addressbooks/         → address book home set (depth 2)
// /{userID}/addressbooks/{book}/  → address book (depth 3); {book} is "default"
//                                   or the 32-hex id of one of the user's books
// /{userID}/addressbooks/{book}/{guid}.vcf → address object (depth 4)

// Backend implements the carddav.Backend interface.
type Backend struct {
	contacts *store.ContactStore
	books    *store.AddressBookStore
	search   *store.SearchManager
	logger   *slog.Logger

	// quota gates PUT. Nil means unlimited (dev, tests, edge). DELETE is never
	// gated so a user over quota can still free space.
	quota QuotaChecker

	// storage refuses PUT while the data volume is nearly full. Nil means
	// never refuse. DELETE and reads are not gated.
	storage StorageGate
}

// StorageGate is satisfied by *diskspace.Monitor.
type StorageGate interface {
	Low() bool
}

// ErrStorageLow is wrapped into the 507 Insufficient Storage that a PUT
// returns while the server is short of disk space.
var ErrStorageLow = errors.New("insufficient system storage")

// SetStorageGate installs the free-space gate. Call before serving.
func (b *Backend) SetStorageGate(g StorageGate) { b.storage = g }

// QuotaChecker is satisfied by *store.DomainQuotaStore. PUT asks whether the
// stored record (create) or its growth (update) would push the owner's domain
// over quota, the same question the web handlers ask.
type QuotaChecker interface {
	RecipientWouldExceedQuota(userID string, addBytes int64) (bool, error)
}

// ErrStorageQuotaExceeded is wrapped into the 507 Insufficient Storage that a
// PUT returns when the object would not fit in the owner's domain quota.
var ErrStorageQuotaExceeded = errors.New("storage quota exceeded")

// SetQuotaChecker installs the storage-quota checker. Call before serving.
func (b *Backend) SetQuotaChecker(qc QuotaChecker) { b.quota = qc }

// checkQuota refuses a write that would not fit. addBytes is the stored record
// size for a create or the growth of the record for an update; a PUT that does
// not grow the record (addBytes <= 0) is always allowed, even at quota, because
// clients re-PUT the whole object on every edit and a user at quota must be
// able to shrink or fix it. This matches the web handlers. DELETE is never
// gated. The check is best effort: the usage figure is recomputed periodically
// (about ten minutes stale) and concurrent PUTs can overshoot, so it is not a
// hard limit. A checker error fails open, as on the web paths: a transient
// store error must not turn into data loss for a user's contacts.
func (b *Backend) checkQuota(userID string, addBytes int64) error {
	if b.quota == nil || addBytes <= 0 {
		return nil
	}
	exceeds, err := b.quota.RecipientWouldExceedQuota(userID, addBytes)
	if err != nil {
		b.logger.Warn("quota check error (fail open)", "user", userID, "error", err)
		return nil
	}
	if exceeds {
		return webdav.NewHTTPError(http.StatusInsufficientStorage, ErrStorageQuotaExceeded)
	}
	return nil
}

// SetAddressBooks enables books beyond the default one. Without it the backend
// serves the default book only and MKCOL is refused. Call before serving.
func (b *Backend) SetAddressBooks(books *store.AddressBookStore) { b.books = books }

// NewBackend creates a CardDAV backend.
func NewBackend(contacts *store.ContactStore, search *store.SearchManager, logger *slog.Logger) *Backend {
	return &Backend{contacts: contacts, search: search, logger: logger}
}

type ctxKey string

const userIDCtxKey ctxKey = "carddav-user-id"

// WithUserID adds a userID to the context.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDCtxKey, userID)
}

func getUserID(ctx context.Context) string {
	if v, ok := ctx.Value(userIDCtxKey).(string); ok {
		return v
	}
	return ""
}

func userPath(userID string) string {
	return "/carddav/" + userID + "/"
}

func homeSetPath(userID string) string {
	return "/carddav/" + userID + "/addressbooks/"
}

// bookPath is the collection path of a book: store.DefaultAddressBookID for
// the default book, otherwise the book's id.
func bookPath(userID, bookID string) string {
	return "/carddav/" + userID + "/addressbooks/" + bookID + "/"
}

func objectPath(userID, bookID, guid string) string {
	return "/carddav/" + userID + "/addressbooks/" + bookID + "/" + guid + ".vcf"
}

// parseBookPath splits a request path into the user segment, the book segment
// and, for an address object, its file segment. ok is false for any other
// shape. The segments are returned as sent: callers must compare the user with
// the authenticated one and canonicalise the book.
func parseBookPath(p string) (user, seg, object string, ok bool) {
	rest, found := strings.CutPrefix(p, "/carddav/")
	if !found {
		return "", "", "", false
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) < 3 || len(parts) > 4 || parts[1] != "addressbooks" || parts[0] == "" || parts[2] == "" {
		return "", "", "", false
	}
	if len(parts) == 4 {
		if parts[3] == "" {
			return "", "", "", false
		}
		object = parts[3]
	}
	return parts[0], parts[2], object, true
}

func notFound(msg string) error {
	return webdav.NewHTTPError(http.StatusNotFound, errors.New(msg))
}

// resolveBook maps a request path to the authenticated user and one of their
// books. The path's user segment must be the authenticated user, and the book
// is read from that user's own keys, so another user's book id answers 404 and
// never reaches their data. The default book needs no record. A missing book
// wraps store.ErrAddressBookNotFound.
func (b *Backend) resolveBook(ctx context.Context, p string) (userID, bookID, object string, err error) {
	userID = getUserID(ctx)
	if userID == "" {
		return "", "", "", fmt.Errorf("no user in context")
	}
	user, seg, object, ok := parseBookPath(p)
	if !ok || user != userID {
		return "", "", "", notFound("not found")
	}
	bookID, ok = store.CanonicalAddressBookSegment(seg)
	if !ok {
		return "", "", "", notFound("not found")
	}
	if bookID == store.DefaultAddressBookID {
		return userID, bookID, object, nil
	}
	if b.books == nil {
		return "", "", "", notFound("not found")
	}
	if _, err := b.books.Get(userID, bookID); err != nil {
		if errors.Is(err, store.ErrAddressBookNotFound) {
			return "", "", "", webdav.NewHTTPError(http.StatusNotFound, err)
		}
		return "", "", "", fmt.Errorf("reading address book: %w", err)
	}
	return userID, bookID, object, nil
}

// resolveObject is resolveBook for a path that must name an address object.
func (b *Backend) resolveObject(ctx context.Context, p string) (userID, bookID, guid string, err error) {
	userID, bookID, object, err := b.resolveBook(ctx, p)
	if err != nil {
		return "", "", "", err
	}
	guid = guidFromPath(object)
	if object == "" || guid == "" {
		return "", "", "", notFound("not found")
	}
	return userID, bookID, guid, nil
}

// CurrentUserPrincipal returns the principal path for the current user.
func (b *Backend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return "", fmt.Errorf("no user in context")
	}
	return userPath(userID), nil
}

// AddressBookHomeSetPath returns the address book home set path.
func (b *Backend) AddressBookHomeSetPath(ctx context.Context) (string, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return "", fmt.Errorf("no user in context")
	}
	return homeSetPath(userID), nil
}

func defaultAddressBook(userID string) carddav.AddressBook {
	return carddav.AddressBook{
		Path:        bookPath(userID, store.DefaultAddressBookID),
		Name:        "Contacts",
		Description: "Personal contacts",
	}
}

func toDAVAddressBook(userID string, ab *bongov1.AddressBook) carddav.AddressBook {
	return carddav.AddressBook{
		Path:        bookPath(userID, ab.Id),
		Name:        ab.Name,
		Description: ab.Description,
	}
}

// ListAddressBooks returns the default book and every book the user owns.
func (b *Backend) ListAddressBooks(ctx context.Context) ([]carddav.AddressBook, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}
	out := []carddav.AddressBook{defaultAddressBook(userID)}
	if b.books == nil {
		return out, nil
	}
	books, err := b.books.List(userID)
	if err != nil {
		return nil, fmt.Errorf("listing address books: %w", err)
	}
	for _, ab := range books {
		out = append(out, toDAVAddressBook(userID, ab))
	}
	return out, nil
}

// GetAddressBook returns address book metadata, or 404 for a book the user
// does not own.
func (b *Backend) GetAddressBook(ctx context.Context, path string) (*carddav.AddressBook, error) {
	userID, bookID, object, err := b.resolveBook(ctx, path)
	if err != nil {
		return nil, err
	}
	if object != "" {
		return nil, notFound("not found")
	}
	if bookID == store.DefaultAddressBookID {
		ab := defaultAddressBook(userID)
		return &ab, nil
	}
	rec, err := b.books.Get(userID, bookID)
	if err != nil {
		return nil, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	ab := toDAVAddressBook(userID, rec)
	return &ab, nil
}

// CreateAddressBook creates the book named by the last segment of addressBook.Path
// (MKCOL). The stored id is the canonical form of the segment, so a client that
// creates a collection under an upper-case or hyphenated UUID can keep using
// its own URL: every lookup canonicalises the same way. It never overwrites.
// The count check is serialised per node only (see AddressBookStore.Create).
func (b *Backend) CreateAddressBook(ctx context.Context, addressBook *carddav.AddressBook) error {
	userID := getUserID(ctx)
	if userID == "" {
		return fmt.Errorf("no user in context")
	}
	user, seg, object, ok := parseBookPath(addressBook.Path)
	if !ok || object != "" || user != userID {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("address books are created under your own home set"))
	}
	id, ok := store.CanonicalAddressBookSegment(seg)
	if !ok {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("invalid address book name"))
	}
	if id == store.DefaultAddressBookID || b.books == nil {
		return webdav.NewHTTPError(http.StatusMethodNotAllowed, errors.New("address book already exists"))
	}
	name := store.CleanAddressBookText(addressBook.Name, store.MaxAddressBookNameLen)
	if name == "" {
		name = "Contacts"
	}
	desc := store.CleanAddressBookText(addressBook.Description, store.MaxAddressBookDescLen)

	_, err := b.books.Create(userID, id, name, desc)
	switch {
	case err == nil:
		b.logger.Info("carddav: address book created", "user", userID, "book", id)
		return nil
	case errors.Is(err, store.ErrAddressBookExists):
		return webdav.NewHTTPError(http.StatusMethodNotAllowed, err)
	case errors.Is(err, store.ErrAddressBookLimit):
		return webdav.NewHTTPError(http.StatusInsufficientStorage, err)
	}
	return fmt.Errorf("creating address book: %w", err)
}

// DeleteAddressBook deletes a book and its contacts. The default book answers
// 403 and an unknown one 404. The delete is resumable and not atomic: see
// store.PurgeAddressBook. A failure part way answers 500 and leaves the book in
// place, so repeating the DELETE finishes the job.
func (b *Backend) DeleteAddressBook(ctx context.Context, path string) error {
	userID, bookID, object, err := b.resolveBook(ctx, path)
	if err != nil {
		return err
	}
	if object != "" {
		return notFound("not found")
	}
	if bookID == store.DefaultAddressBookID {
		return webdav.NewHTTPError(http.StatusForbidden, store.ErrAddressBookProtected)
	}
	err = store.PurgeAddressBook(b.books, b.contacts, userID, bookID)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrAddressBookNotFound):
		return webdav.NewHTTPError(http.StatusNotFound, err)
	case errors.Is(err, store.ErrAddressBookGrantsNotRevoked):
		// The book is gone; the leader's orphan sweep removes what is left.
		b.logger.Warn("carddav: address book deleted but grants not revoked", "user", userID, "book", bookID, "error", err)
	default:
		b.logger.Warn("carddav: address book delete failed", "user", userID, "book", bookID, "error", err)
		return err
	}
	b.logger.Info("carddav: address book deleted", "user", userID, "book", bookID)
	return nil
}

// GetAddressObject returns a single contact as a CardDAV address object. A
// contact filed in another book answers 404 under this path.
func (b *Backend) GetAddressObject(ctx context.Context, path string, req *carddav.AddressDataRequest) (*carddav.AddressObject, error) {
	userID, bookID, guid, err := b.resolveObject(ctx, path)
	if err != nil {
		return nil, err
	}

	contact, err := b.contacts.Get(userID, guid)
	if err != nil || store.ContactBook(contact) != bookID {
		return nil, webdav.NewHTTPError(http.StatusNotFound, errors.New("contact not found"))
	}

	return contactToAddressObject(userID, contact)
}

// ListAddressObjects returns the contacts of the book the path names.
func (b *Backend) ListAddressObjects(ctx context.Context, path string, req *carddav.AddressDataRequest) ([]carddav.AddressObject, error) {
	userID, bookID, object, err := b.resolveBook(ctx, path)
	if err != nil {
		return nil, err
	}
	if object != "" {
		return nil, notFound("not found")
	}

	contacts, _, err := b.contacts.ListByBook(userID, bookID, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("listing contacts: %w", err)
	}

	var objects []carddav.AddressObject
	for _, c := range contacts {
		obj, err := contactToAddressObject(userID, c)
		if err != nil {
			continue
		}
		objects = append(objects, *obj)
	}
	return objects, nil
}

// QueryAddressObjects handles search queries against contacts.
func (b *Backend) QueryAddressObjects(ctx context.Context, path string, query *carddav.AddressBookQuery) ([]carddav.AddressObject, error) {
	objects, err := b.ListAddressObjects(ctx, path, &query.DataRequest)
	if err != nil {
		return nil, err
	}
	return carddav.Filter(query, objects)
}

// PutAddressObject creates or updates a contact from a vCard. The book is
// resolved first, so a PUT into a book that does not exist (or that a
// concurrent DELETE has just removed) answers 409 before anything is written.
// A GUID that already exists in another book answers 409: a PUT never moves a
// contact. An update keeps the contact's book.
func (b *Backend) PutAddressObject(ctx context.Context, path string, card vcard.Card, opts *carddav.PutAddressObjectOptions) (*carddav.AddressObject, error) {
	if b.storage != nil && b.storage.Low() {
		return nil, webdav.NewHTTPError(http.StatusInsufficientStorage, ErrStorageLow)
	}
	userID, bookID, guid, err := b.resolveObject(ctx, path)
	if err != nil {
		if errors.Is(err, store.ErrAddressBookNotFound) {
			return nil, webdav.NewHTTPError(http.StatusConflict, err)
		}
		return nil, err
	}

	// Encode vCard to raw bytes.
	var rawBuf bytes.Buffer
	if err := vcard.NewEncoder(&rawBuf).Encode(card); err != nil {
		return nil, fmt.Errorf("encoding vcard: %w", err)
	}

	contact := &bongov1.Contact{
		Guid:        guid,
		DisplayName: card.PreferredValue(vcard.FieldFormattedName),
		Email:       card.PreferredValue(vcard.FieldEmail),
		Phone:       card.PreferredValue(vcard.FieldTelephone),
		Company:     card.PreferredValue(vcard.FieldOrganization),
		Notes:       card.PreferredValue(vcard.FieldNote),
		VcardRaw:    rawBuf.Bytes(),
	}
	if bookID != store.DefaultAddressBookID {
		contact.AddressbookId = bookID
	}

	// Check if contact exists (update vs create).
	existing, _ := b.contacts.Get(userID, guid)
	if existing != nil && store.ContactBook(existing) != bookID {
		return nil, webdav.NewHTTPError(http.StatusConflict, errors.New("a contact with this id exists in another address book"))
	}

	// Conditional headers are enforced before any write. See the CalDAV
	// backend for the reasoning; the failure was identical here.
	if err := checkPreconditions(opts, existing); err != nil {
		return nil, err
	}

	// Size the write with the quantity the usage recompute counts: the stored
	// record. A create is the whole record; an update is its growth.
	var addBytes int64
	if existing != nil {
		contact.CreatedAt = existing.CreatedAt
		contact.UpdatedAt = time.Now().Unix()
		addBytes = int64(proto.Size(contact) - proto.Size(existing))
	} else {
		addBytes = store.ContactRecordSize(contact)
	}
	if err := b.checkQuota(userID, addBytes); err != nil {
		return nil, err
	}

	if existing != nil {
		if err := b.contacts.Update(userID, guid, contact); err != nil {
			if errors.Is(err, store.ErrContactGroupTooLarge) {
				return nil, webdav.NewHTTPError(http.StatusForbidden, err)
			}
			return nil, fmt.Errorf("updating contact: %w", err)
		}
		b.logger.Info("carddav: contact updated", "user", userID, "guid", guid)
	} else {
		if _, err := b.contacts.Create(userID, contact); err != nil {
			if errors.Is(err, store.ErrContactGroupTooLarge) {
				return nil, webdav.NewHTTPError(http.StatusForbidden, err)
			}
			return nil, fmt.Errorf("creating contact: %w", err)
		}
		b.logger.Info("carddav: contact created", "user", userID, "guid", guid)
	}

	return contactToAddressObject(userID, contact)
}

// DeleteAddressObject removes a contact. A contact filed in another book
// answers 404 under this path.
func (b *Backend) DeleteAddressObject(ctx context.Context, path string) error {
	userID, bookID, guid, err := b.resolveObject(ctx, path)
	if err != nil {
		return err
	}

	existing, err := b.contacts.Get(userID, guid)
	if err == nil && store.ContactBook(existing) != bookID {
		return webdav.NewHTTPError(http.StatusNotFound, errors.New("contact not found"))
	}

	if err := b.contacts.Delete(userID, guid); err != nil {
		b.logger.Warn("carddav: delete contact failed", "user", userID, "guid", guid, "error", err)
		return err
	}
	b.logger.Info("carddav: contact deleted", "user", userID, "guid", guid)
	return nil
}

// contactToAddressObject converts a Contact proto to a CardDAV AddressObject.
func contactToAddressObject(userID string, c *bongov1.Contact) (*carddav.AddressObject, error) {
	var card vcard.Card

	if len(c.VcardRaw) > 0 {
		dec := vcard.NewDecoder(bytes.NewReader(c.VcardRaw))
		var err error
		card, err = dec.Decode()
		if err != nil {
			card = nil
		}
	}

	if card == nil {
		card = make(vcard.Card)
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
	}

	modTime := time.Unix(c.UpdatedAt, 0)
	if c.UpdatedAt == 0 {
		modTime = time.Unix(c.CreatedAt, 0)
	}

	return &carddav.AddressObject{
		Path:    objectPath(userID, store.ContactBook(c), c.Guid),
		Card:    card,
		ModTime: modTime,
		ETag:    store.ContentETagValue(c.VcardRaw, c.UpdatedAt),
	}, nil
}

// guidFromPath extracts the GUID from a CardDAV path.
func guidFromPath(path string) string {
	if len(path) > 4 && path[len(path)-4:] == ".vcf" {
		path = path[:len(path)-4]
	}
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// ErrPreconditionFailed is the cause wrapped into every 412 this backend
// returns. go-webdav's HTTPError lives in its internal package, so its status
// code cannot be inspected from outside the library; wrapping a sentinel gives
// callers and tests something stable to match with errors.Is without depending
// on message text.
var ErrPreconditionFailed = errors.New("precondition failed")

// checkPreconditions enforces If-Match and If-None-Match for a PUT.
//
// If-None-Match: * is create-only; If-Match pins the version being replaced.
// Both answer 412 so the client re-reads and retries instead of silently
// overwriting another client's edit. A malformed condition fails rather than
// being ignored: a client that sent a condition it meant must never have the
// write applied unconditionally.
func checkPreconditions(opts *carddav.PutAddressObjectOptions, existing *bongov1.Contact) error {
	if opts == nil {
		return nil
	}

	if opts.IfNoneMatch.IsSet() {
		if !opts.IfNoneMatch.IsWildcard() {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: If-None-Match supports only *", ErrPreconditionFailed))
		}
		if existing != nil {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: If-None-Match: * but the object already exists", ErrPreconditionFailed))
		}
	}

	if opts.IfMatch.IsSet() {
		if existing == nil {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: If-Match on an object that does not exist", ErrPreconditionFailed))
		}
		want := store.ContentETagValue(existing.VcardRaw, existing.UpdatedAt)
		ok, err := opts.IfMatch.MatchETag(want)
		if err != nil {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: malformed If-Match: %v", ErrPreconditionFailed, err))
		}
		if !ok {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: If-Match does not match the current object", ErrPreconditionFailed))
		}
	}

	return nil
}
