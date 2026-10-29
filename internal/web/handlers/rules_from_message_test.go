package handlers

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/rules"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// deliverFrom stores a message with the given From header and returns its UID.
func deliverFrom(t *testing.T, env *mailTagsTestEnv, userID, mailbox, guid, from string) uint32 {
	t.Helper()
	_ = env.mailboxes.CreateMailbox(userID, mailbox)
	meta := &bongov1.MailMeta{Guid: guid, From: from, Subject: "s", ReceivedAt: time.Now().Unix()}
	b, _ := proto.Marshal(meta)
	if err := env.applier.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)), Value: b,
	}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	uid, err := env.mailboxes.DeliverMessage(userID, mailbox, meta, 0)
	if err != nil {
		t.Fatal(err)
	}
	return uid
}

func newRulePage(t *testing.T, h *RulesHandler, userID, query string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/settings/rules/new?"+query, nil)
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))
	w := httptest.NewRecorder()
	h.NewRule(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	return html.UnescapeString(w.Body.String())
}

// TestNewRuleFromMessage: "Create filter" prefills From contains the sender
// of the user's own stored message. The address is never taken from the URL,
// so another user's message, a missing UID or an unparseable From leaves the
// default empty condition.
func TestNewRuleFromMessage(t *testing.T) {
	env := newMailTagsTestEnv(t)
	h := NewRulesHandler(rules.NewRuleStore(env.ps, env.applier), env.mailboxes, env.ps, nil)
	ann := deliverFrom(t, env, "u1", "INBOX", "g1", `"Ann O'Brien" <ann@example.com>`)
	bad := deliverFrom(t, env, "u1", "INBOX", "g2", "not an address")
	other := deliverFrom(t, env, "u2", "Private", "g3", "secret@other.example")

	const empty = `[{"field":"from","operator":"contains","value":""}]`
	cases := []struct {
		name, user, query, wantConds, wantName string
	}{
		{"own message", "u1", fmt.Sprintf("mailbox=INBOX&uid=%d", ann), `[{"field":"from","operator":"contains","value":"ann@example.com"}]`, `value="Mail from ann@example.com"`},
		{"virtual mailbox", "u1", fmt.Sprintf("mailbox=unread&uid=%d", ann), `"value":"ann@example.com"`, "Mail from ann@example.com"},
		{"unparseable from", "u1", fmt.Sprintf("mailbox=INBOX&uid=%d", bad), empty, ""},
		{"missing uid", "u1", "mailbox=INBOX&uid=999", empty, ""},
		{"bad uid", "u1", "mailbox=INBOX&uid=x", empty, ""},
		{"another user's message", "u1", fmt.Sprintf("mailbox=Private&uid=%d", other), empty, ""},
		{"address in the URL is ignored", "u1", "from=evil@example.com", empty, ""},
		{"plain new rule", "u1", "", empty, ""},
	}
	for _, c := range cases {
		out := newRulePage(t, h, c.user, c.query)
		if !strings.Contains(out, c.wantConds) {
			t.Errorf("%s: conditions missing %s", c.name, c.wantConds)
		}
		if c.wantName != "" && !strings.Contains(out, c.wantName) {
			t.Errorf("%s: name missing %q", c.name, c.wantName)
		}
		for _, leak := range []string{"secret@other.example", "evil@example.com"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s: page contains %s", c.name, leak)
			}
		}
	}
}
