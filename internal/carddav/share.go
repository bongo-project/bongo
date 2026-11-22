package carddav

import (
	"strings"
	"time"

	"github.com/emersion/go-webdav/carddav"
	"github.com/google/uuid"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// BookGrantReader is the part of the grant store the backend consults. It is
// read on every request: authorisation is never cached across requests.
type BookGrantReader interface {
	AddressBookLevel(owner, book, userID string) (string, error)
	AddressBooksSharedWith(userID string, now time.Time) ([]store.HeldShare, error)
	UserEmail(userID string) string
}

// SetGrants enables shared address books. Without it the backend serves only
// the authenticated user's own books and every shared path answers 404.
func (b *Backend) SetGrants(g BookGrantReader) { b.grants = g }

// sharedPrefix starts the collection name of a book shared with the user:
// shared-{owner}-{book}. A user id is a 36-character UUID, which contains "-",
// so the owner is the fixed-width part and the book the rest.
const sharedPrefix = "shared-"

const uuidLen = 36

func sharedSegment(owner, bookID string) string { return sharedPrefix + owner + "-" + bookID }

// parseSharedBookSegment splits a shared collection name. The owner must be a
// UUID in its lower-case canonical form (a user id is stored that way; CalDAV is
// laxer) and the book exactly the stored 32 lower-case hex form, never
// "default" and never a canonicalised spelling, so a crafted segment can
// neither steer a key prefix nor name an owner that is not a user id.
func parseSharedBookSegment(seg string) (owner, bookID string, ok bool) {
	rest, found := strings.CutPrefix(seg, sharedPrefix)
	if !found || len(rest) <= uuidLen+1 || rest[uuidLen] != '-' {
		return "", "", false
	}
	owner, bookID = rest[:uuidLen], rest[uuidLen+1:]
	u, err := uuid.Parse(owner)
	if err != nil || u.String() != owner || !store.ValidAddressBookID(bookID) {
		return "", "", false
	}
	return owner, bookID, true
}

// sharedDAVAddressBook names a shared book "{name} ({owner address})" under the
// recipient's home set, as CalDAV names a shared calendar.
func (b *Backend) sharedDAVAddressBook(userID, owner string, ab *bongov1.AddressBook) carddav.AddressBook {
	name := ab.Name
	if email := b.grants.UserEmail(owner); email != "" {
		name += " (" + email + ")"
	}
	return carddav.AddressBook{
		Path:        bookPath(userID, sharedSegment(owner, ab.Id)),
		Name:        name,
		Description: ab.Description,
	}
}

// sharedAddressBooks lists the books shared with userID. A book that cannot be
// read is skipped: a listing never fails over one share.
func (b *Backend) sharedAddressBooks(userID string) []carddav.AddressBook {
	if b.grants == nil || b.books == nil {
		return nil
	}
	held, err := b.grants.AddressBooksSharedWith(userID, time.Now())
	if err != nil {
		b.logger.Warn("carddav: listing shared address books failed", "user", userID, "error", err)
		return nil
	}
	var out []carddav.AddressBook
	for _, h := range held {
		rec, err := b.books.Get(h.Owner, h.Resource)
		if err != nil {
			continue
		}
		out = append(out, b.sharedDAVAddressBook(userID, h.Owner, rec))
	}
	return out
}
