package templates

import (
	"bufio"
	"os"
	"sort"
	"strings"
	"sync"
)

var (
	tzOnce sync.Once
	tzList []string
)

// commonTimezones returns the IANA time-zone names for the time-zone selectors.
// The canonical list is read once from the system tz database (zone1970.tab,
// shipped by the tzdata package in the runtime image) and cached, so it stays
// current with the OS without a hardcoded list. If tzdata is unavailable (e.g. a
// stripped-down environment) it falls back to a small curated list.
func commonTimezones() []string {
	tzOnce.Do(func() {
		if z := loadSystemTimezones(); len(z) > 0 {
			tzList = z
		} else {
			tzList = fallbackTimezones()
		}
	})
	return tzList
}

// loadSystemTimezones parses the canonical zone names (3rd column) from the tz
// database's zone table. Returns nil if it cannot be read.
func loadSystemTimezones() []string {
	for _, path := range []string{"/usr/share/zoneinfo/zone1970.tab", "/usr/share/zoneinfo/zone.tab"} {
		f, err := os.Open(path) // #nosec G304 -- fixed constant system paths
		if err != nil {
			continue
		}
		var zones []string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			cols := strings.Split(line, "\t")
			if len(cols) >= 3 {
				if tz := strings.TrimSpace(cols[2]); tz != "" {
					zones = append(zones, tz)
				}
			}
		}
		_ = f.Close()
		if len(zones) > 0 {
			sort.Strings(zones)
			return append([]string{"UTC"}, zones...)
		}
	}
	return nil
}

// fallbackTimezones is a small curated list used only when the system tz
// database cannot be read.
func fallbackTimezones() []string {
	return []string{
		"UTC",
		"Europe/London", "Europe/Dublin", "Europe/Lisbon", "Europe/Paris", "Europe/Madrid",
		"Europe/Amsterdam", "Europe/Brussels", "Europe/Berlin", "Europe/Zurich", "Europe/Rome",
		"Europe/Vienna", "Europe/Prague", "Europe/Warsaw", "Europe/Stockholm", "Europe/Oslo",
		"Europe/Copenhagen", "Europe/Helsinki", "Europe/Athens", "Europe/Bucharest", "Europe/Istanbul",
		"Europe/Kyiv", "Europe/Moscow",
		"Africa/Casablanca", "Africa/Lagos", "Africa/Cairo", "Africa/Johannesburg", "Africa/Nairobi",
		"Asia/Jerusalem", "Asia/Dubai", "Asia/Tehran", "Asia/Karachi", "Asia/Kolkata", "Asia/Dhaka",
		"Asia/Bangkok", "Asia/Jakarta", "Asia/Singapore", "Asia/Hong_Kong", "Asia/Shanghai",
		"Asia/Taipei", "Asia/Tokyo", "Asia/Seoul",
		"Australia/Perth", "Australia/Adelaide", "Australia/Sydney", "Australia/Brisbane",
		"Pacific/Auckland", "Pacific/Honolulu",
		"America/Anchorage", "America/Los_Angeles", "America/Denver", "America/Phoenix",
		"America/Chicago", "America/Mexico_City", "America/New_York", "America/Toronto",
		"America/Bogota", "America/Lima", "America/Santiago", "America/Sao_Paulo",
		"America/Argentina/Buenos_Aires", "America/Halifax", "America/St_Johns",
	}
}
