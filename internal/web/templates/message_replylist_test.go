package templates

import (
	"strings"
	"testing"
)

func TestReplyListButton(t *testing.T) {
	tests := []struct {
		name string
		list bool
		want bool
	}{
		{"with List-Post", true, true},
		{"without List-Post", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := viewFixture()
			msg.HasListPost = tt.list
			for name, out := range allViews(t, msg, nil) {
				if name == "messageTags" {
					continue
				}
				has := strings.Contains(out, `href="/compose/reply-list/INBOX/7"`)
				if has != tt.want {
					t.Errorf("%s: reply-list link present = %v, want %v", name, has, tt.want)
				}
				if tt.want && !strings.Contains(out, "Reply to list") {
					t.Errorf("%s: button label missing", name)
				}
				if !strings.Contains(out, `data-action="archive"`) {
					t.Errorf("%s: archive button missing", name)
				}
			}
		})
	}
}
