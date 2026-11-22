package agent

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestAdminSharedInboxesThroughTheProductionRouter drives the member
// administration of a shared inbox through the real router with the real
// stores: routes, admin gate, CSRF, domain scope, the one refusal for bad
// members, and the effect of a removal on folder rights and send-as.
func TestAdminSharedInboxesThroughTheProductionRouter(t *testing.T) {
	cfg := peerRunConfig(t, "127.0.0.1", "127.0.0.1")
	a, stop := runAgentUntilReady(t, cfg)
	defer stop()
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)

	for _, d := range []string{"a.test", "b.test"} {
		if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/system/domain/" + d), Value: []byte("enabled")}, 5*time.Second); err != nil {
			t.Fatalf("domain %s: %v", d, err)
		}
	}
	const pw = "pass-1234-xyz"
	mk := func(email string, admin bool) string {
		id, err := auth.CreateUser(a, a.Metadata, email, email, pw, admin)
		if err != nil {
			t.Fatalf("CreateUser %s: %v", email, err)
		}
		return id
	}
	mk("root@localhost", true)
	domAdminID := mk("adm@a.test", false)
	aliceID := mk("alice@a.test", false)
	bobID := mk("bob@a.test", false)
	mk("outsider@b.test", false)
	mk("plain@a.test", false)
	if err := store.NewDomainAdminStore(a.Store, a).Grant("a.test", domAdminID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateResource(a, a.Metadata, "room@a.test", "Room", auth.ResourceRoom, 4); err != nil {
		t.Fatal(err)
	}
	grp, err := a.Groups.CreateGroup(&bongov1.Group{Address: "team@a.test", MemberIds: []string{bobID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Groups.CreateGroup(&bongov1.Group{Address: "team@b.test"}); err != nil {
		t.Fatal(err)
	}

	root := newAdminGroupsSession(t, base, "root@localhost", pw)
	dom := newAdminGroupsSession(t, base, "adm@a.test", pw)
	plain := newAdminGroupsSession(t, base, "plain@a.test", pw)

	created := func(s *adminGroupsSession, user, domain, members string) (int, string) {
		code, body, _ := s.post("/admin/users", url.Values{
			"kind": {"shared"}, "username": {user}, "domain": {domain}, "display_name": {"Support"}, "members": {members},
		}, "")
		return code, body
	}
	idOf := func(email string) string {
		p, err := a.Store.Get([]byte("/system/address/" + email))
		if err != nil {
			return ""
		}
		var rec bongov1.AddressRecord
		_ = protoUnmarshal(p, &rec)
		return rec.UserId
	}

	t.Run("the gate", func(t *testing.T) {
		if code, _ := plain.get("/admin/shared-inboxes/new"); code != http.StatusForbidden {
			t.Errorf("non-admin form = %d, want 403", code)
		}
		if code, _ := created(plain, "sneaky", "a.test", ""); code != http.StatusForbidden || idOf("sneaky@a.test") != "" {
			t.Errorf("non-admin create = %d, account exists %v", code, idOf("sneaky@a.test") != "")
		}
		if code, _ := dom.get("/admin/shared-inboxes/new"); code != http.StatusOK {
			t.Errorf("domain admin form = %d", code)
		}
	})

	t.Run("refused members create nothing", func(t *testing.T) {
		for name, members := range map[string]string{
			"another domain's user":  "outsider@b.test",
			"another domain's group": "team@b.test",
			"a resource":             "room@a.test",
			"an unknown address":     "nobody@a.test",
			"a good and a bad one":   "alice@a.test\noutsider@b.test",
		} {
			code, _ := created(root, "refused", "a.test", members)
			if code != http.StatusUnprocessableEntity {
				t.Errorf("%s: status %d, want 422", name, code)
			}
			if idOf("refused@a.test") != "" {
				t.Errorf("%s: the refused request created the account", name)
			}
		}
		if code, _ := created(dom, "refused", "b.test", ""); code != http.StatusForbidden {
			t.Errorf("domain admin creating in b.test = %d, want 403", code)
		}
		if idOf("refused@b.test") != "" {
			t.Error("a domain admin created an account outside their domain")
		}
	})

	var sharedID string
	t.Run("create with members", func(t *testing.T) {
		code, body := created(dom, "support", "a.test", "alice@a.test, team@a.test")
		if code != http.StatusSeeOther {
			t.Fatalf("create = %d %q", code, body)
		}
		sharedID = idOf("support@a.test")
		p, err := a.Store.Get([]byte("/user/" + sharedID + "/profile"))
		if err != nil {
			t.Fatal(err)
		}
		var prof bongov1.UserProfile
		_ = protoUnmarshal(p, &prof)
		if prof.Kind != auth.SharedKind || prof.IsAdmin || len(prof.SharedMembers) != 2 {
			t.Fatalf("profile = %v", &prof)
		}
		if _, err := a.Mailboxes.GetMailbox(sharedID, "INBOX"); err != nil {
			t.Fatalf("no INBOX: %v", err)
		}
		for _, who := range []string{aliceID, bobID} {
			id, _ := a.Mailboxes.EnsureMailboxID(sharedID, "INBOX")
			r, err := a.ACL.MailboxRights(sharedID, id, who, time.Now())
			if err != nil || r != store.MailboxRightsWrite {
				t.Errorf("rights of %s = %q, %v", who, r, err)
			}
		}
		for who, want := range map[string]bool{aliceID: true, bobID: true} {
			ok, err := store.CanSendAs(a.AddressStore, who, "", "support@a.test")
			if err != nil || ok != want {
				t.Errorf("CanSendAs(%s) = %v, %v", who, ok, err)
			}
		}
		// Login is refused with the answer of a wrong password.
		if _, err := a.Auth.Authenticate("support@a.test", pw); err == nil {
			t.Error("the shared account authenticated")
		}
	})

	t.Run("scope and refusals on an existing inbox", func(t *testing.T) {
		before := len(mustProfile(t, a, sharedID).SharedMembers)
		for name, addr := range map[string]string{
			"another domain's user": "outsider@b.test", "a resource": "room@a.test",
			"the shared account itself": "support@a.test", "a duplicate": "alice@a.test",
		} {
			if code, _, _ := dom.post("/admin/users/"+sharedID+"/members", url.Values{"address": {addr}}, ""); code != http.StatusUnprocessableEntity {
				t.Errorf("add %s = %d, want 422", name, code)
			}
		}
		// A second shared inbox cannot be a member.
		if code, _ := created(root, "billing", "a.test", ""); code != http.StatusSeeOther {
			t.Fatalf("create billing = %d", code)
		}
		if code, _, _ := dom.post("/admin/users/"+sharedID+"/members", url.Values{"address": {"billing@a.test"}}, ""); code != http.StatusUnprocessableEntity {
			t.Errorf("add another shared inbox = %d, want 422", code)
		}
		// Members of an out-of-scope or non-shared account.
		if code, _, _ := dom.post("/admin/users/"+aliceID+"/members", url.Values{"address": {"bob@a.test"}}, ""); code != http.StatusNotFound {
			t.Errorf("add to a person = %d, want 404", code)
		}
		if got := len(mustProfile(t, a, sharedID).SharedMembers); got != before {
			t.Errorf("members %d -> %d after refusals", before, got)
		}
		// A shared account is not a group member.
		if _, err := a.Groups.CreateGroup(&bongov1.Group{Address: "bad@a.test", MemberIds: []string{sharedID}}); err == nil {
			t.Error("a shared account became a group member")
		}
		// No password reset, no admin flag.
		if code, _, _ := root.post("/admin/users/"+sharedID+"/reset-password", url.Values{}, ""); code != http.StatusBadRequest {
			t.Errorf("reset-password = %d, want 400", code)
		}
		root.post("/admin/users/"+sharedID, url.Values{"display_name": {"Support"}, "is_admin": {"1"}}, "")
		if mustProfile(t, a, sharedID).IsAdmin {
			t.Error("a shared account became an administrator")
		}
	})

	t.Run("CSRF on the member routes", func(t *testing.T) {
		for _, p := range []string{"/members", "/members/remove"} {
			code, body, _ := dom.post("/admin/users/"+sharedID+p, url.Values{"address": {"bob@a.test"}, "principal": {store.GranteeUser(aliceID)}}, "-")
			if code != http.StatusForbidden || !strings.Contains(body, csrfBodyText) {
				t.Errorf("POST %s without a token = %d %q", p, code, body)
			}
		}
		if got := len(mustProfile(t, a, sharedID).SharedMembers); got != 2 {
			t.Errorf("members = %d after refused requests", got)
		}
	})

	t.Run("removing a member ends folder access and send-as", func(t *testing.T) {
		code, body, _ := dom.post("/admin/users/"+sharedID+"/members/remove", url.Values{"principal": {store.GranteeUser(aliceID)}}, "")
		if code != http.StatusSeeOther {
			t.Fatalf("remove = %d %q", code, body)
		}
		for _, f := range store.DefaultMailboxes {
			id, _ := a.Mailboxes.EnsureMailboxID(sharedID, f)
			if r, _ := a.ACL.MailboxRights(sharedID, id, aliceID, time.Now()); r != "" {
				t.Errorf("removed member still holds %q on %s", r, f)
			}
			if r, _ := a.ACL.MailboxRights(sharedID, id, bobID, time.Now()); r != store.MailboxRightsWrite {
				t.Errorf("group member lost access on %s: %q", f, r)
			}
		}
		if ok, _ := store.CanSendAs(a.AddressStore, aliceID, "", "support@a.test"); ok {
			t.Error("removed member can still send as the shared address")
		}
		if ok, _ := store.CanSendAs(a.AddressStore, bobID, "", "support@a.test"); !ok {
			t.Error("group member lost send-as")
		}
		// Removing the group ends the group member's access too.
		dom.post("/admin/users/"+sharedID+"/members/remove", url.Values{"principal": {store.GranteeGroup(grp.Id)}}, "")
		if ok, _ := store.CanSendAs(a.AddressStore, bobID, "", "support@a.test"); ok {
			t.Error("group removed but its member can still send")
		}
		// The account's page renders and lists no members.
		if code, page := dom.get("/admin/users/" + sharedID); code != http.StatusOK || strings.Contains(page, "reset-password") {
			t.Errorf("detail page = %d, offers reset-password: %v", code, strings.Contains(page, "reset-password"))
		}
	})

	t.Run("a domain admin cannot reach another domain's inbox", func(t *testing.T) {
		if code, _ := created(root, "other", "b.test", ""); code != http.StatusSeeOther {
			t.Fatalf("create in b.test = %d", code)
		}
		oid := idOf("other@b.test")
		if code, _, _ := dom.post("/admin/users/"+oid+"/members", url.Values{"address": {"outsider@b.test"}}, ""); code != http.StatusForbidden {
			t.Errorf("add member to another domain's inbox = %d, want 403", code)
		}
	})
}

func protoUnmarshal(b []byte, m proto.Message) error { return proto.Unmarshal(b, m) }

func mustProfile(t *testing.T, a *Agent, id string) *bongov1.UserProfile {
	t.Helper()
	b, err := a.Store.Get([]byte("/user/" + id + "/profile"))
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}
