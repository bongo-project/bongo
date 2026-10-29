package templates

import (
	stdhtml "html"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// isClickOutside reports whether an attribute is an Alpine click-outside
// handler (.outside, or its older alias .away).
func isClickOutside(key string) bool {
	return (strings.HasPrefix(key, "x-on:click.") || strings.HasPrefix(key, "@click.")) &&
		(strings.Contains(key, ".outside") || strings.Contains(key, ".away"))
}

func hasClickOutside(n *html.Node) bool {
	for _, a := range n.Attr {
		if isClickOutside(a.Key) {
			return true
		}
	}
	return false
}

// TestClickOutsideNotOnTogglePanel pins where dropdowns hang their dismiss
// handler. On a hidden-bound panel whose toggle button sits outside it, a real
// mouse click opens the panel, Alpine shows it in a microtask, and the
// document-level outside listener then sees a click outside a visible panel and
// closes it again: the button does nothing. The handler belongs on the wrapper
// that holds both the button and the panel.
//
// The context menu is exempt: it opens on contextmenu, which no click follows.
func TestClickOutsideNotOnTogglePanel(t *testing.T) {
	msg := viewFixture()
	views := map[string]string{
		"MessagePage":    renderToString(t, MessagePage(msg, nil)),
		"MailboxContent": renderToString(t, MailboxContent("INBOX", ListCounts{}, []MessageSummary{{UID: 1, Subject: "s", From: "a@b"}}, 1, 1, "", false)),
	}
	for name, out := range views {
		doc, err := html.Parse(strings.NewReader(out))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		walk(doc, func(n *html.Node) {
			if n.Type != html.ElementNode || !hasClickOutside(n) {
				return
			}
			m := attrMap(n)
			_, hidden := m["x-bind:hidden"]
			_, short := m[":hidden"]
			_, show := m["x-show"]
			if (hidden || short || show) && m["x-data"] != "contextMenu" {
				t.Errorf("%s: <%s class=%q> hides itself and dismisses on outside click; put the handler on the wrapper holding its toggle", name, n.Data, m["class"])
			}
		})
	}
}

// TestTagDropdownsDismissFromWrapper checks the two tag dropdowns still close
// on an outside click, from the wrapper that contains their toggle button.
func TestTagDropdownsDismissFromWrapper(t *testing.T) {
	cases := []struct {
		name, out, wrapper, toggle string
	}{
		{"messageTags", renderToString(t, messageTags(viewFixture())), "tag-add-wrapper", "tag-add-btn"},
		{"MailboxContent", renderToString(t, MailboxContent("INBOX", ListCounts{}, []MessageSummary{{UID: 1, Subject: "s", From: "a@b"}}, 1, 1, "", false)), "bulk-tag-dropdown", "tool-btn"},
	}
	for _, c := range cases {
		doc, err := html.Parse(strings.NewReader(c.out))
		if err != nil {
			t.Fatalf("%s: parse: %v", c.name, err)
		}
		var wrapper *html.Node
		walk(doc, func(n *html.Node) {
			if n.Type == html.ElementNode && hasClass(n, c.wrapper) {
				wrapper = n
			}
		})
		if wrapper == nil {
			t.Fatalf("%s: no .%s", c.name, c.wrapper)
		}
		if !hasClickOutside(wrapper) {
			t.Errorf("%s: .%s has no outside-click handler", c.name, c.wrapper)
		}
		toggle := false
		walk(wrapper, func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "button" && hasClass(n, c.toggle) && attrMap(n)["aria-haspopup"] != "" {
				toggle = true
			}
		})
		if !toggle {
			t.Errorf("%s: toggle button .%s is not inside .%s", c.name, c.toggle, c.wrapper)
		}
	}
}

// TestMailViewHandlersHaveNoStatementLists: the Alpine CSP build parses one
// expression per attribute, so "a(); b = c" throws "CSP Parser Error:
// Unexpected token" when the event fires, which is how the bulk Tag... Add
// button failed silently. Quoted semicolons (inside a string literal) are fine.
func TestMailViewHandlersHaveNoStatementLists(t *testing.T) {
	msgs := []MessageSummary{{UID: 1, Subject: "s", From: "a@b"}}
	views := map[string]string{
		"MailboxContent": renderToString(t, MailboxContent("INBOX", ListCounts{}, msgs, 1, 1, "", false)),
		"ContextMenu":    renderToString(t, ContextMenu()),
		"MessagePage":    renderToString(t, MessagePage(viewFixture(), nil)),
	}
	for name, out := range views {
		matches := alpineAttr.FindAllStringSubmatch(out, -1)
		if len(matches) == 0 {
			t.Fatalf("%s: no Alpine attributes found; the scan is not looking at anything", name)
		}
		for _, m := range matches {
			if m[1] == "x-data" {
				continue // component constructors with JSON arguments
			}
			if hasUnquotedSemicolon(stdhtml.UnescapeString(m[2])) {
				t.Errorf("%s: %s=%q is a statement list, which the Alpine CSP build cannot parse", name, m[1], m[2])
			}
		}
	}
}

// hasUnquotedSemicolon reports a ';' outside single- or double-quoted strings.
func hasUnquotedSemicolon(expr string) bool {
	var quote rune
	for _, r := range expr {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ';':
			return true
		}
	}
	return false
}

// TestMessageTagPickerShowsRejection: when the server rejects a tag the picker
// must say so. It used to close silently, which looked like the click did
// nothing. The error text comes from the root element and the create preview
// shows the folded name the server will store.
func TestMessageTagPickerShowsRejection(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(renderToString(t, messageTags(viewFixture()))))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var root, errEl *html.Node
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		a := attrMap(n)
		if strings.HasPrefix(a["x-data"], "messageTags(") {
			root = n
		}
		if hasClass(n, "tag-dropdown-error") {
			errEl = n
		}
	})
	if root == nil {
		t.Fatal("no messageTags root")
	}
	if attrMap(root)["data-tag-error-label"] == "" {
		t.Error("messageTags root has no data-tag-error-label")
	}
	if errEl == nil {
		t.Fatal("no .tag-dropdown-error element")
	}
	if a := attrMap(errEl); a["x-text"] != "tagError" || a["role"] != "alert" {
		t.Errorf(".tag-dropdown-error attrs = %v, want x-text=tagError role=alert", a)
	}
	out := renderToString(t, messageTags(viewFixture()))
	if !strings.Contains(out, `x-text="foldedNewTagName"`) {
		t.Error("create preview does not show the folded tag name")
	}
}
