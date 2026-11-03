package i18n

import (
	"strconv"
	"strings"
	"time"
)

// FormatDate renders d as "<weekday> <day> <month>", plus " <year>" when
// withYear is set, with the weekday and month names of locale ("Fri 3 May",
// "Fr 3 Mai 2026"). It reads the fields of d as given and does no zone
// conversion: the caller converts to the user's zone first.
func FormatDate(locale string, d time.Time, withYear bool) string {
	day := strings.ToLower(d.Weekday().String()[:3])
	s := T(locale, "calendar."+day) + " " + strconv.Itoa(d.Day()) + " " + T(locale, "calendar.monthAbbr."+strconv.Itoa(int(d.Month())))
	if withYear {
		s += " " + strconv.Itoa(d.Year())
	}
	return s
}
