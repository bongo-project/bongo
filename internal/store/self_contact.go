package store

import (
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ErrContactNotFound is returned by ContactStore.Get and SetSelfContact when
// the contact is not in the user's own keyspace.
var ErrContactNotFound = errors.New("contact not found")

// ErrSelfContactGroup is returned when a contact group is marked "This is me".
var ErrSelfContactGroup = errors.New("a contact group cannot be the user's own card")

// SelfContact returns the contact the user marked "This is me". An empty,
// dangling or group-valued preference is unset. It reads the user's own
// keyspace only, so it can never reach another user's card.
func (s *ContactStore) SelfContact(userID string) (*bongov1.Contact, bool) {
	guid := NewPreferenceStore(s.pebble, s.applier).Get(userID).GetSelfContactGuid()
	if guid == "" {
		return nil, false
	}
	c, err := s.Get(userID, guid)
	if err != nil || IsContactGroup(c) {
		return nil, false
	}
	return c, true
}

// IsSelfContact reports whether guid is the stored "This is me" value. It does
// not look the card up: a caller that already holds the person card uses it to
// show the mark, SelfContact is the resolver for sending.
func (s *ContactStore) IsSelfContact(userID, guid string) bool {
	return guid != "" && NewPreferenceStore(s.pebble, s.applier).Get(userID).GetSelfContactGuid() == guid
}

// SetSelfContact marks one of the user's own contacts "This is me", replacing
// any earlier mark. A missing contact is ErrContactNotFound and a contact group
// ErrSelfContactGroup; nothing is written then.
func (s *ContactStore) SetSelfContact(userID, guid string) error {
	c, err := s.Get(userID, guid)
	if err != nil {
		if errors.Is(err, ErrContactNotFound) {
			return err
		}
		return fmt.Errorf("reading contact: %w", err)
	}
	if IsContactGroup(c) {
		return ErrSelfContactGroup
	}
	prefs := NewPreferenceStore(s.pebble, s.applier)
	p := prefs.Get(userID)
	p.SelfContactGuid = guid
	return prefs.Set(userID, p)
}

// ClearSelfContact unmarks the card only when guid is the marked one, so a
// stale tab cannot unmark a different card. It writes nothing otherwise.
func (s *ContactStore) ClearSelfContact(userID, guid string) error {
	prefs := NewPreferenceStore(s.pebble, s.applier)
	p := prefs.Get(userID)
	if guid == "" || p.GetSelfContactGuid() != guid {
		return nil
	}
	p.SelfContactGuid = ""
	return prefs.Set(userID, p)
}

// clearSelfIfMatch is the eager half of "deleting the contact clears the
// preference". It never creates a preferences record and writes only when the
// stored value names guid. A failure is logged and swallowed: the contact is
// already gone and SelfContact treats a dangling value as unset.
func (s *ContactStore) clearSelfIfMatch(userID, guid string) {
	data, err := s.pebble.Get(prefKey(userID))
	if err != nil || len(data) == 0 {
		return
	}
	var p bongov1.UserPreferences
	if proto.Unmarshal(data, &p) != nil || p.SelfContactGuid != guid {
		return
	}
	if err := s.ClearSelfContact(userID, guid); err != nil {
		slog.Warn("clearing self contact after delete failed", "user_id", userID, "contact_id", guid, "error", err)
	}
}
