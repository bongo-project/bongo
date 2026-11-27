package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func acmeServerConfig(t *testing.T, mutate func(*Config)) *Config {
	t.Helper()
	c := Defaults()
	c.Server.Role = "server"
	c.Server.AdvertiseAddr = "127.0.0.1"
	c.Web.TLS.ACME = &ACMEConfig{Enabled: true, Domains: []string{"mail.example.com"}}
	if mutate != nil {
		mutate(c)
	}
	return c
}

func TestACMEDefaults(t *testing.T) {
	c := Defaults()
	if c.Web.TLS.ACMEEnabled() {
		t.Error("ACME must be off by default")
	}
	got := c.Web.TLS.ACMESettings()
	if got.Enabled || got.DirectoryURL != "https://acme-v02.api.letsencrypt.org/directory" {
		t.Errorf("defaults = %+v", got)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("default config with no acme block: %v", err)
	}
}

func TestValidateACME(t *testing.T) {
	long := strings.Repeat("a", 64) + ".example.com"
	many := make([]string, 101)
	for i := range many {
		many[i] = "h" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".example.com"
	}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"accepted", nil, ""},
		{"accepted with email and staging", func(c *Config) {
			c.Web.TLS.ACME.Email = "admin@example.com"
			c.Web.TLS.ACME.DirectoryURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
		}, ""},
		{"accepted with cluster tls files", func(c *Config) {
			c.Cluster.TLS.CertFile, c.Cluster.TLS.KeyFile = "/c.pem", "/k.pem"
		}, ""},
		{"accepted on port 8443", func(c *Config) { c.Web.Port = 8443 }, ""},
		{"disabled block is not validated", func(c *Config) {
			c.Web.TLS.ACME.Enabled = false
			c.Web.TLS.ACME.Domains = []string{"not a host"}
			c.Web.TLS.CertFile, c.Web.TLS.KeyFile = "/c", "/k"
		}, ""},
		{"dev role", func(c *Config) { c.Server.Role = "dev" }, "web.tls.acme.enabled"},
		{"edge role", func(c *Config) { c.Server.Role = "edge" }, "web.tls.acme.enabled"},
		{"web cert file", func(c *Config) { c.Web.TLS.CertFile = "/c.pem" }, "web.tls.cert_file"},
		{"web key file", func(c *Config) { c.Web.TLS.KeyFile = "/k.pem" }, "web.tls.cert_file or web.tls.key_file"},
		{"smtp cert file", func(c *Config) { c.SMTP.TLS.CertFile = "/c.pem" }, "smtp.tls.cert_file"},
		{"plain http", func(c *Config) { c.Web.PlainHTTP = true }, "plain_http"},
		{"behind proxy", func(c *Config) { c.Web.BehindTLSProxy = true }, "behind_tls_proxy"},
		{"no domains", func(c *Config) { c.Web.TLS.ACME.Domains = nil }, "web.tls.acme.domains"},
		{"too many domains", func(c *Config) { c.Web.TLS.ACME.Domains = many }, "at most 100"},
		{"ip literal", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"192.0.2.1"} }, "domains[0]"},
		{"ipv6 literal", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"2001:db8::1"} }, "domains[0]"},
		{"wildcard", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"*.example.com"} }, "wildcard"},
		{"trailing dot", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"mail.example.com."} }, "dot"},
		{"single label", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"localhost"} }, "two labels"},
		{"upper case", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"Mail.Example.com"} }, "lower-case"},
		{"underscore", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"a_b.example.com"} }, "lower-case"},
		{"label of 64", func(c *Config) { c.Web.TLS.ACME.Domains = []string{long} }, "63"},
		{"leading hyphen", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"-a.example.com"} }, "hyphen"},
		{"empty label", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"a..example.com"} }, "label"},
		{"url as domain", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"https://mail.example.com"} }, "domains[0]"},
		{"duplicate", func(c *Config) { c.Web.TLS.ACME.Domains = []string{"a.example.com", "a.example.com"} }, "duplicate"},
		{"email with name", func(c *Config) { c.Web.TLS.ACME.Email = "Admin <admin@example.com>" }, "web.tls.acme.email"},
		{"email not an address", func(c *Config) { c.Web.TLS.ACME.Email = "admin" }, "web.tls.acme.email"},
		{"directory http", func(c *Config) { c.Web.TLS.ACME.DirectoryURL = "http://ca.example/dir" }, "directory_url"},
		{"directory userinfo", func(c *Config) { c.Web.TLS.ACME.DirectoryURL = "https://u:p@ca.example/dir" }, "directory_url"},
		{"directory no host", func(c *Config) { c.Web.TLS.ACME.DirectoryURL = "https:///dir" }, "directory_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := acmeServerConfig(t, tt.mutate).Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestACMEKeyClasses(t *testing.T) {
	for _, k := range []string{"web.tls.acme.enabled", "web.tls.acme.email", "web.tls.acme.domains", "web.tls.acme.directory_url"} {
		if c, ok := ClassOf(k); !ok || c != ClassRestart {
			t.Errorf("ClassOf(%s) = %v, %v; want restart", k, c, ok)
		}
	}
	if c, ok := ClassOf("web.tls.ca_file"); !ok || c != ClassNoOp {
		t.Errorf("web.tls.ca_file = %v, %v; want no-op", c, ok)
	}
	a, b := Defaults(), Defaults()
	b.Web.TLS.ACME = &ACMEConfig{Domains: []string{"mail.example.com"}}
	_, restart, noop := Diff(a, b)
	if len(restart) != 1 || restart[0].Key != "web.tls.acme.domains" || len(noop) != 0 {
		t.Errorf("diff = restart %v noop %v", restart, noop)
	}
	b.Web.TLS.CAFile = "/ca.pem"
	if _, _, noop := Diff(a, b); len(noop) != 1 || noop[0].Key != "web.tls.ca_file" {
		t.Errorf("web.tls.ca_file diff = %v", noop)
	}
}

