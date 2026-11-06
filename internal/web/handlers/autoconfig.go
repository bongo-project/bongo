package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AutoconfigHandler serves mail-client autoconfiguration documents so a user can
// set up an account by entering only their email address and password:
//
//   - Mozilla autoconfig (Thunderbird): GET /.well-known/autoconfig/mail/config-v1.1.xml
//   - Microsoft Autodiscover (Outlook): POST/GET /autodiscover/autodiscover.xml
//   - Apple profile (iOS/macOS mail, calendar, contacts): GET /.well-known/mobileconfig
//
// These endpoints are public by necessity — clients fetch them before any
// credentials exist. They expose only already-public data (the mail hostname and
// standard IMAP/submission ports); no secrets are ever included or logged.
type AutoconfigHandler struct {
	mailHost       string // hostname clients connect to (e.g. mail.example.com)
	imapPort       int    // IMAP STARTTLS port (143)
	imapsPort      int    // IMAP implicit-TLS port (993)
	submissionPort int    // SMTP submission STARTTLS port (587)
	// submissionsPort is the SMTP implicit-TLS port (465); 0 means the
	// listener is not served and 465 is left out of every document.
	submissionsPort int

	// hostsDomain reports whether we serve mail for the given domain. When nil,
	// every plausibly-formed domain is answered (dev convenience). Wiring it in
	// production restricts responses to configured domains so the endpoint cannot
	// be used as an open autoconfig generator for arbitrary domains.
	hostsDomain func(domain string) bool
}

// NewAutoconfigHandler builds an AutoconfigHandler. Zero ports fall back to the
// IANA-registered defaults (143/993/587), except submissionsPort, where 0 means
// implicit-TLS submission is not served and is omitted.
func NewAutoconfigHandler(mailHost string, imapPort, imapsPort, submissionPort, submissionsPort int) *AutoconfigHandler {
	if imapPort == 0 {
		imapPort = 143
	}
	if imapsPort == 0 {
		imapsPort = 993
	}
	if submissionPort == 0 {
		submissionPort = 587
	}
	return &AutoconfigHandler{
		mailHost:       mailHost,
		imapPort:       imapPort,
		imapsPort:      imapsPort,
		submissionPort: submissionPort,

		submissionsPort: submissionsPort,
	}
}

// SetDomainChecker restricts responses to domains for which the checker returns
// true. Call this in production; leave unset in dev.
func (h *AutoconfigHandler) SetDomainChecker(fn func(domain string) bool) {
	h.hostsDomain = fn
}

// host returns the advertised mail hostname, falling back to the request host if
// none was configured (single-node deployments always configure smtp.hostname).
func (h *AutoconfigHandler) host(r *http.Request) string {
	if h.mailHost != "" {
		return h.mailHost
	}
	return stripPort(r.Host)
}

// serves reports whether we should answer for domain. An empty or implausible
// domain is rejected; a configured checker further restricts to hosted domains.
func (h *AutoconfigHandler) serves(domain string) bool {
	if !isPlausibleDomain(domain) {
		return false
	}
	if h.hostsDomain != nil {
		return h.hostsDomain(domain)
	}
	return true
}

// Transport security names for a ClientSettings endpoint.
const (
	securitySSL      = "SSL"      // implicit TLS
	securitySTARTTLS = "STARTTLS" // upgrade after connecting
)

// davHTTPSPort is the port the CalDAV and CardDAV accounts connect to. It is
// fixed because the web server is reached over public HTTPS.
const davHTTPSPort = 443

// Fixed DAV mount points, shared by the Apple profile and the settings shown
// in the web UI.
const (
	caldavPath  = "/caldav/"
	carddavPath = "/carddav/"
)

// MailEndpoint is one mail server port a client connects to.
type MailEndpoint struct {
	Port     int
	Security string // securitySSL or securitySTARTTLS
}

// DAVEndpoint is a CalDAV or CardDAV port and path.
type DAVEndpoint struct {
	Port int
	Path string
}

