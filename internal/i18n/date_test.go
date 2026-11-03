package i18n

import (
	"testing"
	"time"
)

func TestFormatDate(t *testing.T) {
	Init("en")
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("zone unavailable: %v", err)
	}
	cases := []struct {
		name     string
		locale   string
		d        time.Time
		withYear bool
		want     string
	}{
		{"en single digit", "en", time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC), false, "Mon 4 May"},
		{"de single digit year", "de", time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC), true, "Mo 4 Mai 2026"},
		{"de march", "de", time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC), false, "Mo 9 Mär"},
		{"de year boundary", "de", time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC), true, "Mi 31 Dez 2025"},
		{"de new year", "de", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true, "Do 1 Jan 2026"},
		// 03:00 UTC on 5 May is still 4 May in Denver: d's own zone is used.
		{"non-UTC zone", "de", time.Date(2026, 5, 5, 3, 0, 0, 0, time.UTC).In(denver), true, "Mo 4 Mai 2026"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDate(tc.locale, tc.d, tc.withYear); got != tc.want {
				t.Errorf("FormatDate = %q, want %q", got, tc.want)
			}
		})
	}
}

// fr and es are translated, not left as keys or English.
func TestFormatDateOtherLocales(t *testing.T) {
	Init("en")
	d := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	en := FormatDate("en", d, true)
	for _, loc := range []string{"fr", "es"} {
		got := FormatDate(loc, d, true)
		if got == en || got == "" {
			t.Errorf("%s = %q, want translated (en is %q)", loc, got, en)
		}
	}
}
