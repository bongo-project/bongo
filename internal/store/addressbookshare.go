package store

import "time"

// AddressBookLevel returns the level userID holds on the address book
// (owner, bookID): "read", "write" or "" for none. It is EffectiveGrant's
// answer, the union of the user's own accepted grant and the accepted grants
// of every group they belong to, so a pending or expired grant adds nothing.
// The default book is never shareable, so its id, like any id that is not the
// stored 32-hex form, answers "". It does not check that the book exists.
//
// This is the one resolver CardDAV, the web handlers and autocomplete use. The
// result must never be cached beyond one request: a revoke or leaving a group
// has to take effect on the next one.
func (s *ACLStore) AddressBookLevel(owner, bookID, userID string) (string, error) {
	if owner == "" || userID == "" || !ValidAddressBookID(bookID) {
		return "", nil
	}
	eff, err := s.EffectiveGrant(ACLKindAddressBook, owner, bookID, userID)
	if err != nil {
		return "", err
	}
	return eff.Level, nil
}

// AddressBooksSharedWith lists the address books other users shared with
// userID (accepted, the book still exists), ordered by owner then id. A book
// userID owns is never listed, even when a group they belong to is granted it.
// ListGrantsHeld already skips such a resource; the owner filter here repeats
// that check as defence in depth.
func (s *ACLStore) AddressBooksSharedWith(userID string, now time.Time) ([]HeldShare, error) {
	held, err := s.ListGrantsHeld(userID, GrantStateAccepted, now)
	if err != nil {
		return nil, err
	}
	var out []HeldShare
	for _, h := range held {
		if h.Kind == ACLKindAddressBook && h.Owner != userID {
			out = append(out, h)
		}
	}
	return out, nil
}
