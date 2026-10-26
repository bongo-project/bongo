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
