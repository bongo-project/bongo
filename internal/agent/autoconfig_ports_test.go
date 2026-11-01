package agent

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/web/handlers"
)

// Client autoconfiguration must advertise the ports the listeners really
// bind, and 465 only when the SMTP server will start it.
func TestAutoconfigPorts(t *testing.T) {
	withTLS := func(c *config.Config) {
		c.SMTP.TLS = config.TLSConfig{CertFile: "smtp.crt", KeyFile: "smtp.key"}
	}
	tests := []struct {
		name        string
		modify      func(*config.Config)
		imap, imaps int
		submission  int
		submissions int
	}{
		{"dev defaults use dev ports", func(c *config.Config) { c.Server.Role = "dev" }, 1143, 1993, 2587, 2465},
		{"dev honours explicit submissions port", func(c *config.Config) {
			c.Server.Role = "dev"
			c.SMTP.SubmissionsPort = 4650
		}, 1143, 1993, 2587, 4650},
		{"dev with smtp disabled", func(c *config.Config) {
			c.Server.Role = "dev"
			c.SMTP.Enabled = false
		}, 1143, 1993, 2587, 0},
		{"dev with submissions port 0", func(c *config.Config) {
			c.Server.Role = "dev"
			c.SMTP.SubmissionsPort = 0
		}, 1143, 1993, 2587, 0},
		{"server with certificate", func(c *config.Config) {
			c.Server.Role = "server"
			withTLS(c)
		}, 143, 993, 587, 465},
		{"server without certificate", func(c *config.Config) { c.Server.Role = "server" }, 143, 993, 587, 0},
		{"server with certificate but smtp disabled", func(c *config.Config) {
			c.Server.Role = "server"
			withTLS(c)
			c.SMTP.Enabled = false
		}, 143, 993, 587, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			tt.modify(cfg)
			imap, imaps, sub, subs := autoconfigPorts(cfg)
			if imap != tt.imap || imaps != tt.imaps || sub != tt.submission || subs != tt.submissions {
				t.Fatalf("ports = %d/%d/%d/%d, want %d/%d/%d/%d",
					imap, imaps, sub, subs, tt.imap, tt.imaps, tt.submission, tt.submissions)
			}

			h := handlers.NewAutoconfigHandler("mail.example.com", imap, imaps, sub, subs)
			rec := httptest.NewRecorder()
			h.MozillaConfig(rec, httptest.NewRequest(http.MethodGet, "/mail/config-v1.1.xml?emailaddress=a@example.com", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			var doc struct {
				Outgoing []struct {
					Port int `xml:"port"`
				} `xml:"emailProvider>outgoingServer"`
			}
			if err := xml.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&doc); err != nil {
				t.Fatalf("decode: %v", err)
			}
			var got []int
			for _, o := range doc.Outgoing {
				got = append(got, o.Port)
			}
			want := []int{tt.submission}
			if tt.submissions != 0 {
				want = []int{tt.submissions, tt.submission}
			}
			if len(got) != len(want) {
				t.Fatalf("outgoing ports = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("outgoing ports = %v, want %v", got, want)
				}
			}
		})
	}
}
