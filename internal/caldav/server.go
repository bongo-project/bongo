package caldav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	webdav "github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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
	logger    *slog.Logger

	// quota gates PUT. Nil means unlimited (dev, tests, edge). DELETE is never
	// gated so a user over quota can still free space.
	quota QuotaChecker
}

// QuotaChecker is satisfied by *store.DomainQuotaStore. PUT asks whether the
// stored record (create) or its growth (update) would push the owner's domain
// over quota, the same question the web handlers ask.
type QuotaChecker interface {
	RecipientWouldExceedQuota(userID string, addBytes int64) (bool, error)
}

// ErrStorageQuotaExceeded is wrapped into the 507 Insufficient Storage that a
// PUT returns when the object would not fit in the owner's domain quota.
var ErrStorageQuotaExceeded = errors.New("storage quota exceeded")

// SetQuotaChecker installs the storage-quota checker. Call before serving.
func (b *Backend) SetQuotaChecker(qc QuotaChecker) { b.quota = qc }

// checkQuota refuses a write that would not fit. addBytes is the stored record
// size for a create or the growth of the record for an update; a PUT that does
// not grow the record (addBytes <= 0) is always allowed, even at quota, because
// clients re-PUT the whole object on every edit and a user at quota must be
// able to shrink or fix it. This matches the web handlers. DELETE is never
// gated. The check is best effort: the usage figure is recomputed periodically
// (about ten minutes stale) and concurrent PUTs can overshoot, so it is not a
// hard limit. A checker error fails open, as on the web paths: a transient
// store error must not turn into data loss for a user's calendar or contacts.
func (b *Backend) checkQuota(userID string, addBytes int64) error {
	if b.quota == nil || addBytes <= 0 {
		return nil
	}
	exceeds, err := b.quota.RecipientWouldExceedQuota(userID, addBytes)
	if err != nil {
		b.logger.Warn("quota check error (fail open)", "user", userID, "error", err)
		return nil
	}
	if exceeds {
		return webdav.NewHTTPError(http.StatusInsufficientStorage, ErrStorageQuotaExceeded)
	}
	return nil
}

// NewBackend creates a CalDAV backend.
func NewBackend(events *store.EventStore, calendars *store.CalendarStore, logger *slog.Logger) *Backend {
	return &Backend{events: events, calendars: calendars, logger: logger}
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
	if err != nil {
		b.logger.Warn("caldav: create calendar failed", "user", userID, "name", calendar.Name, "error", err)
		return err
	}
	b.logger.Info("caldav: calendar created", "user", userID, "name", calendar.Name)
	return nil
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
		calendar.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")
	}

	// Encode iCal to raw bytes for round-trip fidelity.
	var rawBuf bytes.Buffer
	if err := ical.NewEncoder(&rawBuf).Encode(calendar); err != nil {
		return nil, fmt.Errorf("encoding ical: %w", err)
	}

	// Extract VEVENT fields.
	event, fallback := icalToEvent(calendar, guid, calID)
	if fallback {
		b.logger.Debug("caldav: no master VEVENT, using earliest override", "user", userID, "guid", guid)
	}
	event.IcalRaw = rawBuf.Bytes()

	// Check if event exists (update vs create).
	existing, _ := b.events.Get(userID, guid)

	// Honour the conditional headers before writing anything. go-webdav parses
	// If-Match/If-None-Match into opts; ignoring them made every concurrent
	// edit a silent last-write-wins, which is the failure mode these headers
	// exist to prevent.
	if err := checkPreconditions(opts, existing); err != nil {
		return nil, err
	}

	// Size the write with the quantity the usage recompute counts: the stored
	// record. A create is the whole record; an update is its growth.
	var addBytes int64
	if existing != nil {
		event.CreatedAt = existing.CreatedAt
		event.UpdatedAt = time.Now().Unix()
		addBytes = int64(proto.Size(event) - proto.Size(existing))
	} else {
		addBytes = store.EventRecordSize(event)
	}
	if err := b.checkQuota(userID, addBytes); err != nil {
		return nil, err
	}

	if existing != nil {
		if err := b.events.Update(userID, guid, event); err != nil {
			return nil, fmt.Errorf("updating event: %w", err)
		}
		b.logger.Info("caldav: event updated", "user", userID, "calendar", calID, "guid", guid)
	} else {
		if _, err := b.events.Create(userID, event); err != nil {
			return nil, fmt.Errorf("creating event: %w", err)
		}
		b.logger.Info("caldav: event created", "user", userID, "calendar", calID, "guid", guid)
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

	if err := b.events.Delete(userID, guid); err != nil {
		b.logger.Warn("caldav: delete event failed", "user", userID, "guid", guid, "error", err)
		return err
	}
	b.logger.Info("caldav: event deleted", "user", userID, "guid", guid)
	return nil
}

