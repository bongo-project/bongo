package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Effective is what a user may do on one resource once their own grant and
// the grants of every group they belong to are merged. The zero value means
// no access. Level is set for calendars and address books, Rights for
// mailboxes.
type Effective struct {
	Level  string
	Rights string
	// Direct is true when the user's own accepted grant contributes.
	Direct bool
	// Groups lists the ids of the groups whose accepted grant contributes.
	Groups []string
}

// Granted reports whether anything authorises the user.
func (e Effective) Granted() bool { return e.Level != "" || e.Rights != "" }

// userGroupIDs returns the ids of the groups userID belongs to, from the member
// index alone. The index is written and deleted in the same batch as the group,
// so no group read is needed. It is read on every call and never cached:
// leaving a group must end access on the next request.
func (s *ACLStore) userGroupIDs(userID string) ([]string, error) {
	if !validGroupSegment(userID) {
		return nil, nil
	}
	prefix := groupMemberPrefix(userID)
	keys, err := s.pebble.scanKeys(prefix)
	if err != nil {
		return nil, fmt.Errorf("listing memberships: %w", err)
	}
	var ids []string
	for _, k := range keys {
		id := strings.TrimPrefix(string(k), string(prefix))
		if groupIDPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// grantPermissionValid reports whether level or rights is valid for the kind.
func grantPermissionValid(kind, level, rights string) bool {
	switch kind {
	case ACLKindCalendar:
		return ValidCalendarLevel(level)
	case ACLKindAddressBook:
		return ValidAddressBookLevel(level)
	case ACLKindMailbox:
		return ValidMailboxRights(rights)
	}
	return false
}

// EffectiveGrant returns what userID may do on the resource: the union of
// their own accepted u: grant and the accepted g: grants of every group they
// belong to. A calendar or address book takes the highest level, a mailbox the
// union of the RFC 4314 letters in mailboxRightLetters order. A pending grant,
// however it was created, authorises nothing. It does not check that the
// resource exists and does not treat the owner specially: callers that need
// either do it themselves. Group membership and grants are read on every call.
func (s *ACLStore) EffectiveGrant(kind, owner, resource, userID string) (Effective, error) {
	var eff Effective
	if owner == "" || resource == "" || userID == "" || !validACLKind(kind) {
		return eff, nil
	}
	gids, err := s.userGroupIDs(userID)
	if err != nil {
		return eff, err
	}
	rank := 0
	var letters string
	merge := func(principal string) (bool, error) {
		g, err := s.GetGrant(kind, owner, resource, principal)
		if err != nil {
			return false, err
		}
		if g == nil || g.State != GrantStateAccepted || !grantPermissionValid(kind, g.Level, g.Rights) {
			return false, nil
		}
		if kind == ACLKindMailbox {
			letters += g.Rights
		} else if r := calendarLevelRank[g.Level]; r > rank {
			rank, eff.Level = r, g.Level
		}
		return true, nil
	}
	if ok, err := merge(GranteeUser(userID)); err != nil {
		return Effective{}, err
	} else if ok {
		eff.Direct = true
	}
	for _, gid := range gids {
		if ok, err := merge(GranteeGroup(gid)); err != nil {
			return Effective{}, err
		} else if ok {
			eff.Groups = append(eff.Groups, gid)
		}
	}
	if kind == ACLKindMailbox {
		var b strings.Builder
		for _, r := range mailboxRightLetters {
			if strings.ContainsRune(letters, r) {
				b.WriteRune(r)
			}
		}
		eff.Rights = b.String()
	}
	return eff, nil
}

// ListGrantsHeld returns the grants of every kind that grantee holds in the
// given state, in one scan. For the accepted state the grants of every group
// the grantee belongs to count too, merged per resource by EffectiveGrant, and
// a resource the grantee owns is left out when only a group reaches it. A
// pending grant is a personal invitation and is read from the user's own
// grants only. A pending grant expired at now, a permission the kind does not
// accept and a grant whose calendar, mailbox or address book is gone are left out. Sorted by
// kind, owner then resource. Nothing here authorises access; it feeds the
// shares page and the invitation count, which must agree.
func (s *ACLStore) ListGrantsHeld(grantee, state string, now time.Time) ([]HeldShare, error) {
	if grantee == "" {
		return nil, nil
	}
	principals := []string{GranteeUser(grantee)}
	if state == GrantStateAccepted {
		gids, err := s.userGroupIDs(grantee)
		if err != nil {
			return nil, err
		}
		for _, gid := range gids {
			principals = append(principals, GranteeGroup(gid))
		}
	}
	type resKey struct{ kind, owner, resource string }
	seen := map[resKey]bool{}
	var out []HeldShare
	for _, principal := range principals {
		keys, err := s.pebble.scanKeys(aclGranteePrefix(principal))
		if err != nil {
			return nil, fmt.Errorf("scanning grants held: %w", err)
		}
		for _, k := range keys {
			ref, ok := parseACLKey(string(k))
			if !ok || ref.Principal != principal {
				continue
			}
			rk := resKey{ref.Kind, ref.Owner, ref.Resource}
			if seen[rk] {
				continue
			}
			seen[rk] = true
			h := HeldShare{Kind: ref.Kind, Owner: ref.Owner, Resource: ref.Resource}
			if state == GrantStateAccepted {
				eff, err := s.EffectiveGrant(ref.Kind, ref.Owner, ref.Resource, grantee)
				if err != nil {
					return nil, err
				}
				if !eff.Granted() || (!eff.Direct && ref.Owner == grantee) {
					continue
				}
				h.Level, h.Rights, h.Direct, h.ViaGroups = eff.Level, eff.Rights, eff.Direct, eff.Groups
			} else {
				g, err := s.GetGrant(ref.Kind, ref.Owner, ref.Resource, principal)
				if err != nil {
					return nil, err
				}
				if g == nil || g.State != state || PendingGrantExpired(g, now.Unix()) ||
					!grantPermissionValid(ref.Kind, g.Level, g.Rights) {
					continue
				}
				h.Level, h.Rights, h.Direct = g.Level, g.Rights, true
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
			case ACLKindAddressBook:
				if _, err := s.pebble.Get(addressBookKey(ref.Owner, ref.Resource)); err != nil {
					if !errors.Is(err, ErrNotFound) {
						return nil, fmt.Errorf("reading address book for grant: %w", err)
					}
					gone = true
				}
			}
			if gone {
				continue
			}
			out = append(out, h)
		}
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
// and address-book grants, Rights for a mailbox grant. Direct is true when the
// user's own grant contributes; ViaGroups lists the groups that do. A share
// reached only through groups has Direct false: the user cannot decline it.
type HeldShare struct {
	Kind      string
	Owner     string
	Resource  string
	Level     string
	Rights    string
	Direct    bool
	ViaGroups []string
}
