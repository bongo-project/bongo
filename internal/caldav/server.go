package caldav

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// Path hierarchy (after stripping /caldav prefix):
// /                                     -> root (depth 0)
// /{userID}/                            -> user principal (depth 1)
// /{userID}/calendars/                  -> calendar home set (depth 2)
// /{userID}/calendars/{calID}/          -> calendar (depth 3)
// /{userID}/calendars/{calID}/{guid}.ics -> calendar object (depth 4)

// Backend implements the caldav.Backend interface.
type Backend struct {
	events    *store.EventStore
	calendars *store.CalendarStore
}

// NewBackend creates a CalDAV backend.
func NewBackend(events *store.EventStore, calendars *store.CalendarStore) *Backend {
	return &Backend{events: events, calendars: calendars}
}

type ctxKey string

const userIDCtxKey ctxKey = "caldav-user-id"

// WithUserID adds a userID to the context.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDCtxKey, userID)
}

func getUserID(ctx context.Context) string {
	if v, ok := ctx.Value(userIDCtxKey).(string); ok {
		return v
	}
	return ""
}

func userPath(userID string) string {
	return "/caldav/" + userID + "/"
}

func homeSetPath(userID string) string {
	return "/caldav/" + userID + "/calendars/"
}

func calPath(userID, calID string) string {
	return "/caldav/" + userID + "/calendars/" + calID + "/"
}

func objectPath(userID, calID, guid string) string {
	return "/caldav/" + userID + "/calendars/" + calID + "/" + guid + ".ics"
}

// CurrentUserPrincipal returns the principal path for the current user.
func (b *Backend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return "", fmt.Errorf("no user in context")
	}
	return userPath(userID), nil
}

// CalendarHomeSetPath returns the calendar home set path.
func (b *Backend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return "", fmt.Errorf("no user in context")
	}
	return homeSetPath(userID), nil
}

// ListCalendars returns the user's calendars.
func (b *Backend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}

	// Ensure default calendar exists.
	if _, err := b.calendars.EnsureDefault(userID); err != nil {
		return nil, fmt.Errorf("ensuring default calendar: %w", err)
	}

	cals, err := b.calendars.List(userID)
	if err != nil {
		return nil, fmt.Errorf("listing calendars: %w", err)
	}

	var result []caldav.Calendar
	for _, c := range cals {
		result = append(result, caldav.Calendar{
			Path:                  calPath(userID, c.Guid),
			Name:                  c.Name,
			Description:           c.Description,
			SupportedComponentSet: []string{"VEVENT"},
		})
	}
	return result, nil
}

// GetCalendar returns calendar metadata.
func (b *Backend) GetCalendar(ctx context.Context, path string) (*caldav.Calendar, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}

	calID := calIDFromPath(path)
	if calID == "" {
		return nil, fmt.Errorf("invalid calendar path")
	}

	cal, err := b.calendars.Get(userID, calID)
	if err != nil {
		return nil, fmt.Errorf("calendar not found: %w", err)
	}

	return &caldav.Calendar{
		Path:                  calPath(userID, cal.Guid),
		Name:                  cal.Name,
		Description:           cal.Description,
		SupportedComponentSet: []string{"VEVENT"},
	}, nil
}

// CreateCalendar creates a new calendar.
func (b *Backend) CreateCalendar(ctx context.Context, calendar *caldav.Calendar) error {
	userID := getUserID(ctx)
	if userID == "" {
		return fmt.Errorf("no user in context")
	}

	cal := &bongov1.Calendar{
		Name:        calendar.Name,
		Description: calendar.Description,
	}
	_, err := b.calendars.Create(userID, cal)
	return err
}

// GetCalendarObject returns a single event as a CalDAV calendar object.
func (b *Backend) GetCalendarObject(ctx context.Context, path string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}

	guid := guidFromPath(path)
	if guid == "" {
		return nil, fmt.Errorf("invalid object path")
	}

	event, err := b.events.Get(userID, guid)
	if err != nil {
		return nil, fmt.Errorf("event not found: %w", err)
	}

	calID := calIDFromObjectPath(path)
	return eventToCalendarObject(userID, calID, event)
}

