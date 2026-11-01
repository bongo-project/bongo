package templates

import (
	"context"
	"net/url"
	"strings"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// SharedKeyPrefix starts the key a calendar owned by someone else is shown
// under. Calendar IDs are only unique per owner (every user has a "default"
// one), so views, colour maps, the visibility filter and the event form's
// calendar select name a shared calendar as "shared:{owner}:{calendar}". The
// key is display-only: it is never stored.
const SharedKeyPrefix = "shared:"

// SharedKey is the display key of calendar cal owned by owner.
func SharedKey(owner, cal string) string {
	return SharedKeyPrefix + owner + ":" + cal
}

// ParseSharedKey splits a display key. ok is false for anything that is not a
// well-formed shared key, including one with an empty owner or calendar.
func ParseSharedKey(key string) (owner, cal string, ok bool) {
	rest, found := strings.CutPrefix(key, SharedKeyPrefix)
	if !found {
		return "", "", false
	}
	owner, cal, found = strings.Cut(rest, ":")
	if !found || owner == "" || cal == "" {
		return "", "", false
	}
	return owner, cal, true
}

// SharedCalendar is a calendar shared with the viewer, as the sidebar and the
// event form list it.
type SharedCalendar struct {
	Key    string // SharedKey(owner, calendar)
	Name   string
	Owner  string // owner's address, for the label
	Colour string
	Level  string // "read" or "write"
}

// eventOwner is the owner of a shared event as the views rekey it, or "" for
// the viewer's own event.
func eventOwner(e *bongov1.Event) string {
	owner, _, _ := ParseSharedKey(e.CalendarId)
	return owner
}

type sharedCalendarsKey struct{}

// WithSharedCalendars records the calendars shared with the viewer.
func WithSharedCalendars(ctx context.Context, cals []SharedCalendar) context.Context {
	return context.WithValue(ctx, sharedCalendarsKey{}, cals)
}

func sharedCalendarsFromCtx(ctx context.Context) []SharedCalendar {
	cals, _ := ctx.Value(sharedCalendarsKey{}).([]SharedCalendar)
	return cals
}

// writableShared is the shared calendars the viewer may create events in.
func writableShared(ctx context.Context) []SharedCalendar {
	var out []SharedCalendar
	for _, c := range sharedCalendarsFromCtx(ctx) {
		if c.Level == "write" {
			out = append(out, c)
		}
	}
	return out
}

// SharedEvent says an event shown on the form lives in another user's
// calendar, and at what level the viewer holds it.
type SharedEvent struct {
	Owner string
	Level string
}

type sharedEventKey struct{}

// WithSharedEvent records that the event form shows another user's event.
func WithSharedEvent(ctx context.Context, owner, level string) context.Context {
	return context.WithValue(ctx, sharedEventKey{}, SharedEvent{Owner: owner, Level: level})
}

func sharedEventFromCtx(ctx context.Context) (SharedEvent, bool) {
	se, ok := ctx.Value(sharedEventKey{}).(SharedEvent)
	return se, ok
}

// eventFormReadOnly reports whether the form is shown to a viewer who may not
// save it. The server enforces this; the form only avoids offering the buttons.
func eventFormReadOnly(ctx context.Context) bool {
	se, ok := sharedEventFromCtx(ctx)
	return ok && se.Level != "write"
}

// sharedCalendarLabel is the name a shared calendar is listed under, with its
// owner so two users' "Work" calendars can be told apart.
func sharedCalendarLabel(ctx context.Context, key string) string {
	for _, c := range sharedCalendarsFromCtx(ctx) {
		if c.Key == key {
			if c.Owner != "" {
				return c.Name + " (" + c.Owner + ")"
			}
			return c.Name
		}
	}
	return key
}

type preselectKey struct{}

// WithPreselectedCalendar names the calendar the new-event form selects.
func WithPreselectedCalendar(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, preselectKey{}, key)
}

func preselectedCalendar(ctx context.Context) string {
	k, _ := ctx.Value(preselectKey{}).(string)
	return k
}

// eventDeleteURL is the DELETE target of the event form; a shared event is
// addressed through its owner.
func eventDeleteURL(ctx context.Context, e *bongov1.Event) string {
	u := "/calendar/event/" + url.PathEscape(e.Guid)
	if se, ok := sharedEventFromCtx(ctx); ok {
		u += "?owner=" + url.QueryEscape(se.Owner)
	}
	return u
}

// settingsColour is the value of the colour input: the stored #rrggbb, or the
// default blue when none is set or it is not a plain hex colour.
func settingsColour(hex string) string {
	if _, _, _, ok := parseHexColour(hex); ok && len(hex) == 7 {
		return hex
	}
	return "#4A90D9"
}

// GrantRow is one line of the sharing list in the calendar settings card.
type GrantRow struct {
	Grantee string // user ID, posted back to unshare
	Label   string // address shown
	Level   string
	State   string
}

// CalendarSettings is the data of the settings card for one own calendar.
type CalendarSettings struct {
	Calendar string
	Name     string
	Colour   string
	Grants   []GrantRow
	CanShare bool // false when sharing is unavailable on this node
}
