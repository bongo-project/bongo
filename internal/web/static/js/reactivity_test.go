package js_test

import (
	"strings"
	"testing"
)

func TestMailStreamFollowsVisibleMailboxAndPatchesRows(t *testing.T) {
	js := readJS(t, "bongo.js")
	start := strings.Index(js, "Alpine.data('notifications'")
	if start < 0 {
		t.Fatal("notifications component is missing")
	}
	component := js[start:]
	for _, want := range []string{
		"currentMailbox() || 'INBOX'",
		"'/events?mailbox=' + encodeURIComponent(this.streamMailbox)",
		"'htmx:afterSettle'",
		"addEventListener('mailboxchange'",
		"'/api/mail/' + encodeURIComponent(data.mailbox)",
		"document.getElementById('msg-' + data.uid)",
		"oldRow.replaceWith(newRow)",
		"list.prepend(newRow)",
		"list.scrollTop += list.scrollHeight - before",
		"this.resyncOnOpen = true",
		"this.resyncVisibleMailbox()",
		"addEventListener('resync'",
		"listReloadURL(mailbox)",
		"row.classList.add('keyboard-focus')",
	} {
		if !strings.Contains(component, want) {
			t.Errorf("live mailbox component missing %q", want)
		}
	}
	if strings.Contains(component, "EventSource('/events?mailbox=INBOX')") {
		t.Error("event stream is still pinned to INBOX")
	}
}
