package agent

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestContactGroupRoutesThroughRouter drives the contact group routes through
// the production router of a server-role agent (the dev role does not enforce
// CSRF): a write needs a session and a CSRF token, the session user is the owner
// and another user's group is a 404, and static segments win over /contacts/{guid}.
func TestContactGroupRoutesThroughRouter(t *testing.T) {
	cfg := peerRunConfig(t, "127.0.0.1", "127.0.0.1")
	a, stop := runAgentUntilReady(t, cfg)
	defer stop()

	const password = "pass-1234-xyz"
	aliceID, err := auth.CreateUser(a, a.Metadata, "alice@localhost", "Alice", password, true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := auth.CreateUser(a, a.Metadata, "bob@localhost", "Bob", password, false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)

	newClient := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	session := func(email string) (*http.Client, string) {
		c := newClient()
		resp, err := c.PostForm(base+"/login", url.Values{"email": {email}, "password": {password}})
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		page, err := c.Get(base + "/settings/vacation")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(page.Body)
		_ = page.Body.Close()
		m := csrfHeaderRe.FindSubmatch(body)
		if page.StatusCode != http.StatusOK || m == nil {
			t.Fatalf("page status %d, token found: %v", page.StatusCode, m != nil)
		}
		return c, string(m[1])
	}
	alice, aliceTok := session("alice@localhost")
	bob, bobTok := session("bob@localhost")

	type result struct {
		code int
		body string
		loc  string
	}
	do := func(c *http.Client, method, tok, path string, form url.Values) result {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if tok != "" {
			req.Header.Set("X-CSRF-Token", tok)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return result{resp.StatusCode, string(b), resp.Header.Get("Location")}
	}
	want := func(r result, code int, what string) {
		t.Helper()
		if r.code != code {
			t.Fatalf("%s: status %d, want %d; body %.200q", what, r.code, code, r.body)
		}
	}

	ann := strings.Repeat("a", 32)
	if _, err := a.Contacts.Create(aliceID, &bongov1.Contact{Guid: ann, DisplayName: "Ann", Email: "ann@example.org"}); err != nil {
		t.Fatal(err)
	}
	gid := strings.Repeat("c", 32)
	writes := []struct {
		path string
		form url.Values
	}{
		{"/contacts/groups", url.Values{"name": {"Team"}}},
		{"/contacts/groups/" + gid + "/rename", url.Values{"name": {"x"}}},
		{"/contacts/groups/" + gid + "/delete", nil},
		{"/contacts/groups/" + gid + "/members/remove", url.Values{"member": {ann}}},
		{"/contacts/" + ann + "/add-to-group", url.Values{"group": {gid}}},
	}

	// No session: every write is bounced to the login page and nothing is written.
	anon := newClient()
	for _, w := range writes {
		r := do(anon, http.MethodPost, "", w.path, w.form)
		if r.code != http.StatusSeeOther && r.code != http.StatusFound && r.code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want a login redirect", w.path, r.code)
		}
	}
	// A session without a token: 403.
	for _, w := range writes {
		r := do(alice, http.MethodPost, "", w.path, w.form)
		if r.code != http.StatusForbidden || !strings.Contains(r.body, csrfBodyText) {
			t.Errorf("%s without a CSRF token: %d %q, want 403", w.path, r.code, r.body)
		}
	}
	if groups, _ := a.Contacts.ListContactGroups(aliceID, ""); len(groups) != 0 {
		t.Fatalf("a refused request created %d groups", len(groups))
	}

	// Create, add, then another user's attempts.
	r := do(alice, http.MethodPost, aliceTok, "/contacts/groups", url.Values{"name": {"Team"}})
	want(r, http.StatusSeeOther, "create")
	groups, _ := a.Contacts.ListContactGroups(aliceID, "")
	if len(groups) != 1 {
		t.Fatalf("groups = %d", len(groups))
	}
	gid = groups[0].Guid
	want(do(alice, http.MethodPost, aliceTok, "/contacts/"+ann+"/add-to-group", url.Values{"group": {gid}}), http.StatusSeeOther, "add")
	if g, _ := a.Contacts.Get(aliceID, gid); len(g.Members) != 1 {
		t.Fatalf("members = %v", g.Members)
	}

	for _, path := range []string{"/contacts/groups/" + gid + "/rename", "/contacts/groups/" + gid + "/delete", "/contacts/groups/" + gid + "/members/remove"} {
		want(do(bob, http.MethodPost, bobTok, path, url.Values{"name": {"x"}, "member": {ann}}), http.StatusNotFound, "bob on "+path)
	}
	want(do(bob, http.MethodPost, bobTok, "/contacts/"+ann+"/add-to-group", url.Values{"group": {gid}}), http.StatusNotFound, "bob adds")
	if g, err := a.Contacts.Get(aliceID, gid); err != nil || g.DisplayName != "Team" || len(g.Members) != 1 {
		t.Fatalf("bob changed alice's group: %v %+v", err, g)
	}

	// The addresses endpoint: alice gets the address, bob a 404.
	get := func(c *http.Client, path string) result {
		resp, err := c.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return result{resp.StatusCode, string(b), ""}
	}
	if r := get(alice, "/api/contact-groups/"+gid+"/addresses"); r.code != http.StatusOK || !strings.Contains(r.body, "ann@example.org") {
		t.Errorf("alice addresses: %d %q", r.code, r.body)
	}
	if r := get(bob, "/api/contact-groups/"+gid+"/addresses"); r.code != http.StatusNotFound {
		t.Errorf("bob addresses: %d, want 404", r.code)
	}
	if r := get(newClient(), "/api/contact-groups/"+gid+"/addresses"); r.code == http.StatusOK {
		t.Errorf("anonymous addresses: %d, want a refusal", r.code)
	}
	// The group page renders for its owner and is a 404 for another user.
	if r := get(alice, "/contacts/"+gid); r.code != http.StatusOK || !strings.Contains(r.body, "Team") {
		t.Errorf("alice group page: %d", r.code)
	}
	if r := get(bob, "/contacts/"+gid); r.code != http.StatusNotFound {
		t.Errorf("bob group page: %d, want 404", r.code)
	}

	want(do(alice, http.MethodPost, aliceTok, "/contacts/groups/"+gid+"/delete", nil), http.StatusSeeOther, "delete")
	if _, err := a.Contacts.Get(aliceID, gid); err == nil {
		t.Error("the group survived its delete")
	}
}
