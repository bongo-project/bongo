package handlers

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Authorisation for calendars owned by someone else lives in this file, in
// two helpers every route calls first: resolveEventTarget for a route that
// names an existing event, resolveCalendarTarget for one that names a
// calendar. They return the owner whose key space the route must use for
// every store call and whose quota it must charge.
//
// The grant is read from the store on every request and never cached. A
// revoke that races an in-flight write may let that one write through; the
// next request is refused.
//
// Refusals: a user holding a read grant on a write route gets 403 (the grant
// exists and says no). Anyone else -- no grant, a freebusy grant, a pending
// grant, a grant on a deleted calendar, a grant on a different calendar of
// the same owner -- gets 404, the answer for a missing event, so the owner's
// guid and calendar space cannot be probed. Free/busy never authorises a
// route that returns or changes events.

const (
	needRead  = store.GrantLevelRead
	needWrite = store.GrantLevelWrite
)

// refreshSettings asks htmx to reload the settings card after a change to the
// calendar or its sharing list. A plain request is unaffected.
func refreshSettings(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
	}
}

// requestedOwner is the owner named by ?owner= or the form field; the actor
// when absent. It is a claim to be authorised, never trusted as is.
func requestedOwner(r *http.Request, actor string) string {
	if o := r.FormValue("owner"); o != "" {
		return o
	}
	return actor
}

// validOwnerID rejects values that could steer a key prefix.
func validOwnerID(owner string) bool {
	return owner != "" && !strings.ContainsAny(owner, "/\x00\r\n")
}

// calendarAccess authorises actor for need on the one calendar (owner,
// calID). It returns the actor's level and 0 when allowed, else a status.
func (h *CalendarHandler) calendarAccess(actor, owner, calID, need string) (string, int) {
	if owner == actor {
		return store.GrantLevelWrite, 0
	}
	if h.acl == nil || !validOwnerID(owner) {
		return "", http.StatusNotFound
	}
	if calID == "" {
		calID = "default"
	}
	level, err := h.acl.CalendarAccessLevel(owner, calID, actor)
	if err != nil {
		slog.Error("calendar: reading grant failed", "user", actor, "error", err)
		return "", http.StatusInternalServerError
	}
	if !store.LevelSatisfies(level, needRead) {
		return "", http.StatusNotFound
	}
	if !store.LevelSatisfies(level, need) {
		return level, http.StatusForbidden
	}
	return level, 0
}

// resolveEventTarget finds the event a route names and authorises the actor
// for need. The grant is checked against the event's STORED calendar, never
// against a calendar ID the client sent: otherwise a write grant on one
// calendar would reach the owner's events in every other calendar by guid.
// owner is always set, also on failure.
func (h *CalendarHandler) resolveEventTarget(r *http.Request, actor, guid, need string) (owner string, ev *bongov1.Event, level string, status int) {
	owner = requestedOwner(r, actor)
	if owner != actor && !validOwnerID(owner) {
		return owner, nil, "", http.StatusNotFound
	}
	ev, err := h.events.Get(owner, guid)
	if err != nil {
		return owner, nil, "", http.StatusNotFound
	}
	level, status = h.calendarAccess(actor, owner, ev.CalendarId, need)
	if status != 0 {
		return owner, nil, level, status
	}
	return owner, ev, level, 0
}

// resolveCalendarTarget authorises the actor for need on the calendar named
// by ref: a plain calendar ID of the actor's own, or a shared display key.
// An empty ref is the actor's default calendar. A shared target must name an
// existing calendar the actor holds a grant on.
func (h *CalendarHandler) resolveCalendarTarget(actor, ref, need string) (owner, calID string, status int) {
	if ref == "" {
		return actor, "default", 0
	}
	if strings.HasPrefix(ref, templates.SharedKeyPrefix) {
		o, c, ok := templates.ParseSharedKey(ref)
		if !ok {
			return actor, "", http.StatusBadRequest
		}
		if o != actor {
			_, st := h.calendarAccess(actor, o, c, need)
			return o, c, st
		}
		ref = c
	}
	return actor, ref, 0
}

// resolveMoveTarget returns the calendar, in owner's key space, an update
// stores the event in. A move may not change owner: moving the actor's own
// event into a shared calendar, or a shared event out to another owner, is
// refused with 400. Within a shared owner the destination needs write
// access too, otherwise a write grant on calendar A could move events into
// calendar B.
func (h *CalendarHandler) resolveMoveTarget(actor, owner string, existing *bongov1.Event, raw string) (string, int) {
	cal := raw
	if strings.HasPrefix(raw, templates.SharedKeyPrefix) {
		o, c, ok := templates.ParseSharedKey(raw)
		if !ok || o != owner {
			return "", http.StatusBadRequest
		}
		cal = c
	}
	if owner == actor {
		if cal == "" {
			cal = "default"
		}
		return cal, 0
	}
	if cal == "" {
		cal = existing.CalendarId
	}
	if cal == "" {
		cal = "default"
	}
	if _, status := h.calendarAccess(actor, owner, cal, needWrite); status != 0 {
		if status == http.StatusNotFound {
			status = http.StatusForbidden
		}
		return "", status
	}
	return cal, 0
}

// sharedSet maps owner -> calendar IDs shared with the viewer.
type sharedSet map[string]map[string]bool

