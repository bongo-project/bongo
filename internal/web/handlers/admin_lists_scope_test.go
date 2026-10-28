package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// TestAdminScreens_DomainAdminScoping drives the real handlers with two
// domains and an admin of the first: the infrastructure screens (and so the
// KPI rows) are closed to them and the lists carry only their domain's rows.
func TestAdminScreens_DomainAdminScoping(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	seedAlias(t, env.applier, "info@"+testDomain1, "user1@"+testDomain1)
	seedAlias(t, env.applier, "info@"+testDomain2, "user2@"+testDomain2)

	do := func(fn func(http.ResponseWriter, *http.Request), path string, global bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if global {
			r = withGlobalAdmin(r, env.globalAdminID)
		} else {
			r = withDomainAdmin1(r, env.domainAdmin1ID)
		}
		w := httptest.NewRecorder()
		fn(w, r)
		return w
	}

	t.Run("dashboard redirects with no KPI row", func(t *testing.T) {
		w := do(env.h.Dashboard, "/admin", false)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/domains" {
			t.Fatalf("status %d location %q, want 303 /admin/domains", w.Code, w.Header().Get("Location"))
		}
		if strings.Contains(w.Body.String(), "kpi-row") {
			t.Error("redirect body must not render a KPI row")
		}
	})
	t.Run("queue and cluster forbidden", func(t *testing.T) {
		for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
			"/admin/queue":   env.h.QueueDashboard,
			"/admin/cluster": env.h.ClusterOverview,
		} {
			if w := do(fn, name, false); w.Code != http.StatusForbidden {
				t.Errorf("%s status %d, want 403", name, w.Code)
			}
		}
	})
	t.Run("lists show only own domain", func(t *testing.T) {
		cases := []struct {
			path string
			fn   func(http.ResponseWriter, *http.Request)
			own  string
			dflt string
		}{
			{"/admin/users", env.h.ListUsers, "user1@" + testDomain1, "user2@" + testDomain2},
			{"/admin/domains", env.h.ListDomains, testDomain1, testDomain2},
			{"/admin/aliases", env.h.ListAliases, "info@" + testDomain1, "info@" + testDomain2},
		}
		for _, c := range cases {
			// Control: a global admin sees both, so the absence below means something.
			g := do(c.fn, c.path, true).Body.String()
			if !strings.Contains(g, c.own) || !strings.Contains(g, c.dflt) {
				t.Fatalf("%s control: global admin must see both %q and %q", c.path, c.own, c.dflt)
			}
			w := do(c.fn, c.path, false)
			if w.Code != http.StatusOK {
				t.Fatalf("%s status %d", c.path, w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, c.own) {
				t.Errorf("%s missing own row %q", c.path, c.own)
			}
			if strings.Contains(body, c.dflt) {
				t.Errorf("%s leaks %q", c.path, c.dflt)
			}
		}
	})
}

// TestDashboard_KPIFlagsFollowLoadedValues drives the real Dashboard handler
// and counts the KPI cards: Users and Storage appear only when their value
// was actually loaded; Uptime and Nodes are always present.
func TestDashboard_KPIFlagsFollowLoadedValues(t *testing.T) {
	cards := func(t *testing.T, h *AdminHandler, userID string) int {
		t.Helper()
		r := withGlobalAdmin(httptest.NewRequest(http.MethodGet, "/admin", nil), userID)
		w := httptest.NewRecorder()
		h.Dashboard(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		return strings.Count(w.Body.String(), `class="kpi"`)
	}

	t.Run("everything loaded", func(t *testing.T) {
		env := newAdminScopeTestEnv(t)
		if n := cards(t, env.h, env.globalAdminID); n != 4 {
			t.Errorf("cards = %d, want 4 (users, storage, uptime, nodes)", n)
		}
	})
	t.Run("no metadata hides users", func(t *testing.T) {
		env := newAdminScopeTestEnv(t)
		h := NewAdminHandler(env.ps, nil, nil, env.applier, discardLogger())
		if n := cards(t, h, env.globalAdminID); n != 3 {
			t.Errorf("cards = %d, want 3 (storage, uptime, nodes)", n)
		}
	})
	t.Run("failing count hides users", func(t *testing.T) {
		env := newAdminScopeTestEnv(t)
		if err := env.md.System.DB().Close(); err != nil {
			t.Fatal(err)
		}
		if n := cards(t, env.h, env.globalAdminID); n != 3 {
			t.Errorf("cards = %d, want 3 (storage, uptime, nodes)", n)
		}
	})
	t.Run("no store or blobs hides storage", func(t *testing.T) {
		env := newAdminScopeTestEnv(t)
		h := NewAdminHandler(nil, nil, env.md, env.applier, discardLogger())
		if n := cards(t, h, env.globalAdminID); n != 3 {
			t.Errorf("cards = %d, want 3 (users, uptime, nodes)", n)
		}
	})
	t.Run("blobs alone show storage", func(t *testing.T) {
		env := newAdminScopeTestEnv(t)
		bs, err := store.NewBlobStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		h := NewAdminHandler(nil, bs, env.md, env.applier, discardLogger())
		if n := cards(t, h, env.globalAdminID); n != 4 {
			t.Errorf("cards = %d, want 4", n)
		}
	})
}