// icalToEvent extracts the summary fields of the series master into an Event
// proto. fallback reports that the calendar held only overrides.
func icalToEvent(cal *ical.Calendar, guid, calendarID string) (event *bongov1.Event, fallback bool) {
	event, fallback = icalevent.ToEvent(cal)
	event.Guid = guid
	event.CalendarId = calendarID
	return event, fallback
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
		cal.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")

		vevent := ical.NewEvent()
		vevent.Props.SetText(ical.PropUID, e.Guid)
		vevent.Props.SetText(ical.PropSummary, e.Title)

		if e.Description != "" {
			vevent.Props.SetText(ical.PropDescription, e.Description)
		}
		if e.Location != "" {
			vevent.Props.SetText(ical.PropLocation, e.Location)
		}

		if e.AllDay {
			// A date, not an instant: the zone plays no part.
			vevent.Props.SetDate(ical.PropDateTimeStart, icalevent.AllDayDate(e.StartTime))
			vevent.Props.SetDate(ical.PropDateTimeEnd, icalevent.AllDayDate(e.EndTime))
		} else {
			loc := time.UTC
			if e.Timezone != "" {
				if l, err := time.LoadLocation(e.Timezone); err == nil {
					loc = l
				}
			}
			vevent.Props.SetDateTime(ical.PropDateTimeStart, time.Unix(e.StartTime, 0).In(loc))
			vevent.Props.SetDateTime(ical.PropDateTimeEnd, time.Unix(e.EndTime, 0).In(loc))
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
		ETag:          store.ContentETag(e.IcalRaw, e.UpdatedAt),
		Data:          cal,
	}, nil
}

// guidFromPath extracts the GUID from a CalDAV object path (strips .ics suffix).
func guidFromPath(path string) string {
	path = strings.TrimSuffix(path, ".ics")
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

// ErrPreconditionFailed is the cause wrapped into every 412 this backend
// returns. go-webdav's HTTPError lives in its internal package, so its status
// code cannot be inspected from outside the library; wrapping a sentinel gives
// callers and tests something stable to match with errors.Is without depending
// on message text.
var ErrPreconditionFailed = errors.New("precondition failed")

// checkPreconditions enforces If-Match and If-None-Match for a PUT.
//
// If-None-Match: * means "create only" -- fail if the resource exists.
// If-Match: <etag> means "update only this version" -- fail if the resource is
// gone or has moved on. Both answer 412 so the client re-reads and retries
// rather than silently clobbering somebody else's edit.
//
// A malformed header value is treated as a failed precondition rather than
// ignored: a client that sent a condition it meant must never have the write
// applied unconditionally.
func checkPreconditions(opts *caldav.PutCalendarObjectOptions, existing *bongov1.Event) error {
	if opts == nil {
		return nil
	}

	if opts.IfNoneMatch.IsSet() {
		if !opts.IfNoneMatch.IsWildcard() {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: If-None-Match supports only *", ErrPreconditionFailed))
		}
		if existing != nil {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: If-None-Match: * but the object already exists", ErrPreconditionFailed))
		}
	}

	if opts.IfMatch.IsSet() {
		if existing == nil {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: If-Match on an object that does not exist", ErrPreconditionFailed))
		}
		want := store.ContentETagValue(existing.IcalRaw, existing.UpdatedAt)
		ok, err := opts.IfMatch.MatchETag(want)
		if err != nil {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: malformed If-Match: %v", ErrPreconditionFailed, err))
		}
		if !ok {
			return webdav.NewHTTPError(http.StatusPreconditionFailed,
				fmt.Errorf("%w: If-Match does not match the current object", ErrPreconditionFailed))
		}
	}

	return nil
}