// ListCalendarObjects returns all events in a calendar.
func (b *Backend) ListCalendarObjects(ctx context.Context, path string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}

	calID := calIDFromPath(path)
	events, _, err := b.events.ListAll(userID, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}

	var objects []caldav.CalendarObject
	for _, e := range events {
		// Filter by calendar.
		if calID != "" && e.CalendarId != calID {
			continue
		}
		obj, err := eventToCalendarObject(userID, calID, e)
		if err != nil {
			continue
		}
		objects = append(objects, *obj)
	}
	return objects, nil
}

// QueryCalendarObjects handles search/filter queries.
func (b *Backend) QueryCalendarObjects(ctx context.Context, path string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	objects, err := b.ListCalendarObjects(ctx, path, &query.CompRequest)
	if err != nil {
		return nil, err
	}
	return caldav.Filter(query, objects)
}

// PutCalendarObject creates or updates an event from iCal data.
func (b *Backend) PutCalendarObject(ctx context.Context, path string, calendar *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, fmt.Errorf("no user in context")
	}

	guid := guidFromPath(path)
	if guid == "" {
		return nil, fmt.Errorf("invalid object path")
	}

	calID := calIDFromObjectPath(path)
	if calID == "" {
		calID = "default"
	}

	// Ensure the calendar exists.
	if _, err := b.calendars.EnsureDefault(userID); err != nil {
		return nil, fmt.Errorf("ensuring default calendar: %w", err)
	}

	// Ensure required calendar properties for encoding.
	if calendar.Props.Get(ical.PropProductID) == nil {
		calendar.Props.SetText(ical.PropProductID, "-//Bongo-Go//Calendar//EN")
	}

	// Encode iCal to raw bytes for round-trip fidelity.
	var rawBuf bytes.Buffer
	if err := ical.NewEncoder(&rawBuf).Encode(calendar); err != nil {
		return nil, fmt.Errorf("encoding ical: %w", err)
	}

	// Extract VEVENT fields.
	event := icalToEvent(calendar, guid, calID)
	event.IcalRaw = rawBuf.Bytes()

	// Check if event exists (update vs create).
	existing, _ := b.events.Get(userID, guid)
	if existing != nil {
		event.CreatedAt = existing.CreatedAt
		if err := b.events.Update(userID, guid, event); err != nil {
			return nil, fmt.Errorf("updating event: %w", err)
		}
	} else {
		if _, err := b.events.Create(userID, event); err != nil {
			return nil, fmt.Errorf("creating event: %w", err)
		}
	}

	return eventToCalendarObject(userID, calID, event)
}

// DeleteCalendarObject removes an event.
func (b *Backend) DeleteCalendarObject(ctx context.Context, path string) error {
	userID := getUserID(ctx)
	if userID == "" {
		return fmt.Errorf("no user in context")
	}

	guid := guidFromPath(path)
	if guid == "" {
		return fmt.Errorf("invalid object path")
	}

	return b.events.Delete(userID, guid)
}

// icalToEvent extracts fields from an iCal calendar into an Event proto.
func icalToEvent(cal *ical.Calendar, guid, calendarID string) *bongov1.Event {
	event := &bongov1.Event{
		Guid:       guid,
		CalendarId: calendarID,
	}

	events := cal.Events()
	if len(events) == 0 {
		return event
	}

	vevent := events[0]

	if summary, err := vevent.Props.Text(ical.PropSummary); err == nil {
		event.Title = summary
	}
	if desc, err := vevent.Props.Text(ical.PropDescription); err == nil {
		event.Description = desc
	}
	if loc, err := vevent.Props.Text(ical.PropLocation); err == nil {
		event.Location = loc
	}

	if dtstart, err := vevent.DateTimeStart(time.UTC); err == nil {
		event.StartTime = dtstart.Unix()

		// Check if all-day (DATE value type, no time component).
		if prop := vevent.Props.Get(ical.PropDateTimeStart); prop != nil {
			for _, param := range prop.Params["VALUE"] {
				if param == "DATE" {
					event.AllDay = true
				}
			}
		}
	}

	if dtend, err := vevent.DateTimeEnd(time.UTC); err == nil {
		event.EndTime = dtend.Unix()
	}

	// Timezone from DTSTART TZID parameter.
	if prop := vevent.Props.Get(ical.PropDateTimeStart); prop != nil {
		if tzids := prop.Params["TZID"]; len(tzids) > 0 {
			event.Timezone = tzids[0]
		}
	}

	// RRULE for round-trip.
	if rruleProp := vevent.Props.Get(ical.PropRecurrenceRule); rruleProp != nil {
		event.Rrule = rruleProp.Value
	}

	return event
}

