package handlers

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTestAutoconfig() *AutoconfigHandler {
	h := NewAutoconfigHandler("mail.example.com", 143, 993, 587, 465)
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
			"<integer>465</integer>",
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
	h := NewAutoconfigHandler("mail.example.com", 143, 993, 587, 465)
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
		"noatsign":          "",
		"trailing@":         "",
		"":                  "",
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

// submissionsDoc fetches one autoconfig document from a handler.
func submissionsDoc(t *testing.T, h *AutoconfigHandler, doc string) string {
	t.Helper()
	var req *http.Request
	rec := httptest.NewRecorder()
	switch doc {
	case "mozilla":
		req = httptest.NewRequest(http.MethodGet, "/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=alice@example.com", nil)
		h.MozillaConfig(rec, req)
	case "autodiscover":
		req = httptest.NewRequest(http.MethodGet, "/autodiscover/autodiscover.xml", nil)
		req.Host = "autodiscover.example.com"
		h.Autodiscover(rec, req)
	default:
		req = httptest.NewRequest(http.MethodGet, "/.well-known/mobileconfig?email=alice@example.com", nil)
		h.AppleMobileconfig(rec, req)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d", doc, rec.Code)
	}
	return rec.Body.String()
}

func TestAutoconfigImplicitTLSSubmission(t *testing.T) {
	tests := []struct {
		name            string
		submissionsPort int
		doc             string
		wantInOrder     []string
		wantAbsent      []string
	}{
		{"mozilla lists 465 SSL before 587 STARTTLS", 465, "mozilla",
			[]string{"<port>465</port>", "<socketType>SSL</socketType>", "<port>587</port>", "<socketType>STARTTLS</socketType>"}, nil},
		{"mozilla omits 465 when disabled", 0, "mozilla",
			[]string{"<port>587</port>"}, []string{"<port>465</port>"}},
		{"autodiscover lists 465 SSL before 587 TLS", 465, "autodiscover",
			[]string{"<Port>465</Port>", "<Encryption>SSL</Encryption>", "<Port>587</Port>", "<Encryption>TLS</Encryption>"}, nil},
		{"autodiscover omits 465 when disabled", 0, "autodiscover",
			[]string{"<Port>587</Port>"}, []string{"<Port>465</Port>"}},
		{"apple uses 465 when served", 465, "apple",
			[]string{"<integer>465</integer>"}, []string{"<integer>587</integer>"}},
		{"apple falls back to 587 when 465 is off", 0, "apple",
			[]string{"<integer>587</integer>"}, []string{"<integer>465</integer>"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewAutoconfigHandler("mail.example.com", 143, 993, 587, tc.submissionsPort)
			h.SetDomainChecker(func(d string) bool { return d == "example.com" })
			body := submissionsDoc(t, h, tc.doc)
			pos := 0
			for _, want := range tc.wantInOrder {
				i := strings.Index(body[pos:], want)
				if i < 0 {
					t.Fatalf("%q missing or out of order after offset %d in:\n%s", want, pos, body)
				}
				pos += i + len(want)
			}
			for _, bad := range tc.wantAbsent {
				if strings.Contains(body, bad) {
					t.Errorf("unexpected %q in:\n%s", bad, body)
				}
			}
		})
	}
}

// plistPayload is one parsed PayloadContent dict: key to scalar value
// ("true"/"false" for booleans).
type plistPayload map[string]string

// parseMobileconfig parses a profile and returns the outer dict's scalar keys
// and the payloads in PayloadContent. Parsing proves the output is well-formed.
func parseMobileconfig(t *testing.T, body string) (outer plistPayload, payloads []plistPayload) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(body))
	outer = plistPayload{}
	depth := 0 // dict and array nesting; the top-level dict is depth 1
	var cur plistPayload
	var key string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("profile is not well-formed: %v", err)
		}
		switch el := tok.(type) {
		case xml.EndElement:
			if el.Name.Local == "dict" || el.Name.Local == "array" {
				if el.Name.Local == "dict" && depth == 3 {
					payloads = append(payloads, cur)
					cur = nil
				}
				depth--
			}
		case xml.StartElement:
			switch el.Name.Local {
			case "dict", "array":
				depth++
				if el.Name.Local == "dict" && depth == 3 {
					cur = plistPayload{}
				}
			case "key", "string", "integer":
				var v string
				if err := dec.DecodeElement(&v, &el); err != nil {
					t.Fatalf("decode %s: %v", el.Name.Local, err)
				}
				if el.Name.Local == "key" {
					key = v
				} else if cur != nil {
					cur[key] = v
				} else if depth == 1 {
					outer[key] = v
				}
			case "true", "false":
				if cur != nil {
					cur[key] = el.Name.Local
				} else if depth == 1 {
					outer[key] = el.Name.Local
				}
			}
		}
	}
	return outer, payloads
}

