package icalevent

import (
	"testing"
	"time"
)

func TestZoneWindowsNames(t *testing.T) {
	if _, err := time.LoadLocation("Europe/Amsterdam"); err != nil {
		t.Skip("no zone database")
	}
	for name, want := range map[string]string{
		"W. Europe Standard Time":        "Europe/Berlin",
		"Eastern Standard Time":          "America/New_York",
		"Pacific Standard Time (Mexico)": "America/Tijuana",
		"Romance Standard Time":          "Europe/Paris",
	} {
		l, ok := Zone(name)
		if !ok {
			t.Errorf("Zone(%q) not ok", name)
			continue
		}
		if l.String() != want {
			t.Errorf("Zone(%q) = %s, want %s", name, l, want)
		}
		// A second call is served from the cache and agrees.
		if l2, _ := Zone(name); l2 != l {
			t.Errorf("Zone(%q) differs between calls", name)
		}
	}
}

// CLDR maps the Windows name "UTC" to Etc/UTC. An IANA name must keep winning,
// or a UTC series would be written with TZID=Etc/UTC and a VTIMEZONE.
func TestZoneUTCStaysUTC(t *testing.T) {
	if l, ok := Zone("UTC"); !ok || l != time.UTC {
		t.Errorf("Zone(UTC) = %v, %v; want time.UTC", l, ok)
	}
}

// Every table target must load. Hosts without a zone database skip; an old
// database that lacks a newer name fails, which says the database is stale.
func TestWindowsZoneTargetsLoad(t *testing.T) {
	if _, err := time.LoadLocation("Europe/Amsterdam"); err != nil {
		t.Skip("no zone database")
	}
	for win, iana := range windowsZones {
		if _, err := time.LoadLocation(iana); err != nil {
			t.Errorf("%q -> %q does not load: %v", win, iana, err)
		}
	}
}
