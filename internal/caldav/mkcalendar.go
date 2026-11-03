package caldav

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/emersion/go-webdav/caldav"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Calendar creation over CalDAV. go-webdav v0.7.0 answers MKCALENDAR with 405,
// and its extended MKCOL decodes only the displayname (it drops
// supported-calendar-component-set and calendar-description) and mints no
// calendar at the requested path. This handler therefore takes both methods on
// /caldav/{user}/calendars/{segment}/ itself, reads the body, and calls
// Backend.CreateCalendar, the single creation path.

var (
	errCalendarExists    = errors.New("calendar already exists")
	errBadCalendarName   = errors.New("invalid calendar path")
	errNoCalendarComps   = errors.New("no supported calendar component")
	errCalendarForeign   = errors.New("calendar belongs to another user")
	errCalendarSharedSeg = errors.New("shared collections cannot be created")
)

const (
	maxMkcalendarBody = 64 << 10
	maxCalendarName   = 255
	maxCalendarDesc   = 4096
)

// calendarSegmentRE is the one rule for a client-chosen calendar ID. The
// segment becomes a Pebble key part, a web route parameter and a grant ID
// segment, so it is strict. Case is kept: Apple and Thunderbird send upper-case
// UUIDs.
var calendarSegmentRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func validCalendarSegment(seg string) bool {
	return calendarSegmentRE.MatchString(seg) &&
		!strings.HasPrefix(seg, sharedPrefix) &&
		!strings.HasPrefix(seg, "sub-") &&
		!strings.HasSuffix(strings.ToLower(seg), ".ics")
}

