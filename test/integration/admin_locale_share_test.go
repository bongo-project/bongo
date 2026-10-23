//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
)

// TestAdminGroupsInjectLocale drives the real router: the admin and quarantine
// groups must render in the user's locale, with English as the control.
func TestAdminGroupsInjectLocale(t *testing.T) {
	baseURL, cancel := startAdminTestAgent(t)
	defer cancel()
	client := loginAs(t, baseURL, "admin@localhost", "admin")

	get := func(path string) string {
		t.Helper()
		resp, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, resp.StatusCode)
		}
		return string(body)
	}

	pages := []struct{ path, en, fr string }{
		{"/admin", "Admin Dashboard", "Tableau de bord admin"},
		{"/admin/quarantine", "Quarantine", "Quarantaine"},
	}

	// English control: default locale.
	for _, p := range pages {
		if body := get(p.path); !strings.Contains(body, p.en) || strings.Contains(body, p.fr) {
			t.Errorf("%s (default locale): want %q and not %q", p.path, p.en, p.fr)
		}
	}

	setLocaleCookie(t, client, baseURL, "fr")
	for _, p := range pages {
		if body := get(p.path); !strings.Contains(body, p.fr) {
			t.Errorf("%s (fr): missing %q", p.path, p.fr)
		}
	}
}

// TestShareCalendarThroughRouter checks the resolver is wired into the real
// route: an unknown address is refused, an existing user is stored by ID.
func TestShareCalendarThroughRouter(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	a, cancel, _ := sharedStartAgent(t, cfg)
	defer cancel()
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port)

	bobID, err := auth.CreateUser(a, a.Metadata, "bob@localhost", "Bob", "bobpass123", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	adminID := resolveAdminUserID(t, a)
	calID, err := a.Calendars.EnsureDefault(adminID)
	if err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}

	client := loginAs(t, baseURL, "admin@localhost", "admin")
	share := func(target string) int {
		resp, err := client.PostForm(baseURL+"/calendar/"+calID+"/share", url.Values{"share_with": {target}})
		if err != nil {
			t.Fatalf("POST share: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := share("nobody@localhost"); c != http.StatusBadRequest {
		t.Errorf("unknown target: status %d, want 400", c)
	}
	if c := share("bob@localhost"); c != http.StatusOK {
		t.Errorf("existing target: status %d, want 200", c)
	}
	cal, err := a.Calendars.Get(adminID, calID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.SharedWith) != 1 || cal.SharedWith[0] != bobID {
		t.Errorf("SharedWith = %v, want [%s]", cal.SharedWith, bobID)
	}

	// Unshare goes through the same router: mutating route, 200 with the
	// refresh trigger, entry gone; a second call is a no-op 200.
	unshare := func(target string) *http.Response {
		resp, err := client.PostForm(baseURL+"/calendar/"+calID+"/unshare", url.Values{"share_with": {target}})
		if err != nil {
			t.Fatalf("POST unshare: %v", err)
		}
		resp.Body.Close()
		return resp
	}
	resp := unshare(bobID)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("HX-Trigger") != "calendarRefresh" {
		t.Errorf("unshare: status %d, HX-Trigger %q; want 200 and calendarRefresh", resp.StatusCode, resp.Header.Get("HX-Trigger"))
	}
	cal, err = a.Calendars.Get(adminID, calID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.SharedWith) != 0 {
		t.Errorf("SharedWith after unshare = %v, want empty", cal.SharedWith)
	}
	if resp := unshare(bobID); resp.StatusCode != http.StatusOK {
		t.Errorf("repeat unshare: status %d, want 200", resp.StatusCode)
	}
	// A client with no session cannot unshare: re-share, try, entry stays.
	if c := share("bob@localhost"); c != http.StatusOK {
		t.Fatalf("re-share: status %d, want 200", c)
	}
	bare := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := bare.PostForm(baseURL+"/calendar/"+calID+"/unshare", url.Values{"share_with": {bobID}})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode == http.StatusOK {
		t.Errorf("sessionless unshare: status 200, want a refusal")
	}
	if cal, err = a.Calendars.Get(adminID, calID); err != nil || len(cal.SharedWith) != 1 {
		t.Errorf("SharedWith after sessionless unshare = %v, %v; want bob kept", cal.SharedWith, err)
	}
}

// TestShareCalendarRateLimitThroughRouter checks the per-user share limiter is
// installed on the real route: the 21st request is refused with 429 even for a
// valid live account, and nothing is stored.
func TestShareCalendarRateLimitThroughRouter(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	a, cancel, _ := sharedStartAgent(t, cfg)
	defer cancel()
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port)

	if _, err := auth.CreateUser(a, a.Metadata, "bob@localhost", "Bob", "bobpass123", false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	adminID := resolveAdminUserID(t, a)
	calID, err := a.Calendars.EnsureDefault(adminID)
	if err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	client := loginAs(t, baseURL, "admin@localhost", "admin")
	post := func(target string) *http.Response {
		resp, err := client.PostForm(baseURL+"/calendar/"+calID+"/share", url.Values{"share_with": {target}})
		if err != nil {
			t.Fatalf("POST share: %v", err)
		}
		resp.Body.Close()
		return resp
	}
	for i := range 20 {
		if c := post("nobody@localhost").StatusCode; c != http.StatusBadRequest {
			t.Fatalf("request %d: status %d, want 400", i+1, c)
		}
	}
	resp := post("bob@localhost")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("21st request: status %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}
	cal, err := a.Calendars.Get(adminID, calID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cal.SharedWith) != 0 {
		t.Errorf("SharedWith = %v, want empty", cal.SharedWith)
	}
}
