package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
)

type fakeACMEStatus struct {
	notAfter time.Time
	issued   bool
	err      error
}

func (f fakeACMEStatus) ACMEExpiry() (time.Time, bool, error) { return f.notAfter, f.issued, f.err }

func TestDashboardACMENotice(t *testing.T) {
	day := 24 * time.Hour
	now := time.Now()
	warnNone := i18n.T("en", "admin.acmeNoCertBanner")
	warnExpiring := strings.SplitN(i18n.T("en", "admin.acmeExpiringBanner"), "%s", 2)[0]
	info := strings.SplitN(i18n.T("en", "admin.acmeExpiryInfo"), "%s", 2)[0]

	tests := []struct {
		name         string
		status       ACMEStatusReader
		wantNone     bool // "no certificate" warning
		wantExpiring bool
		wantInfo     bool
		wantDate     string
	}{
		{"acme off", nil, false, false, false, ""},
		{"no certificate yet", fakeACMEStatus{}, true, false, false, ""},
		{"unreadable record", fakeACMEStatus{err: errors.New("boom")}, true, false, false, ""},
		{"13 days left warns", fakeACMEStatus{notAfter: now.Add(13 * day), issued: true}, false, true, false, now.Add(13 * day).UTC().Format("2006-01-02")},
		{"20 days left shows only the date", fakeACMEStatus{notAfter: now.Add(20 * day), issued: true}, false, false, true, now.Add(20 * day).UTC().Format("2006-01-02")},
		{"expired warns", fakeACMEStatus{notAfter: now.Add(-day), issued: true}, false, true, false, now.Add(-day).UTC().Format("2006-01-02")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newAdminScopeTestEnv(t)
			env.h.SetACMEStatus(tt.status)

			r := withGlobalAdmin(httptest.NewRequest(http.MethodGet, "/admin", nil), env.globalAdminID)
			w := httptest.NewRecorder()
			env.h.Dashboard(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			body := w.Body.String()
			if got := strings.Contains(body, warnNone); got != tt.wantNone {
				t.Errorf("no-certificate warning = %v, want %v", got, tt.wantNone)
			}
			if got := strings.Contains(body, warnExpiring); got != tt.wantExpiring {
				t.Errorf("expiring warning = %v, want %v", got, tt.wantExpiring)
			}
			if got := strings.Contains(body, info); got != tt.wantInfo {
				t.Errorf("expiry info = %v, want %v", got, tt.wantInfo)
			}
			if tt.wantDate != "" && !strings.Contains(body, tt.wantDate) {
				t.Errorf("body lacks the expiry date %s", tt.wantDate)
			}
		})
	}
}

func TestDashboardACMEDomainAdminRedirected(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	env.h.SetACMEStatus(fakeACMEStatus{})

	r := withDomainAdmin1(httptest.NewRequest(http.MethodGet, "/admin", nil), env.domainAdmin1ID)
	w := httptest.NewRecorder()
	env.h.Dashboard(w, r)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "/admin/domains") {
		t.Fatalf("status %d, location %q", w.Code, w.Header().Get("Location"))
	}
	if strings.Contains(w.Body.String(), "ACME") {
		t.Error("a domain admin was shown the certificate notice")
	}
}

func TestACMEStringsInEveryLocale(t *testing.T) {
	keys := []string{"admin.acmeExpiringBanner", "admin.acmeNoCertBanner", "admin.acmeExpiryInfo"}
	diacritics := map[string]string{"de": "ä", "es": "ó", "fr": "é"}
	for _, loc := range []string{"en", "de", "es", "fr"} {
		raw, err := os.ReadFile("../../i18n/messages/" + loc + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		joined := ""
		for _, k := range keys {
			v := m[k]
			if v == "" {
				t.Errorf("%s: %s missing", loc, k)
			}
			joined += v
		}
		if !strings.Contains(m["admin.acmeExpiringBanner"], "%s") || !strings.Contains(m["admin.acmeExpiryInfo"], "%s") {
			t.Errorf("%s: date placeholder missing", loc)
		}
		if d, ok := diacritics[loc]; ok && !strings.Contains(joined, d) {
			t.Errorf("%s: expected diacritics such as %q", loc, d)
		}
		if loc != "en" && m["admin.acmeNoCertBanner"] == "" {
			t.Errorf("%s: no-certificate text missing", loc)
		}
	}
}
