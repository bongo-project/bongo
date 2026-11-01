package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Published calendar feeds (US-010).
//
// GET /pub/calendar/{owner}/{calendar}/{token}.ics is the one unauthenticated
// data route. The token is a capability stored on the Calendar record: 32
// random bytes, base64url in the link, independent of the session secret. It
// is never logged: no handler here logs r.URL or the token, and no error path
// passes either to slog.
//
// Every failure before the token has been validated answers one identical
// 404, so an unknown owner, an unknown or unpublished calendar, a malformed
// token, a wrong token and another calendar's token cannot be told apart. The
// password check (401, 429) happens only after the token validates, and the
// event scan only after the password.

const (
	publishTokenBytes = 32
	publishDetailFull = "full"
	publishDetailBusy = "busy"
	// publishMaxAge is the Cache-Control max-age of a feed response, seconds.
	publishMaxAge = 300
	// publishMaxFailures wrong passwords within publishFailWindow lock a key
	// (the client IP, and the calendar as a whole) out with 429. The
	// per-calendar key is a deliberate denial-of-service lever for anyone who
	// holds the link but not the password: the throttle the requirement asks
	// for costs the legitimate subscribers their feed for the window.
	publishMaxFailures = 10
	publishFailWindow  = 15 * time.Minute
	// Bounds for a feed password. 72 is bcrypt's input limit.
	publishPasswordMin = 8
	publishPasswordMax = 72
)

// dummyToken is compared against when the calendar does not exist, so the
// miss costs the same comparison as a hit.
var dummyToken = make([]byte, publishTokenBytes)

// CalendarPublishHandler serves published calendar feeds.
type CalendarPublishHandler struct {
	events    *store.EventStore
	calendars *store.CalendarStore
	// limiter throttles wrong feed passwords per IP and per calendar. It is
	// its own instance: sharing the CalDAV one would let feed failures lock a
	// client out of CalDAV/CardDAV.
	limiter *authlimit.Limiter
}

// NewCalendarPublishHandler creates the feed handler.
func NewCalendarPublishHandler(events *store.EventStore, calendars *store.CalendarStore) *CalendarPublishHandler {
	return &CalendarPublishHandler{
		events:    events,
		calendars: calendars,
		limiter:   authlimit.New(publishMaxFailures, publishFailWindow),
	}
}

// feedNotFound writes the one 404 every pre-authorisation failure shares.
func feedNotFound(w http.ResponseWriter) {
	http.Error(w, "not found", http.StatusNotFound)
}

