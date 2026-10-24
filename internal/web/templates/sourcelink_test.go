package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// TestSourceLink checks the AGPL section 13 source link on every page chrome:
// both app layouts and the login layout.
func TestSourceLink(t *testing.T) {
	const custom = "https://git.example.org/me/bongo-fork"
	pages := map[string]templ.Component{
		"Layout":            Layout("Inbox", "u@example.com"),
		"LayoutWithContent": LayoutWithContent("Inbox", "u@example.com"),
		"LoginLayout":       LoginLayout("Login"),
	}
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"default", context.Background(), config.DefaultSourceURL},
		{"custom", middleware.WithSourceURL(context.Background(), custom), custom},
	}
	for page, c := range pages {
		for _, tt := range tests {
			t.Run(page+"/"+tt.name, func(t *testing.T) {
				var buf bytes.Buffer
				if err := c.Render(tt.ctx, &buf); err != nil {
					t.Fatal(err)
				}
				out := buf.String()
				if !strings.Contains(out, `href="`+tt.want+`"`) {
					t.Errorf("output lacks source link to %s", tt.want)
				}
				if !strings.Contains(out, ">Source code<") {
					t.Error("output lacks the translated link text")
				}
				if tt.want == custom && strings.Contains(out, config.DefaultSourceURL) {
					t.Error("custom URL page still links the default")
				}
			})
		}
	}
}
