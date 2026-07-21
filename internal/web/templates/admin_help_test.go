package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestAdminDomainDetailRendersDNSStatusLegend verifies the domain Manage page
// includes the DNS status legend (task 8.1) explaining the ok/fail/neutral/error
// check states, plus the DKIM publish reminder. It asserts on the static CSS
// classes so it is independent of the active locale.
func TestAdminDomainDetailRendersDNSStatusLegend(t *testing.T) {
	d := DomainInfo{
		Name:    "example.com",
		Enabled: true,
		DNSRows: []DNSCheckRow{
			{Label: "MX", Status: "ok"},
			{Label: "SPF", Status: "fail"},
		},
	}

	var buf bytes.Buffer
	if err := AdminDomainDetail(d, "admin@example.com", "", true).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render AdminDomainDetail: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, `class="dns-legend`) {
		t.Fatal("domain detail page is missing the DNS status legend (task 8.1)")
	}
	// The legend must explain all four states via their status classes.
	for _, cls := range []string{"dns-check", "dns-fail", "dns-neutral", "dns-error"} {
		if !strings.Contains(html, cls) {
			t.Errorf("DNS status legend missing state class %q", cls)
		}
	}
	// The DKIM section must carry the publish reminder key.
	if !strings.Contains(html, "admin.dkimPublishNote") && !strings.Contains(strings.ToLower(html), "publish") {
		t.Error("DKIM section missing the publish/rotate reminder (task 8.1)")
	}
}
