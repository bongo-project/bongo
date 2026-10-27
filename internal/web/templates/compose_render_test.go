package templates

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// filesSummaryLabel is the translated drop-zone summary; inside a test the
// parameter t hides the package's t().
func filesSummaryLabel() string {
	return t(context.Background(), "compose.filesSummary")
}

// hasAttrValue reports whether any element carries attribute name with value.
func hasAttrValue(doc *html.Node, name, value string) bool {
	found := false
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && attrMap(n)[name] == value {
			found = true
		}
	})
	return found
}

func renderComposeDoc(t *testing.T, mode, to, cc string, from []string) (*html.Node, string) {
	t.Helper()
	ctx, _ := csrfRenderCtx(t)
	var buf bytes.Buffer
	if err := ComposePage(mode, to, cc, "Subj", "", "INBOX", 7, from, "", "").Render(ctx, &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	doc, err := html.Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc, buf.String()
}

func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

func attrMap(n *html.Node) map[string]string {
	m := map[string]string{}
	for _, a := range n.Attr {
		m[a.Key] = a.Val
	}
	return m
}

func hasClass(n *html.Node, class string) bool {
	return slicesContains(strings.Fields(attrMap(n)["class"]), class)
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// findAll returns elements matching tag and (optionally) a name attribute.
func findByName(doc *html.Node, name string) []*html.Node {
	var out []*html.Node
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && attrMap(n)["name"] == name {
			out = append(out, n)
		}
	})
	return out
}

func TestComposeRendersPreservedHooks(t *testing.T) {
	const quotedCc = `"Doe, Jane" <j@x.example>, bob@y.example`
	modes := []string{"new", "reply", "forward", "draft"}
	froms := [][]string{nil, {"a@x.example", "b@x.example"}}
	for _, mode := range modes {
		for _, from := range froms {
			name := mode
			if len(from) > 1 {
				name += "/multi-from"
			}
			t.Run(name, func(t *testing.T) {
				doc, raw := renderComposeDoc(t, mode, `"Doe, Jane" <j@x.example>`, quotedCc, from)

				// The first form control inside the form is the hidden _csrf.
				var first *html.Node
				var inForm bool
				walk(doc, func(n *html.Node) {
					if n.Type != html.ElementNode {
						return
					}
					if n.Data == "form" {
						inForm = true
					}
					if inForm && first == nil && (n.Data == "input" || n.Data == "button" || n.Data == "select" || n.Data == "textarea") {
						first = n
					}
				})
				if first == nil || attrMap(first)["name"] != "_csrf" || attrMap(first)["type"] != "hidden" {
					t.Fatalf("first form control is not hidden _csrf: %+v", first)
				}

				sid := findByName(doc, "send_id")
				if len(sid) != 1 || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(attrMap(sid[0])["value"]) {
					t.Errorf("send_id missing or malformed")
				}

				for _, f := range []struct {
					name, value string
					required    bool
				}{{"to", `"Doe, Jane" <j@x.example>`, true}, {"cc", quotedCc, false}, {"bcc", "", false}} {
					els := findByName(doc, f.name)
					if len(els) != 1 {
						t.Fatalf("%s: %d inputs", f.name, len(els))
					}
					a := attrMap(els[0])
					if a["id"] != f.name || a["type"] != "text" {
						t.Errorf("%s: id/type changed: %v", f.name, a)
					}
					if f.name == "bcc" {
						if _, ok := a["value"]; ok {
							t.Errorf("bcc gained a value attribute")
						}
					} else if a["value"] != f.value {
						t.Errorf("%s value = %q, want %q", f.name, a["value"], f.value)
					}
					if _, ok := a["required"]; ok != f.required {
						t.Errorf("%s required = %v, want %v", f.name, ok, f.required)
					}
					if a["x-data"] != "galAutocomplete" || a[":value"] != "query" ||
						a["@input.debounce.300ms"] != "onInput($el)" ||
						a["@keydown"] != "onKeydown($event)" {
						t.Errorf("%s lost its galAutocomplete bindings: %v", f.name, a)
					}
				}

				if got := len(findByName(doc, "from")); (got == 1) != (len(from) > 1) {
					t.Errorf("from select count = %d with %d options", got, len(from))
				}
				att := findByName(doc, "attachments")
				if len(att) != 1 {
					t.Fatalf("attachments inputs = %d", len(att))
				}
				if _, ok := attrMap(att[0])["multiple"]; !ok || attrMap(att[0])["x-on:change"] != "updateFiles($el)" {
					t.Errorf("attachments input lost multiple/updateFiles: %v", attrMap(att[0]))
				}
				if len(findByName(doc, "body_mode")) != 1 {
					t.Errorf("body_mode missing")
				}
				reply := mode == "reply" || mode == "forward"
				for _, h := range []string{"reply_mailbox", "reply_uid", "mode"} {
					if got := len(findByName(doc, h)); (got == 1) != reply {
						t.Errorf("%s count = %d (reply-like=%v)", h, got, reply)
					}
				}
				if !strings.Contains(raw, `formaction="/compose/draft"`) {
					t.Errorf("save-draft formaction missing")
				}
				if strings.Contains(raw, " style=") {
					t.Errorf("inline style attribute rendered")
				}
				// Every To/Cc/Bcc input sits inside a pills wrapper.
				if strings.Count(raw, `x-data="recipientPills"`) != 3 {
					t.Errorf("want 3 recipientPills wrappers, got %d", strings.Count(raw, `x-data="recipientPills"`))
				}
				if strings.Count(raw, `x-bind:class="{'has-pills': showPills}"`) != 3 {
					t.Errorf("recipientPills wrapper class binding malformed")
				}
				if !hasAttrValue(doc, "data-files-summary", filesSummaryLabel()) {
					t.Errorf("drop-zone lost data-files-summary bound to compose.filesSummary")
				}
				if len(att) == 1 && !slicesContains(strings.Fields(attrMap(att[0])["class"]), "sr-only") {
					t.Errorf("attachments input lost sr-only: %v", attrMap(att[0]))
				}
				pillGroups := 0
				walk(doc, func(n *html.Node) {
					if n.Type == html.ElementNode && hasClass(n, "recipient-pills") {
						pillGroups++
						a := attrMap(n)
						if a["role"] != "button" || a["aria-label"] == "" || a["tabindex"] != "0" {
							t.Errorf("recipient-pills lost role/aria-label/tabindex: %v", a)
						}
					}
				})
				if pillGroups != 3 {
					t.Errorf("recipient-pills groups = %d, want 3", pillGroups)
				}
				if !strings.Contains(raw, `for="attachments"`) {
					t.Errorf("attach label for=attachments missing")
				}
			})
		}
	}
}
