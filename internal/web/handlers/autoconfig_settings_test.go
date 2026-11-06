package handlers

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The settings the web UI shows must be the values the served documents carry.
// Each document is fetched and parsed, then compared with ClientSettings, so a
// document that grows its own literal fails here.
func TestClientSettingsMatchServedDocuments(t *testing.T) {
	tests := []struct {
		name string
		h    func() *AutoconfigHandler
		want ClientSettings
	}{
		{
			name: "implicit TLS submission served",
			h:    newTestAutoconfig,
			want: ClientSettings{
				Host:    "mail.example.com",
				IMAP:    MailEndpoint{Port: 993, Security: "SSL"},
				SMTP:    []MailEndpoint{{Port: 465, Security: "SSL"}, {Port: 587, Security: "STARTTLS"}},
				CalDAV:  DAVEndpoint{Port: 443, Path: "/caldav/"},
				CardDAV: DAVEndpoint{Port: 443, Path: "/carddav/"},
			},
		},
		{
			name: "no implicit TLS submission",
			h: func() *AutoconfigHandler {
				h := NewAutoconfigHandler("mx.example.com", 0, 1993, 2525, 0)
				h.SetDomainChecker(func(d string) bool { return d == "example.com" })
				return h
			},
			want: ClientSettings{
				Host:    "mx.example.com",
				IMAP:    MailEndpoint{Port: 1993, Security: "SSL"},
				SMTP:    []MailEndpoint{{Port: 2525, Security: "STARTTLS"}},
				CalDAV:  DAVEndpoint{Port: 443, Path: "/caldav/"},
				CardDAV: DAVEndpoint{Port: 443, Path: "/carddav/"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := tt.h()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			got := h.ClientSettings(req)
			if got.Host != tt.want.Host || got.IMAP != tt.want.IMAP || got.CalDAV != tt.want.CalDAV || got.CardDAV != tt.want.CardDAV ||
				len(got.SMTP) != len(tt.want.SMTP) {
				t.Fatalf("ClientSettings = %+v, want %+v", got, tt.want)
			}
			for i := range got.SMTP {
				if got.SMTP[i] != tt.want.SMTP[i] {
					t.Fatalf("SMTP[%d] = %+v, want %+v", i, got.SMTP[i], tt.want.SMTP[i])
				}
			}

			// Mozilla.
			rec := httptest.NewRecorder()
			h.MozillaConfig(rec, httptest.NewRequest(http.MethodGet, "/?emailaddress=a@example.com", nil))
			var moz clientConfig
			if err := xml.Unmarshal(rec.Body.Bytes(), &moz); err != nil {
				t.Fatalf("mozilla: %v", err)
			}
			in := moz.Provider.Incoming
			if in.Hostname != got.Host || in.Port != got.IMAP.Port || in.SocketType != got.IMAP.Security {
				t.Errorf("mozilla incoming = %+v, settings %+v", in, got.IMAP)
			}
			if len(moz.Provider.Outgoing) != len(got.SMTP) {
				t.Fatalf("mozilla outgoing = %d, settings %d", len(moz.Provider.Outgoing), len(got.SMTP))
			}
			for i, out := range moz.Provider.Outgoing {
				if out.Hostname != got.Host || out.Port != got.SMTP[i].Port || out.SocketType != got.SMTP[i].Security {
					t.Errorf("mozilla outgoing[%d] = %+v, settings %+v", i, out, got.SMTP[i])
				}
			}

			// Autodiscover.
			rec = httptest.NewRecorder()
			body := strings.NewReader("<Autodiscover><Request><EMailAddress>a@example.com</EMailAddress></Request></Autodiscover>")
			h.Autodiscover(rec, httptest.NewRequest(http.MethodPost, "/autodiscover/autodiscover.xml", body))
			var ad autodiscoverResponse
			if err := xml.Unmarshal(rec.Body.Bytes(), &ad); err != nil {
				t.Fatalf("autodiscover: %v", err)
			}
			protos := ad.Response.Account.Protocols
			if len(protos) != 1+len(got.SMTP) {
				t.Fatalf("autodiscover protocols = %d", len(protos))
			}
			if protos[0].Server != got.Host || protos[0].Port != got.IMAP.Port {
				t.Errorf("autodiscover imap = %+v", protos[0])
			}
			for i, ep := range got.SMTP {
				p := protos[1+i]
				if p.Server != got.Host || p.Port != ep.Port {
					t.Errorf("autodiscover smtp[%d] = %+v, settings %+v", i, p, ep)
				}
			}

			// Apple profile.
			_, payloads := parseMobileconfig(t, fetchMobileconfig(t, h, "a@example.com", ""))
			if len(payloads) != 3 {
				t.Fatalf("profile has %d payloads", len(payloads))
			}
			mail := payloads[0]
			if mail["IncomingMailServerHostName"] != got.Host || mail["IncomingMailServerPortNumber"] != strconv.Itoa(got.IMAP.Port) ||
				mail["OutgoingMailServerPortNumber"] != strconv.Itoa(got.SMTP[0].Port) {
				t.Errorf("profile mail = %v, settings %+v", mail, got)
			}
			for i, dav := range []struct {
				kind string
				ep   DAVEndpoint
			}{{"CalDAV", got.CalDAV}, {"CardDAV", got.CardDAV}} {
				p := payloads[1+i]
				if p[dav.kind+"HostName"] != got.Host || p[dav.kind+"Port"] != strconv.Itoa(dav.ep.Port) || p[dav.kind+"PrincipalURL"] != dav.ep.Path {
					t.Errorf("profile %s = %v, settings %+v", dav.kind, p, dav.ep)
				}
			}
		})
	}
}

// With no configured host the request host is advertised, in the settings and
// so everywhere.
func TestClientSettingsFallBackToRequestHost(t *testing.T) {
	h := NewAutoconfigHandler("", 0, 0, 0, 0)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "webmail.example.org:8443"
	if got := h.ClientSettings(req).Host; got != "webmail.example.org" {
		t.Errorf("Host = %q", got)
	}
}
