package templates

import (
	"strings"
	"testing"
)

// TestCreateFilterEntryPoints: the message More menu and the row context menu
// both offer "Create filter", pointing the new-rule page at the message by
// mailbox and UID (the handler reads the sender itself).
func TestCreateFilterEntryPoints(t *testing.T) {
	msg := viewFixture()
	msg.Mailbox = "INBOX/Receipts & Co"
	out := renderToString(t, MessagePage(msg, nil))
	if want := `href="/settings/rules/new?mailbox=INBOX%2FReceipts+%26+Co&amp;uid=7"`; !strings.Contains(out, want) {
		i := strings.Index(out, "/settings/rules/new")
		t.Errorf("More menu link not found (want %s); got %q", want, out[max(i, 0):min(max(i, 0)+90, len(out))])
	}
	if !strings.Contains(out, ">Create filter<") {
		t.Error("More menu has no translated Create filter item")
	}
	ctxOut := renderToString(t, ContextMenu())
	if !strings.Contains(ctxOut, `x-on:click="action(&#39;create-filter&#39;)"`) && !strings.Contains(ctxOut, `x-on:click="action('create-filter')"`) {
		t.Error("context menu has no create-filter action")
	}
}
