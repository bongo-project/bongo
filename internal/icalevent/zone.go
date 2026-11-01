package icalevent

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// zoneCache holds only zones that loaded. A TZID is client-controlled, so
// caching failures would let a client grow the map without bound.
var zoneCache sync.Map

// zoneWarned remembers which unloadable names have been logged, up to
// maxZoneWarnings, so a stream of odd TZIDs cannot flood the log or the map.
var (
	zoneWarnMu   sync.Mutex
	zoneWarned   = map[string]bool{}
	maxZoneWarns = 128
)

// Zone returns the IANA zone called name. ok is false for an empty name, for
// "Local" (the process zone is never an event's zone), for names that are not
// shaped like an IANA name (this keeps control characters and parameter
// delimiters out of anything later written as a TZID) and for names the
// system cannot load, such as Outlook's "W. Europe Standard Time". A caller
// that needs a zone anyway uses UTC.
func Zone(name string) (loc *time.Location, ok bool) {
	if name == "" || name == "Local" || len(name) > 64 || !zoneNameShape(name) {
		return nil, false
	}
	if v, hit := zoneCache.Load(name); hit {
		return v.(*time.Location), true
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		warnZoneOnce(name)
		return nil, false
	}
	zoneCache.Store(name, l)
	return l, true
}

func zoneNameShape(name string) bool {
	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '+' || r == '-' || r == '/':
		default:
			return false
		}
	}
	return true
}

func warnZoneOnce(name string) {
	zoneWarnMu.Lock()
	first := !zoneWarned[name] && len(zoneWarned) < maxZoneWarns
	if first {
		zoneWarned[name] = true
	}
	zoneWarnMu.Unlock()
	if first {
		slog.Default().Warn("calendar: time zone is not an IANA name, using UTC", "tzid", name)
	}
}

// ZoneOrUTC is Zone with UTC as the fallback.
func ZoneOrUTC(name string) *time.Location {
	if l, ok := Zone(name); ok {
		return l
	}
	return time.UTC
}

// formatOffset renders a UTC offset in seconds as an iCalendar UTC-OFFSET
// (+hhmm, or +hhmmss when the offset has seconds).
func formatOffset(sec int) string {
	sign := '+'
	if sec < 0 {
		sign = '-'
		sec = -sec
	}
	h, m, s := sec/3600, sec%3600/60, sec%60
	if s != 0 {
		return fmt.Sprintf("%c%02d%02d%02d", sign, h, m, s)
	}
	return fmt.Sprintf("%c%02d%02d", sign, h, m)
}
