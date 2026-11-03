package icalevent

//go:generate go run ../../scripts/gen-windowszones -in testdata/windowsZones.xml -out windowszones_gen.go -tag release-48

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
// system cannot load. A Windows name such as Outlook's "W. Europe Standard
// Time" is read as the IANA zone CLDR gives it (windowszones_gen.go). The match
// is exact, and an IANA name always wins over the table: CLDR maps "UTC" to
// Etc/UTC, which must not replace time.UTC. A caller that needs a zone anyway
// uses UTC.
func Zone(name string) (loc *time.Location, ok bool) {
	if name == "" || name == "Local" || len(name) > 64 {
		return nil, false
	}
	if v, hit := zoneCache.Load(name); hit {
		return v.(*time.Location), true
	}
	var l *time.Location
	var err error
	shaped := zoneNameShape(name)
	if shaped {
		l, err = time.LoadLocation(name)
	}
	if !shaped || err != nil {
		// Not an IANA name that loads: try the Windows table. Its keys are
		// fixed, so a name outside the IANA shape gets in only by exact match.
		if iana, found := windowsZones[name]; found {
			l, err = time.LoadLocation(iana)
			shaped = true
		}
	}
	if !shaped {
		return nil, false
	}
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
