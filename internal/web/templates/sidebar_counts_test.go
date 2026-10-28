package templates

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestSidebarCountsAreLiveTargets: every folder renders its count element,
// hidden at zero, so refreshUnreadCounts can show it when the folder gains
// unread mail without a reload; the accessible label is translated.
func TestSidebarCountsAreLiveTargets(t *testing.T) {
	boxes := []MailboxInfo{{Name: "INBOX", Label: "INBOX", Unread: 3}, {Name: "Archive", Label: "Archive"}}
	out := renderToString(t, Sidebar(boxes, nil, "INBOX"))
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]string{}
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode {
			if mb, ok := attrMap(n)["data-unread-for"]; ok {
				got[mb] = attrMap(n)
			}
		}
	})
	inbox, archive := got["INBOX"], got["Archive"]
	if inbox == nil || archive == nil {
		t.Fatalf("count elements missing: %v", got)
	}
	if _, hidden := inbox["hidden"]; hidden {
		t.Error("INBOX count with 3 unread is hidden")
	}
	if _, hidden := archive["hidden"]; !hidden {
		t.Error("Archive count with 0 unread is not hidden")
	}
	if inbox["class"] != "unread" || archive["class"] != "count" {
		t.Errorf("classes = %q, %q; want unread (INBOX pill) and count", inbox["class"], archive["class"])
	}
	if inbox["aria-label"] != "3 unread messages" || inbox["data-unread-label"] != "%d unread messages" {
		t.Errorf("label = %q, format = %q", inbox["aria-label"], inbox["data-unread-label"])
	}
}
