package carddav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/emersion/go-vcard"
	webdav "github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/carddav"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// Path hierarchy (after stripping /carddav prefix):
// /                               → root (depth 0)
// /{userID}/                      → user principal (depth 1)
// /{userID}/addressbooks/         → address book home set (depth 2)
// /{userID}/addressbooks/default/ → address book (depth 3)
// /{userID}/addressbooks/default/{guid}.vcf → address object (depth 4)

// Backend implements the carddav.Backend interface.
type Backend struct {
	contacts *store.ContactStore
	search   *store.SearchManager
	logger   *slog.Logger
}

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

func bookPath(userID string) string {
	return "/carddav/" + userID + "/addressbooks/default/"
}

func objectPath(userID, guid string) string {
	return "/carddav/" + userID + "/addressbooks/default/" + guid + ".vcf"
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

// ListAddressBooks returns the user's address books (single default book).
func (b *Backend) ListAddressBooks(ctx context.Context) ([]carddav.AddressBook, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}
	return []carddav.AddressBook{
		{
			Path:        bookPath(userID),
			Name:        "Contacts",
			Description: "Personal contacts",
		},
	}, nil
}

// GetAddressBook returns address book metadata.
func (b *Backend) GetAddressBook(ctx context.Context, path string) (*carddav.AddressBook, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}
	return &carddav.AddressBook{
		Path:        bookPath(userID),
		Name:        "Contacts",
		Description: "Personal contacts",
	}, nil
}

// CreateAddressBook is not supported (single default book).
func (b *Backend) CreateAddressBook(ctx context.Context, addressBook *carddav.AddressBook) error {
	return fmt.Errorf("creating additional address books is not supported")
}

// DeleteAddressBook is not supported (single default book).
func (b *Backend) DeleteAddressBook(ctx context.Context, path string) error {
	return fmt.Errorf("deleting the default address book is not supported")
}

// GetAddressObject returns a single contact as a CardDAV address object.
func (b *Backend) GetAddressObject(ctx context.Context, path string, req *carddav.AddressDataRequest) (*carddav.AddressObject, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}

	guid := guidFromPath(path)
	if guid == "" {
		return nil, fmt.Errorf("invalid path")
	}

	contact, err := b.contacts.Get(userID, guid)
	if err != nil {
		return nil, fmt.Errorf("contact not found: %w", err)
	}

	return contactToAddressObject(userID, contact)
}

// ListAddressObjects returns all contacts as CardDAV address objects.
func (b *Backend) ListAddressObjects(ctx context.Context, path string, req *carddav.AddressDataRequest) ([]carddav.AddressObject, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}

	contacts, _, err := b.contacts.List(userID, 0, 0)
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

// PutAddressObject creates or updates a contact from a vCard.
func (b *Backend) PutAddressObject(ctx context.Context, path string, card vcard.Card, opts *carddav.PutAddressObjectOptions) (*carddav.AddressObject, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}

	guid := guidFromPath(path)
	if guid == "" {
		return nil, fmt.Errorf("invalid path")
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

	// Check if contact exists (update vs create).
	existing, _ := b.contacts.Get(userID, guid)

	// Conditional headers are enforced before any write. See the CalDAV
	// backend for the reasoning; the failure was identical here.
	if err := checkPreconditions(opts, existing); err != nil {
		return nil, err
	}

	if existing != nil {
		contact.CreatedAt = existing.CreatedAt
		if err := b.contacts.Update(userID, guid, contact); err != nil {
			return nil, fmt.Errorf("updating contact: %w", err)
		}
		b.logger.Info("carddav: contact updated", "user", userID, "guid", guid)
	} else {
		if _, err := b.contacts.Create(userID, contact); err != nil {
			return nil, fmt.Errorf("creating contact: %w", err)
		}
		b.logger.Info("carddav: contact created", "user", userID, "guid", guid)
	}

	return contactToAddressObject(userID, contact)
}

// DeleteAddressObject removes a contact.
func (b *Backend) DeleteAddressObject(ctx context.Context, path string) error {
	userID := getUserID(ctx)
	if userID == "" {
		return fmt.Errorf("no user in context")
	}

	guid := guidFromPath(path)
	if guid == "" {
		return fmt.Errorf("invalid path")
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
		Path:    objectPath(userID, c.Guid),
		Card:    card,
		ModTime: modTime,
		ETag:    store.ContentETag(c.VcardRaw, c.UpdatedAt),
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
