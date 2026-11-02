package agent

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

// Apple Calendar and Contacts start account discovery with a PROPFIND on
// /.well-known/caldav or /.well-known/carddav (RFC 6764), before sending any
// credentials. A redirect registered for GET alone answered that with 405 and
// account setup failed, so every method must get the redirect.
func TestDAVWellKnownRedirectsEveryMethod(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	runWiringAgent(t, cfg, testLogger(), nil)

	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, tc := range []struct{ path, want string }{
		{"/.well-known/caldav", "/caldav/"},
		{"/.well-known/carddav", "/carddav/"},
	} {
		for _, method := range []string{"PROPFIND", "OPTIONS", "GET", "HEAD", "REPORT"} {
			req, _ := http.NewRequest(method, base+tc.path, nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, tc.path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusMovedPermanently {
				t.Errorf("%s %s: status %d, want 301", method, tc.path, resp.StatusCode)
				continue
			}
			if got := resp.Header.Get("Location"); got != tc.want {
				t.Errorf("%s %s: Location %q, want %q", method, tc.path, got, tc.want)
			}
		}
	}
}
