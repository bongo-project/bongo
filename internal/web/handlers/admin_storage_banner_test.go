package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
)

func TestDashboardStorageLowBanner(t *testing.T) {
	banner := i18n.T("en", "admin.storageLowBanner")
	tests := []struct {
		name string
		gate storageGate
		want bool
	}{
		{"no gate", nil, false},
		{"gate ok", &fakeStorageGate{low: false}, false},
		{"gate low", &fakeStorageGate{low: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newAdminScopeTestEnv(t)
			env.h.SetStorageGate(tt.gate)

			r := httptest.NewRequest(http.MethodGet, "/admin", nil)
			r = withGlobalAdmin(r, env.globalAdminID)
			w := httptest.NewRecorder()
			env.h.Dashboard(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			if got := strings.Contains(w.Body.String(), banner); got != tt.want {
				t.Errorf("banner present = %v, want %v", got, tt.want)
			}
		})
	}
}
