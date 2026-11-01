package caldav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	webdav "github.com/emersion/go-webdav"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// GrantReader is the part of the grant store the backend consults. It is read
// on every request: authorisation is never cached across requests.
type GrantReader interface {
	CalendarAccessLevel(owner, calendar, grantee string) (string, error)
	ListCalendarGrantsHeld(grantee string) ([]store.HeldGrant, error)
	UserEmail(userID string) string
}

// SetGrants enables shared calendars. Without it the backend serves only the
// authenticated user's own calendars and every shared path is refused.
func (b *Backend) SetGrants(g GrantReader) { b.grants = g }

// sharedPrefix starts the collection name of a calendar shared with the user:
// shared-{owner}-{calendar}. User IDs are 36-character UUIDs, which contain
// "-", so the owner is the fixed-width part and the calendar the rest.
const sharedPrefix = "shared-"

const uuidLen = 36

func sharedSegment(owner, calID string) string { return sharedPrefix + owner + "-" + calID }

// parseSharedSegment splits a shared collection name. It accepts only a
// well-formed UUID owner and a non-empty calendar, so a crafted segment can
// neither steer a key prefix nor name an owner that is not a user ID.
func parseSharedSegment(seg string) (owner, calID string, ok bool) {
	rest, found := strings.CutPrefix(seg, sharedPrefix)
	if !found || len(rest) <= uuidLen+1 || rest[uuidLen] != '-' {
		return "", "", false
	}
	owner, calID = rest[:uuidLen], rest[uuidLen+1:]
	if _, err := uuid.Parse(owner); err != nil || strings.Contains(calID, "/") {
		return "", "", false
	}
	return owner, calID, true
}

type pathKind int

const (
	kindInvalid pathKind = iota
	kindRoot
	kindPrincipal
	kindHome
	kindCollection
	kindObject
)

// davPath is a CalDAV path split with exact segment counts.
type davPath struct {
	kind     pathKind
	user     string // path user
	calSeg   string // collection name as it appears in the path
	guid     string // object name without ".ics"
	owner    string // for a shared collection: the owner; else ""
	calendar string // calendar ID in the owner's key space
}

func (p davPath) shared() bool { return p.owner != "" }

// parseDAVPath splits /caldav/{user}/calendars/{calendar}/{guid}.ics. Every
// kind has an exact segment count, so "/caldav/u/calendars/x" is a
// collection and never an object.
func parseDAVPath(path string) davPath {
	rest, ok := strings.CutPrefix(path, "/caldav")
	if !ok {
		return davPath{}
	}
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return davPath{kind: kindRoot}
	}
	parts := strings.Split(rest, "/")
	for _, s := range parts {
		if s == "" || s == "." || s == ".." {
			return davPath{}
		}
	}
	p := davPath{user: parts[0]}
	switch len(parts) {
	case 1:
		p.kind = kindPrincipal
		return p
	case 2:
		if parts[1] != "calendars" {
			return davPath{}
		}
		p.kind = kindHome
		return p
	case 3, 4:
		if parts[1] != "calendars" {
			return davPath{}
		}
		p.calSeg = parts[2]
		p.calendar = parts[2]
		if strings.HasPrefix(parts[2], sharedPrefix) {
			owner, cal, ok := parseSharedSegment(parts[2])
			if !ok {
				return davPath{}
			}
			p.owner, p.calendar = owner, cal
		}
		p.kind = kindCollection
		if len(parts) == 4 {
			p.kind = kindObject
			p.guid = strings.TrimSuffix(parts[3], ".ics")
			if p.guid == "" {
				return davPath{}
			}
		}
		return p
	}
	return davPath{}
}

func httpErr(code int, msg string) error {
	return webdav.NewHTTPError(code, errors.New(msg))
}

// target is a path resolved for the authenticated user: whose key space it
// lives in and what the user may do there.
type target struct {
	davPath
	user     string // authenticated user
	keyOwner string // user ID whose key space holds the data and quota
	level    string // store.GrantLevelRead or Write; Write for own data
}

