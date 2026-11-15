package store

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// UserEmail returns a user's primary address for display, or "" when the
// profile is gone or unreadable. It is a label, never an authorisation input.
func (s *ACLStore) UserEmail(userID string) string {
	if userID == "" {
		return ""
	}
	data, err := s.pebble.Get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		return ""
	}
	var p bongov1.UserProfile
	if proto.Unmarshal(data, &p) != nil {
		return ""
	}
	return p.Email
}

// HeldGrant is one accepted calendar grant a user holds.
type HeldGrant struct {
	Owner    string
	Calendar string
	Level    string
}

// CalendarGrantEntry is one grant on a calendar, for the owner's sharing list.
type CalendarGrantEntry struct {
	// Grantee is the user id, or "" for a group grant.
	Grantee string
	// GroupID is the group id of a group grant, "" for a user grant.
	GroupID string
	Level   string
	State   string
}

// CalendarAccessLevel returns the level grantee holds on the one calendar
// (owner, calendar): "" unless the grant exists, is accepted and the calendar
// still exists. The level is the highest of the grantee's own grant and the
// grants of the groups they belong to (see EffectiveGrant). Route authorisation must use this and not HasCalendarAccess,
// which answers for any calendar of the owner: a grant on calendar A would
// then authorise a write to calendar B.
func (s *ACLStore) CalendarAccessLevel(owner, calendar, grantee string) (string, error) {
	eff, err := s.EffectiveGrant(ACLKindCalendar, owner, calendar, grantee)
	if err != nil || eff.Level == "" {
		return "", err
	}
	if _, err := s.pebble.Get(calendarKey(owner, calendar)); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("reading calendar for grant: %w", err)
	}
	return eff.Level, nil
}

// LevelSatisfies reports whether level is at least need. An unknown level
// satisfies nothing.
func LevelSatisfies(level, need string) bool {
	have, ok := calendarLevelRank[level]
	want, ok2 := calendarLevelRank[need]
	return ok && ok2 && have >= want
}

// ListCalendarGrantsHeld returns the accepted grants grantee holds, at any
// level, on calendars that still exist, including those reached through a
// group (the strongest level when both apply), sorted by owner then calendar.
func (s *ACLStore) ListCalendarGrantsHeld(grantee string) ([]HeldGrant, error) {
	held, err := s.ListGrantsHeld(grantee, GrantStateAccepted, time.Time{})
	if err != nil {
		return nil, err
	}
	var out []HeldGrant
	for _, h := range held {
		if h.Kind == ACLKindCalendar {
			out = append(out, HeldGrant{Owner: h.Owner, Calendar: h.Resource, Level: h.Level})
		}
	}
	return out, nil
}

// ListPendingCalendarGrantsHeld returns the pending grants grantee holds on
// calendars that still exist and have not expired at now, sorted by owner then
// calendar. Nothing here authorises access; it feeds the invitation page.
func (s *ACLStore) ListPendingCalendarGrantsHeld(grantee string, now time.Time) ([]HeldGrant, error) {
	if grantee == "" {
		return nil, nil
	}
	prefix := append(aclGranteePrefix(GranteeUser(grantee)), []byte(ACLKindCalendar+"/")...)
	keys, err := s.pebble.scanKeys(prefix)
	if err != nil {
		return nil, fmt.Errorf("scanning grants held: %w", err)
	}
	var out []HeldGrant
	for _, k := range keys {
		ref, ok := parseACLKey(string(k))
		if !ok || ref.Principal != GranteeUser(grantee) || ref.Kind != ACLKindCalendar {
			continue
		}
		g, err := s.GetCalendarGrant(ref.Owner, ref.Resource, grantee)
		if err != nil {
			return nil, err
		}
		if g == nil || g.State != GrantStatePending || PendingGrantExpired(g, now.Unix()) || !ValidCalendarLevel(g.Level) {
			continue
		}
		if _, err := s.pebble.Get(calendarKey(ref.Owner, ref.Resource)); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("reading calendar for grant: %w", err)
		}
		out = append(out, HeldGrant{Owner: ref.Owner, Calendar: ref.Resource, Level: g.Level})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner != out[j].Owner {
			return out[i].Owner < out[j].Owner
		}
		return out[i].Calendar < out[j].Calendar
	})
	return out, nil
}

// ListCalendarGrants returns every grant on one calendar, in any state,
// sorted by grantee, for the owner's sharing list.
func (s *ACLStore) ListCalendarGrants(owner, calendar string) ([]CalendarGrantEntry, error) {
	if owner == "" || calendar == "" {
		return nil, nil
	}
	keys, err := s.pebble.scanKeys(aclResourcePrefix(ACLKindCalendar, owner, calendar))
	if err != nil {
		return nil, fmt.Errorf("scanning grants: %w", err)
	}
	var out []CalendarGrantEntry
	for _, k := range keys {
		ref, ok := parseACLKey(string(k))
		if !ok || ref.Owner != owner || ref.Resource != calendar {
			continue
		}
		g, err := s.GetGrant(ACLKindCalendar, owner, calendar, ref.Principal)
		if err != nil {
			return nil, err
		}
		if g == nil {
			continue
		}
		e := CalendarGrantEntry{Level: g.Level, State: g.State}
		if uid, isUser := ref.UserID(); isUser {
			e.Grantee = uid
		} else {
			_, e.GroupID, _ = splitGrantee(ref.Principal)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupID != out[j].GroupID {
			return out[i].GroupID < out[j].GroupID
		}
		return out[i].Grantee < out[j].Grantee
	})
	return out, nil
}
