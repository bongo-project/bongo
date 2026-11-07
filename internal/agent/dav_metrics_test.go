package agent

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/observability/obstest"
)

// TestDAVMetricsWiredIntoAgent sends requests to the real /caldav/ and
// /carddav/ routes of a running agent. A method outside the fixed set (PATCH,
// which the router passes through) must land under "other" and never create a
// series of its own, known methods keep their
// label, and the in-flight gauge returns to its start.
func TestDAVMetricsWiredIntoAgent(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	runWiringAgent(t, cfg, testLogger(), nil)
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)

	do := func(method, path string) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("admin@localhost", "admin")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		_ = resp.Body.Close()
	}

	for _, tt := range []struct {
		name, protocol, path, method, label string
	}{
		{"caldav unknown method", "caldav", "/caldav/", "PATCH", "other"},
		{"caldav known method", "caldav", "/caldav/", "OPTIONS", "OPTIONS"},
		{"carddav unknown method", "carddav", "/carddav/", "PATCH", "other"},
		{"carddav known method", "carddav", "/carddav/", "OPTIONS", "OPTIONS"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{"protocol": tt.protocol}
			cmd0 := obstest.Command(t, tt.protocol, tt.label)
			req0 := obstest.Counter(t, "bongo_dav_requests_total", labels)
			do(tt.method, tt.path)
			if got := obstest.Command(t, tt.protocol, tt.label) - cmd0; got != 1 {
				t.Errorf("%s/%s samples +%d, want +1", tt.protocol, tt.label, got)
			}
			if got := obstest.Counter(t, "bongo_dav_requests_total", labels) - req0; got != 1 {
				t.Errorf("requests +%v, want +1", got)
			}
			if got := obstest.Gauge(t, "bongo_dav_requests_in_flight", labels); got != 0 {
				t.Errorf("in flight = %v after the request returned, want 0", got)
			}
		})
	}
}