// ClientSettings is the one description of how a client reaches this server.
// The autoconfig documents and the Add a device page both render from it, so
// the two cannot disagree.
type ClientSettings struct {
	Host    string
	IMAP    MailEndpoint
	SMTP    []MailEndpoint // implicit TLS first when it is served
	CalDAV  DAVEndpoint
	CardDAV DAVEndpoint
}

// ClientSettings returns the settings for the request's host. Only IMAP over
// implicit TLS is advertised, as in every document.
func (h *AutoconfigHandler) ClientSettings(r *http.Request) ClientSettings {
	return h.SettingsForHost(h.host(r))
}

// SettingsForHost returns the settings for an already-resolved host name, with
// the ports from configuration. A device link stores the host resolved on the
// desktop and rebuilds the rest here, so the request that redeems the link
// (an unauthenticated phone) never chooses the host a profile advertises.
func (h *AutoconfigHandler) SettingsForHost(host string) ClientSettings {
	s := ClientSettings{
		Host:    host,
		IMAP:    MailEndpoint{Port: h.imapsPort, Security: securitySSL},
		CalDAV:  DAVEndpoint{Port: davHTTPSPort, Path: caldavPath},
		CardDAV: DAVEndpoint{Port: davHTTPSPort, Path: carddavPath},
	}
	if h.submissionsPort != 0 {
		s.SMTP = append(s.SMTP, MailEndpoint{Port: h.submissionsPort, Security: securitySSL})
	}
	s.SMTP = append(s.SMTP, MailEndpoint{Port: h.submissionPort, Security: securitySTARTTLS})
	return s
}

// MozillaConfig serves the Thunderbird/Mozilla clientConfig document. The domain
// comes from the emailaddress query parameter, falling back to the request host
// (with any autoconfig. prefix stripped).
func (h *AutoconfigHandler) MozillaConfig(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.URL.Query().Get("emailaddress"))
	domain := domainOf(email)
	if domain == "" {
		domain = strings.TrimPrefix(stripPort(r.Host), "autoconfig.")
	}
	if !h.serves(domain) {
		http.NotFound(w, r)
		return
	}

	user := "%EMAILADDRESS%"
	if email != "" {
		user = email
	}
	settings := h.ClientSettings(r)
	cfg := clientConfig{
		Version: "1.1",
		Provider: emailProvider{
			ID:               domain,
			Domain:           domain,
			DisplayName:      domain + " Mail",
			DisplayShortName: domain,
			Incoming: incomingServer{
				Type: "imap", Hostname: settings.Host, Port: settings.IMAP.Port,
				SocketType: settings.IMAP.Security, Authentication: "password-cleartext", Username: user,
			},
			Outgoing: outgoingServers(settings, user),
		},
	}
	writeXML(w, "application/xml; charset=utf-8", cfg)
}

// outgoingServers lists the SMTP submission endpoints in settings order.
func outgoingServers(settings ClientSettings, user string) []outgoingServer {
	out := make([]outgoingServer, 0, len(settings.SMTP))
	for _, ep := range settings.SMTP {
		out = append(out, outgoingServer{
			Type: "smtp", Hostname: settings.Host, Port: ep.Port,
			SocketType: ep.Security, Authentication: "password-cleartext", Username: user,
		})
	}
	return out
}

// Autodiscover serves the Microsoft Outlook POX Autodiscover response. Outlook
// POSTs an XML body carrying the address in <EMailAddress>; a GET falls back to
// the request host.
func (h *AutoconfigHandler) Autodiscover(w http.ResponseWriter, r *http.Request) {
	var email string
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err == nil {
			email = extractAutodiscoverEmail(body)
		}
	}
	domain := domainOf(email)
	if domain == "" {
		domain = strings.TrimPrefix(stripPort(r.Host), "autodiscover.")
	}
	if !h.serves(domain) {
		http.NotFound(w, r)
		return
	}

	login := email // Outlook uses the full address as the login name
	settings := h.ClientSettings(r)
	protocols := []autodiscoverProtocol{{
		Type: "IMAP", Server: settings.Host, Port: settings.IMAP.Port,
		SSL: "on", Encryption: "SSL", SPA: "off", AuthRequired: "on", LoginName: login,
	}}
	for _, ep := range settings.SMTP {
		// Outlook names the STARTTLS upgrade "TLS".
		enc := "SSL"
		if ep.Security == securitySTARTTLS {
			enc = "TLS"
		}
		protocols = append(protocols, autodiscoverProtocol{
			Type: "SMTP", Server: settings.Host, Port: ep.Port,
			SSL: "on", Encryption: enc, SPA: "off", AuthRequired: "on", LoginName: login,
		})
	}
	resp := autodiscoverResponse{
		Response: autodiscoverInner{
			Account: autodiscoverAccount{
				AccountType: "email",
				Action:      "settings",
				Protocols:   protocols,
			},
		},
	}
	writeXML(w, "application/xml; charset=utf-8", resp)
}

