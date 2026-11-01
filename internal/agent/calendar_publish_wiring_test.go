package agent

import (
	"encoding/base64"
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

// The published feed on the real router: reachable without a session, no
// cookie on any outcome, GET only, and every settings mutation behind CSRF.
// Deleting the route line or one of the four mutation lines in agent.go turns
// this red while the handler tests stay green.
func TestPublishedCalendarFeedWiredIntoAgent(t *testing.T) {
	cfg := peerRunConfig(t, "127.0.0.1", "127.0.0.1")
	a, stop := runAgentUntilReady(t, cfg)
	defer stop()

	userID, err := auth.CreateUser(a, a.Metadata, "pub@localhost", "Pub", "pass-1234-xyz", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := a.Calendars.EnsureDefault(userID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Events.Create(userID, &bongov1.Event{Guid: "ev1", CalendarId: "default", Title: "FeedTitle", StartTime: 1792058400, EndTime: 1792062000}); err != nil {
		t.Fatal(err)
	}

	jar, _ := cookiejar.New(nil)
	session := &http.Client{
		Jar:           jar,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	anon := &http.Client{Timeout: 15 * time.Second, CheckRedirect: session.CheckRedirect}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)

	login, err := session.PostForm(base+"/login", url.Values{"email": {"pub@localhost"}, "password": {"pass-1234-xyz"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()
	page, err := session.Get(base + "/calendar/settings")
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	m := csrfHeaderRe.FindSubmatch(pageBody)
	if page.StatusCode != http.StatusOK || m == nil {
		t.Fatalf("settings page status %d, token found: %v", page.StatusCode, m != nil)
	}
	csrf := string(m[1])

	post := func(path string, form url.Values, withCSRF bool) (int, string) {
		if withCSRF {
			form.Set("_csrf", csrf)
		}
		req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := session.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Every mutation is refused without the CSRF token, and publishes nothing.
	for _, path := range []string{
		"/calendar/default/publish",
		"/calendar/default/publish/regenerate",
		"/calendar/default/publish/password",
		"/calendar/default/unpublish",
	} {
		code, body := post(path, url.Values{"detail": {"full"}, "password": {"hunter2-hunter2"}}, false)
		if code != http.StatusForbidden || !strings.Contains(body, csrfBodyText) {
			t.Errorf("%s without CSRF: %d %q, want 403 %q", path, code, body, csrfBodyText)
		}
	}
	if c, _ := a.Calendars.Get(userID, "default"); c.Published {
		t.Fatal("a refused request published the calendar")
	}

	if code, body := post("/calendar/default/publish", url.Values{"detail": {"full"}}, true); code != http.StatusOK {
		t.Fatalf("publish: %d %s", code, body)
	}
	cal, _ := a.Calendars.Get(userID, "default")
	feedURL := func(token []byte) string {
		return base + "/pub/calendar/" + userID + "/default/" + base64.RawURLEncoding.EncodeToString(token) + ".ics"
	}

	// The settings page shows both links for the owner.
	page, _ = session.Get(base + "/calendar/settings")
	pageBody, _ = io.ReadAll(page.Body)
	_ = page.Body.Close()
	tok := base64.RawURLEncoding.EncodeToString(cal.PublishToken)
	for _, want := range []string{"https://127.0.0.1:" + strconv.Itoa(cfg.Web.Port) + "/pub/calendar/" + userID + "/default/" + tok + ".ics", "webcal://127.0.0.1:"} {
		if !strings.Contains(string(pageBody), want) {
			t.Errorf("settings page lacks %q", want)
		}
	}

	do := func(method, u string, hdr map[string]string) (*http.Response, string) {
		req, _ := http.NewRequest(method, u, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := anon.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, u, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	noCookie := func(name string, resp *http.Response) {
		t.Helper()
		if c := resp.Header.Values("Set-Cookie"); len(c) != 0 {
			t.Errorf("%s set a cookie: %v", name, c)
		}
	}

	resp, body := do(http.MethodGet, feedURL(cal.PublishToken), nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "FeedTitle") {
		t.Fatalf("feed: %d %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, max-age=300" {
		t.Errorf("Cache-Control = %q (SecurityHeaders must not override the feed's)", got)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/calendar") || resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("Content-Type %q, Content-Encoding %q", resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding"))
	}
	noCookie("200", resp)
	etag := resp.Header.Get("ETag")
	if r304, _ := do(http.MethodGet, feedURL(cal.PublishToken), map[string]string{"If-None-Match": etag}); r304.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match = %d", r304.StatusCode)
	}

	// A request that carries the owner's session cookie is still served
	// without it being read or refreshed.
	req, _ := http.NewRequest(http.MethodGet, feedURL(cal.PublishToken), nil)
	for _, c := range jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	sresp, err := anon.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = sresp.Body.Close()
	if sresp.StatusCode != http.StatusOK {
		t.Errorf("with a session cookie: %d", sresp.StatusCode)
	}
	noCookie("200 with session", sresp)

	bad := make([]byte, 32)
	r404, b404 := do(http.MethodGet, feedURL(bad), nil)
	if r404.StatusCode != http.StatusNotFound {
		t.Errorf("wrong token = %d", r404.StatusCode)
	}
	noCookie("404", r404)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		r, _ := do(method, feedURL(cal.PublishToken), nil)
		if r.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", method, r.StatusCode)
		}
		noCookie(method, r)
	}

	// Password: 401 with a challenge and no cookie.
	if code, body := post("/calendar/default/publish/password", url.Values{"password": {"hunter2-hunter2"}}, true); code != http.StatusOK {
		t.Fatalf("set password: %d %s", code, body)
	}
	r401, _ := do(http.MethodGet, feedURL(cal.PublishToken), nil)
	if r401.StatusCode != http.StatusUnauthorized || r401.Header.Get("WWW-Authenticate") == "" {
		t.Errorf("password feed without credentials: %d %v", r401.StatusCode, r401.Header)
	}
	noCookie("401", r401)

	// Regenerate: the old link dies at once with the same 404 as any other.
	if code, body := post("/calendar/default/publish/regenerate", url.Values{}, true); code != http.StatusOK {
		t.Fatalf("regenerate: %d %s", code, body)
	}
	rOld, bOld := do(http.MethodGet, feedURL(cal.PublishToken), nil)
	if rOld.StatusCode != http.StatusNotFound || bOld != b404 {
		t.Errorf("old link after regenerate: %d %q, want the uniform 404 %q", rOld.StatusCode, bOld, b404)
	}
	newCal, _ := a.Calendars.Get(userID, "default")
	if code, _ := post("/calendar/default/unpublish", url.Values{}, true); code != http.StatusOK {
		t.Fatalf("unpublish: %d", code)
	}
	rGone, bGone := do(http.MethodGet, feedURL(newCal.PublishToken), nil)
	if rGone.StatusCode != http.StatusNotFound || bGone != b404 {
		t.Errorf("link after unpublish: %d %q", rGone.StatusCode, bGone)
	}
}
