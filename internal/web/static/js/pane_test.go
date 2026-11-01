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

// Only sender and subject are links, so clicks on the rest of a row did
// nothing. The capture handler opens the row's message for those clicks too,
// but leaves the row's own controls alone.
func TestRowClickOpensMessage(t *testing.T) {
	js := readJS(t, "bongo.js")
	start := strings.Index(js, "const row = e.target.closest('.msg-list .msg-row[data-uid]');")
	if start < 0 {
		t.Fatal("click handler does not handle clicks on the rest of a row")
	}
	body := js[start : start+strings.Index(js[start:], "\n}, true);")]
	for _, want := range []string{"'a, button, input, label, [hx-get], [hx-post]'", "openInPane(row)", "rowLink.click()"} {
		if !strings.Contains(body, want) {
			t.Errorf("row click handling missing %s", want)
		}
	}
}

// #main-content's data-selected-mailbox is rendered once per full page load and
// goes stale when the sidebar swaps in another mailbox; d, r and drag-and-drop
// then acted on the right UID in the wrong mailbox. They must read the mailbox
// from the list that is on screen.
func TestKeyboardAndDragUseTheListsMailbox(t *testing.T) {
	js := readJS(t, "bongo.js")
	if strings.Contains(js, "querySelector('[data-selected-mailbox]')") {
		t.Error("bongo.js still reads the stale data-selected-mailbox")
	}
	if !strings.Contains(funcBody(t, js, "currentMailbox"), "'.mail-view[data-mailbox]'") {
		t.Error("currentMailbox must read the mailbox from the list on screen")
	}
	if !strings.Contains(funcBody(t, js, "deleteRow"), "currentMailbox()") {
		t.Error("deleteRow must use currentMailbox")
	}
	if !strings.Contains(js, "'/compose/reply/' + encodeURIComponent(mailbox)") {
		t.Error("r must escape the mailbox in the reply URL")
	}
}

// The mobile swipe, the e shortcut and the shortcut help all depend on the
// archive endpoints; a comment that still says "Trash" is the bug being fixed.
func TestArchiveSwipeAndShortcut(t *testing.T) {
	js := readJS(t, "bongo.js")
	// messageList is the component the mailbox template binds the touch
	// handlers to (x-data="messageList"); check the swipe there.
	swipe := js[strings.Index(js, "Alpine.data('messageList'"):]
	swipe = swipe[strings.Index(swipe, "end(event, uid, mailbox)"):]
	swipe = swipe[:strings.Index(swipe, "_resetSwipe() {")]
	for _, want := range []string{"'unarchive' : 'archive'", "resp.ok", "Swipe left: archive (move to Archive)"} {
		if !strings.Contains(swipe, want) {
			t.Errorf("swipe handler missing %q", want)
		}
	}
	for _, want := range []string{
		"row.dataset.realMailbox",
		"real === 'Archive' ? 'unarchive' : 'archive'",
		"'/mail/' + encodeURIComponent(real) + '/' + uid + '/' + verb",
		"if (!resp.ok) {\n            resetSwipeState(row);",
		"}).catch(function() {\n          resetSwipeState(row);",
	} {
		if !strings.Contains(swipe, want) {
			t.Errorf("swipe handler must pick archive or unarchive from the real mailbox and reset the row on failure; missing %q", want)
		}
	}
	if strings.Contains(swipe, "mailbox === 'Archive'") {
		t.Error("swipe verb must not come from the list mailbox")
	}
	row := js[strings.Index(js, "function archiveRow("):]
	row = row[:strings.Index(row, "// archiveOpenMessage")]
	for _, want := range []string{"row.dataset.realMailbox", "real === 'Archive' ? 'unarchive' : 'archive'", "encodeURIComponent(real)"} {
		if !strings.Contains(row, want) {
			t.Errorf("archiveRow missing %q", want)
		}
	}
	if strings.Contains(row, "mailbox === 'Archive'") {
		t.Error("archiveRow verb must not come from the list mailbox")
	}
	if strings.Contains(swipe, "archive (move to Trash)") {
		t.Error("swipe comment still claims archive moves to Trash")
	}
	if strings.Contains(js, "'destination', 'Trash'") {
		t.Error("a swipe still moves to Trash")
	}

	keydown := js[strings.Index(js, "if (e.key === 'e' && archiveOpenMessage())"):]
	if i, j := strings.Index(keydown, "archiveOpenMessage"), strings.Index(keydown, "if (messageRows.length === 0) return;"); i < 0 || j < 0 || i > j {
		t.Error("e must be handled before the empty-list early return so the open-message view gets it")
	}
	if !strings.Contains(js, "case 'e':") || !strings.Contains(js, "<kbd>e</kbd>") || !strings.Contains(js, "js.shortcutArchive") {
		t.Error("e shortcut or its help entry is missing")
	}
}

// The event form wraps the attendees input in the galAutocomplete component to
// make room for the suggestion list, so init must read the seeded value from
// the inner input; otherwise an edit would start with an empty field and drop
// every attendee.
func TestGalAutocompleteSeedsFromWrappedInput(t *testing.T) {
	js := readJS(t, "bongo.js")
	start := strings.Index(js, "Alpine.data('galAutocomplete'")
	if start < 0 {
		t.Fatal("bongo.js has no galAutocomplete component")
	}
	body := js[start : start+strings.Index(js[start:], "onInput(el)")]
	if !strings.Contains(body, "querySelector('input[name]')") {
		t.Error("galAutocomplete.init must fall back to the wrapped input value attribute")
	}
}

// Picking a suggestion in the attendees field replaces only the entry being
// typed, and the search asks about only that entry. The mode comes from
// data-gal-multi on the wrapper, so the compose To/Cc/Bcc fields, which carry
// no such attribute, keep searching and replacing the whole field.
func TestGalAutocompleteMultiValueField(t *testing.T) {
	js := readJS(t, "bongo.js")
	start := strings.Index(js, "Alpine.data('galAutocomplete'")
	if start < 0 {
		t.Fatal("bongo.js has no galAutocomplete component")
	}
	end := strings.Index(js[start:], "onKeydown(event)")
	if end < 0 {
		t.Fatal("galAutocomplete has no onKeydown")
	}
	body := js[start : start+end]
	for _, want := range []string{
		"this.$el.hasAttribute('data-gal-multi')",
		"if (!this.multi) return this.query;",
		"this.query.slice(this.lastSeparator() + 1).trim()",
		"encodeURIComponent(term)",
		"this.query.slice(0, cut)",
		"this.query = entry.email;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("galAutocomplete lost %q", want)
		}
	}
	if strings.Contains(body, "encodeURIComponent(this.query)") {
		t.Error("search must go through searchTerm, not the whole field")
	}

	tmpl, err := os.ReadFile("../../templates/calendar.templ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tmpl), `x-data="galAutocomplete" data-gal-multi`) {
		t.Error("the attendees wrapper must set data-gal-multi")
	}
	compose, err := os.ReadFile("../../templates/compose.templ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(compose), "data-gal-multi") {
		t.Error("compose fields must keep the single-value behaviour")
	}
}