func TestACMERedirectAndSubmissions(t *testing.T) {
	c := acmeServerConfig(t, func(c *Config) { c.Web.HTTPSRedirect = true })
	if !c.redirectListenerEnabled() {
		t.Error("ACME serves TLS, so the redirect listener must be enabled")
	}
	c.Web.TLS.ACME.Enabled = false
	if c.redirectListenerEnabled() {
		t.Error("redirect without certificate files or ACME must stay off")
	}

	c = acmeServerConfig(t, func(c *Config) { c.SMTP.SubmissionsPort = 465 })
	if !c.SubmissionsServed(false) {
		t.Error("ACME supplies the certificate, so submissions is served")
	}
	c.Web.TLS.ACME.Enabled = false
	if c.SubmissionsServed(false) {
		t.Error("submissions without a certificate must stay off")
	}
}

// TestACMEBlockDecodesFromHCL loads a real file, so a wrong hcl tag on the
// block or its fields cannot go unnoticed.
func TestACMEBlockDecodesFromHCL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bongo.hcl")
	base, err := os.ReadFile("../../test/testdata/config.hcl")
	if err != nil {
		t.Fatal(err)
	}
	// The base file's web tls block is the one with cert_file lines after
	// "web {"; replace it with a block holding the acme block.
	const acmeTLS = `tls {
    acme {
      enabled       = true
      domains       = ["mail.example.com", "imap.example.com"]
      email         = "admin@example.com"
      directory_url = "https://acme-staging-v02.api.letsencrypt.org/directory"
    }
  }`
	idx := strings.Index(string(base), "web {")
	if idx < 0 {
		t.Fatal("no web block in the base config")
	}
	head, tail := string(base[:idx]), string(base[idx:])
	tail = strings.Replace(tail, "tls {\n    cert_file = \"/tmp/cert.pem\"\n    key_file  = \"/tmp/key.pem\"\n  }", acmeTLS, 1)
	if !strings.Contains(tail, "acme {") {
		t.Fatal("the base config's web tls block was not replaced")
	}
	src := head + tail
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if !cfg.Web.TLS.ACMEEnabled() {
		t.Fatal("the acme block did not enable ACME")
	}
	got := cfg.Web.TLS.ACMESettings()
	want := ACMEConfig{
		Enabled:      true,
		Domains:      []string{"mail.example.com", "imap.example.com"},
		Email:        "admin@example.com",
		DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
	}
	if got.Enabled != want.Enabled || got.Email != want.Email || got.DirectoryURL != want.DirectoryURL ||
		strings.Join(got.Domains, ",") != strings.Join(want.Domains, ",") {
		t.Errorf("settings = %+v, want %+v", got, want)
	}
}