// collectionPath splits /caldav/{user}/calendars/{segment}[/]. ok is false for
// any other shape.
func collectionPath(path string) (user, seg string, ok bool) {
	rest, found := strings.CutPrefix(path, "/caldav/")
	if !found {
		return "", "", false
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) != 3 || parts[1] != "calendars" || parts[0] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

// normaliseComponents upper-cases names and keeps VEVENT and VTODO, once each,
// in request order.
func normaliseComponents(names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.ToUpper(strings.TrimSpace(n))
		if (n == store.ComponentEvent || n == store.ComponentTodo) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// cleanText drops control characters, trims and caps the length in runes.
func cleanText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > limit {
		s = strings.TrimSpace(string(r[:limit]))
	}
	return s
}

// CreateCalendar creates the calendar named by calendar.Path (the last segment
// is its ID) with the components in calendar.SupportedComponentSet; an empty
// set means VEVENT. It never overwrites: an existing calendar gives
// errCalendarExists. The existence check is serialised per node only, which is
// enough for the single production node.
func (b *Backend) CreateCalendar(ctx context.Context, calendar *caldav.Calendar) error {
	userID := getUserID(ctx)
	if userID == "" {
		return fmt.Errorf("no user in context")
	}
	user, seg, ok := collectionPath(calendar.Path)
	switch {
	case !ok:
		return errBadCalendarName
	case user != userID:
		return errCalendarForeign
	case strings.HasPrefix(seg, sharedPrefix):
		return errCalendarSharedSeg
	case !validCalendarSegment(seg):
		return errBadCalendarName
	}

	var comps []string
	if len(calendar.SupportedComponentSet) > 0 {
		comps = normaliseComponents(calendar.SupportedComponentSet)
		// A VTODO-only calendar is hidden by Backend.components while there
		// is no task store, so it would answer as an event calendar.
		if len(comps) == 0 || (b.tasks == nil && !slices.Contains(comps, store.ComponentEvent)) {
			return errNoCalendarComps
		}
	}
	if slices.Equal(comps, []string{store.ComponentEvent}) {
		comps = nil // the stored default
	}

	name := cleanText(calendar.Name, maxCalendarName)
	if name == "" {
		name = seg
	}

	// A first-ever write must not leave the user with this calendar alone.
	if _, err := b.calendars.EnsureDefault(userID); err != nil {
		return fmt.Errorf("ensuring default calendars: %w", err)
	}
	err := b.calendars.CreateNew(userID, &bongov1.Calendar{
		Guid:        seg,
		Name:        name,
		Description: cleanText(calendar.Description, maxCalendarDesc),
		Components:  comps,
	})
	switch {
	case errors.Is(err, store.ErrExists):
		return errCalendarExists
	case err != nil:
		return fmt.Errorf("creating calendar: %w", err)
	}
	b.logger.Info("caldav: calendar created", "user", userID, "calendar", seg)
	return nil
}

// DeleteCalendar deletes the calendar at userID's collection segment seg, with
// its events, tasks, grants and feed, through the helper the web uses
// (store.PurgeCalendar documents the partial-failure semantics). It returns
// store.ErrNotFound (wrapped) for a missing calendar, and
// store.ErrCalendarProtected, store.ErrCalendarSubscribed or
// store.ErrLastEventCalendar for a refusal, in which case nothing changed.
func (b *Backend) DeleteCalendar(userID, seg string) error {
	if _, err := b.calendars.Get(userID, seg); err != nil {
		return fmt.Errorf("reading calendar: %w", err)
	}
	cals, err := b.calendars.List(userID)
	if err != nil {
		return fmt.Errorf("listing calendars: %w", err)
	}
	if err := store.CheckCalendarDeletable(cals, seg); err != nil {
		return err
	}
	if err := store.PurgeCalendar(b.calendars, b.events, b.tasks, userID, seg); err != nil {
		return err
	}
	b.logger.Info("caldav: calendar deleted", "user", userID, "calendar", seg)
	return nil
}

func serveDeleteCalendar(w http.ResponseWriter, b *Backend, logger *slog.Logger, user, seg string) {
	switch err := b.DeleteCalendar(user, seg); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, store.ErrCalendarProtected):
		http.Error(w, "this calendar cannot be deleted", http.StatusForbidden)
	case errors.Is(err, store.ErrCalendarSubscribed):
		http.Error(w, "unsubscribe from this calendar in the web interface", http.StatusConflict)
	case errors.Is(err, store.ErrLastEventCalendar):
		http.Error(w, "the last calendar for events cannot be deleted", http.StatusConflict)
	default:
		logger.Error("caldav: delete calendar failed", "user", user, "calendar", seg, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// MkcalendarHandler serves MKCALENDAR, and MKCOL on a calendar collection path,
// in front of go-webdav, and DELETE on a calendar collection path (go-webdav
// has no collection delete and would answer 404). Everything else goes to next.
func MkcalendarHandler(next http.Handler, b *Backend, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := getUserID(r.Context())
		if user == "" || (r.Method != "MKCALENDAR" && r.Method != "MKCOL" && r.Method != http.MethodDelete) {
			next.ServeHTTP(w, r)
			return
		}
		owner, seg, ok := collectionPath(r.URL.Path)
		if !ok {
			if r.Method == http.MethodDelete {
				next.ServeHTTP(w, r)
				return
			}
			if r.Method == "MKCOL" {
				next.ServeHTTP(w, r)
				return
			}
			writePrecondition(w, http.StatusForbidden, xml.Name{Space: caldavNS, Local: "calendar-collection-location-ok"})
			return
		}
		// Refused before any store read, so existence cannot be probed.
		if owner != user || strings.HasPrefix(seg, sharedPrefix) || !validCalendarSegment(seg) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodDelete {
			serveDeleteCalendar(w, b, logger, user, seg)
			return
		}
		cal, status := parseMkcalendarBody(r)
		if status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
		cal.Path = calPath(owner, seg)

		switch err := b.CreateCalendar(r.Context(), cal); {
		case err == nil:
			w.Header().Set("Location", cal.Path)
			w.WriteHeader(http.StatusCreated)
		case errors.Is(err, errCalendarExists):
			writePrecondition(w, http.StatusMethodNotAllowed, xml.Name{Space: davNS, Local: "resource-must-be-null"})
		case errors.Is(err, errNoCalendarComps):
			writePrecondition(w, http.StatusForbidden, xml.Name{Space: caldavNS, Local: "supported-calendar-component"})
		case errors.Is(err, errBadCalendarName), errors.Is(err, errCalendarForeign), errors.Is(err, errCalendarSharedSeg):
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			logger.Error("caldav: create calendar failed", "user", user, "calendar", seg, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	})
}

type mkcalendarProp struct {
	DisplayName  *string `xml:"DAV: displayname"`
	Description  *string `xml:"urn:ietf:params:xml:ns:caldav calendar-description"`
	ResourceType *struct {
		Inner []struct {
			XMLName xml.Name
		} `xml:",any"`
	} `xml:"DAV: resourcetype"`
	Components *struct {
		Comp []struct {
			Name string `xml:"name,attr"`
		} `xml:"urn:ietf:params:xml:ns:caldav comp"`
	} `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set"`
}

type mkcalendarBody struct {
	XMLName xml.Name
	Set     []struct {
		Prop mkcalendarProp `xml:"DAV: prop"`
	} `xml:"DAV: set"`
}

// parseMkcalendarBody reads the properties a client asks for. Others (colour,
// order, time zone, transparency) are ignored, not answered with a 207. A
// non-zero status is the HTTP answer to give.
func parseMkcalendarBody(r *http.Request) (*caldav.Calendar, int) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxMkcalendarBody+1))
	if err != nil || len(raw) > maxMkcalendarBody {
		return nil, http.StatusBadRequest
	}
	cal := &caldav.Calendar{}
	if len(strings.TrimSpace(string(raw))) == 0 {
		if r.Method == "MKCOL" {
			return nil, http.StatusForbidden // plain MKCOL: not a calendar
		}
		return cal, 0 // RFC 4791 5.3.1: the body is optional
	}
	var body mkcalendarBody
	if err := xml.Unmarshal(raw, &body); err != nil {
		return nil, http.StatusBadRequest
	}
	wantRoot := xml.Name{Space: caldavNS, Local: "mkcalendar"}
	if r.Method == "MKCOL" {
		wantRoot = xml.Name{Space: davNS, Local: "mkcol"}
	}
	if body.XMLName != wantRoot {
		return nil, http.StatusBadRequest
	}
	var isCollection, isCalendar bool
	for _, set := range body.Set {
		p := set.Prop
		if p.DisplayName != nil {
			cal.Name = *p.DisplayName
		}
		if p.Description != nil {
			cal.Description = *p.Description
		}
		if p.ResourceType != nil {
			for _, t := range p.ResourceType.Inner {
				isCollection = isCollection || t.XMLName == xml.Name{Space: davNS, Local: "collection"}
				isCalendar = isCalendar || t.XMLName == xml.Name{Space: caldavNS, Local: "calendar"}
			}
		}
		if p.Components != nil {
			names := []string{}
			for _, c := range p.Components.Comp {
				names = append(names, c.Name)
			}
			// Present but unusable (empty, or only VJOURNAL and the like) must
			// not read as absent, which means VEVENT.
			if len(normaliseComponents(names)) == 0 {
				names = []string{"NONE"}
			}
			cal.SupportedComponentSet = names
		}
	}
	if r.Method == "MKCOL" && (!isCollection || !isCalendar) {
		return nil, http.StatusForbidden
	}
	return cal, 0
}
