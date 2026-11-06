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
)

// csrfBodyText is the text RequireCSRF writes on a refusal. Asserting it, and
// not only the status, keeps a 403 from RequireSameOrigin, the admin
// middleware or a handler from passing as a CSRF refusal.
const csrfBodyText = "invalid CSRF token"

var csrfHeaderRe = regexp.MustCompile(`X-CSRF-Token(?:&#34;|&quot;|")\s*:\s*(?:&#34;|&quot;|")([^"&]+)`)

// TestProductionRouterCSRFGroup starts a server-role Agent (RequireCSRF does
// not enforce in the dev role), logs in as a global admin through the real
// router and checks that the archive, bulk archive, vacation and domain-alias
// writes sit behind CSRF, and that the reply-list route is registered.
func TestProductionRouterCSRFGroup(t *testing.T) {
	cfg := peerRunConfig(t, "127.0.0.1", "127.0.0.1")
	a, stop := runAgentUntilReady(t, cfg)
	defer stop()

	const rootPassword = "pass-1234-xyz"
	rootID, err := auth.CreateUser(a, a.Metadata, "root@localhost", "Root", rootPassword, true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:           jar,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)

	login, err := client.PostForm(base+"/login", url.Values{"email": {"root@localhost"}, "password": {rootPassword}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = login.Body.Close()
	if login.StatusCode != http.StatusSeeOther && login.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d", login.StatusCode)
	}

	// The token comes from the rendered page so the test follows the
	// production derivation.
	page, err := client.Get(base + "/settings/vacation")
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	m := csrfHeaderRe.FindSubmatch(pageBody)
	if page.StatusCode != http.StatusOK || m == nil {
		t.Fatalf("vacation page status %d, token found: %v", page.StatusCode, m != nil)
	}
	token := string(m[1])
	last := "A"
	if token[len(token)-1] == 'A' {
		last = "B"
	}
	wrong := token[:len(token)-1] + last

	type call struct {
		name string
		path string
		form func(n string) url.Values
	}
	calls := []call{
		{"archive", "/mail/INBOX/1/archive", func(string) url.Values { return url.Values{} }},
		{"unarchive", "/mail/INBOX/1/unarchive", func(string) url.Values { return url.Values{} }},
		{"bulk archive", "/mail/INBOX/bulk", func(string) url.Values {
			return url.Values{"action": {"archive"}, "uids": {"1"}}
		}},
		{"vacation", "/settings/vacation", func(string) url.Values {
			return url.Values{"vacation_enabled": {"0"}}
		}},
		{"calendar subscribe", "/calendar/subscribe", func(n string) url.Values {
			return url.Values{"url": {"https://feeds.example.com/" + n + ".ics"}}
		}},
		{"calendar create", "/calendar", func(n string) url.Values {
			return url.Values{"name": {"Work " + n}, "color": {"#112233"}}
		}},
		{"calendar unsubscribe", "/calendar/missing/unsubscribe", func(string) url.Values { return url.Values{} }},
		{"share accept", "/calendar/shares/accept", func(string) url.Values {
			return url.Values{"owner": {"nobody"}, "calendar": {"none"}}
		}},
		{"share decline", "/calendar/shares/decline", func(string) url.Values {
			return url.Values{"owner": {"nobody"}, "calendar": {"none"}}
		}},
		{"share leave", "/calendar/shares/leave", func(string) url.Values {
			return url.Values{"owner": {"nobody"}, "calendar": {"none"}}
		}},
		{"public link send", "/calendar/missing/publish/send", func(string) url.Values {
			return url.Values{"send_to": {"friend@example.com"}}
		}},
		{"calendar share", "/calendar/missing/share", func(string) url.Values {
			return url.Values{"share_with": {"friend@example.com"}}
		}},
		{"calendar unshare", "/calendar/missing/unshare", func(string) url.Values {
			return url.Values{"share_with": {"nobody"}}
		}},
		{"invitation answer", "/mail/INBOX/1/invitation", func(string) url.Values {
			return url.Values{"action": {"accept"}}
		}},
		{"task create", "/tasks", func(n string) url.Values { return url.Values{"title": {"task " + n}} }},
		{"task update", "/tasks/missing", func(string) url.Values { return url.Values{"title": {"x"}} }},
		{"task complete", "/tasks/missing/complete", func(string) url.Values { return url.Values{} }},
		{"task reopen", "/tasks/missing/reopen", func(string) url.Values { return url.Values{} }},
		{"task delete", "/tasks/missing/delete", func(string) url.Values { return url.Values{} }},
		{"domain alias", "/admin/domains", func(n string) url.Values {
			return url.Values{"domain": {n + ".example"}, "alias_of": {"localhost"}}
		}},
		// The app password routes carry the CORRECT password: a wrong one would
		// feed the login throttle, which the valid-token check tolerates as a
		// 429 and so would hide a route that never ran. Exactly four of these
		// requests may write a credential (create and devices, header and
		// form); the count after the loop proves the refused ones wrote none
		// and the valid ones reached the handler.
		{"app password create", "/settings/security/app-passwords", func(n string) url.Values {
			return url.Values{"label": {"csrf " + n}, "password": {rootPassword}}
		}},
		{"app password revoke", "/settings/security/app-passwords/revoke", func(string) url.Values {
			return url.Values{"id": {"aaaaaa"}}
		}},
		{"add device", "/settings/security/devices", func(n string) url.Values {
			return url.Values{"kind": {"other"}, "label": {"csrf device " + n}, "password": {rootPassword}}
		}},
		{"require app passwords on", "/settings/security/require-app-passwords/on", func(string) url.Values {
			return url.Values{}
		}},
		{"require app passwords off", "/settings/security/require-app-passwords/off", func(string) url.Values {
			return url.Values{"password": {rootPassword}}
		}},
		{"admin revoke all app passwords", "/admin/users/missing/app-passwords/revoke", func(string) url.Values {
			return url.Values{}
		}},
	}

	post := func(c call, tag, hdr, field string) (int, string, string) {
		form := c.form(tag)
		if field != "" {
			form.Set("_csrf", field)
		}
		req, _ := http.NewRequest(http.MethodPost, base+c.path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if hdr != "" {
			req.Header.Set("X-CSRF-Token", hdr)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", c.path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header.Get("Location")
	}

	for i, c := range calls {
		n := strconv.Itoa(i)
		t.Run(c.name+"/no token", func(t *testing.T) {
			code, body, _ := post(c, "none"+n, "", "")
			if code != http.StatusForbidden || !strings.Contains(body, csrfBodyText) {
				t.Errorf("status %d body %q, want 403 %q", code, body, csrfBodyText)
			}
		})
		t.Run(c.name+"/wrong token", func(t *testing.T) {
			code, body, _ := post(c, "wrong"+n, wrong, "")
			if code != http.StatusForbidden || !strings.Contains(body, csrfBodyText) {
				t.Errorf("status %d body %q, want 403 %q", code, body, csrfBodyText)
			}
		})
		for _, via := range []string{"header", "form"} {
			t.Run(c.name+"/valid token "+via, func(t *testing.T) {
				hdr, field := token, ""
				if via == "form" {
					hdr, field = "", token
				}
				code, body, loc := post(c, via+n, hdr, field)
				if strings.Contains(body, csrfBodyText) {
					t.Errorf("valid token refused by CSRF: status %d", code)
				}
				// A handler may answer 404 for the missing message; the
				// no-token case above proves the route is registered.
				if code == http.StatusMethodNotAllowed || code == http.StatusForbidden {
					t.Errorf("status %d with a valid token", code)
				}
				if strings.HasPrefix(loc, "/login") {
					t.Errorf("redirected to login: session not accepted")
				}
			})
		}
	}

	// Four valid-token requests create a credential (create and add device,
	// each by header and by form). More means a refused request wrote one;
	// fewer means a valid one never reached its handler.
	if got, err := a.AppPasswords.List(rootID); err != nil || len(got) != 4 {
		t.Fatalf("root holds %d app passwords (%v), want exactly 4", len(got), err)
	}

	t.Run("admin revoke all is refused to non-admins", func(t *testing.T) {
		const memberPassword = "member-pass-9876"
		if _, err := auth.CreateUser(a, a.Metadata, "member@localhost", "Member", memberPassword, false); err != nil {
			t.Fatalf("CreateUser member: %v", err)
		}
		// The member's own session and the token from the member's own page,
		// so a CSRF refusal cannot pass as an admin refusal.
		mjar, _ := cookiejar.New(nil)
		mc := &http.Client{Jar: mjar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		ml, err := mc.PostForm(base+"/login", url.Values{"email": {"member@localhost"}, "password": {memberPassword}})
		if err != nil {
			t.Fatal(err)
		}
		_ = ml.Body.Close()
		mp, err := mc.Get(base + "/settings/vacation")
		if err != nil {
			t.Fatal(err)
		}
		mb, _ := io.ReadAll(mp.Body)
		_ = mp.Body.Close()
		mm := csrfHeaderRe.FindSubmatch(mb)
		if mp.StatusCode != http.StatusOK || mm == nil {
			t.Fatalf("member page status %d, token found: %v", mp.StatusCode, mm != nil)
		}
		revokeAs := func(c *http.Client, tok, target string) (int, string) {
			req, _ := http.NewRequest(http.MethodPost, base+"/admin/users/"+target+"/app-passwords/revoke", strings.NewReader(""))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-CSRF-Token", tok)
			resp, err := c.Do(req)
			if err != nil {
				t.Fatalf("POST revoke: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(b)
		}
		// Root's four credentials from the table above are the victim's.
		before, _ := a.AppPasswords.List(rootID)
		if len(before) != 4 {
			t.Fatalf("fixture: victim holds %d credentials", len(before))
		}

		code, body := revokeAs(mc, string(mm[1]), rootID)
		if code != http.StatusForbidden || strings.Contains(body, csrfBodyText) {
			t.Errorf("member revoke-all on root: status %d body %q, want 403 from the admin check", code, body)
		}
		if after, _ := a.AppPasswords.List(rootID); len(after) != len(before) {
			t.Errorf("a non-admin removed credentials: %d -> %d", len(before), len(after))
		}

		// Control: the same request by the admin does revoke, so the refusal
		// above is about who asked.
		code, body = revokeAs(client, token, rootID)
		if code != http.StatusSeeOther && code != http.StatusOK {
			t.Fatalf("admin revoke-all on root: status %d body %q", code, body)
		}
		if after, _ := a.AppPasswords.List(rootID); len(after) != 0 {
			t.Errorf("the admin's revoke-all left %d credentials", len(after))
		}
	})

	// DELETE /calendar/{id} carries its token in the header only (the
	// middleware reads the form of body-bearing methods), so it is outside the
	// POST table above.
	del := func(hdr string) (int, string) {
		req, _ := http.NewRequest(http.MethodDelete, base+"/calendar/missing", nil)
		if hdr != "" {
			req.Header.Set("X-CSRF-Token", hdr)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("DELETE: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for name, hdr := range map[string]string{"no token": "", "wrong token": wrong} {
		t.Run("calendar delete/"+name, func(t *testing.T) {
			if code, body := del(hdr); code != http.StatusForbidden || !strings.Contains(body, csrfBodyText) {
				t.Errorf("status %d body %q, want 403 %q", code, body, csrfBodyText)
			}
		})
	}
	t.Run("calendar delete/valid token", func(t *testing.T) {
		if code, body := del(token); code != http.StatusNotFound || strings.Contains(body, csrfBodyText) {
			t.Errorf("status %d body %q, want 404 from the handler", code, body)
		}
	})

	// adminH.SetResourceBookings: without it the bookings page answers 503.
	t.Run("resource bookings wired", func(t *testing.T) {
		id, err := auth.CreateResource(a, a.Metadata, "room1@localhost", "Room 1", auth.ResourceRoom, 4)
		if err != nil {
			t.Fatalf("CreateResource: %v", err)
		}
		resp, err := client.Get(base + "/admin/users/" + id + "/bookings")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET bookings = %d %q, want 200", resp.StatusCode, b)
		}
	})

	t.Run("share invitations page", func(t *testing.T) {
		resp, err := client.Get(base + "/calendar/shares")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /calendar/shares = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("reply-list answers", func(t *testing.T) {
		resp, err := client.Get(base + "/compose/reply-list/INBOX/1")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			t.Fatalf("status %d, route not registered", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "/login") {
			t.Errorf("redirected to login: session not accepted")
		}
	})
}
