package templates

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

var (
	agendaHeadRe   = regexp.MustCompile(`class="agenda-day-head">([^<]*)<`)
	toolbarTitleRe = regexp.MustCompile(`class="calendar-month-title">([^<]*)<`)
)

func renderAgendaLocale(t *testing.T, locale string, data AgendaData) string {
	t.Helper()
	initI18nForTest()
	var buf bytes.Buffer
	ctx := middleware.WithLocale(context.Background(), locale)
	if err := AgendaContent(data).Render(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// The agenda day headers and the toolbar title use the request locale's
// weekday and month names, not Go's English ones.
func TestAgendaUsesLocaleDates(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		name, locale string
		from, to     time.Time
		days         []time.Time
		wantHeads    []string
		wantTitle    string
	}{
		{"de january", "de", d(2026, 1, 5), d(2026, 1, 11), []time.Time{d(2026, 1, 5), d(2026, 1, 9)},
			[]string{"Mo 5 Jan", "Fr 9 Jan"}, "Mo 5 Jan 2026 - So 11 Jan 2026"},
		{"de march", "de", d(2026, 3, 9), d(2026, 3, 15), []time.Time{d(2026, 3, 9)},
			[]string{"Mo 9 Mär"}, "Mo 9 Mär 2026 - So 15 Mär 2026"},
		{"de may", "de", d(2026, 5, 4), d(2026, 5, 10), []time.Time{d(2026, 5, 4)},
			[]string{"Mo 4 Mai"}, "Mo 4 Mai 2026 - So 10 Mai 2026"},
		{"en", "en", d(2026, 1, 5), d(2026, 1, 11), []time.Time{d(2026, 1, 5)},
			[]string{"Mon 5 Jan"}, "Mon 5 Jan 2026 - Sun 11 Jan 2026"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := AgendaData{From: tc.from, To: tc.to}
			for _, day := range tc.days {
				data.Days = append(data.Days, AgendaDay{Date: day})
			}
			out := renderAgendaLocale(t, tc.locale, data)
			var heads []string
			for _, m := range agendaHeadRe.FindAllStringSubmatch(out, -1) {
				heads = append(heads, m[1])
			}
			if strings.Join(heads, "|") != strings.Join(tc.wantHeads, "|") {
				t.Errorf("day heads = %q, want %q", heads, tc.wantHeads)
			}
			m := toolbarTitleRe.FindStringSubmatch(out)
			if m == nil {
				t.Fatalf("no toolbar title in:\n%s", out)
			}
			if m[1] != tc.wantTitle {
				t.Errorf("title = %q, want %q", m[1], tc.wantTitle)
			}
			text := m[1] + " " + strings.Join(heads, " ")
			for _, bad := range []string{"Monday", "January", "Sunday", "March"} {
				if strings.Contains(text, bad) {
					t.Errorf("English name %q rendered", bad)
				}
			}
		})
	}
}