// eventToCalendarObject converts an Event proto to a CalDAV CalendarObject.
func eventToCalendarObject(userID, calID string, e *bongov1.Event) (*caldav.CalendarObject, error) {
	var cal *ical.Calendar

	// If we have raw iCal data, use it for round-trip fidelity.
	if len(e.IcalRaw) > 0 {
		dec := ical.NewDecoder(bytes.NewReader(e.IcalRaw))
		var err error
		cal, err = dec.Decode()
		if err != nil {
			cal = nil
		}
	}

	// Otherwise, synthesize from proto fields.
	if cal == nil {
		cal = ical.NewCalendar()
		cal.Props.SetText(ical.PropVersion, "2.0")
		cal.Props.SetText(ical.PropProductID, "-//Bongo-Go//Calendar//EN")

		vevent := ical.NewEvent()
		vevent.Props.SetText(ical.PropUID, e.Guid)
		vevent.Props.SetText(ical.PropSummary, e.Title)

		if e.Description != "" {
			vevent.Props.SetText(ical.PropDescription, e.Description)
		}
		if e.Location != "" {
			vevent.Props.SetText(ical.PropLocation, e.Location)
		}

		loc := time.UTC
		if e.Timezone != "" {
			if l, err := time.LoadLocation(e.Timezone); err == nil {
				loc = l
			}
		}

		startTime := time.Unix(e.StartTime, 0).In(loc)
		endTime := time.Unix(e.EndTime, 0).In(loc)

		if e.AllDay {
			vevent.Props.SetDate(ical.PropDateTimeStart, startTime)
			vevent.Props.SetDate(ical.PropDateTimeEnd, endTime)
		} else {
			vevent.Props.SetDateTime(ical.PropDateTimeStart, startTime)
			vevent.Props.SetDateTime(ical.PropDateTimeEnd, endTime)
		}

		vevent.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())

		cal.Children = append(cal.Children, vevent.Component)
	}

	// Encode to bytes to get content length.
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("encoding ical: %w", err)
	}

	if calID == "" {
		calID = e.CalendarId
	}

	modTime := time.Unix(e.UpdatedAt, 0)
	if e.UpdatedAt == 0 {
		modTime = time.Unix(e.CreatedAt, 0)
	}

	return &caldav.CalendarObject{
		Path:          objectPath(userID, calID, e.Guid),
		ModTime:       modTime,
		ContentLength: int64(buf.Len()),
		ETag:          fmt.Sprintf(`"%d"`, e.UpdatedAt),
		Data:          cal,
	}, nil
}

// guidFromPath extracts the GUID from a CalDAV object path (strips .ics suffix).
func guidFromPath(path string) string {
	if strings.HasSuffix(path, ".ics") {
		path = path[:len(path)-4]
	}
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// calIDFromPath extracts the calendar ID from a collection path like
// /caldav/{userID}/calendars/{calID}/ or /caldav/{userID}/calendars/{calID}
func calIDFromPath(path string) string {
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(path, "/")
	// Expected: ["", "caldav", userID, "calendars", calID]
	if len(parts) >= 5 {
		return parts[4]
	}
	return ""
}

// calIDFromObjectPath extracts the calendar ID from an object path like
// /caldav/{userID}/calendars/{calID}/{guid}.ics
func calIDFromObjectPath(path string) string {
	parts := strings.Split(path, "/")
	// Expected: ["", "caldav", userID, "calendars", calID, "guid.ics"]
	if len(parts) >= 6 {
		return parts[4]
	}
	return ""
}
