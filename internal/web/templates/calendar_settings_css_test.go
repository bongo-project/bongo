package templates

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var classAttr = regexp.MustCompile(`class="([^"]*)"`)

// calendarSettingsFixture reaches every branch of the settings page: the new
// form, an owned card with grants (pending and addressable), a published
// calendar with a password, and a subscription with both a success and an error.
func calendarSettingsFixture() []CalendarSettings {
	return []CalendarSettings{
		{
			Calendar: "cal1", Name: "Work", Colour: "#112233", CanDelete: true, CanShare: true,
			Grants: []GrantRow{
				{Grantee: "u1", Label: "u1", Level: "read", State: "pending"},
				{Grantee: "u2", Label: "bob@example.com", Level: "write", State: "accepted"},
			},
			Publish: PublishSettings{
				Calendar: "cal1", Published: true, HasPassword: true,
				HTTPSLink: "https://x/p/t", WebcalLink: "webcal://x/p/t",
			},
		},
		{
			Calendar: "cal2", Name: "Feed", Colour: "#445566",
			Subscription: &SubscriptionInfo{Host: "example.org", LastSuccess: "today", LastError: "boom", EventCount: 3},
			Publish:      PublishSettings{Calendar: "cal2"},
		},
	}
}

func TestCalendarSettingsClassesHaveCSSRules(t *testing.T) {
	out := renderToString(t, CalendarSettingsPage(calendarSettingsFixture(), nil, "a@b.c"))
	start := strings.Index(out, "<main")
	end := strings.Index(out, "</main>")
	if start < 0 || end < start {
		t.Fatal("no <main> in the rendered page")
	}
	main := out[start:end]

	classes := map[string]bool{}
	for _, m := range classAttr.FindAllStringSubmatch(main, -1) {
		for _, c := range strings.Fields(m[1]) {
			classes[c] = true
		}
	}
	if len(classes) < 20 {
		t.Fatalf("only %d classes collected; the scan is not looking at the page", len(classes))
	}
	for _, want := range []string{"calendar-new-card", "share-item", "calendar-subscription", "publish-link", "form-error", "share-state", "form-check"} {
		if !classes[want] {
			t.Errorf("fixture did not reach class %q", want)
		}
	}

	var css strings.Builder
	for _, p := range []string{"../static/css/bongo.css", "../static/css/componentlib/colors_and_type.css", "../static/css/generated_geometry.css"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		css.Write(b)
	}
	for cls := range classes {
		re := regexp.MustCompile(`\.` + regexp.QuoteMeta(cls) + `(?:[^\w-]|$)`)
		if !re.MatchString(css.String()) {
			t.Errorf("class %q is used on /calendar/settings but has no CSS rule", cls)
		}
	}
}
