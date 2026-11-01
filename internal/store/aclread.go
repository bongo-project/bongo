package store

import (
	"errors"
	"fmt"
	"sort"

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
	Grantee string
	Level   string
	State   string
}

// CalendarAccessLevel returns the level grantee holds on the one calendar
// (owner, calendar): "" unless the grant exists, is accepted and the calendar
// still exists. Route authorisation must use this and not HasCalendarAccess,
// which answers for any calendar of the owner: a grant on calendar A would
// then authorise a write to calendar B.
func (s *ACLStore) CalendarAccessLevel(owner, calendar, grantee string) (string, error) {
	g, err := s.GetCalendarGrant(owner, calendar, grantee)
	if err != nil || g == nil {
		return "", err
	}
	if g.State != GrantStateAccepted || !ValidCalendarLevel(g.Level) {
		return "", nil
	}
	if _, err := s.pebble.Get(calendarKey(owner, calendar)); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("reading calendar for grant: %w", err)
	}
	return g.Level, nil
}

// LevelSatisfies reports whether level is at least need. An unknown level
// satisfies nothing.
func LevelSatisfies(level, need string) bool {
	have, ok := calendarLevelRank[level]
	want, ok2 := calendarLevelRank[need]
	return ok && ok2 && have >= want
}

// ListCalendarGrantsHeld returns the accepted grants grantee holds, at any
// level, on calendars that still exist, sorted by owner then calendar.
func (s *ACLStore) ListCalendarGrantsHeld(grantee string) ([]HeldGrant, error) {
	if grantee == "" {
		return nil, nil
	}
	prefix := append(aclGranteePrefix(grantee), []byte(ACLKindCalendar+"/")...)
	keys, err := s.pebble.scanKeys(prefix)
	if err != nil {
		return nil, fmt.Errorf("scanning grants held: %w", err)
	}
	var out []HeldGrant
	for _, k := range keys {
		ref, ok := parseACLKey(string(k))
		if !ok || ref.Grantee != grantee || ref.Kind != ACLKindCalendar {
			continue
		}
		level, err := s.CalendarAccessLevel(ref.Owner, ref.Resource, grantee)
		if err != nil {
			return nil, err
		}
		if level != "" {
			out = append(out, HeldGrant{Owner: ref.Owner, Calendar: ref.Resource, Level: level})
		}
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
		g, err := s.GetCalendarGrant(owner, calendar, ref.Grantee)
		if err != nil {
			return nil, err
		}
		if g != nil {
			out = append(out, CalendarGrantEntry{Grantee: ref.Grantee, Level: g.Level, State: g.State})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Grantee < out[j].Grantee })
	return out, nil
}
