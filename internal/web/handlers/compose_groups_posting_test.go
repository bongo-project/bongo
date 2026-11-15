package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// setGroupPolicy makes bob the only member, dave the only owner and applies
// policy. The group is stored through the real GroupStore.
func setGroupPolicy(t *testing.T, gs *store.GroupStore, g *bongov1.Group, policy string) {
	t.Helper()
	if _, err := gs.UpdateGroup(&bongov1.Group{
		Id: g.Id, MemberIds: []string{"u-bob"}, OwnerIds: []string{"u-dave"}, PostingPolicy: policy,
	}); err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}
}

func sendLogCount(t *testing.T, env *composeSendAsEnv, uid string) int {
	t.Helper()
	it, err := env.ps.PrefixScan(store.SendLogPrefix(uid))
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	n := 0
	for it.Next() {
		n++
	}
	return n
}

func TestComposeGroupPostingPolicy(t *testing.T) {
	type actor struct{ id, email string }
	var (
		member    = actor{"u-bob", "bob@example.com"}
		owner     = actor{"u-dave", "dave@example.com"}
		colleague = actor{"u-carol", "carol@example.com"}
		other     = actor{"u-eve", "eve@other.example"}
	)
	allow := map[string]map[actor]bool{
		"anyone":  {member: true, owner: true, colleague: true, other: true},
		"domain":  {member: true, owner: true, colleague: true},
		"members": {member: true, owner: true},
		"owners":  {owner: true},
	}
	for _, policy := range []string{"anyone", "domain", "members", "owners"} {
		for _, a := range []actor{member, owner, colleague, other} {
			t.Run(policy+"/"+a.email, func(t *testing.T) {
				env, gs, g := newGroupComposeEnv(t)
				env.addLocalUser(t, other.id, other.email)
				setGroupPolicy(t, gs, g, policy)
				w := postSendForm(env, a.id, a.email, sendID(), url.Values{
					"to": {"team@example.com, alice@example.com"}, "subject": {"group"}, "body": {"hello"},
				})
				if allow[policy][a] {
					if w.Code != http.StatusSeeOther {
						t.Fatalf("status %d: %s", w.Code, w.Body.String())
					}
					if got := envelopeRecipients(env); !slices.Contains(got, "bob@example.com") || !slices.Contains(got, "alice@example.com") {
						t.Fatalf("envelopes for %v, want the member and the direct recipient", got)
					}
					return
				}
				if w.Code != http.StatusForbidden {
					t.Fatalf("status %d, want 403: %s", w.Code, w.Body.String())
				}
				if !strings.Contains(w.Body.String(), "team@example.com") {
					t.Errorf("body %q does not name the group", w.Body.String())
				}
				if got := envelopeRecipients(env); len(got) != 0 {
					t.Errorf("a refused send spooled for %v, including the direct recipient", got)
				}
				if sent, _ := env.mailboxes.ListMessages(a.id, "Sent"); len(sent) != 0 {
					t.Error("a refused send stored a Sent copy")
				}
				if n := sendLogCount(t, env, a.id); n != 0 {
					t.Errorf("a refused send left %d send-log steps", n)
				}
			})
		}
	}
}

func TestComposeGroupRestrictedIsTranslated(t *testing.T) {
	env, gs, g := newGroupComposeEnv(t)
	setGroupPolicy(t, gs, g, "owners")
	form := url.Values{"to": {"team@example.com"}, "subject": {"s"}, "body": {"b"}, "send_id": {sendID()}}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, "u-carol", "carol@example.com")
	req = req.WithContext(middleware.WithLocale(req.Context(), "de"))
	w := httptest.NewRecorder()
	env.handler.Send(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "team@example.com") || !strings.Contains(w.Body.String(), "eingeschränkt") {
		t.Errorf("body %q is not the German refusal naming the group", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type %q, want text/plain", ct)
	}
}

// The From field may be an address the user can send as; it must not decide.
func TestComposeGroupPostingIgnoresFromField(t *testing.T) {
	t.Run("send-as address in the group's domain does not satisfy domain", func(t *testing.T) {
		env, gs, g := newGroupComposeEnv(t)
		env.addLocalUser(t, "u-eve", "eve@other.example")
		env.seedAddress(t, "eve-work@example.com", "u-eve", "example.com", true, true)
		setGroupPolicy(t, gs, g, "domain")
		w := postSendForm(env, "u-eve", "eve@other.example", sendID(), url.Values{
			"to": {"team@example.com"}, "subject": {"s"}, "body": {"b"}, "from": {"eve-work@example.com"},
		})
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "team@example.com") {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if got := envelopeRecipients(env); len(got) != 0 {
			t.Errorf("spooled for %v", got)
		}
	})
}
