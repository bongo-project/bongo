package store

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// ListGrantsHeld returns the grants of every kind that grantee holds in the
// given state, in one scan. A pending grant expired at now, a permission the
// kind does not accept and a grant whose calendar or mailbox is gone are left
// out. Address books do not exist yet, so none is probed. Sorted by kind, owner
// then resource. Nothing here authorises access; it feeds the shares page and
// the invitation count, which must agree.
func (s *ACLStore) ListGrantsHeld(grantee, state string, now time.Time) ([]HeldShare, error) {
	if grantee == "" {
		return nil, nil
	}
	typed := GranteeUser(grantee)
	keys, err := s.pebble.scanKeys(aclGranteePrefix(typed))
	if err != nil {
		return nil, fmt.Errorf("scanning grants held: %w", err)
	}
	var out []HeldShare
	for _, k := range keys {
		ref, ok := parseACLKey(string(k))
		if !ok || ref.Principal != typed {
			continue
		}
		g, err := s.GetGrant(ref.Kind, ref.Owner, ref.Resource, typed)
		if err != nil {
			return nil, err
		}
		if g == nil || g.State != state || PendingGrantExpired(g, now.Unix()) {
			continue
		}
		var valid bool
		switch ref.Kind {
		case ACLKindCalendar:
			valid = ValidCalendarLevel(g.Level)
		case ACLKindMailbox:
			valid = ValidMailboxRights(g.Rights)
		case ACLKindAddressBook:
			valid = ValidAddressBookLevel(g.Level)
		}
		if !valid {
			continue
		}
		gone := false
		switch ref.Kind {
		case ACLKindCalendar:
			if _, err := s.pebble.Get(calendarKey(ref.Owner, ref.Resource)); err != nil {
				if !errors.Is(err, ErrNotFound) {
					return nil, fmt.Errorf("reading calendar for grant: %w", err)
				}
				gone = true
			}
		case ACLKindMailbox:
			if gone, err = s.mailboxGone(ref.Owner, ref.Resource); err != nil {
				return nil, err
			}
		}
		if gone {
			continue
		}
		h := HeldShare{Kind: ref.Kind, Owner: ref.Owner, Resource: ref.Resource, Level: g.Level, Rights: g.Rights}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Owner != out[j].Owner {
			return out[i].Owner < out[j].Owner
		}
		return out[i].Resource < out[j].Resource
	})
	return out, nil
}

// CountPendingGrantsHeld counts the pending invitations ListGrantsHeld would
// list, so the badge and the page agree.
func (s *ACLStore) CountPendingGrantsHeld(grantee string, now time.Time) (int, error) {
	held, err := s.ListGrantsHeld(grantee, GrantStatePending, now)
	return len(held), err
}

// HeldShare is one grant of any kind a user holds. Level is set for calendar
// and address-book grants, Rights for a mailbox grant.
type HeldShare struct {
	Kind     string
	Owner    string
	Resource string
	Level    string
	Rights   string
}
