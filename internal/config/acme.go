package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
	"strings"
)

// DefaultACMEDirectoryURL is Let's Encrypt production.
const DefaultACMEDirectoryURL = "https://acme-v02.api.letsencrypt.org/directory"

// maxACMEDomains bounds web.tls.acme.domains (Let's Encrypt allows 100 names
// per certificate).
const maxACMEDomains = 100

// WebTLSConfig is the web.tls block: explicit certificate files, or an acme
// block. It is its own type so smtp.tls, cluster.tls and agent_server.tls
// do not accept an acme block that would mean nothing there. The three file
// fields mirror TLSConfig.
type WebTLSConfig struct {
	CertFile string `hcl:"cert_file,optional" mapstructure:"cert_file"`
	KeyFile  string `hcl:"key_file,optional" mapstructure:"key_file"`
	CAFile   string `hcl:"ca_file,optional" mapstructure:"ca_file"`
	// ACME obtains the certificate from an ACME CA over TLS-ALPN-01. A
	// pointer block so a config file without it still decodes; read it
	// through ACMESettings.
	ACME *ACMEConfig `hcl:"acme,block" mapstructure:"acme"`
}

// ACMEConfig is the web.tls.acme block. The issued certificate and the
// account key are replicated through Raft and included in backups, like DKIM
// keys.
type ACMEConfig struct {
	Enabled bool `hcl:"enabled,optional" mapstructure:"enabled"`
	// Email is the optional account contact. Let's Encrypt no longer needs one.
	Email string `hcl:"email,optional" mapstructure:"email"`
	// Domains are the mail host names the certificate covers.
	Domains []string `hcl:"domains,optional" mapstructure:"domains"`
	// DirectoryURL is the CA's ACME directory. Default: Let's Encrypt production.
	DirectoryURL string `hcl:"directory_url,optional" mapstructure:"directory_url"`
}

// ACMESettings returns the acme block with defaults filled in.
func (w WebTLSConfig) ACMESettings() ACMEConfig {
	a := ACMEConfig{}
	if w.ACME != nil {
		a = *w.ACME
	}
	if a.DirectoryURL == "" {
		a.DirectoryURL = DefaultACMEDirectoryURL
	}
	return a
}

// ACMEEnabled reports whether web.tls.acme.enabled is set.
func (w WebTLSConfig) ACMEEnabled() bool { return w.ACME != nil && w.ACME.Enabled }

// SubmissionsServed reports whether the implicit-TLS submission listener is
// served: SubmissionsEnabled, or ACME supplying the certificate.
func (c *Config) SubmissionsServed(dev bool) bool {
	if c.SMTP.SubmissionsEnabled(dev) {
		return true
	}
	return c.Web.TLS.ACMEEnabled() && c.SMTP.SubmissionsListenPort(dev) != 0
}

// ValidACMEDomain reports whether d is a lower-case DNS host name usable on a
// TLS-ALPN-01 certificate: at least two labels, each 1-63 characters of
// a-z, 0-9 and hyphen (not leading or trailing), at most 253 in all, no
// wildcard, no trailing dot and no IP literal.
func ValidACMEDomain(d string) error {
	if d == "" {
		return errors.New("empty name")
	}
	if len(d) > 253 {
		return errors.New("longer than 253 characters")
	}
	if _, err := netip.ParseAddr(d); err == nil {
		return errors.New("an IP address is not a host name")
	}
	if strings.Contains(d, "*") {
		return errors.New("wildcards cannot be validated with TLS-ALPN-01")
	}
	if strings.HasSuffix(d, ".") {
		return errors.New("must not end with a dot")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return errors.New("needs at least two labels")
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 {
			return errors.New("label empty or longer than 63 characters")
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return errors.New("label starts or ends with a hyphen")
		}
		for i := range len(l) {
			ch := l[i]
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return errors.New("only lower-case letters, digits and hyphens are allowed")
			}
		}
	}
	// A final label that starts with a digit would read as a number.
	if last := labels[len(labels)-1]; last[0] >= '0' && last[0] <= '9' {
		return errors.New("the last label must not start with a digit")
	}
	return nil
}

// validateACME checks web.tls.acme. Errors name the key, never a value.
func (c *Config) validateACME() error {
	if !c.Web.TLS.ACMEEnabled() {
		return nil
	}
	a := c.Web.TLS.ACMESettings()
	if c.Server.Role != "server" {
		return fmt.Errorf("web.tls.acme.enabled: needs server.role = \"server\" (got %q); dev mode makes no outbound calls and edge and agent nodes do not hold the replicated certificate", c.Server.Role)
	}
	if c.Web.TLS.CertFile != "" || c.Web.TLS.KeyFile != "" {
		return errors.New("web.tls.acme.enabled: cannot be combined with web.tls.cert_file or web.tls.key_file")
	}
	if c.SMTP.TLS.CertFile != "" || c.SMTP.TLS.KeyFile != "" {
		return errors.New("web.tls.acme.enabled: cannot be combined with smtp.tls.cert_file or smtp.tls.key_file (the ACME certificate serves SMTP)")
	}
	if c.Web.PlainHTTP || c.Web.BehindTLSProxy {
		return errors.New("web.tls.acme.enabled: cannot be combined with web.plain_http or web.behind_tls_proxy (TLS-ALPN-01 needs this process to terminate TLS)")
	}
	if len(a.Domains) == 0 {
		return errors.New("web.tls.acme.domains: at least one host name is required")
	}
	if len(a.Domains) > maxACMEDomains {
		return fmt.Errorf("web.tls.acme.domains: at most %d names, got %d", maxACMEDomains, len(a.Domains))
	}
	seen := make(map[string]bool, len(a.Domains))
	for i, d := range a.Domains {
		if err := ValidACMEDomain(d); err != nil {
			return fmt.Errorf("web.tls.acme.domains[%d]: %w", i, err)
		}
		if seen[d] {
			return fmt.Errorf("web.tls.acme.domains[%d]: duplicate name", i)
		}
		seen[d] = true
	}
	if a.Email != "" {
		addr, err := mail.ParseAddress(a.Email)
		if err != nil || addr.Name != "" || addr.Address != a.Email {
			return errors.New("web.tls.acme.email: must be a bare address such as admin@example.com")
		}
	}
	u, err := url.Parse(a.DirectoryURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("web.tls.acme.directory_url: must be an https URL with a host and no user information")
	}
	return nil
}
