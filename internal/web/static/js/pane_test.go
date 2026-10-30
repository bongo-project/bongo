package js_test

import (
	"os"
	"strings"
	"testing"
)

// A message link carries hx-get for the full view, and htmx handles the click
// on the link itself. The reading-pane interceptor therefore has to run in the
// capture phase, or htmx swaps the whole view before it sees the click.
func TestReadingPaneClickIsCaptured(t *testing.T) {
	data, err := os.ReadFile("bongo.js")
	if err != nil {
		t.Fatalf("read bongo.js: %v", err)
	}
	js := string(data)
	start := strings.Index(js, "const link = e.target.closest('a[data-pane-url]');")
	if start < 0 {
		t.Fatal("bongo.js has no reading-pane click handler")
	}
	end := strings.Index(js[start:], "\n}")
	body := js[start : start+end]
	if !strings.HasPrefix(js[start+end:], "\n}, true);") {
		t.Error("reading-pane click handler must be registered in the capture phase")
	}
	if strings.Contains(body, "localStorage") {
		t.Error("reading-pane handler must decide from the visible pane, not localStorage")
	}
	if !strings.Contains(body, "pane.hidden") || !strings.Contains(body, "offsetParent") {
		t.Error("reading-pane handler must fall back to full view when the pane is hidden")
	}
}
