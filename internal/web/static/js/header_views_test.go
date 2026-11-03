package js_test

import (
	"strings"
	"testing"
)

// In the To me, From my contacts and Mailing lists views a row names its real
// mailbox, and UIDs are only unique within one mailbox. Every per-row action
// must target that mailbox, and the bulk selection must be keyed by it, or two
// messages with the same UID in different folders are acted on together.
func TestHeaderViewActionsTargetTheRowsRealMailbox(t *testing.T) {
	js := readJS(t, "bongo.js")

	// Only rows of a header view carry data-sel-key, so other views are unchanged.
	if am := funcBody(t, js, "actionMailbox"); !strings.Contains(am, "row.dataset.selKey") || !strings.Contains(am, "row.dataset.realMailbox") {
		t.Error("actionMailbox must use the real mailbox only for rows that carry data-sel-key")
	}
	if k := funcBody(t, js, "selectionKey"); !strings.Contains(k, "row.dataset.selKey") {
		t.Error("selectionKey must prefer data-sel-key over the bare UID")
	}
	if a := funcBody(t, js, "addSelectionToParams"); !strings.Contains(a, "'targets'") || !strings.Contains(a, "'uids'") {
		t.Error("a selection key with a mailbox must go to 'targets', a bare UID to 'uids'")
	}
	if m := funcBody(t, js, "selectionMessageId"); !strings.Contains(m, "mailbox:") {
		t.Error("selectionMessageId must build {mailbox, uid} pairs for the tag endpoint")
	}

	for name, body := range map[string]string{
		"deleteRow":       funcBody(t, js, "deleteRow"),
		"context menu":    js[strings.Index(js, "openContextMenu(event"):],
		"keyboard reply":  js[strings.Index(js, "case 'r': // Reply"):],
		"drag and drop":   funcBody(t, js, "initDragDrop"),
		"swipe to delete": js[strings.Index(js, "var row2 = this.swipeTarget;"):],
	} {
		if !strings.Contains(body, "actionMailbox(") {
			t.Errorf("%s does not resolve the row's real mailbox", name)
		}
	}

	// The context menu sends the action to the real mailbox but reloads the
	// view that is on screen.
	action := js[strings.Index(js, "    action(act) {"):]
	action = action[:strings.Index(action, "  // Contact editor rows")]
	if !strings.Contains(action, "const mailbox = this.real || view;") {
		t.Error("context menu actions must target the real mailbox")
	}
	if strings.Contains(action, "htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox)") {
		t.Error("context menu must reload the view, not the real mailbox")
	}

	// The message list keys its selection by data-sel-key.
	list := js[strings.Index(js, "Alpine.data('messageList'"):]
	list = list[:strings.Index(list, "toggleBulkTag() {")]
	for _, want := range []string{"selectionKey(row)", "selectionKey(r)", "isSelected(uid, el)", "addSelectionToParams(params, key)"} {
		if !strings.Contains(list, want) {
			t.Errorf("messageList selection lacks %s", want)
		}
	}
}

// A header view posts its bulk actions to the URL it names in data-bulk-url,
// never to /mail/<view name>/bulk, which is a folder's route.
func TestBulkActionUsesTheViewsBulkURL(t *testing.T) {
	js := readJS(t, "bongo.js")
	if b := funcBody(t, js, "bulkURL"); !strings.Contains(b, "bulkUrl") {
		t.Error("bulkURL must read data-bulk-url")
	}
	if n := strings.Count(js, "await fetch(bulkURL(mailbox), {"); n != 2 {
		t.Errorf("both bulkAction copies must post to bulkURL(mailbox); found %d", n)
	}
	if strings.Contains(js, "await fetch('/mail/' + encodeURIComponent(mailbox) + '/bulk'") {
		t.Error("a bulkAction still builds the folder bulk URL by hand")
	}
}

// A list reload must go through listReloadURL, or an action in the unread
// To me view drops the filter.
func TestListReloadsKeepTheListURL(t *testing.T) {
	js := readJS(t, "bongo.js")
	if strings.Contains(js, "htmx.ajax('GET', '/mail/' + encodeURIComponent(") {
		t.Error("a list reload builds its URL by hand; use listReloadURL")
	}
	if b := funcBody(t, js, "listReloadURL"); !strings.Contains(b, "listUrl") {
		t.Error("listReloadURL must read data-list-url")
	}
}
