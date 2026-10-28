package templates

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func renderAdmin(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func kpiLabels(kpis []AdminKPI) []string {
	var out []string
	for _, k := range kpis {
		out = append(out, k.Label)
	}
	return out
}

func findKPI(kpis []AdminKPI, label string) (AdminKPI, bool) {
	for _, k := range kpis {
		if k.Label == label {
			return k, true
		}
	}
	return AdminKPI{}, false
}

func TestDashboardKPIsAbsentValuesAreOmitted(t *testing.T) {
	ctx := context.Background()
	users, storage := translated("admin.users"), translated("admin.storage")
	uptime, nodes := translated("admin.uptime"), translated("admin.kpiNodes")

	tests := []struct {
		name        string
		health      SystemHealth
		wantPresent []string
		wantAbsent  []string
	}{
		{"nothing known", SystemHealth{}, []string{uptime}, []string{users, storage, nodes}},
		{"users known zero", SystemHealth{UserCountKnown: true}, []string{users, uptime}, []string{storage}},
		{"storage known zero", SystemHealth{StorageKnown: true}, []string{storage, uptime}, []string{users}},
		{"everything", SystemHealth{UserCountKnown: true, StorageKnown: true, NodeCount: 3, Uptime: time.Hour}, []string{users, storage, uptime, nodes}, nil},
		{"users count without known flag", SystemHealth{UserCount: 7}, nil, []string{users}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kpis := dashboardKPIs(ctx, tc.health)
			for _, l := range tc.wantPresent {
				if _, ok := findKPI(kpis, l); !ok {
					t.Errorf("missing %q in %v", l, kpiLabels(kpis))
				}
			}
			for _, l := range tc.wantAbsent {
				if _, ok := findKPI(kpis, l); ok {
					t.Errorf("%q must be omitted, got %v", l, kpiLabels(kpis))
				}
			}
		})
	}
}

func TestDashboardKPIValuesAndNoInventedDelta(t *testing.T) {
	h := SystemHealth{UserCountKnown: true, UserCount: 0, StorageKnown: true, PebbleSize: 1024, BlobSize: 2048, NodeCount: 3, Uptime: 90 * time.Minute, MessageRate: 99}
	kpis := dashboardKPIs(context.Background(), h)
	if u, _ := findKPI(kpis, translated("admin.users")); u.Value != "0" {
		t.Errorf("known zero users must show 0, got %q", u.Value)
	}
	if s, _ := findKPI(kpis, translated("admin.storage")); s.Value != formatBytes(3072) {
		t.Errorf("storage value %q, want %q", s.Value, formatBytes(3072))
	}
	if n, _ := findKPI(kpis, translated("admin.kpiNodes")); n.Value != "3" {
		t.Errorf("nodes value %q", n.Value)
	}
	for _, k := range kpis {
		if k.Delta != "" || strings.Contains(k.Value, "99") {
			t.Errorf("no previous value or message rate exists, got %+v", k)
		}
	}
}

func TestKPIDeltaClassFollowsSign(t *testing.T) {
	for sign, want := range map[int]string{1: "", 0: "", 5: "", -1: "down", -9: "down"} {
		if got := kpiDeltaClass(sign); got != want {
			t.Errorf("kpiDeltaClass(%d) = %q, want %q", sign, got, want)
		}
	}
}

var deltaDiv = regexp.MustCompile(`<div class="d( down)?">([^<]*)</div>`)

func TestKPIRowRendering(t *testing.T) {
	html := renderAdmin(t, AdminKPIRow([]AdminKPI{
		{Label: "Up", Value: "10", Delta: "+4%", DeltaSign: 1},
		{Label: "Down", Value: "20", Delta: "-3%", DeltaSign: -1},
		{Label: "Plain", Value: "30"},
	}))
	m := deltaDiv.FindAllStringSubmatch(html, -1)
	if len(m) != 2 {
		t.Fatalf("want exactly 2 delta elements (empty Delta renders none), got %d in %s", len(m), html)
	}
	if m[0][1] != "" || m[0][2] != "+4%" {
		t.Errorf("positive delta: %q", m[0][0])
	}
	if m[1][1] != " down" || m[1][2] != "-3%" {
		t.Errorf("negative delta: %q", m[1][0])
	}
	for _, want := range []string{`class="kpi-row"`, `class="l">Up<`, `class="v">30<`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	if renderAdmin(t, AdminKPIRow(nil)) != "" {
		t.Error("no KPIs must render no row")
	}
}

func TestAdminDashboardRendersOnlySuppliedKPIs(t *testing.T) {
	render := func(h SystemHealth) string {
		return renderAdmin(t, AdminDashboard(h, "a@example.com", false, ""))
	}
	users := `class="l">` + translated("admin.users") + `<`
	if strings.Contains(render(SystemHealth{}), users) {
		t.Error("Users card shown although the count is unknown")
	}
	got := render(SystemHealth{UserCountKnown: true, UserCount: 0})
	if !strings.Contains(got, users) || !strings.Contains(got, `class="v">0<`) {
		t.Error("known zero users must render a 0 card")
	}
	if deltaDiv.MatchString(got) {
		t.Error("dashboard must not render a delta")
	}
}

func TestQueueKPIs(t *testing.T) {
	stats := &bongov1.QueueStats{PendingCount: 4, FailedCount: 0, OldestPendingSeconds: 120}
	kpis := queueKPIs(context.Background(), stats)
	if len(kpis) != 3 {
		t.Fatalf("want 3 KPIs, got %d", len(kpis))
	}
	if kpis[0].Value != "4" || kpis[1].Value != "0" || kpis[2].Value != formatSeconds(120) {
		t.Errorf("values %+v", kpis)
	}
	html := renderAdmin(t, AdminQueueDashboard(stats, nil, nil, "a@example.com"))
	if strings.Count(html, `class="kpi"`) != 3 {
		t.Errorf("queue page must render three KPI cards")
	}
}

func TestKPICardsHaveAccessibleName(t *testing.T) {
	html := renderAdmin(t, AdminKPIRow([]AdminKPI{
		{Label: "Users", Value: "4"},
		{Label: "Up & away", Value: "9", Delta: "+1%", DeltaSign: 1},
	}))
	if got := strings.Count(html, `<div class="kpi" role="group" aria-label="`); got != 2 {
		t.Fatalf("want 2 named KPI groups, got %d in %s", got, html)
	}
	for _, want := range []string{`aria-label="Users"`, `aria-label="Up &amp; away"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in %s", want, html)
		}
	}
}
