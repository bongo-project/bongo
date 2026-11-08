package templates

import (
	"os"
	"regexp"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

var classAttr = regexp.MustCompile(`\sclass="([^"]*)"`)

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
	cals := []*bongov1.Calendar{{Guid: "cal1", Name: "Personal"}}
	out := renderToString(t, CalendarSettingsPage(calendarSettingsFixture(), cals, "a@b.c"))
	start := strings.Index(out, `<aside class="sidebar"`)
	end := strings.Index(out, "</main>")
	if start < 0 || end < start {
		t.Fatal("no sidebar and <main> in the rendered page")
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
	// calendar-list-panel only anchors tests and Alpine; it needs no rule.
	delete(classes, "calendar-list-panel")
	for _, want := range []string{"mini-calendar-nav", "calendar-new-card", "share-item", "calendar-subscription", "publish-link", "form-error", "share-state", "form-check"} {
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

// The page keeps the v0.13 shape: a page head, a padded card each, shared input
// styles and a styled button on every submit.
func TestCalendarSettingsUsesSharedStyles(t *testing.T) {
	out := renderToString(t, CalendarSettingsPage(calendarSettingsFixture(), nil, "a@b.c"))
	main := out[strings.Index(out, "<main"):strings.Index(out, "</main>")]
	if !strings.Contains(main, `class="page-head"`) || !strings.Contains(main, "<h1") {
		t.Error("no page head with an h1")
	}
	cards := strings.Count(main, `<section class="card`)
	if pads := strings.Count(main, `class="settings-card-pad"`); cards < 3 || pads != cards {
		t.Errorf("%d cards, %d padded", cards, pads)
	}
	for _, tag := range regexp.MustCompile(`(?s)<(?:input|select)\b[^>]*>`).FindAllString(main, -1) {
		if strings.Contains(tag, `type="hidden"`) || strings.Contains(tag, `type="checkbox"`) {
			continue
		}
		if !strings.Contains(tag, "form-input") {
			t.Errorf("control without form-input: %s", tag)
		}
	}
	buttons := regexp.MustCompile(`(?s)<button\b[^>]*>`).FindAllString(main, -1)
	if len(buttons) == 0 {
		t.Fatal("no buttons rendered")
	}
	for _, b := range buttons {
		if !regexp.MustCompile(`class="[^"]*(btn-primary|btn-secondary|btn-danger|tool-btn)`).MatchString(b) {
			t.Errorf("button with no shared style: %s", b)
		}
	}
}

func TestCalendarPublishingDestructiveActionsConfirm(t *testing.T) {
	out := renderToString(t, CalendarPublishSection(PublishSettings{Calendar: "cal1", Published: true, HasPassword: true}))
	for _, class := range []string{"publish-regenerate-form", "publish-unpublish-form", "publish-password-remove-form"} {
		form := regexp.MustCompile(`<form[^>]*hx-confirm="[^"]+"[^>]*class="` + class + `"`).FindString(out)
		if form == "" {
			t.Errorf("%s has no confirmation", class)
		}
	}
}

// FR-1: the calendar pages outside settings keep no unstyled submit either.
func TestCalendarFormsHaveStyledSubmits(t *testing.T) {
	out := renderToString(t, CalendarSharesPage([]PendingShare{{Owner: "o", OwnerLabel: "o@b.c", Calendar: "c", CalendarName: "Work", Level: "read"}}, nil, nil, "a@b.c"))
	if !strings.Contains(out, `class="page-head"`) {
		t.Error("shares page has no page head")
	}
	if !regexp.MustCompile(`<button type="submit" class="btn-primary">`).MatchString(out) {
		t.Error("Accept is not a primary button")
	}
	src, err := os.ReadFile("calendar.templ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), `<button type="submit">`) {
		t.Error("calendar.templ has a submit button with no class")
	}
}
