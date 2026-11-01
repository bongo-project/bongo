package handlers

import (
	"fmt"
	"strings"

	"github.com/bongo-project/bongo/internal/web/templates"
)

// dnsGuideTTL is the TTL emitted for every generated record. 3600s (1h) is a
// sane default that avoids the excessively-low TTLs some registrar exports use.
const dnsGuideTTL = 3600

// buildDNSGuide produces the canonical set of DNS records a domain owner must
// publish so this bongo server can host `domain`. It is pure and deterministic
// (no I/O) so the same values feed both the on-page guide and its unit tests,
// and so the guide can never drift from the DNS status checks.
//
// mailHostname is the server's shared SMTP hostname (SMTP.Hostname); MX and all
// SRV targets point here (the "shared server host" model). serverIPv4/6 are the
// public addresses for the A/AAAA records, each omitted when empty (lookup
// failed / not available). dkimSelector and dkimTXTRecord (the derived record
// line from dkimTXTRecord()) are empty when no key has been generated.
//
// The second return value is true when DKIM is still pending, so the UI can
// prompt the operator to generate a key instead of publishing a broken
// empty-p= record.
func buildDNSGuide(domain, mailHostname string, submissions bool, serverIPv4, serverIPv6, dkimSelector, dkimTXTRecord string) ([]templates.DNSGuideRow, bool) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	mailHostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(mailHostname), "."))

	var rows []templates.DNSGuideRow
	add := func(t, h, v string) {
		rows = append(rows, templates.DNSGuideRow{Type: t, Host: h, TTL: dnsGuideTTL, Value: v})
	}

	// A / AAAA — point the domain apex and the client-facing hostnames at the
	// server. autoconfig/autodiscover are required for HTTP-based client setup.
	if serverIPv4 != "" {
		for _, h := range []string{"@", "mail", "autoconfig", "autodiscover"} {
			add("A", h, serverIPv4)
		}
	}
	if serverIPv6 != "" {
		add("AAAA", "@", serverIPv6)
		add("AAAA", "mail", serverIPv6)
	}

	// MX — shared server host.
	if mailHostname != "" {
		add("MX", "@", fmt.Sprintf("10 %s.", mailHostname))
	}

	// SPF + DMARC. "mx" authorises whatever the MX resolves to (the server).
	// DMARC starts at p=none so the operator can watch rua reports before
	// tightening to quarantine/reject once DKIM is verified.
	add("TXT", "@", "v=spf1 mx ~all")
	add("TXT", "_dmarc", fmt.Sprintf("v=DMARC1; p=none; rua=mailto:postmaster@%s", domain))

	// DKIM — only emit a real record when a key exists; never publish empty p=.
	dkimPending := true
	if v := extractDKIMValue(dkimTXTRecord); v != "" && dkimSelector != "" {
		add("TXT", dkimSelector+"._domainkey", v)
		dkimPending = false
	}

	// SRV — RFC 6186 (imaps/submission) + autodiscover + CalDAV/CardDAV, all
	// pointed at the shared host so clients auto-configure.
	if mailHostname != "" {
		add("SRV", "_autodiscover._tcp", fmt.Sprintf("0 1 443 %s.", mailHostname))
		add("SRV", "_imaps._tcp", fmt.Sprintf("0 1 993 %s.", mailHostname))
		// RFC 8314 prefers implicit TLS, so 465 is listed before 587.
		if submissions {
			add("SRV", "_submissions._tcp", fmt.Sprintf("0 1 465 %s.", mailHostname))
		}
		add("SRV", "_submission._tcp", fmt.Sprintf("0 1 587 %s.", mailHostname))
		add("SRV", "_caldavs._tcp", fmt.Sprintf("0 1 443 %s.", mailHostname))
		add("SRV", "_carddavs._tcp", fmt.Sprintf("0 1 443 %s.", mailHostname))
	}
	// CalDAV/CardDAV discovery path hints.
	add("TXT", "_caldavs._tcp", "path=/caldav/")
	add("TXT", "_carddavs._tcp", "path=/carddav/")

	return rows, dkimPending
}

// extractDKIMValue pulls the quoted value out of a derived dkimTXTRecord line:
//
//	`mail._domainkey.example.com TXT "v=DKIM1; k=rsa; p=..."` -> `v=DKIM1; k=rsa; p=...`
//
// Returns "" when the input has no quoted value (e.g. no key generated).
func extractDKIMValue(rec string) string {
	i := strings.Index(rec, "\"")
	j := strings.LastIndex(rec, "\"")
	if i < 0 || j <= i {
		return ""
	}
	return rec[i+1 : j]
}

// dnsGuideZone renders the guide rows as a BIND-format zone fragment ready for
// download. Names are expanded to FQDNs and TXT values are quoted.
func dnsGuideZone(domain string, rows []templates.DNSGuideRow) string {
	domain = strings.ToLower(strings.TrimSpace(domain))

	var b strings.Builder
	fmt.Fprintf(&b, ";; Bongo DNS records for %s\n", domain)
	b.WriteString(";; Publish these at your DNS provider. Edit TTLs to taste.\n")
	b.WriteString(";; If a DKIM record is absent below, generate a DKIM key in the\n")
	b.WriteString(";; domain's admin page first, then re-download this file.\n\n")

	for _, r := range rows {
		name := domain + "."
		if r.Host != "@" {
			name = r.Host + "." + domain + "."
		}
		val := r.Value
		if r.Type == "TXT" {
			val = "\"" + r.Value + "\""
		}
		fmt.Fprintf(&b, "%-40s %d IN %-5s %s\n", name, r.TTL, r.Type, val)
	}
	return b.String()
}