func fetchMobileconfig(t *testing.T, h *AutoconfigHandler, email, reqHost string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/mobileconfig?email="+url.QueryEscape(email), nil)
	if reqHost != "" {
		req.Host = reqHost
	}
	rec := httptest.NewRecorder()
	h.AppleMobileconfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// assertNoPasswords fails if any payload carries a password-valued key. Exact
// names are checked: OutgoingPasswordSameAsIncoming is legitimate.
func assertNoPasswords(t *testing.T, payloads []plistPayload) {
	t.Helper()
	banned := []string{"CalDAVPassword", "CardDAVPassword", "IncomingPassword", "OutgoingPassword"}
	for _, p := range payloads {
		for _, k := range banned {
			if _, ok := p[k]; ok {
				t.Errorf("payload %s carries %s", p["PayloadType"], k)
			}
		}
	}
}

func TestAppleMobileconfigDAVPayloads(t *testing.T) {
	h := newTestAutoconfig()
	outer, payloads := parseMobileconfig(t, fetchMobileconfig(t, h, "alice@example.com", ""))

	wantTypes := []string{"com.apple.mail.managed", "com.apple.caldav.account", "com.apple.carddav.account"}
	if len(payloads) != len(wantTypes) {
		t.Fatalf("payloads = %d, want %d", len(payloads), len(wantTypes))
	}
	for i, want := range wantTypes {
		if got := payloads[i]["PayloadType"]; got != want {
			t.Errorf("payload %d type = %q, want %q", i, got, want)
		}
	}

	tests := []struct {
		name   string
		idx    int
		prefix string
		path   string
	}{
		{"caldav", 1, "CalDAV", "/caldav/"},
		{"carddav", 2, "CardDAV", "/carddav/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := payloads[tc.idx]
			want := map[string]string{
				tc.prefix + "HostName":     "mail.example.com",
				tc.prefix + "Port":         "443",
				tc.prefix + "UseSSL":       "true",
				tc.prefix + "PrincipalURL": tc.path,
				tc.prefix + "Username":     "alice@example.com",
				"PayloadVersion":           "1",
			}
			for k, v := range want {
				if p[k] != v {
					t.Errorf("%s = %q, want %q", k, p[k], v)
				}
			}
		})
	}
	assertNoPasswords(t, payloads)

	// Identifiers and UUIDs are unique across the outer profile and every payload.
	uuids, ids := map[string]bool{}, map[string]bool{}
	for _, p := range append([]plistPayload{outer}, payloads...) {
		if p["PayloadUUID"] == "" || p["PayloadIdentifier"] == "" {
			t.Fatalf("payload missing identifier or UUID: %v", p)
		}
		if uuids[p["PayloadUUID"]] {
			t.Errorf("duplicate PayloadUUID %s", p["PayloadUUID"])
		}
		if ids[p["PayloadIdentifier"]] {
			t.Errorf("duplicate PayloadIdentifier %s", p["PayloadIdentifier"])
		}
		uuids[p["PayloadUUID"]], ids[p["PayloadIdentifier"]] = true, true
	}
}

func TestAppleMobileconfigStableIdentifiers(t *testing.T) {
	h := newTestAutoconfig()
	identity := func(email string) (map[string]bool, map[string]bool) {
		outer, payloads := parseMobileconfig(t, fetchMobileconfig(t, h, email, ""))
		u, i := map[string]bool{}, map[string]bool{}
		for _, p := range append([]plistPayload{outer}, payloads...) {
			u[p["PayloadUUID"]], i[p["PayloadIdentifier"]] = true, true
		}
		return u, i
	}
	u1, i1 := identity("alice@example.com")
	u2, i2 := identity("alice@example.com")
	u3, _ := identity("bob@example.com")
	if len(u1) != 4 {
		t.Fatalf("distinct UUIDs = %d, want 4", len(u1))
	}
	for k := range u1 {
		if !u2[k] {
			t.Errorf("UUID %s not stable across downloads", k)
		}
		if u3[k] {
			t.Errorf("UUID %s shared with another user", k)
		}
	}
	for k := range i1 {
		if !i2[k] {
			t.Errorf("identifier %s not stable across downloads", k)
		}
	}
}

func TestAppleMobileconfigInjection(t *testing.T) {
	evilUser := "a</string><key>CalDAVPassword</key><string>x@example.com"
	evilHost := "x</string><key>CardDAVPassword</key><string>y"
	tests := []struct {
		name     string
		mailHost string
		email    string
		reqHost  string
		wantUser string
		wantHost string
	}{
		{"markup in local part", "mail.example.com", evilUser, "", evilUser, "mail.example.com"},
		{"markup in request host fallback", "", "alice@example.com", evilHost, "alice@example.com", evilHost},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewAutoconfigHandler(tc.mailHost, 143, 993, 587, 465)
			h.SetDomainChecker(func(d string) bool { return d == "example.com" })
			_, payloads := parseMobileconfig(t, fetchMobileconfig(t, h, tc.email, tc.reqHost))
			if len(payloads) != 3 {
				t.Fatalf("payloads = %d, want 3", len(payloads))
			}
			assertNoPasswords(t, payloads)
			if got := payloads[1]["CalDAVUsername"]; got != tc.wantUser {
				t.Errorf("CalDAVUsername = %q, want %q", got, tc.wantUser)
			}
			if got := payloads[2]["CardDAVHostName"]; got != tc.wantHost {
				t.Errorf("CardDAVHostName = %q, want %q", got, tc.wantHost)
			}
		})
	}
}
