package templates

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// Switching a feature off hides its settings entry points; switching nothing
// off shows them all.
func TestSettingsPageHidesDisabledFeatures(t *testing.T) {
	base, _ := csrfRenderCtx(t)
	fwd := ForwardView{Targets: []ForwardTargetInfo{{Address: "t@example.org", State: "pending"}}}
	tests := []struct {
		name     string
		disabled []string
		present  []string
		absent   []string
	}{
		{
			name:    "nothing off",
			present: []string{`href="/settings/rules"`, `href="/settings/vacation"`, `name="forward_address"`, `/settings/forwarding/`, `settingsTabs(5)`},
		},
		{
			name:     "rules off",
			disabled: []string{store.FeatureRules},
			present:  []string{`href="/settings/vacation"`, `name="forward_address"`},
			absent:   []string{`href="/settings/rules"`},
		},
		{
			name:     "vacation off",
			disabled: []string{store.FeatureVacation},
			present:  []string{`href="/settings/rules"`},
			absent:   []string{`href="/settings/vacation"`},
		},
		{
			name:     "forwarding off",
			disabled: []string{store.FeatureForwarding},
			present:  []string{`href="/settings/rules"`, `settingsTabs(4)`},
			absent:   []string{`name="forward_address"`, `name="forward_keep_copy"`, `/settings/forwarding/`, `settings-tab-forwarding`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := middleware.WithDisabledFeatures(base, tt.disabled...)
			var buf bytes.Buffer
			if err := SettingsPage(store.DefaultPreferences(), "u@example.com", "", fwd).Render(ctx, &buf); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			for _, w := range tt.present {
				if !strings.Contains(out, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			for _, w := range tt.absent {
				if strings.Contains(out, w) {
					t.Errorf("page still has %q", w)
				}
			}
		})
	}
}
