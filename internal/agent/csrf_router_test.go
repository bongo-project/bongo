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

	if _, err := auth.CreateUser(a, a.Metadata, "root@localhost", "Root", "pass-1234-xyz", true); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:           jar,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)

	login, err := client.PostForm(base+"/login", url.Values{"email": {"root@localhost"}, "password": {"pass-1234-xyz"}})
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
		{"domain alias", "/admin/domains", func(n string) url.Values {
			return url.Values{"domain": {n + ".example"}, "alias_of": {"localhost"}}
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
