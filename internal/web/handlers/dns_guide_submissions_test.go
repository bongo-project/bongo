package handlers

import (
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/web/templates"
)

func TestBuildDNSGuideSubmissionsRow(t *testing.T) {
	index := func(rows []templates.DNSGuideRow, host string) int {
		for i, r := range rows {
			if r.Type == "SRV" && r.Host == host {
				return i
			}
		}
		return -1
	}
	tests := []struct {
		name        string
		submissions bool
		mailHost    string
		want        bool
	}{
		{"enabled", true, "mail.example.com", true},
		{"disabled", false, "mail.example.com", false},
		{"enabled without mail hostname", true, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, _ := buildDNSGuide("example.net", tt.mailHost, tt.submissions, "", "", "", "")
			subs := index(rows, "_submissions._tcp")
			if (subs >= 0) != tt.want {
				t.Fatalf("_submissions._tcp present = %v, want %v", subs >= 0, tt.want)
			}
			zone := dnsGuideZone("example.net", rows)
			if got := strings.Contains(zone, "_submissions._tcp"); got != tt.want {
				t.Errorf("zone export contains _submissions._tcp = %v, want %v", got, tt.want)
			}
			if !tt.want {
				return
			}
			if rows[subs].Value != "0 1 465 mail.example.com." {
				t.Errorf("value = %q", rows[subs].Value)
			}
			if sub := index(rows, "_submission._tcp"); sub < 0 || subs > sub {
				t.Errorf("_submissions (%d) must come before _submission (%d)", subs, sub)
			}
		})
	}
}
