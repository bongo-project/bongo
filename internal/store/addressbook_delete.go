package store

import (
	"errors"
	"fmt"
)

// ErrAddressBookGrantsNotRevoked reports that an address book was deleted but
// revoking the grants on it failed. The delete is complete; callers log it and
// still report success, since a retry would only find no such book.
var ErrAddressBookGrantsNotRevoked = errors.New("address book deleted but its grants were not revoked")

// PurgeAddressBook deletes the user's address book id, its contacts and its
// grants. It refuses the default book with ErrAddressBookProtected and a book
// that does not exist with ErrAddressBookNotFound.
//
// The writes are separate Raft commands and are not atomic. The contacts go
// first, one command each, and the book record goes last, so a failure part
// way leaves the book in place with the contacts that remain. A retried delete
// finds the book still there and finishes the job; do not reverse this order.
// The grants are revoked after the book record, as DeleteMailbox does: that
// step is best-effort, its failure wraps ErrAddressBookGrantsNotRevoked and the
// book is gone (the leader's orphan sweep removes what is left).
//
// A contact written into the book after the listing, or by an older node that
// does not know about books, can outlive it. Such a contact names a book that
// no longer exists: the web lists it under all contacts, autocomplete and usage
// still count it, and CardDAV does not show it. The user can delete it from the
// web list.
//
// A large book costs one Raft command per contact, so a request may outlast a
// client's timeout; the deletion carries on server side.
func PurgeAddressBook(books *AddressBookStore, contacts *ContactStore, userID, id string) error {
	if id == DefaultAddressBookID {
		return ErrAddressBookProtected
	}
	if _, err := books.Get(userID, id); err != nil {
		return err
	}
	inBook, _, err := contacts.ListByBook(userID, id, 0, 0)
	if err != nil {
		return fmt.Errorf("listing contacts to remove: %w", err)
	}
	for _, c := range inBook {
		if err := contacts.Delete(userID, c.Guid); err != nil {
			return fmt.Errorf("removing contact: %w", err)
		}
	}
	if err := books.Delete(userID, id); err != nil {
		return fmt.Errorf("deleting address book: %w", err)
	}
	if err := RevokeResourceGrants(books.applier, ACLKindAddressBook, userID, id); err != nil {
		return fmt.Errorf("%w: %w", ErrAddressBookGrantsNotRevoked, err)
	}
	return nil
}
