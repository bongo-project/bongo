package templates

import (
	"regexp"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestStatusPillClass(t *testing.T) {
	tests := []struct{ kind, status, want string }{
		{"user", "active", "ok"}, {"user", "disabled", "neutral"},
		{"domain", "enabled", "ok"}, {"domain", "disabled", "neutral"},
		{"mx", "valid", "ok"}, {"mx", "missing", "err"},
		{"dns", "ok", "ok"}, {"dns", "fail", "err"}, {"dns", "neutral", "neutral"}, {"dns", "error", "warn"}, {"dns", "", "err"},
		{"node", "alive", "ok"}, {"node", "Alive", "ok"}, {"node", "suspect", "warn"}, {"node", "dead", "err"}, {"node", "", "err"},
		{"raft", "leader", "ok"}, {"raft", "Follower", "info"}, {"raft", "candidate", "warn"}, {"raft", "unknown", "neutral"}, {"raft", "", "neutral"},
		{"queue", "pending", "info"}, {"queue", "bounced", "err"},
		{"address", "sendable", "ok"}, {"address", "unverified", "warn"}, {"address", "primary", "info"},
		{"nonsense", "active", "neutral"}, {"user", "nonsense", "neutral"},
	}
	for _, tc := range tests {
		if got := statusPillClass(tc.kind, tc.status); got != tc.want {
			t.Errorf("statusPillClass(%q,%q) = %q, want %q", tc.kind, tc.status, got, tc.want)
		}
	}
}

func pillClasses(html string) []string {
	re := regexp.MustCompile(`<span class="(status-pill [^"]*)"><span class="dot"></span>([^<]*)</span>`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(html, -1) {
		out = append(out, m[1]+"|"+strings.TrimSpace(m[2]))
	}
	return out
}

func wantPill(t *testing.T, html, class, label string) {
	t.Helper()
	want := class + "|" + label
	for _, p := range pillClasses(html) {
		if p == want {
			return
		}
	}
	t.Errorf("no pill %q in %v", want, pillClasses(html))
}

func TestAdminTablesRenderPillsWithClass(t *testing.T) {
	t.Run("users", func(t *testing.T) {
		html := renderAdmin(t, AdminUserList([]AdminUserInfo{
			{UserID: "1", Email: "a@x.test"}, {UserID: "2", Email: "b@x.test", Disabled: true},
		}, "", "admin@x.test", ""))
		wantPill(t, html, "status-pill ok", translated("common.active"))
		wantPill(t, html, "status-pill neutral", translated("common.disabled"))
		if !strings.Contains(html, "badge-user") {
			t.Error("role badges stay badges")
		}
	})
	t.Run("domains", func(t *testing.T) {
		html := renderAdmin(t, AdminDomainList([]DomainInfo{
			{Name: "a.test", Enabled: true, HasMX: true}, {Name: "b.test"},
		}, "admin@x.test", "", true))
		wantPill(t, html, "status-pill ok", translated("common.enabled"))
		wantPill(t, html, "status-pill neutral", translated("common.disabled"))
		wantPill(t, html, "status-pill ok dns-check", translated("admin.valid"))
		wantPill(t, html, "status-pill err dns-fail", translated("admin.missing"))
	})
	t.Run("dns rows keep legacy classes", func(t *testing.T) {
		html := renderAdmin(t, AdminDomainDetail(DomainInfo{Name: "a.test", DNSRows: []DNSCheckRow{
			{Label: "MX", Status: "ok"}, {Label: "SPF", Status: "fail"}, {Label: "DKIM", Status: "neutral"}, {Label: "DMARC", Status: "error"},
		}}, "admin@x.test", "", true))
		wantPill(t, html, "status-pill ok dns-check", translated("admin.valid"))
		wantPill(t, html, "status-pill err dns-fail", translated("admin.missing"))
		wantPill(t, html, "status-pill neutral dns-neutral", translated("admin.dnsNotGenerated"))
		wantPill(t, html, "status-pill warn dns-error", translated("admin.dnsCheckError"))
	})
	t.Run("cluster", func(t *testing.T) {
		html := renderAdmin(t, AdminClusterOverview([]NodeStatus{
			{Name: "n1", Role: "leader", Status: "alive"}, {Name: "n2", Role: "follower", Status: "suspect"}, {Name: "n3", Role: "x", Status: "dead"},
		}, "leader", "admin@x.test"))
		wantPill(t, html, "status-pill ok", "alive")
		wantPill(t, html, "status-pill warn", "suspect")
		wantPill(t, html, "status-pill err", "dead")
		wantPill(t, html, "status-pill neutral", "follower")
	})
	t.Run("queue", func(t *testing.T) {
		html := renderAdmin(t, AdminQueueDashboard(&bongov1.QueueStats{},
			[]*bongov1.OutboundMessage{{Id: "p1", SenderEmail: "s@x.test"}},
			[]*bongov1.OutboundMessage{{Id: "b1", SenderEmail: "s@x.test"}}, "admin@x.test"))
		wantPill(t, html, "status-pill info", translated("admin.pending"))
		wantPill(t, html, "status-pill err", translated("admin.failed"))
	})
	t.Run("addresses", func(t *testing.T) {
		html := renderAdmin(t, AdminUserForm(&AdminUserInfo{UserID: "1", Email: "a@x.test", PrimaryEmail: "a@x.test",
			Addresses: []AdminAddressInfo{{Address: "a@x.test"}, {Address: "s@x.test", CanSend: true, Verified: true}, {Address: "u@x.test"}}},
			nil, false, "admin@x.test", ""))
		wantPill(t, html, "status-pill ok", translated("admin.sendCapable"))
		wantPill(t, html, "status-pill warn", translated("admin.notVerified"))
		wantPill(t, html, "status-pill info", translated("admin.primary"))
	})
}

func TestAdminTablesScrollInsideCard(t *testing.T) {
	for name, html := range map[string]string{
		"users":   renderAdmin(t, AdminUserList(nil, "", "a@x.test", "")),
		"domains": renderAdmin(t, AdminDomainList(nil, "a@x.test", "", true)),
		"aliases": renderAdmin(t, AdminAliasList(nil, nil, "a@x.test", "")),
		"detail":  renderAdmin(t, AdminDomainDetail(DomainInfo{Name: "a.test", DNSGuide: []DNSGuideRow{{Type: "MX", Host: "@", Value: "m"}}}, "a@x.test", "", true)),
	} {
		scrolls := strings.Contains(html, `class="card-body flush"`) || strings.Contains(html, `class="table-scroll`)
		if !scrolls || !strings.Contains(html, `class="admin-table data-table`) {
			t.Errorf("%s: table must sit in .card-body.flush with .data-table", name)
		}
	}
}