// writeDAVPayload appends one com.apple.<kind>.account payload dict. kind is the
// key prefix ("CalDAV" or "CardDAV"), slug the lower-case form used in the
// payload type and identifier. idPrefix is the reversed domain plus the
// per-user address tag. The password key is written only when password is
// non-empty, so the public profile carries none and the device asks for it.
// The UUID is derived from uuidSeed plus the address, so it is stable across
// downloads and a reinstall replaces the account.
func writeDAVPayload(b *bytes.Buffer, kind, slug, description, host string, dav DAVEndpoint, email, idPrefix, uuidSeed, password string) {
	b.WriteString("    <dict>\n")
	plistStr(b, kind+"AccountDescription", description)
	plistStr(b, kind+"HostName", host)
	plistInt(b, kind+"Port", dav.Port)
	plistStr(b, kind+"PrincipalURL", dav.Path)
	plistBool(b, kind+"UseSSL", true)
	plistStr(b, kind+"Username", email)
	if password != "" {
		plistStr(b, kind+"Password", password)
	}
	plistStr(b, "PayloadDescription", "Configures "+kind+" account for "+email)
	plistStr(b, "PayloadDisplayName", description)
	plistStr(b, "PayloadIdentifier", idPrefix+"."+slug+".account")
	plistStr(b, "PayloadType", "com.apple."+slug+".account")
	plistStr(b, "PayloadUUID", stableUUID(uuidSeed+email))
	plistInt(b, "PayloadVersion", 1)
	b.WriteString("    </dict>\n")
}

// appleCredential is the app password a personal profile carries. LookupID is
// the public lookup group of the password (never the secret), used to give
// each credential's profile its own top-level UUID.
type appleCredential struct {
	Password string
	LookupID string
}

// appleProfileFilename is the download name for a profile for email's domain.
// It never includes the user's label or address.
func appleProfileFilename(email string) string {
	return domainOf(email) + ".mobileconfig"
}

