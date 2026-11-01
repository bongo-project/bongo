package templates

import (
	"context"
	"fmt"
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

// eventMovable reports whether a week or day block may carry the drag handle
// (the data-guid the script looks for). A recurring event is moved through its
// form, and an event of a subscribed calendar or of a calendar shared read-only
// is refused by the server, so none of them offers the handle.
func eventMovable(ctx context.Context, calendars []*bongov1.Calendar, e *bongov1.Event) bool {
	if e.Rrule != "" {
		return false
	}
	if eventOwner(e) != "" {
		for _, c := range sharedCalendarsFromCtx(ctx) {
			if c.Key == e.CalendarId {
				return c.Level == "write"
			}
		}
		return false
	}
	for _, c := range calendars {
		if c.Guid == e.CalendarId {
			return c.Subscription == nil || c.Subscription.SourceUrl == ""
		}
	}
	return true
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
	return readOnlyEventFromCtx(ctx) || (ok && se.Level != "write")
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
	Publish  PublishSettings
	// Subscription is set for a calendar that mirrors an external feed.
	Subscription *SubscriptionInfo
}

// SubscriptionInfo is the refresh state of a subscribed calendar as the
// settings card shows it. It carries the feed's host only: the URL is a secret.
type SubscriptionInfo struct {
	Host        string
	LastSuccess string // formatted in the user's zone; empty before the first refresh
	LastError   string
	EventCount  int
}

// calendarOffered reports whether the event form's calendar select lists cal.
// A subscribed calendar is read-only, so it is not a place to put an event; it
// is listed only when the event shown already lives in it.
func calendarOffered(cal *bongov1.Calendar, event *bongov1.Event) bool {
	if cal.Subscription == nil || cal.Subscription.SourceUrl == "" {
		return true
	}
	return event != nil && event.CalendarId == cal.Guid
}

// subscriptionEventCount formats the "Events: %d" message.
func subscriptionEventCount(ctx context.Context, n int) string {
	return fmt.Sprintf(t(ctx, "calendar.subscriptionEventCount"), n)
}

type readOnlyEventKey struct{}

// WithReadOnlyEvent marks the event form as showing an event of a subscribed
// calendar, which nobody may change.
func WithReadOnlyEvent(ctx context.Context) context.Context {
	return context.WithValue(ctx, readOnlyEventKey{}, true)
}

func readOnlyEventFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(readOnlyEventKey{}).(bool)
	return v
}
