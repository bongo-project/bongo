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
	js := readJS(t, "bongo.js")
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
	if !strings.Contains(body, "readingPaneVisible()") {
		t.Error("reading-pane handler must fall back to full view when the pane is hidden")
	}
	if vis := funcBody(t, js, "readingPaneVisible"); !strings.Contains(vis, "pane.hidden") || !strings.Contains(vis, "offsetParent") {
		t.Error("readingPaneVisible must check the hidden attribute and CSS display")
	}
}

func readJS(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// funcBody returns the source of a top-level function in bongo.js.
func funcBody(t *testing.T, js, name string) string {
	t.Helper()
	start := strings.Index(js, "\nfunction "+name+"(")
	if start < 0 {
		t.Fatalf("bongo.js has no function %s", name)
	}
	end := strings.Index(js[start+1:], "\n}")
	return js[start : start+1+end]
}

// In the pane modes j and k open the highlighted message after a pause, so
// holding the key skims the list without marking every message read.
func TestKeyboardNavigationOpensInPaneAfterPause(t *testing.T) {
	js := readJS(t, "bongo.js")
	move := funcBody(t, js, "moveKeyboardFocus")
	for _, want := range []string{"readingPaneVisible()", "setTimeout(", "PANE_OPEN_DELAY_MS", "clearTimeout(paneOpenTimer)", "openInPane(row)"} {
		if !strings.Contains(move, want) {
			t.Errorf("moveKeyboardFocus missing %s", want)
		}
	}
	if !strings.Contains(js, "case 'j': // Next message\n      e.preventDefault();\n      if (idx < messageRows.length - 1) moveKeyboardFocus(") {
		t.Error("j must move through moveKeyboardFocus")
	}
	if !strings.Contains(js, "if (idx > 0) moveKeyboardFocus(") {
		t.Error("k must move through moveKeyboardFocus")
	}
	if !strings.Contains(funcBody(t, js, "openInPane"), "setKeyboardFocus(row)") {
		t.Error("opening a message in the pane must move the keyboard highlight to it")
	}
}

// d once clicked a [data-action="delete"] button that rows do not have, so it
// did nothing. It must move the message to Trash, and never delete from Trash,
// where deletion is permanent.
func TestDeleteKeyMovesToTrash(t *testing.T) {
	js := readJS(t, "bongo.js")
	if strings.Contains(js, `[data-action="delete"]`) {
		t.Error("d still looks for a delete button the rows do not have")
	}
	if !strings.Contains(js, "deleteRow(current, idx);") {
		t.Error("d must call deleteRow")
	}
	del := funcBody(t, js, "deleteRow")
	for _, want := range []string{"mailbox === 'Trash'", "params.set('action', 'delete')", "'/bulk'"} {
		if !strings.Contains(del, want) {
			t.Errorf("deleteRow missing %s", want)
		}
	}
}

// theme.js must read the same key appState writes.
func TestThemeScriptReadsSavedTheme(t *testing.T) {
	theme := readJS(t, "theme.js")
	app := readJS(t, "bongo.js")
	if !strings.Contains(app, "localStorage.setItem('bongo-theme'") || !strings.Contains(theme, "localStorage.getItem('bongo-theme')") {
		t.Error("theme.js and appState must use the same bongo-theme key")
	}
	if !strings.Contains(theme, "prefers-color-scheme: dark") || !strings.Contains(theme, "setAttribute('data-theme'") {
		t.Error("theme.js must resolve auto from the OS preference and set data-theme")
	}
}