// buildAppleProfile renders the Apple profile for email. With a nil cred it is
// the public profile and carries no password. With a cred it is the personal
// profile: the same document with the app password in the mail, CalDAV and
// CardDAV payloads and a top-level PayloadUUID per credential.
//
// It is pure: no request, context or session, so the public handler, Add a
// device and a later link-based download all call it. The caller must have
// checked the domain and that s.SMTP is not empty.
//
// Identifiers are per user and identical for both kinds (the reversed domain
// plus addressTag), so installing a personal profile replaces an earlier
// profile of the same user instead of adding second accounts. Only the
// top-level UUID differs per credential, which makes a reinstall a new
// version of the same profile rather than a duplicate. The UUID is derived
// from the lookup ID, never from the secret.
func buildAppleProfile(s ClientSettings, email string, cred *appleCredential) []byte {
	domain := domainOf(email)
	host := s.Host
	rev := reverseDomain(domain)
	tag := addressTag(email)
	profileUUID := stableUUID("bongo-mail-profile:" + email)
	accountUUID := stableUUID("bongo-mail-account:" + email)
	var password string
	if cred != nil {
		password = cred.Password
		profileUUID = stableUUID("bongo-personal-profile:" + email + ":" + cred.LookupID)
	}

	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("  <key>PayloadContent</key>\n  <array>\n    <dict>\n")
	plistStr(&b, "EmailAccountDescription", domain+" mail")
	plistStr(&b, "EmailAccountName", email)
	plistStr(&b, "EmailAccountType", "EmailTypeIMAP")
	plistStr(&b, "EmailAddress", email)
	plistStr(&b, "IncomingMailServerAuthentication", "EmailAuthPassword")
	plistStr(&b, "IncomingMailServerHostName", host)
	plistInt(&b, "IncomingMailServerPortNumber", s.IMAP.Port)
	plistBool(&b, "IncomingMailServerUseSSL", true)
	plistStr(&b, "IncomingMailServerUsername", email)
	if password != "" {
		plistStr(&b, "IncomingPassword", password)
	}
	plistStr(&b, "OutgoingMailServerAuthentication", "EmailAuthPassword")
	plistStr(&b, "OutgoingMailServerHostName", host)
	// The profile carries one outgoing server: implicit TLS when it is served.
	plistInt(&b, "OutgoingMailServerPortNumber", s.SMTP[0].Port)
	plistBool(&b, "OutgoingMailServerUseSSL", true)
	plistStr(&b, "OutgoingMailServerUsername", email)
	if password != "" {
		// Both mail keys are written so the profile does not depend on how
		// the device reads OutgoingPasswordSameAsIncoming.
		plistStr(&b, "OutgoingPassword", password)
	}
	plistBool(&b, "OutgoingPasswordSameAsIncoming", true)
	plistStr(&b, "PayloadDescription", "Configures mail account for "+domain)
	plistStr(&b, "PayloadDisplayName", email)
	plistStr(&b, "PayloadIdentifier", rev+".mail."+tag+".account")
	plistStr(&b, "PayloadType", "com.apple.mail.managed")
	plistStr(&b, "PayloadUUID", accountUUID)
	plistInt(&b, "PayloadVersion", 1)
	b.WriteString("    </dict>\n")
	writeDAVPayload(&b, "CalDAV", "caldav", domain+" calendar", host, s.CalDAV, email, rev+"."+tag, "bongo-caldav-account:", password)
	writeDAVPayload(&b, "CardDAV", "carddav", domain+" contacts", host, s.CardDAV, email, rev+"."+tag, "bongo-carddav-account:", password)
	b.WriteString("  </array>\n")
	plistStr(&b, "PayloadDisplayName", domain+" mail, calendar and contacts")
	plistStr(&b, "PayloadIdentifier", rev+".mail."+tag)
	plistBool(&b, "PayloadRemovalDisallowed", false)
	plistStr(&b, "PayloadType", "Configuration")
	plistStr(&b, "PayloadUUID", profileUUID)
	plistInt(&b, "PayloadVersion", 1)
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

// AppleMobileconfig serves the public, unsigned Apple .mobileconfig profile for
// iOS and macOS Mail, Calendar and Contacts. The address is required via the
// email query parameter. The profile contains no password and the device asks
// for it; the personal profile with a password is built by Add a device from
// the same buildAppleProfile. The profile is unsigned, so the OS shows it as
// "Unverified" on install.
func (h *AutoconfigHandler) AppleMobileconfig(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	domain := domainOf(email)
	if domain == "" || !h.serves(domain) {
		http.Error(w, "unknown or missing email domain", http.StatusBadRequest)
		return
	}

	body := buildAppleProfile(h.ClientSettings(r), email, nil)
	w.Header().Set("Content-Type", "application/x-apple-aspen-config; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", appleProfileFilename(email)))
	_, _ = w.Write(body)
}

// --- XML document types (encoding/xml escapes all values automatically) ---

type clientConfig struct {
	XMLName  xml.Name      `xml:"clientConfig"`
	Version  string        `xml:"version,attr"`
	Provider emailProvider `xml:"emailProvider"`
}

type emailProvider struct {
	ID               string           `xml:"id,attr"`
	Domain           string           `xml:"domain"`
	DisplayName      string           `xml:"displayName"`
	DisplayShortName string           `xml:"displayShortName"`
	Incoming         incomingServer   `xml:"incomingServer"`
	Outgoing         []outgoingServer `xml:"outgoingServer"`
}