// loadShared lists the calendars shared with actor at read or write level
// (freebusy grants never reach a view) as sidebar entries, and the same
// grants grouped by owner for event loading. Failures degrade to "nothing
// shared": a view of the user's own calendar must not break on a bad grant.
func (h *CalendarHandler) loadShared(actor string) ([]templates.SharedCalendar, sharedSet, map[string]string) {
	if h.acl == nil {
		return nil, nil, nil
	}
	grants, err := h.acl.ListCalendarGrantsHeld(actor)
	if err != nil {
		slog.Warn("calendar: listing shared calendars failed", "user", actor, "error", err)
		return nil, nil, nil
	}
	var cals []templates.SharedCalendar
	set := sharedSet{}
	colours := map[string]string{}
	labels := map[string]string{}
	for _, g := range grants {
		if !store.LevelSatisfies(g.Level, needRead) {
			continue
		}
		cal, err := h.calendars.Get(g.Owner, g.Calendar)
		if err != nil {
			continue
		}
		if _, ok := labels[g.Owner]; !ok {
			labels[g.Owner] = h.acl.UserEmail(g.Owner)
		}
		key := templates.SharedKey(g.Owner, g.Calendar)
		if set[g.Owner] == nil {
			set[g.Owner] = map[string]bool{}
		}
		set[g.Owner][g.Calendar] = true
		if cal.Color != "" {
			colours[key] = cal.Color
		}
		cals = append(cals, templates.SharedCalendar{
			Key: key, Name: cal.Name, Owner: labels[g.Owner], Colour: cal.Color, Level: g.Level,
		})
	}
	return cals, set, colours
}

// sharedEvents loads the shared calendars' events in [start, end), one store
// scan per owner, and rekeys each to its display key. The events are fresh
// from the store, so rewriting CalendarId touches nothing persisted.
func (h *CalendarHandler) sharedEvents(set sharedSet, start, end int64) ([]*bongov1.Event, error) {
	owners := make([]string, 0, len(set))
	for o := range set {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	var out []*bongov1.Event
	for _, owner := range owners {
		evs, err := h.events.ListByDateRange(owner, start, end, "")
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			cal := e.CalendarId
			if cal == "" {
				cal = "default"
			}
			if !set[owner][cal] {
				continue
			}
			e.CalendarId = templates.SharedKey(owner, cal)
			out = append(out, e)
		}
	}
	return out, nil
}

// withShared adds the calendars shared with the viewer to a calendar view:
// their events in [start, end), their colours in colours (keyed by display
// key) and the sidebar group on the request context.
func (h *CalendarHandler) withShared(r *http.Request, actor string, events []*bongov1.Event, colours map[string]string, start, end int64) (*http.Request, []*bongov1.Event) {
	cals, set, shareColours := h.loadShared(actor)
	if len(cals) == 0 {
		return r, events
	}
	for k, v := range shareColours {
		colours[k] = v
	}
	shared, err := h.sharedEvents(set, start, end)
	if err != nil {
		slog.Warn("calendar: loading shared events failed", "user", actor, "error", err)
	} else if len(shared) > 0 {
		events = append(events, shared...)
		sort.SliceStable(events, func(i, j int) bool { return events[i].StartTime < events[j].StartTime })
	}
	return r.WithContext(templates.WithSharedCalendars(r.Context(), cals)), events
}

// sharedSidebar puts the shared calendars on the context for pages that list
// calendars but load no events (the event form).
func (h *CalendarHandler) sharedSidebar(r *http.Request, actor string) *http.Request {
	cals, _, _ := h.loadShared(actor)
	if len(cals) == 0 {
		return r
	}
	return r.WithContext(templates.WithSharedCalendars(r.Context(), cals))
}

// CalendarSettings renders the settings card of each of the user's own
// calendars: name, colour and the sharing list.
func (h *CalendarHandler) CalendarSettings(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	if _, err := h.calendars.EnsureDefault(userID); err != nil {
		http.Error(w, "failed to init calendar", http.StatusInternalServerError)
		return
	}
	calendars, err := h.calendars.List(userID)
	if err != nil {
		http.Error(w, "failed to load calendars", http.StatusInternalServerError)
		return
	}
	cards := make([]templates.CalendarSettings, 0, len(calendars))
	for _, c := range calendars {
		card := templates.CalendarSettings{
			Calendar: c.Guid, Name: c.Name, Colour: c.Color,
			CanShare: h.acl != nil && h.shares != nil,
		}
		if h.acl != nil {
			grants, err := h.acl.ListCalendarGrants(userID, c.Guid)
			if err != nil {
				http.Error(w, "failed to load sharing", http.StatusInternalServerError)
				return
			}
			for _, g := range grants {
				label := h.acl.UserEmail(g.Grantee)
				if label == "" {
					label = g.Grantee
				}
				card.Grants = append(card.Grants, templates.GrantRow{Grantee: g.Grantee, Label: label, Level: g.Level, State: g.State})
			}
		}
		cards = append(cards, card)
	}
	r = clockRequest(r, h.nowIn(userID))
	r = h.sharedSidebar(r, userID)
	templ.Handler(templates.CalendarSettingsPage(cards, calendars, email)).ServeHTTP(w, r)
}
