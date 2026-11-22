package templates

import (
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// The participant heading holds attacker-controlled text (an address from a
// URL); it must be escaped when rendered, whatever the handler lets through.
func TestParticipantHeadingIsEscaped(t *testing.T) {
	view := &store.HeaderView{Kind: store.ViewParticipant, Participants: []string{"<b>x</b>@x.test"}}
	out := renderToString(t, MailboxContent(view.URL(), view, ListCounts{}, nil, 1, 1, "", false, view.URL(), ""))
	if strings.Contains(out, "<b>x</b>") {
		t.Error("the participant heading rendered markup")
	}
	if !strings.Contains(out, "Conversations with &lt;b&gt;x&lt;/b&gt;@x.test") {
		t.Errorf("escaped heading missing: %s", out)
	}
}