type incomingServer struct {
	Type           string `xml:"type,attr"`
	Hostname       string `xml:"hostname"`
	Port           int    `xml:"port"`
	SocketType     string `xml:"socketType"`
	Authentication string `xml:"authentication"`
	Username       string `xml:"username"`
}

type outgoingServer struct {
	Type           string `xml:"type,attr"`
	Hostname       string `xml:"hostname"`
	Port           int    `xml:"port"`
	SocketType     string `xml:"socketType"`
	Authentication string `xml:"authentication"`
	Username       string `xml:"username"`
}

type autodiscoverResponse struct {
	XMLName  xml.Name          `xml:"http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006 Autodiscover"`
	Response autodiscoverInner `xml:"http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a Response"`
}

type autodiscoverInner struct {
	Account autodiscoverAccount `xml:"Account"`
}

type autodiscoverAccount struct {
	AccountType string                 `xml:"AccountType"`
	Action      string                 `xml:"Action"`
	Protocols   []autodiscoverProtocol `xml:"Protocol"`
}

type autodiscoverProtocol struct {
	Type         string `xml:"Type"`
	Server       string `xml:"Server"`
	Port         int    `xml:"Port"`
	SSL          string `xml:"SSL"`
	Encryption   string `xml:"Encryption"`
	SPA          string `xml:"SPA"`
	AuthRequired string `xml:"AuthRequired"`
	LoginName    string `xml:"LoginName"`
}

// --- helpers ---

func writeXML(w http.ResponseWriter, contentType string, v any) {
	out, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = io.WriteString(w, xml.Header)
	_, _ = w.Write(out)
}

// extractAutodiscoverEmail pulls the address out of an Outlook Autodiscover
// request body (<EMailAddress>user@domain</EMailAddress>), tolerant of namespace
// prefixes and element casing.
func extractAutodiscoverEmail(body []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok && strings.EqualFold(se.Name.Local, "EMailAddress") {
			var v string
			if err := dec.DecodeElement(&v, &se); err != nil {
				return ""
			}
			return strings.TrimSpace(v)
		}
	}
}

// domainOf returns the lower-cased domain part of an email address, or "".
func domainOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[at+1:]))
}

// isPlausibleDomain does a cheap sanity check to reject junk before it reaches a
// response. It is not a full RFC validation; the authoritative gate is the
// hostsDomain checker.
func isPlausibleDomain(d string) bool {
	if len(d) < 3 || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, c := range d {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-':
		default:
			return false
		}
	}
	return true
}

func reverseDomain(d string) string {
	parts := strings.Split(d, ".")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ".")
}

func stripPort(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

// stableUUID derives a deterministic UUID-formatted string from a seed so that
// reinstalling a profile replaces the existing one rather than duplicating it.
func stableUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50 // version 5 (name-based), cosmetic
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// addressTag returns 16 hex characters of a SHA-256 over the trimmed,
// lower-cased address, for use in PayloadIdentifiers. It keeps the address out
// of the identifier and keeps the identifier in reverse-DNS form. It is a
// per-user discriminator, not a secret: the profile body carries the address.
func addressTag(email string) string {
	sum := sha256.Sum256([]byte("bongo-profile-id:" + strings.ToLower(strings.TrimSpace(email))))
	return fmt.Sprintf("%x", sum[:8])
}

func plistStr(b *bytes.Buffer, key, val string) {
	fmt.Fprintf(b, "      <key>%s</key>\n      <string>%s</string>\n", key, xmlEscape(val))
}

func plistInt(b *bytes.Buffer, key string, val int) {
	fmt.Fprintf(b, "      <key>%s</key>\n      <integer>%d</integer>\n", key, val)
}

func plistBool(b *bytes.Buffer, key string, val bool) {
	tag := "false"
	if val {
		tag = "true"
	}
	fmt.Fprintf(b, "      <key>%s</key>\n      <%s/>\n", key, tag)
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
