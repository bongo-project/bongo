package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLayoutsCarryVariantAttributes(t *testing.T) {
	ctx := context.Background()
	for name, render := range map[string]func(*bytes.Buffer) error{
		"Layout":            func(b *bytes.Buffer) error { return Layout("t", "a@b.c").Render(ctx, b) },
		"LayoutWithContent": func(b *bytes.Buffer) error { return LayoutWithContent("t", "a@b.c").Render(ctx, b) },
		"LoginLayout":       func(b *bytes.Buffer) error { return LoginLayout("t").Render(ctx, b) },
	} {
		var buf bytes.Buffer
		if err := render(&buf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		html := buf.String()
		start := strings.Index(html, "<html")
		html = html[start : start+strings.Index(html[start:], ">")]
		for _, a := range []string{`data-accent="bongo"`, `data-sidebar="dark"`} {
			if !strings.Contains(html, a) {
				t.Errorf("%s: <html> missing %s", name, a)
			}
		}
	}
}

// Alpine sets data-theme only after the page has painted, so the app layouts
// load theme.js, without defer, ahead of the stylesheets. Without it a
// dark-mode user saw the light theme flash on every full page load.
func TestAppLayoutsSetThemeBeforePaint(t *testing.T) {
	ctx := context.Background()
	for name, render := range map[string]func(*bytes.Buffer) error{
		"Layout":            func(b *bytes.Buffer) error { return Layout("t", "a@b.c").Render(ctx, b) },
		"LayoutWithContent": func(b *bytes.Buffer) error { return LayoutWithContent("t", "a@b.c").Render(ctx, b) },
	} {
		var buf bytes.Buffer
		if err := render(&buf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := buf.String()
		script := strings.Index(out, `<script src="/static/js/theme.js`)
		css := strings.Index(out, `/static/css/bongo.css`)
		if script < 0 || css < 0 || script > css {
			t.Errorf("%s: theme.js must load before the stylesheets", name)
		}
		if strings.Contains(out[script:script+strings.Index(out[script:], ">")], "defer") {
			t.Errorf("%s: theme.js must not be deferred", name)
		}
		start := strings.Index(out, "<html")
		if tag := out[start : start+strings.Index(out[start:], ">")]; !strings.Contains(tag, `data-theme="auto"`) {
			t.Errorf("%s: <html> needs data-theme=\"auto\" as the fallback before any script runs: %s", name, tag)
		}
	}
}
