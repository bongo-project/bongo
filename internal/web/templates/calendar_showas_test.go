package templates

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// showAsForm renders the event form and returns the selected value of the
// show_as select ("" when none is marked), the option values, and the x-data
// of the form.
func showAsForm(t *testing.T, event *bongov1.Event, isNew bool) (selected string, options []string, xdata string) {
	t.Helper()
	out := renderToString(t, EventFormPage(event, nil, isNew, "2030-05-06", "me@example.com"))
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	found := false
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" && strings.Contains(attr(n, "class"), "event-form") {
			xdata = attr(n, "x-data")
		}
		if n.Type == html.ElementNode && n.Data == "select" && attr(n, "name") == "show_as" {
			found = true
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type != html.ElementNode || c.Data != "option" {
					continue
				}
				options = append(options, attr(c, "value"))
				for _, a := range c.Attr {
					if a.Key == "selected" {
						selected = attr(c, "value")
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if !found {
		t.Fatalf("no show_as select in the event form:\n%s", out)
	}
	return selected, options, xdata
}

func TestEventFormShowAsDefaults(t *testing.T) {
	for _, tc := range []struct {
		name      string
		event     *bongov1.Event
		isNew     bool
		want      string
		wantXData string
	}{
		{"new, no event", nil, true, "busy", "eventForm(false, 'all', 'busy', false)"},
		{"new, all-day prefill", &bongov1.Event{AllDay: true}, true, "free", "eventForm(true, 'all', 'free', false)"},
		{"new, timed prefill", &bongov1.Event{}, true, "busy", "eventForm(false, 'all', 'busy', false)"},
		{"existing transparent", &bongov1.Event{Guid: "g", Title: "x", Transparent: true}, false, "free", "eventForm(false, 'all', 'free', true)"},
		{"existing opaque all-day", &bongov1.Event{Guid: "g", Title: "x", AllDay: true}, false, "busy", "eventForm(true, 'all', 'busy', true)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, options, xdata := showAsForm(t, tc.event, tc.isNew)
			if selected != tc.want {
				t.Errorf("selected = %q, want %q", selected, tc.want)
			}
			if len(options) != 2 || options[0] != "busy" || options[1] != "free" {
				t.Errorf("options = %v, want [busy free]", options)
			}
			if xdata != tc.wantXData {
				t.Errorf("x-data = %q, want %q", xdata, tc.wantXData)
			}
		})
	}
}

func TestEventFormShowAsIsLabelled(t *testing.T) {
	out := renderToString(t, EventFormPage(nil, nil, true, "2030-05-06", "me@example.com"))
	for _, want := range []string{"Show as", "Busy", "Free"} {
		if !strings.Contains(out, want) {
			t.Errorf("form lacks %q", want)
		}
	}
}