func (t target) canWrite() bool { return store.LevelSatisfies(t.level, store.GrantLevelWrite) }

// resolve parses path, checks it against the authenticated user and, for a
// shared collection, against the grant. The path user must be the
// authenticated user: shared calendars are listed in the recipient's own
// home, so a path under someone else's home is never served, with or without
// a grant. A freebusy grant, a pending grant and a grant on a missing
// calendar all answer 404, like a collection that does not exist.
func (b *Backend) resolve(ctx context.Context, path string, want ...pathKind) (target, error) {
	user := getUserID(ctx)
	if user == "" {
		return target{}, fmt.Errorf("no user in context")
	}
	p := parseDAVPath(path)
	if p.kind == kindInvalid || !slicesContains(want, p.kind) {
		return target{}, httpErr(http.StatusNotFound, "not found")
	}
	if p.user != "" && p.user != user {
		return target{}, httpErr(http.StatusForbidden, "forbidden")
	}
	t := target{davPath: p, user: user, keyOwner: user, level: store.GrantLevelWrite}
	if p.shared() {
		if b.grants == nil || p.owner == user {
			return target{}, httpErr(http.StatusNotFound, "not found")
		}
		level, err := b.grants.CalendarAccessLevel(p.owner, p.calendar, user)
		if err != nil {
			return target{}, fmt.Errorf("reading grant: %w", err)
		}
		if !store.LevelSatisfies(level, store.GrantLevelRead) {
			return target{}, httpErr(http.StatusNotFound, "not found")
		}
		t.keyOwner, t.level = p.owner, level
	}
	// A subscription mirrors an external feed and is read-only for everyone,
	// its owner and a write grantee included: the next refresh would undo an
	// edit. Only a collection path has a calendar to ask about.
	if p.kind == kindCollection || p.kind == kindObject {
		subscribed, err := b.calendars.CalendarIsSubscribed(t.keyOwner, p.calendar)
		if err != nil {
			return target{}, fmt.Errorf("reading calendar: %w", err)
		}
		if subscribed {
			t.level = store.GrantLevelRead
		}
	}
	return t, nil
}

// inSubscription reports whether event lives in one of owner's subscribed
// calendars. PUT and DELETE act on an event by its guid whatever collection
// the path names, so the stored calendar is what decides, never the path.
func (b *Backend) inSubscription(owner string, event *bongov1.Event) (bool, error) {
	if event == nil {
		return false, nil
	}
	subscribed, err := b.calendars.CalendarIsSubscribed(owner, eventCalendar(event))
	if err != nil {
		return false, fmt.Errorf("reading calendar: %w", err)
	}
	return subscribed, nil
}

func slicesContains(kinds []pathKind, k pathKind) bool {
	for _, w := range kinds {
		if w == k {
			return true
		}
	}
	return false
}

// ReadOnlyShare reports whether href names a collection shared with the
// authenticated user at read level, for the PROPFIND privilege rewrite. The
// grant is read now, not cached.
func (b *Backend) ReadOnlyShare(ctx context.Context, href string) bool {
	user := getUserID(ctx)
	p := parseDAVPath(href)
	if user == "" || p.kind != kindCollection || p.user != user {
		return false
	}
	owner := user
	if p.shared() {
		if b.grants == nil {
			return false
		}
		owner = p.owner
		level, err := b.grants.CalendarAccessLevel(p.owner, p.calendar, user)
		if err != nil || !store.LevelSatisfies(level, store.GrantLevelRead) {
			return false
		}
		if level == store.GrantLevelRead {
			return true
		}
	}
	// A write grantee, or the owner, of a subscription: read-only as well.
	subscribed, err := b.calendars.CalendarIsSubscribed(owner, p.calendar)
	return err == nil && subscribed
}

// sharedName is the display name of a shared collection: the calendar's name
// and its owner, so two users' "Work" calendars can be told apart.
func (b *Backend) sharedName(ownerID, calName string) string {
	if email := b.grants.UserEmail(ownerID); email != "" {
		return calName + " (" + email + ")"
	}
	return calName
}
