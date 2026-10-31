package templates

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// Every settings sidebar links to the vacation page.
func TestSettingsSidebarsLinkToVacation(t *testing.T) {
	ctx, _ := csrfRenderCtx(t)
	pages := map[string]func(*bytes.Buffer) error{
		"settings": func(b *bytes.Buffer) error {
			return SettingsPage(store.DefaultPreferences(), "u@example.com", "", ForwardView{}).Render(ctx, b)
		},
		"security": func(b *bytes.Buffer) error { return SecuritySettings(false, "u@example.com", "").Render(ctx, b) },
		"vacation": func(b *bytes.Buffer) error {
			return VacationPage(VacationView{Interval: 7}, "u@example.com").Render(ctx, b)
		},
	}
	for name, render := range pages {
		var buf bytes.Buffer
		if err := render(&buf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(buf.String(), `href="/settings/vacation"`) {
			t.Errorf("%s page has no link to /settings/vacation", name)
		}
	}
}
