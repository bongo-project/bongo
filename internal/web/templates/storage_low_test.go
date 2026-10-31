package templates

import (
	"strings"
	"testing"
)

func TestAdminDashboardStorageLowBanner(t *testing.T) {
	banner := translated("admin.storageLowBanner")
	tests := []struct {
		name string
		low  bool
		want bool
	}{
		{"low shows banner", true, true},
		{"ok hides banner", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := renderAdmin(t, AdminDashboard(SystemHealth{StorageLow: tt.low}, "a@example.com", false, ""))
			if got := strings.Contains(html, banner); got != tt.want {
				t.Errorf("banner present = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStorageLowPageUsesOwnTitle(t *testing.T) {
	html := renderAdmin(t, StorageLowPage("msg here", "a@example.com"))
	if !strings.Contains(html, translated("storage.lowTitle")) {
		t.Error("page does not carry the storage title")
	}
	if strings.Contains(html, translated("quota.title")) {
		t.Error("page must not reuse the quota title")
	}
}

// FlashMessage already carries role="alert"; a wrapper with its own role would
// nest two alert regions and the banner could be announced twice.
func TestAdminDashboardStorageLowBannerSingleAlert(t *testing.T) {
	html := renderAdmin(t, AdminDashboard(SystemHealth{StorageLow: true}, "a@example.com", false, ""))
	if n := strings.Count(html, `role="alert"`); n != 1 {
		t.Errorf("role=\"alert\" appears %d times, want 1", n)
	}
}
