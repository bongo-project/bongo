package agent

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// adminGroupsSession is a logged-in client with the CSRF token of its own page.
type adminGroupsSession struct {
	t      *testing.T
	client *http.Client
	base   string
	token  string
}

func newAdminGroupsSession(t *testing.T, base, email, password string) *adminGroupsSession {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{
		Jar: jar, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.PostForm(base+"/login", url.Values{"email": {email}, "password": {password}})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Fatalf("login %s: status %d", email, resp.StatusCode)
	}
	page, err := c.Get(base + "/settings/vacation")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	m := csrfHeaderRe.FindSubmatch(body)
	if page.StatusCode != http.StatusOK || m == nil {
		t.Fatalf("%s: vacation page status %d, token found %v", email, page.StatusCode, m != nil)
	}
	return &adminGroupsSession{t: t, client: c, base: base, token: string(m[1])}
}

func (s *adminGroupsSession) get(path string) (int, string) {
	s.t.Helper()
	resp, err := s.client.Get(s.base + path)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// post sends the form with the session's token in the form field unless token
// says otherwise; "-" sends none.
func (s *adminGroupsSession) post(path string, form url.Values, token string) (int, string, string) {
	s.t.Helper()
	if token == "" {
		token = s.token
	}
	if token != "-" {
		form.Set("_csrf", token)
	}
	req, _ := http.NewRequest(http.MethodPost, s.base+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Location")
}

var groupEditLinkRe = regexp.MustCompile(`href="/admin/groups/([0-9a-f]{32})"`)

func listedGroupIDs(body string) []string {
	var ids []string
	for _, m := range groupEditLinkRe.FindAllStringSubmatch(body, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// TestAdminGroupsThroughTheProductionRouter proves what a handler test cannot:
// the routes exist, sit behind the admin gate and CSRF, and carry each caller's
// domain scope, with the real GroupStore and address check wired in.
func TestAdminGroupsThroughTheProductionRouter(t *testing.T) {
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
	memberID := mk("mem@a.test", false)
	otherID := mk("oth@a.test", false)
	bobID := mk("bob@b.test", false)
	plainID := mk("plain@a.test", false)
	_ = plainID
	if err := store.NewDomainAdminStore(a.Store, a).Grant("a.test", domAdminID); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	root := newAdminGroupsSession(t, base, "root@localhost", pw)
	dom := newAdminGroupsSession(t, base, "adm@a.test", pw)
	plain := newAdminGroupsSession(t, base, "plain@a.test", pw)

	t.Run("the gate", func(t *testing.T) {
		for _, path := range []string{"/admin/groups", "/admin/groups/new"} {
			if code, _ := plain.get(path); code != http.StatusForbidden {
				t.Errorf("non-admin GET %s = %d, want 403", path, code)
			}
		}
		code, body, _ := plain.post("/admin/groups", url.Values{"domain": {"a.test"}, "local": {"sneaky"}}, "")
		if code != http.StatusForbidden || strings.Contains(body, csrfBodyText) {
			t.Errorf("non-admin POST = %d %q, want 403 from the admin gate", code, body)
		}
		if gs, _ := a.Groups.ListGroups(""); len(gs) != 0 {
			t.Fatalf("a non-admin created %d groups", len(gs))
		}
		for _, s := range []*adminGroupsSession{root, dom} {
			for _, path := range []string{"/admin/groups", "/admin/groups/new?domain=a.test"} {
				if code, _ := s.get(path); code != http.StatusOK {
					t.Errorf("admin GET %s = %d", path, code)
				}
			}
		}
	})

	var salesID string
	t.Run("create, taken address and the form's own keys", func(t *testing.T) {
		form := url.Values{
			"domain": {"a.test"}, "local": {"sales"}, "display_name": {"Sales"},
			"posting_policy": {"anyone"}, "member": {memberID}, "owner": {memberID},
		}
		code, body, loc := dom.post("/admin/groups", form, "")
		if code != http.StatusSeeOther || !strings.Contains(loc, "flash=group-created") {
			t.Fatalf("create = %d %q loc %q", code, body, loc)
		}
		_, list := dom.get("/admin/groups")
		ids := listedGroupIDs(list)
		if len(ids) != 1 || !strings.Contains(list, "sales@a.test") {
			t.Fatalf("list = %v\n%s", ids, list)
		}
		salesID = ids[0]
		// The edit form renders the user id as the checkbox value; the posted
		// form above used the same key, so what the UI sends is what is stored.
		_, edit := dom.get("/admin/groups/" + salesID)
		if !strings.Contains(edit, `name="member" value="`+memberID+`" checked`) {
			t.Errorf("edit form does not render the member checked:\n%s", edit)
		}

		// The same address again, and an address an account already holds.
		for _, local := range []string{"sales", "mem"} {
			code, body, _ := dom.post("/admin/groups", url.Values{"domain": {"a.test"}, "local": {local}}, "")
			if code != http.StatusConflict || !strings.Contains(body, "That address is already in use.") {
				t.Errorf("create %q again = %d, want 409 with the translated message", local, code)
			}
		}
	})

	var secretID string
	t.Run("a domain admin stays inside their domains", func(t *testing.T) {
		code, _, loc := root.post("/admin/groups", url.Values{"domain": {"b.test"}, "local": {"secret"}, "member": {bobID}}, "")
		if code != http.StatusSeeOther {
			t.Fatalf("global admin create in b.test = %d", code)
		}
		_ = loc
		all, _ := a.Groups.ListGroups("b.test")
		if len(all) != 1 {
			t.Fatalf("b.test groups = %d", len(all))
		}
		secretID = all[0].Id

		// Listing hides it, and every route on it answers the same 403.
		if _, list := dom.get("/admin/groups"); strings.Contains(list, "secret@b.test") {
			t.Error("the domain admin's list shows another domain's group")
		}
		if code, _ := dom.get("/admin/groups/" + secretID); code != http.StatusForbidden {
			t.Errorf("view = %d, want 403", code)
		}
		if code, _, _ := dom.post("/admin/groups/"+secretID, url.Values{"display_name": {"Mine now"}}, ""); code != http.StatusForbidden {
			t.Errorf("update = %d, want 403", code)
		}
		if code, _, _ := dom.post("/admin/groups/"+secretID+"/delete", url.Values{}, ""); code != http.StatusForbidden {
			t.Errorf("delete = %d, want 403", code)
		}
		// An id that does not exist answers like one out of scope.
		if code, _ := dom.get("/admin/groups/00000000000000000000000000000000"); code != http.StatusForbidden {
			t.Errorf("view of a missing group = %d, want the same 403", code)
		}
		if g, err := a.Groups.GetGroup(secretID); err != nil || g.DisplayName != "" {
			t.Errorf("the other domain's group changed: %v %v", g, err)
		}
		// Creating in, or pulling users from, another domain is refused.
		if code, _, _ := dom.post("/admin/groups", url.Values{"domain": {"b.test"}, "local": {"mine"}}, ""); code != http.StatusForbidden {
			t.Errorf("create in b.test = %d, want 403", code)
		}
		code, body, _ := dom.post("/admin/groups/"+salesID, url.Values{"member": {memberID, bobID}}, "")
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "existing users of the group") {
			t.Errorf("adding another tenant's user = %d %q", code, body)
		}
		if g, _ := a.Groups.GetGroup(salesID); len(g.MemberIds) != 1 {
			t.Errorf("members after the refused update = %v", g.MemberIds)
		}
		// The global admin sees both.
		_, all2 := root.get("/admin/groups")
		if !strings.Contains(all2, "secret@b.test") || !strings.Contains(all2, "sales@a.test") {
			t.Errorf("the global admin's list is incomplete:\n%s", all2)
		}
	})

	t.Run("CSRF on every mutation", func(t *testing.T) {
		paths := []string{"/admin/groups", "/admin/groups/" + salesID, "/admin/groups/" + salesID + "/delete"}
		wrong := dom.token[:len(dom.token)-1] + map[bool]string{true: "B", false: "A"}[dom.token[len(dom.token)-1] == 'A']
		for _, p := range paths {
			for name, tok := range map[string]string{"none": "-", "wrong": wrong} {
				code, body, _ := dom.post(p, url.Values{"domain": {"a.test"}, "local": {"csrf"}}, tok)
				if code != http.StatusForbidden || !strings.Contains(body, csrfBodyText) {
					t.Errorf("POST %s with %s token = %d %q, want 403 %q", p, name, code, body, csrfBodyText)
				}
			}
		}
		if gs, _ := a.Groups.ListGroups("a.test"); len(gs) != 1 {
			t.Errorf("a refused request changed the groups: %d", len(gs))
		}
	})

	t.Run("update and delete, grants go with the group", func(t *testing.T) {
		if _, err := a.Calendars.Create(memberID, &bongov1.Calendar{Guid: "shared", Name: "Shared"}); err != nil {
			t.Fatal(err)
		}
		if err := a.ACL.SetGrant(store.ACLKindCalendar, memberID, "shared", store.GranteeGroup(salesID),
			store.GrantLevelRead, "", store.GrantStateAccepted, memberID, time.Now()); err != nil {
			t.Fatal(err)
		}
		if lvl, _ := a.ACL.CalendarAccessLevel(memberID, "shared", otherID); lvl != "" {
			t.Fatalf("non-member level = %q", lvl)
		}
		code, body, loc := dom.post("/admin/groups/"+salesID, url.Values{
			"display_name": {"Sales Team"}, "posting_policy": {"members"}, "member": {memberID, otherID},
		}, "")
		if code != http.StatusSeeOther || !strings.Contains(loc, "flash=group-updated") {
			t.Fatalf("update = %d %q", code, body)
		}
		g, _ := a.Groups.GetGroup(salesID)
		if g.DisplayName != "Sales Team" || g.PostingPolicy != "members" || len(g.MemberIds) != 2 || g.Address != "sales@a.test" {
			t.Errorf("group after update = %+v", g)
		}
		if lvl, _ := a.ACL.CalendarAccessLevel(memberID, "shared", otherID); lvl != store.GrantLevelRead {
			t.Errorf("a new member's level = %q, want read", lvl)
		}

		code, body, loc = dom.post("/admin/groups/"+salesID+"/delete", url.Values{}, "")
		if code != http.StatusSeeOther || !strings.Contains(loc, "flash=group-deleted") {
			t.Fatalf("delete = %d %q", code, body)
		}
		if _, err := a.Groups.GetGroup(salesID); err == nil {
			t.Error("the group survived its delete")
		}
		if lvl, _ := a.ACL.CalendarAccessLevel(memberID, "shared", otherID); lvl != "" {
			t.Errorf("access survived the group: %q", lvl)
		}
		refs, _ := a.ACL.ListGrantRefs()
		for _, r := range refs {
			if r.Principal == store.GranteeGroup(salesID) {
				t.Errorf("grant left behind: %+v", r)
			}
		}
	})
}

// A calendar shared with a group through the production router: the share
// handler has the group lookup wired, the member sees the share on /shares
// without a Leave button, and the group's delete takes the grant with it.
func TestCalendarShareWithGroupThroughTheProductionRouter(t *testing.T) {
	cfg := peerRunConfig(t, "127.0.0.1", "127.0.0.1")
	a, stop := runAgentUntilReady(t, cfg)
	defer stop()
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/system/domain/a.test"), Value: []byte("enabled")}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	const pw = "pass-1234-xyz"
	ownerID, err := auth.CreateUser(a, a.Metadata, "own@a.test", "Owner", pw, false)
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := auth.CreateUser(a, a.Metadata, "mem@a.test", "Member", pw, false)
	if err != nil {
		t.Fatal(err)
	}
	g, err := a.Groups.CreateGroup(&bongov1.Group{Address: "team@a.test", DisplayName: "Team", MemberIds: []string{memberID}})
	if err != nil {
		t.Fatal(err)
	}
	calID, err := a.Calendars.Create(ownerID, &bongov1.Calendar{Name: "Roadmap"})
	if err != nil {
		t.Fatal(err)
	}

	owner := newAdminGroupsSession(t, base, "own@a.test", pw)
	member := newAdminGroupsSession(t, base, "mem@a.test", pw)

	code, body, _ := owner.post("/calendar/"+calID+"/share", url.Values{"share_with": {"team@a.test"}, "level": {"read"}}, "")
	if code != http.StatusOK {
		t.Fatalf("share = %d %q", code, body)
	}
	if lvl, _ := a.ACL.CalendarAccessLevel(ownerID, calID, memberID); lvl != store.GrantLevelRead {
		t.Fatalf("member level = %q, want read", lvl)
	}
	_, page := member.get("/shares")
	if !strings.Contains(page, "Roadmap") || !strings.Contains(page, "team@a.test") || strings.Contains(page, "/shares/leave") {
		t.Errorf("shares page for the member:\n%s", page)
	}
	if err := a.Groups.DeleteGroup(g.Id); err != nil {
		t.Fatal(err)
	}
	if lvl, _ := a.ACL.CalendarAccessLevel(ownerID, calID, memberID); lvl != "" {
		t.Errorf("access survived the group: %q", lvl)
	}
}
