package icalevent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// GroupByUID groups the VEVENTs of cal by UID in first-seen order, so a master
// and its overrides become one event. A VEVENT without a UID is a group of its
// own (it fails encoding later and is counted as failed).
func GroupByUID(cal *ical.Calendar) [][]*ical.Component {
	var groups [][]*ical.Component
	index := map[string]int{}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		uid := ""
		if p := c.Props.Get(ical.PropUID); p != nil {
			uid = p.Value
		}
		if uid == "" {
			groups = append(groups, []*ical.Component{c})
			continue
		}
		i, ok := index[uid]
		if !ok {
			i = len(groups)
			index[uid] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], c)
	}
	return groups
}

// ReferencedTZIDs lists the distinct TZID parameter values on c's properties
// (DTSTART, DTEND, EXDATE, RDATE, RECURRENCE-ID and so on), sorted. The order
// is fixed on purpose: the properties are a map, and an encoding that varied
// with map order would make an unchanged event look changed on every feed
// refresh.
func ReferencedTZIDs(c *ical.Component) []string {
	set := map[string]bool{}
	for _, props := range c.Props {
		for _, p := range props {
			for _, tz := range p.Params["TZID"] {
				set[tz] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for tz := range set {
		out = append(out, tz)
	}
	sort.Strings(out)
	return out
}

// EncodeGroup builds a standalone calendar from one UID group plus the
// VTIMEZONEs its TZID parameters reference, and encodes it. The error from the
// encoder is returned: an event whose data cannot be encoded is not stored.
// The output is deterministic for the same input.
func EncodeGroup(src *ical.Calendar, group []*ical.Component) ([]byte, error) {
	zones := map[string]*ical.Component{}
	for _, c := range src.Children {
		if c.Name != ical.CompTimezone {
			continue
		}
		if p := c.Props.Get(ical.PropTimezoneID); p != nil {
			zones[p.Value] = c
		}
	}

	out := ical.NewCalendar()
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")

	added := map[string]bool{}
	for _, vevent := range group {
		for _, tzid := range ReferencedTZIDs(vevent) {
			if z, ok := zones[tzid]; ok && !added[tzid] {
				added[tzid] = true
				out.Children = append(out.Children, z)
			}
		}
	}
	out.Children = append(out.Children, group...)

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(out); err != nil {
		return nil, fmt.Errorf("encoding imported event: %w", err)
	}
	return buf.Bytes(), nil
}

// FeedEvent is one event of a subscribed feed: every VEVENT sharing a UID.
type FeedEvent struct {
	UID string
	// Raw is the standalone iCalendar the event is stored from. It is the same
	// bytes for the same feed content, so it can be compared to a stored copy.
	Raw   []byte
	Event *bongov1.Event
}

// Feed is a parsed subscription body.
type Feed struct {
	Events []FeedEvent
	// RefreshHint is the feed's REFRESH-INTERVAL or X-PUBLISHED-TTL, 0 if none.
	RefreshHint time.Duration
	// Skipped counts VEVENTs without a UID, which cannot be kept in step.
	Skipped int
}

// ErrFeedTooLarge is returned when a feed holds more events than allowed.
var ErrFeedTooLarge = errors.New("feed has too many events")

// ParseFeed parses a whole subscription body. It is all or nothing: a decode
// error anywhere, a body with no calendar or no event
// or a feed over limit events fails the parse. The caller applies a feed only
// when it parsed completely, because the refresh deletes events the feed no
// longer lists, and a partial parse would delete the rest.
func ParseFeed(body []byte, limit int) (*Feed, error) {
	dec := ical.NewDecoder(bytes.NewReader(body))
	var cals []*ical.Calendar
	for {
		cal, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing feed: %w", err)
		}
		cals = append(cals, cal)
	}
	if len(cals) == 0 {
		return nil, errors.New("feed holds no calendar")
	}
	// The decoder reports a body cut off mid-calendar as a plain io.EOF, the
	// same as a clean end, so a truncated feed would parse as a shorter one
	// and the refresh would delete the events after the cut. Every
	// BEGIN:VCALENDAR must have come back as a calendar.
	if opened := countLines(body, "BEGIN:VCALENDAR"); opened != len(cals) {
		return nil, fmt.Errorf("feed is truncated: %d calendars opened, %d complete", opened, len(cals))
	}

	feed := &Feed{}
	type group struct {
		cal  *ical.Calendar
		uid  string
		vevs []*ical.Component
	}
	var groups []group
	seen := map[string]bool{}
	for _, cal := range cals {
		feed.RefreshHint = max(feed.RefreshHint, refreshHint(cal))
		for _, g := range GroupByUID(cal) {
			p := g[0].Props.Get(ical.PropUID)
			if p == nil || p.Value == "" {
				// Nothing to replace it by on the next refresh.
				feed.Skipped++
				continue
			}
			if seen[p.Value] {
				continue // a UID repeated across calendars: the first wins
			}
			seen[p.Value] = true
			groups = append(groups, group{cal: cal, uid: p.Value, vevs: g})
		}
	}
	if len(groups) == 0 {
		return nil, errors.New("feed holds no events")
	}
	// Counted before any encoding, so an over-cap feed costs nothing.
	if len(groups) > limit {
		return nil, fmt.Errorf("%w: %d, the limit is %d", ErrFeedTooLarge, len(groups), limit)
	}

	for _, g := range groups {
		// A feed is somebody else's calendar: its alarms must not ring on the
		// subscriber's devices through CalDAV, and the reminder loop reads
		// reminder_minutes, which stays 0.
		for _, v := range g.vevs {
			stripAlarms(v)
		}
		raw, err := EncodeGroup(g.cal, g.vevs)
		if err != nil {
			return nil, fmt.Errorf("parsing feed: %w", err)
		}
		rawCal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
		if err != nil {
			return nil, fmt.Errorf("parsing feed: %w", err)
		}
		ev, _ := ToEvent(rawCal)
		feed.Events = append(feed.Events, FeedEvent{UID: g.uid, Raw: raw, Event: ev})
	}
	return feed, nil
}

// stripAlarms removes the VALARM children of a VEVENT.
func stripAlarms(v *ical.Component) {
	kept := v.Children[:0]
	for _, c := range v.Children {
		if c.Name != ical.CompAlarm {
			kept = append(kept, c)
		}
	}
	v.Children = kept
}

// countLines counts the lines of body that equal want, ignoring case and
// surrounding white space.
func countLines(body []byte, want string) int {
	n := 0
	for _, line := range bytes.Split(body, []byte("\n")) {
		if strings.EqualFold(string(bytes.TrimSpace(line)), want) {
			n++
		}
	}
	return n
}

// refreshHint reads REFRESH-INTERVAL (RFC 7986) or the older X-PUBLISHED-TTL.
// The latter usually omits VALUE=DURATION, so the value is read as a duration
// whatever the parameter says.
func refreshHint(cal *ical.Calendar) time.Duration {
	for _, name := range []string{"REFRESH-INTERVAL", "X-PUBLISHED-TTL"} {
		p := cal.Props.Get(name)
		if p == nil {
			continue
		}
		dp := ical.NewProp(name)
		dp.Value = p.Value
		dp.SetValueType(ical.ValueDuration)
		d, err := dp.Duration()
		if err == nil && d > 0 {
			return d
		}
	}
	return 0
}
