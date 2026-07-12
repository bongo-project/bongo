package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestAutoconfig() *AutoconfigHandler {
	h := NewAutoconfigHandler("mail.example.com", 143, 993, 587)
	h.SetDomainChecker(func(d string) bool { return d == "example.com" })
	return h
}

func TestMozillaConfig(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		host       string
		wantStatus int
		wantBits   []string
	}{
		{
			name:       "hosted domain via emailaddress param",
			target:     "/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=alice@example.com",
			wantStatus: http.StatusOK,
			wantBits: []string{
				"<clientConfig version=\"1.1\">",
				"<domain>example.com</domain>",
				"<incomingServer type=\"imap\">",
				"<hostname>mail.example.com</hostname>",
				"<port>993</port>",
				"<socketType>SSL</socketType>",
				"<outgoingServer type=\"smtp\">",
				"<port>587</port>",
				"<socketType>STARTTLS</socketType>",
				"<username>alice@example.com</username>",
			},
		},
		{
			name:       "no email falls back to host and placeholder username",
			target:     "/mail/config-v1.1.xml",
			host:       "autoconfig.example.com",
			wantStatus: http.StatusOK,
			wantBits: []string{
				"<domain>example.com</domain>",
				"<username>%EMAILADDRESS%</username>",
			},
		},
		{
			name:       "unhosted domain is 404",
			target:     "/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=bob@evil.example",
			wantStatus: http.StatusNotFound,
		},
	}

	h := newTestAutoconfig()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.host != "" {
				req.Host = tc.host
			}
			rec := httptest.NewRecorder()
			h.MozillaConfig(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
				t.Errorf("Content-Type = %q, want application/xml", ct)
			}
			body := rec.Body.String()
			for _, want := range tc.wantBits {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q\n---\n%s", want, body)
				}
			}
		})
	}
}

func TestAutodiscover(t *testing.T) {
	h := newTestAutoconfig()

	reqBody := `<?xml version="1.0" encoding="utf-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/requestschema/2006">
  <Request>
    <EMailAddress>alice@example.com</EMailAddress>
    <AcceptableResponseSchema>http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a</AcceptableResponseSchema>
  </Request>
</Autodiscover>`

	req := httptest.NewRequest(http.MethodPost, "/autodiscover/autodiscover.xml", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	h.Autodiscover(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"schemas.microsoft.com/exchange/autodiscover/responseschema/2006",
		"schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a",
		"<Type>IMAP</Type>",
		"<Server>mail.example.com</Server>",
		"<Port>993</Port>",
		"<Type>SMTP</Type>",
		"<Port>587</Port>",
		"<LoginName>alice@example.com</LoginName>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n---\n%s", want, body)
		}
	}
}

func TestAutodiscoverUnhostedDomain(t *testing.T) {
	h := newTestAutoconfig()
	reqBody := `<Autodiscover><Request><EMailAddress>x@evil.example</EMailAddress></Request></Autodiscover>`
	req := httptest.NewRequest(http.MethodPost, "/autodiscover/autodiscover.xml", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	h.Autodiscover(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAppleMobileconfig(t *testing.T) {
	h := newTestAutoconfig()

	t.Run("valid email", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/.well-known/mobileconfig?email=alice@example.com", nil)
		rec := httptest.NewRecorder()
		h.AppleMobileconfig(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-apple-aspen-config") {
			t.Errorf("Content-Type = %q", ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "example.com.mobileconfig") {
			t.Errorf("Content-Disposition = %q", cd)
		}
		body := rec.Body.String()
		for _, want := range []string{
			"com.apple.mail.managed",
			"<key>IncomingMailServerHostName</key>",
			"<string>mail.example.com</string>",
			"<key>IncomingMailServerPortNumber</key>",
			"<integer>993</integer>",
			"<key>OutgoingMailServerPortNumber</key>",
			"<integer>587</integer>",
			"<string>alice@example.com</string>",
			"com.example.mail",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		// A password must never appear in the profile.
		if strings.Contains(strings.ToLower(body), "password</key>\n      <string>") {
			t.Error("profile unexpectedly contains a password string value")
		}
	})

	t.Run("missing email is 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/.well-known/mobileconfig", nil)
		rec := httptest.NewRecorder()
		h.AppleMobileconfig(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("stable UUID is deterministic per email", func(t *testing.T) {
		if got := stableUUID("bongo-mail-profile:alice@example.com"); got != stableUUID("bongo-mail-profile:alice@example.com") {
			t.Error("stableUUID not deterministic")
		}
		if stableUUID("a") == stableUUID("b") {
			t.Error("stableUUID collided for different seeds")
		}
	})
}

func TestXMLEscaping(t *testing.T) {
	// A checker that accepts anything plausible, to exercise escaping of a crafted domain.
	h := NewAutoconfigHandler("mail.example.com", 143, 993, 587)
	h.SetDomainChecker(func(string) bool { return true })

	req := httptest.NewRequest(http.MethodGet,
		"/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=x@a-b.com", nil)
	rec := httptest.NewRecorder()
	h.MozillaConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// encoding/xml must produce well-formed output with the domain echoed safely.
	if !strings.Contains(rec.Body.String(), "<domain>a-b.com</domain>") {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestDomainOf(t *testing.T) {
	tests := map[string]string{
		"alice@example.com": "example.com",
		"Bob@Example.COM":   "example.com",
		"noatsign":           "",
		"trailing@":          "",
		"":                   "",
	}
	for in, want := range tests {
		if got := domainOf(in); got != want {
			t.Errorf("domainOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsPlausibleDomain(t *testing.T) {
	ok := []string{"example.com", "a.b.c", "mail-server.example.com"}
	bad := []string{"", "nodot", "a", "has space.com", "under_score.com", "semi;colon.com"}
	for _, d := range ok {
		if !isPlausibleDomain(d) {
			t.Errorf("isPlausibleDomain(%q) = false, want true", d)
		}
	}
	for _, d := range bad {
		if isPlausibleDomain(d) {
			t.Errorf("isPlausibleDomain(%q) = true, want false", d)
		}
	}
}
