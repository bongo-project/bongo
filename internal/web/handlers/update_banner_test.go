package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/updatecheck"
	"github.com/bongo-project/bongo/internal/version"
)

func setRunningVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	version.Version = v
}

func seedUpdateRecord(t *testing.T, env *adminScopeTestEnv, rec updatecheck.Record) {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.ps.Set([]byte(updatecheck.KeyResult), b); err != nil {
		t.Fatal(err)
	}
}

func updateStatusFor(env *adminScopeTestEnv, enabled bool) *UpdateStatus {
	u, _ := url.Parse("https://update.example.test/v1/check")
	return &UpdateStatus{Enabled: enabled, CheckURL: u, Store: env.ps}
}

func TestDashboardUpdateBanner(t *testing.T) {
	security := i18n.T("en", "admin.updateSecurity")
	plain := i18n.T("en", "admin.updateAvailable")
	notes := i18n.T("en", "admin.updateNotes")
	const goodURL = "https://update.example.test/releases/0.18.0"
	tests := []struct {
		name     string
		running  string
		rec      updatecheck.Record
		enabled  bool
		domain   bool
		wantPlan string // "", "plain" or "security"
	}{
		{"newer", "v0.17.0", updatecheck.Record{Latest: "0.18.0", ReleaseNotesURL: goodURL}, true, false, "plain"},
		{"security", "v0.17.0", updatecheck.Record{Latest: "0.17.1", ReleaseNotesURL: goodURL, Security: true}, true, false, "security"},
		{"same version", "v0.18.0", updatecheck.Record{Latest: "0.18.0", ReleaseNotesURL: goodURL}, true, false, ""},
		{"older latest", "v0.19.0", updatecheck.Record{Latest: "0.18.0", ReleaseNotesURL: goodURL}, true, false, ""},
		{"dev build", "dev", updatecheck.Record{Latest: "0.18.0", ReleaseNotesURL: goodURL}, true, false, ""},
		{"check disabled", "v0.17.0", updatecheck.Record{Latest: "0.18.0", ReleaseNotesURL: goodURL}, false, false, ""},
		{"other host stored", "v0.17.0", updatecheck.Record{Latest: "0.18.0", ReleaseNotesURL: "https://evil.example/x"}, true, false, ""},
		{"markup in latest", "v0.17.0", updatecheck.Record{Latest: "9.0.0-<script>alert(1)</script>", ReleaseNotesURL: goodURL}, true, false, ""},
		{"markup in URL", "v0.17.0", updatecheck.Record{Latest: "9.0.0", ReleaseNotesURL: `https://update.example.test/"><script>alert(1)</script>`}, true, false, ""},
		{"domain admin redirected", "v0.17.0", updatecheck.Record{Latest: "0.18.0", ReleaseNotesURL: goodURL}, true, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRunningVersion(t, tt.running)
			env := newAdminScopeTestEnv(t)
			seedUpdateRecord(t, env, tt.rec)
			env.h.SetUpdateCheck(updateStatusFor(env, tt.enabled))

			r := httptest.NewRequest(http.MethodGet, "/admin", nil)
			if tt.domain {
				r = withDomainAdmin1(r, env.domainAdmin1ID)
			} else {
				r = withGlobalAdmin(r, env.globalAdminID)
			}
			w := httptest.NewRecorder()
			env.h.Dashboard(w, r)
			body := w.Body.String()

			if tt.domain {
				if w.Code != http.StatusSeeOther || strings.Contains(body, notes) {
					t.Fatalf("domain admin: status %d", w.Code)
				}
				return
			}
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			if strings.Contains(body, "<script>alert(1)") {
				t.Fatal("unescaped markup in the page")
			}
			gotBanner := strings.Contains(body, `class="update-banner"`)
			switch tt.wantPlan {
			case "":
				if gotBanner {
					t.Fatal("banner shown")
				}
			case "plain":
				if !gotBanner || !strings.Contains(body, plain) || strings.Contains(body, security) ||
					!strings.Contains(body, `href="`+goodURL+`"`) || !strings.Contains(body, notes) || !strings.Contains(body, "flash-warning") {
					t.Fatalf("plain banner wrong:\n%s", between(body, "update-banner"))
				}
				if banner := between(body, "update-banner"); !strings.Contains(banner, `role="status"`) ||
					strings.Contains(banner, "aria-live") || strings.Contains(banner, `role="alert"`) {
					t.Fatalf("plain banner must be role=status with no aria-live:\n%s", banner)
				}
			case "security":
				if !gotBanner || !strings.Contains(body, security) || !strings.Contains(body, "flash-error") ||
					!strings.Contains(body, `href="`+goodURL+`"`) {
					t.Fatalf("security banner wrong:\n%s", between(body, "update-banner"))
				}
				if banner := between(body, "update-banner"); !strings.Contains(banner, `role="alert"`) || strings.Contains(banner, "aria-live") {
					t.Fatalf("security banner must be role=alert with no aria-live:\n%s", banner)
				}
			}
		})
	}
}

func between(s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return "(no banner)"
	}
	return s[i:min(len(s), i+600)]
}

func TestVersionEndpointUpdateStatus(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	seedUpdateRecord(t, env, updatecheck.Record{CheckedAt: 1796083200, Result: updatecheck.ResultOK, Latest: "0.18.0", ReleaseNotesURL: "https://update.example.test/r"})
	h := NewVersionHandler(updateStatusFor(env, true))

	get := func(r *http.Request) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		var m map[string]any
		if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	global := get(withGlobalAdmin(httptest.NewRequest(http.MethodGet, "/admin/version", nil), env.globalAdminID))
	uc, ok := global["update_check"].(map[string]any)
	if !ok {
		t.Fatalf("global admin got no update_check: %v", global)
	}
	if uc["enabled"] != true || uc["result"] != "ok" || uc["latest"] != "0.18.0" || uc["last_checked_at"] != "2026-12-01T00:00:00Z" {
		t.Errorf("update_check = %v", uc)
	}
	if global["version"] == nil || global["commit"] == nil {
		t.Errorf("build fields missing: %v", global)
	}

	domain := get(withDomainAdmin1(httptest.NewRequest(http.MethodGet, "/admin/version", nil), env.domainAdmin1ID))
	if _, has := domain["update_check"]; has {
		t.Errorf("domain admin sees update_check: %v", domain)
	}

	// A global admin before any check ran: enabled, no time, no result.
	empty := NewVersionHandler(&UpdateStatus{Enabled: true, Store: newAdminScopeTestEnv(t).ps})
	w := httptest.NewRecorder()
	empty.ServeHTTP(w, withGlobalAdmin(httptest.NewRequest(http.MethodGet, "/admin/version", nil), env.globalAdminID))
	if !strings.Contains(w.Body.String(), `"last_checked_at":null`) {
		t.Errorf("body = %s", w.Body.String())
	}
}