// ServeFeed is the GET handler: the calendar as iCalendar.
func (h *CalendarPublishHandler) ServeFeed(w http.ResponseWriter, r *http.Request) {
	owner := chi.URLParam(r, "owner")
	guid := chi.URLParam(r, "calendar")

	// A bad encoding or length still falls through to the same lookup and
	// comparison, so every failure path does the same work.
	token, decErr := base64.RawURLEncoding.DecodeString(chi.URLParam(r, "token"))
	wellFormed := decErr == nil && len(token) == publishTokenBytes
	if !wellFormed {
		token = dummyToken
	}

	var cal *bongov1.Calendar
	if validOwnerID(owner) && validOwnerID(guid) {
		cal, _ = h.calendars.Get(owner, guid)
	}
	stored := dummyToken
	live := cal != nil && cal.Published && len(cal.PublishToken) == publishTokenBytes
	if live {
		stored = cal.PublishToken
	}
	if subtle.ConstantTimeCompare(token, stored) != 1 || !wellFormed || !live {
		feedNotFound(w)
		return
	}

	if cal.PublishPasswordHash != "" && !h.authorise(w, r, owner, guid, cal.PublishPasswordHash) {
		return
	}

	events, _, err := h.events.ListAll(owner, 0, 0)
	if err != nil {
		slog.Error("calendar feed: listing events failed", "owner", owner, "calendar", guid, "error", err)
		http.Error(w, "failed to list events", http.StatusInternalServerError)
		return
	}
	inCal := events[:0:0]
	for _, e := range events {
		if eventInCalendar(e, guid) {
			inCal = append(inCal, e)
		}
	}

	busy := cal.PublishDetail == publishDetailBusy
	feed := buildCalendarICS(inCal, publishTransform(busy), func(e *bongov1.Event) time.Time {
		return time.Unix(max(e.UpdatedAt, e.CreatedAt), 0)
	})
	var buf strings.Builder
	if len(feed.Children) == 0 {
		// The encoder refuses a calendar with no components, but an empty
		// published calendar is a valid feed.
		buf.WriteString("BEGIN:VCALENDAR\r\nPRODID:-//Bongo//Calendar//EN\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n")
	} else if err := ical.NewEncoder(&buf).Encode(feed); err != nil {
		slog.Error("calendar feed: encoding failed", "owner", owner, "calendar", guid, "error", err)
		http.Error(w, "failed to encode calendar", http.StatusInternalServerError)
		return
	}
	body := buf.String()

	// The ETag is a hash of the body only; it must never be derived from the
	// token.
	sum := sha256.Sum256([]byte(body))
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	hdr := w.Header()
	hdr.Set("ETag", etag)
	hdr.Set("Cache-Control", fmt.Sprintf("private, max-age=%d", publishMaxAge))
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hdr.Set("Content-Type", "text/calendar; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

// authorise enforces the feed password on a request whose token is valid. It
// writes the 401 or 429 itself and reports whether to continue.
//
// Any user name is accepted. A request without credentials is a plain
// challenge, not a guess: it costs no bcrypt and is not counted, otherwise a
// client that always probes before sending credentials would lock itself out.
func (h *CalendarPublishHandler) authorise(w http.ResponseWriter, r *http.Request, owner, guid, hash string) bool {
	ipKey := middleware.ClientIP(r)
	calKey := "pubcal\x00" + owner + "\x00" + guid
	// Checked before bcrypt runs so a locked-out source costs nothing.
	if h.limiter.Blocked(ipKey) || h.limiter.Blocked(calKey) {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(publishFailWindow.Seconds())))
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return false
	}
	_, pw, ok := r.BasicAuth()
	if ok && bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil {
		return true
	}
	if ok {
		h.limiter.Record(ipKey)
		h.limiter.Record(calKey)
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="calendar", charset="UTF-8"`)
	http.Error(w, "authentication required", http.StatusUnauthorized)
	return false
}

// etagMatches reports whether an If-None-Match header lists etag (or *).
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if part == "*" || part == etag {
			return true
		}
	}
	return false
}

// eventInCalendar reports whether e belongs to calendar guid; an event with
// no calendar belongs to the default one, whose guid is "default".
func eventInCalendar(e *bongov1.Event, guid string) bool {
	if e.CalendarId == "" {
		return guid == "default"
	}
	return e.CalendarId == guid
}

// busyKept is the allow-list of properties a busy-form VEVENT keeps: its
// identity and its time structure. RRULE, RDATE, EXDATE and RECURRENCE-ID are
// kept alongside DTSTART and DTEND because without them a recurring series
// would show only its first instance. Everything else, including every X-
// property, is dropped.
var busyKept = map[string]bool{
	ical.PropUID:             true,
	ical.PropDateTimeStamp:   true,
	ical.PropDateTimeStart:   true,
	ical.PropDateTimeEnd:     true,
	ical.PropDuration:        true,
	ical.PropRecurrenceRule:  true,
	ical.PropRecurrenceDates: true,
	ical.PropExceptionDates:  true,
	ical.PropRecurrenceID:    true,
}

// publishTransform returns the per-VEVENT transform of a feed. Both modes drop
// ATTENDEE, ORGANIZER and every sub-component (VALARM). A private event
// (CLASS PRIVATE or CONFIDENTIAL, read from the raw VEVENT: the Event proto
// has no class field) is reduced to the busy form even in full mode. In busy
// mode, transparent and cancelled events are left out.
func publishTransform(busy bool) func(*ical.Component) *ical.Component {
	return func(c *ical.Component) *ical.Component {
		if busy {
			if v, _ := c.Props.Text(ical.PropTransparency); strings.EqualFold(v, "TRANSPARENT") {
				return nil
			}
			if v, _ := c.Props.Text(ical.PropStatus); strings.EqualFold(v, "CANCELLED") {
				return nil
			}
		}
		class, _ := c.Props.Text(ical.PropClass)
		class = strings.ToUpper(class)
		reduce := busy || class == "PRIVATE" || class == "CONFIDENTIAL"

		delete(c.Props, ical.PropAttendee)
		delete(c.Props, ical.PropOrganizer)
		c.Children = nil
		if !reduce {
			return c
		}
		for name := range c.Props {
			if !busyKept[strings.ToUpper(name)] {
				delete(c.Props, name)
			}
		}
		c.Props.SetText(ical.PropSummary, "Busy")
		return c
	}
}

// newPublishToken returns 32 bytes from crypto/rand.
func newPublishToken() ([]byte, error) {
	t := make([]byte, publishTokenBytes)
	if _, err := rand.Read(t); err != nil {
		return nil, fmt.Errorf("generating publish token: %w", err)
	}
	return t, nil
}

// PublishLinks returns the https:// and webcal:// links of a published
// calendar for host. The token is the capability, so callers show it only to
// the owner.
func PublishLinks(host, owner, guid string, token []byte) (httpsLink, webcalLink string) {
	path := "//" + host + "/pub/calendar/" + url.PathEscape(owner) + "/" + url.PathEscape(guid) + "/" +
		base64.RawURLEncoding.EncodeToString(token) + ".ics"
	return "https:" + path, "webcal:" + path
}

// publishSettings builds the settings-card data for one calendar.
func publishSettings(host, owner string, c *bongov1.Calendar) templates.PublishSettings {
	p := templates.PublishSettings{
		Calendar:    c.Guid,
		Published:   c.Published && len(c.PublishToken) == publishTokenBytes,
		Busy:        c.PublishDetail == publishDetailBusy,
		HasPassword: c.PublishPasswordHash != "",
	}
	if p.Published {
		p.HTTPSLink, p.WebcalLink = PublishLinks(host, owner, c.Guid, c.PublishToken)
	}
	return p
}

// publishDone ends a successful settings mutation.
func publishDone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("HX-Trigger", "calendarRefresh")
	refreshSettings(w, r)
	w.WriteHeader(http.StatusOK)
}

// publishMutation looks the calendar up as its owner (a grantee gets 404),
// then applies fn through the calendar lock. All four mutation routes share
// it.
func (h *CalendarHandler) publishMutation(w http.ResponseWriter, r *http.Request, fn func(*bongov1.Calendar) error) {
	userID, _ := middleware.UserFromContext(r.Context())
	calID := chi.URLParam(r, "calendarID")
	if _, err := h.calendars.Get(userID, calID); err != nil {
		http.Error(w, "calendar not found", http.StatusNotFound)
		return
	}
	if err := h.calendars.Mutate(userID, calID, fn); err != nil {
		slog.Error("calendar: publish update failed", "user", userID, "calendar", calID, "error", err)
		http.Error(w, "failed to update calendar", http.StatusInternalServerError)
		return
	}
	publishDone(w, r)
}

// PublishCalendar handles POST: publish with a detail level, generating a
// token when there is none. Repeating it changes only the detail.
func (h *CalendarHandler) PublishCalendar(w http.ResponseWriter, r *http.Request) {
	detail := r.FormValue("detail")
	if detail == "" {
		detail = publishDetailFull
	}
	if detail != publishDetailFull && detail != publishDetailBusy {
		http.Error(w, "invalid detail", http.StatusBadRequest)
		return
	}
	h.publishMutation(w, r, func(c *bongov1.Calendar) error {
		if len(c.PublishToken) != publishTokenBytes {
			t, err := newPublishToken()
			if err != nil {
				return err
			}
			c.PublishToken = t
		}
		c.Published = true
		c.PublishDetail = detail
		return nil
	})
}

// RegeneratePublish handles POST: a new token; the old link answers 404 at
// once on this node. A calendar that is not published is refused.
func (h *CalendarHandler) RegeneratePublish(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	cal, err := h.calendars.Get(userID, chi.URLParam(r, "calendarID"))
	if err != nil {
		http.Error(w, "calendar not found", http.StatusNotFound)
		return
	}
	if !cal.Published {
		http.Error(w, "calendar is not published", http.StatusConflict)
		return
	}
	h.publishMutation(w, r, func(c *bongov1.Calendar) error {
		if !c.Published {
			return fmt.Errorf("calendar was unpublished meanwhile")
		}
		t, err := newPublishToken()
		if err != nil {
			return err
		}
		c.PublishToken = t
		return nil
	})
}

// UnpublishCalendar handles POST: stop publishing, clear the token and the
// feed password.
func (h *CalendarHandler) UnpublishCalendar(w http.ResponseWriter, r *http.Request) {
	h.publishMutation(w, r, func(c *bongov1.Calendar) error {
		c.Published = false
		c.PublishToken = nil
		c.PublishPasswordHash = ""
		return nil
	})
}

// SetPublishPassword handles POST: set the feed password, or remove it with
// remove_password=1. An empty password without it changes nothing.
func (h *CalendarHandler) SetPublishPassword(w http.ResponseWriter, r *http.Request) {
	remove := r.FormValue("remove_password") != ""
	pw := r.FormValue("password")
	var hash string
	switch {
	case remove:
	case pw == "":
		userID, _ := middleware.UserFromContext(r.Context())
		if _, err := h.calendars.Get(userID, chi.URLParam(r, "calendarID")); err != nil {
			http.Error(w, "calendar not found", http.StatusNotFound)
			return
		}
		publishDone(w, r)
		return
	default:
		if len(pw) < publishPasswordMin || len(pw) > publishPasswordMax {
			http.Error(w, "password must be 8 to 72 bytes", http.StatusBadRequest)
			return
		}
		b, err := bcrypt.GenerateFromPassword([]byte(pw), auth.PasswordHashCost)
		if err != nil {
			http.Error(w, "failed to update calendar", http.StatusInternalServerError)
			return
		}
		hash = string(b)
	}
	h.publishMutation(w, r, func(c *bongov1.Calendar) error {
		c.PublishPasswordHash = hash
		return nil
	})
}
